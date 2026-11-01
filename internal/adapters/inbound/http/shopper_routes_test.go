package http_test

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const addressBody = `{"name":"Ana Example","line1":"1 Sample Street","city":"Austin","region":"TX","postalCode":"78701","countryCode":"US","phone":"+1 555 0100"}`

func TestAddSavedAddressRoute(t *testing.T) {
	e := newEnv(t)
	r := e.do("POST", "/shoppers/shopper-1/addresses", "home", addressBody)
	a := r.json(t)
	if r.Status != 201 || a["addressId"] == "" || a["line1"] != "1 Sample Street" ||
		!strings.HasPrefix(r.Header.Get("Location"), "/shoppers/shopper-1/addresses/") {
		t.Fatalf("add address = %d %s location %q", r.Status, r.Body, r.Header.Get("Location"))
	}
	// The same key with the same body replays the first answer.
	if replay := e.do("POST", "/shoppers/shopper-1/addresses", "home", addressBody); replay.Body != r.Body ||
		replay.Header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay = %s (replayed %q)", replay.Body, replay.Header.Get("Idempotency-Replayed"))
	}
	// The profile lists it.
	p := e.do("GET", "/shoppers/shopper-1", "", "").json(t)
	list := p["savedAddresses"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["addressId"] != a["addressId"] {
		t.Fatalf("profile = %v", p)
	}
	// The twenty-first address is too many.
	for i := 1; i < 20; i++ {
		if c := e.do("POST", "/shoppers/shopper-1/addresses", fmt.Sprintf("home-%d", i), addressBody); c.Status != 201 {
			t.Fatalf("address %d = %d %s", i+1, c.Status, c.Body)
		}
	}
	cap := e.do("POST", "/shoppers/shopper-1/addresses", "one-too-many", addressBody)
	if cap.Status != 422 || cap.json(t)["type"] != "/problems/too-many-addresses" {
		t.Fatalf("cap = %d %s", cap.Status, cap.Body)
	}
	// An unknown shopper is 404, a missing key 400, a bad body 422.
	e.refuses([]refusal{
		{"POST", "/shoppers/shopper-nope/addresses", "k1", addressBody, 404, "not-found"},
		{"POST", "/shoppers/shopper-1/addresses", "", addressBody, 400, "idempotency-key-required"},
		{"POST", "/shoppers/shopper-1/addresses", "k2", `{"name":""}`, 422, "validation-failed"},
		{"GET", "/shoppers/shopper-nope", "", "", 404, "not-found"},
	})
}

// refusal is one expected problem answer.
type refusal struct {
	method, path, key, body string
	status                  int
	slug                    string
}

func (e *env) refuses(cases []refusal) {
	e.t.Helper()
	for _, c := range cases {
		r := e.do(c.method, c.path, c.key, c.body)
		if r.Status != c.status || r.json(e.t)["type"] != "/problems/"+c.slug {
			e.t.Fatalf("%s %s = %d %s, want %d %s", c.method, c.path, r.Status, r.Body, c.status, c.slug)
		}
	}
}

func TestFindShopperByEmailRoute(t *testing.T) {
	e := newEnv(t)
	r := e.do("GET", "/shoppers?email=%20%20Ana%40Example.COM%20", "", "")
	s := r.json(t)
	if r.Status != 200 || s["shopperId"] != "shopper-1" || len(s["savedAddresses"].([]any)) != 0 {
		t.Fatalf("find = %d %s", r.Status, r.Body)
	}
	for _, q := range []string{"", "?email=bob@example.com", "?email=nope"} {
		if r := e.do("GET", "/shoppers"+q, "", ""); r.Status != 404 || r.json(t)["type"] != "/problems/not-found" {
			t.Fatalf("find %q = %d %s, want 404 not-found", q, r.Status, r.Body)
		}
	}
}

func TestOrderHistoryRoute(t *testing.T) {
	e := newEnv(t)
	firstID := e.checkout("history-1").json(e.t)["orderId"].(string)
	e.advance(10 * time.Minute)
	secondID := e.checkout("history-2").json(e.t)["orderId"].(string)
	r := e.do("GET", "/orders?shopperId=shopper-1", "", "")
	page := r.json(t)
	items := page["items"].([]any)
	if r.Status != 200 || len(items) != 2 || items[0].(map[string]any)["orderId"] != secondID ||
		items[1].(map[string]any)["orderId"] != firstID || page["nextCursor"] != nil {
		t.Fatalf("history = %d %s", r.Status, r.Body)
	}
	// The items never carry the address.
	if strings.Contains(r.Body, "shipTo") || strings.Contains(r.Body, "1 Sample Street") {
		t.Fatalf("history leaks the address: %s", r.Body)
	}
	e.refuses([]refusal{
		{"GET", "/orders", "", "", 422, "validation-failed"},
		{"GET", "/orders?shopperId=shopper-nope", "", "", 404, "not-found"},
		{"GET", "/orders?shopperId=shopper-1&limit=101", "", "", 422, "validation-failed"},
		{"GET", "/orders?shopperId=shopper-1&limit=abc", "", "", 422, "validation-failed"},
		{"GET", "/orders?shopperId=shopper-1&after=not-a-cursor", "", "", 422, "validation-failed"},
	})
	e.pagesToTheOlderOrder(secondID, firstID)
}

// pagesToTheOlderOrder walks the cursor: a short page names a nextCursor that
// returns exactly the older order and then no cursor at all.
func (e *env) pagesToTheOlderOrder(newest, oldest string) {
	e.t.Helper()
	half := e.do("GET", "/orders?shopperId=shopper-1&limit=1", "", "").json(e.t)
	cursor, ok := half["nextCursor"].(string)
	if !ok || cursor == "" || len(half["items"].([]any)) != 1 || half["items"].([]any)[0].(map[string]any)["orderId"] != newest {
		e.t.Fatalf("first page = %v", half)
	}
	rest := e.do("GET", "/orders?shopperId=shopper-1&after="+cursor, "", "").json(e.t)
	if len(rest["items"].([]any)) != 1 || rest["items"].([]any)[0].(map[string]any)["orderId"] != oldest || rest["nextCursor"] != nil {
		e.t.Fatalf("second page = %v", rest)
	}
}

func TestCheckoutBySavedAddressRoute(t *testing.T) {
	e := newEnv(t)
	added := e.do("POST", "/shoppers/shopper-1/addresses", "home", addressBody)
	addressID := added.json(t)["addressId"].(string)
	byID := fmt.Sprintf(`{"shopperId":"shopper-1","shipToAddressId":%q,"lines":[{"lineNo":1,"catalogItemId":"sku-100","quantity":2}],`+
		`"groups":[{"groupNo":1,"offerId":"offer-1","lineNos":[1]}]}`, addressID)
	r := e.do("POST", "/checkout", "by-address", byID)
	o := r.json(t)
	if r.Status != 201 || o["status"] != "PLACED" {
		t.Fatalf("checkout by address = %d %s", r.Status, r.Body)
	}
	// The address was copied into the order: the restricted route returns it.
	ship := e.do("GET", "/orders/"+o["orderId"].(string)+"/ship-to", "", "").json(t)
	if ship["line1"] != "1 Sample Street" || ship["postalCode"] != "78701" {
		t.Fatalf("stored ship-to = %v", ship)
	}
	both := fmt.Sprintf(`{"shopperId":"shopper-1","shipTo":%s,"shipToAddressId":%q,"lines":[{"lineNo":1,"catalogItemId":"sku-100","quantity":2}],`+
		`"groups":[{"groupNo":1,"offerId":"offer-1","lineNos":[1]}]}`, addressBody, addressID)
	for name, body := range map[string]string{
		"both given":    both,
		"unknown id":    strings.Replace(byID, addressID, "address-nope", 1),
		"neither given": `{"shopperId":"shopper-1","lines":[{"lineNo":1,"catalogItemId":"sku-100","quantity":2}],"groups":[{"groupNo":1,"offerId":"offer-1","lineNos":[1]}]}`,
	} {
		if r := e.do("POST", "/checkout", "refuse-"+name, body); r.Status != 422 || r.json(t)["type"] != "/problems/validation-failed" {
			t.Fatalf("checkout %s = %d %s", name, r.Status, r.Body)
		}
	}
}

// advance moves the scenario clock, so placedAt differs between orders.
func (e *env) advance(d time.Duration) {
	e.t.Helper()
	e.clock.Advance(d)
}
