package http_test

import (
	"strings"
	"testing"
)

func TestCheckoutCreatesTheOrderAndNeverReturnsTheAddress(t *testing.T) {
	e := newEnv(t)
	r := e.checkout("k1")
	if r.Status != 201 || r.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("checkout = %d %s %s", r.Status, r.Header.Get("Content-Type"), r.Body)
	}
	o := r.json(t)
	id := o["orderId"].(string)
	if r.Header.Get("Location") != "/orders/"+id || o["status"] != "PLACED" || o["destinationPostalPrefix"] != "787" || len(o["groups"].([]any)) != 2 {
		t.Fatalf("order = %v location %q", o, r.Header.Get("Location"))
	}
	if strings.Contains(r.Body, "Sample Street") || strings.Contains(r.Body, "78701") || strings.Contains(r.Body, "Ana Example") {
		t.Fatalf("the order must not carry the address: %s", r.Body)
	}
	g := o["groups"].([]any)[0].(map[string]any)
	if g["status"] != "PROMISED" || g["promiseId"] != "p-1" || g["tier"] != "NEXT_DAY" || g["revisionNo"] != nil {
		t.Fatalf("group = %v", g)
	}
}

func TestCheckoutIsIdempotentOnTheKey(t *testing.T) {
	e := newEnv(t)
	first, second := e.checkout("same"), e.checkout("same")
	if first.Status != 201 || second.Status != 201 || first.Body != second.Body {
		t.Fatalf("replay differs:\n%d %s\n%d %s", first.Status, first.Body, second.Status, second.Body)
	}
	if second.Header.Get("Idempotent-Replayed") != "true" || first.Header.Get("Idempotent-Replayed") != "" {
		t.Fatalf("replay header: first %q second %q", first.Header.Get("Idempotent-Replayed"), second.Header.Get("Idempotent-Replayed"))
	}
	if n := len(e.gw.Made()); n != 2 {
		t.Fatalf("%d promises made, want 2 (the replay must not call delivery-promise)", n)
	}
	other := e.do("POST", "/checkout", "same", strings.Replace(checkoutBody, "sku-100", "sku-101", 1))
	if other.Status != 422 || other.json(t)["type"] != "/problems/idempotency-key-reused" {
		t.Fatalf("a different body under the same key = %d %s", other.Status, other.Body)
	}
	if again := e.checkout("another-key"); again.json(t)["orderId"] == first.json(t)["orderId"] {
		t.Fatal("another key must place another order")
	}
}

func TestDifferentKeysGiveStableDifferentOrderIDs(t *testing.T) {
	a, b := newEnv(t), newEnv(t)
	if a.checkout("k").json(t)["orderId"] != b.checkout("k").json(t)["orderId"] {
		t.Fatal("the order id is derived from the key (A13)")
	}
}

func TestIdempotencyKeyIsRequiredOnEveryPost(t *testing.T) {
	e := newEnv(t)
	long := strings.Repeat("k", 256)
	for _, c := range []struct{ path, key, body string }{
		{"/checkout", "", checkoutBody}, {"/orders/x:cancel", "", `{"reason":"r"}`}, {"/shoppers", "", `{"email":"a@b.co"}`}, {"/checkout", long, checkoutBody},
	} {
		r := e.do("POST", c.path, c.key, c.body)
		if r.Status != 400 || r.Header.Get("Content-Type") != "application/problem+json" || r.json(t)["type"] != "/problems/idempotency-key-required" {
			t.Fatalf("%s key %d chars = %d %s", c.path, len(c.key), r.Status, r.Body)
		}
	}
}

func TestCheckoutErrorsAreProblems(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		name, body string
		status     int
		slug       string
	}{
		{"malformed json", `{`, 400, "malformed-request"},
		{"unknown field", `{"shopperId":"s","extra":1}`, 400, "malformed-request"},
		{"trailing data", checkoutBody + ` {}`, 400, "malformed-request"},
		{"empty body", ``, 400, "malformed-request"},
		{"line in no group", strings.Replace(checkoutBody, `,{"groupNo":2,"offerId":"offer-2","lineNos":[3]}`, "", 1), 422, "line-without-promise"},
		{"bad ship-to", strings.Replace(checkoutBody, `"countryCode":"US"`, `"countryCode":"USA"`, 1), 422, "validation-failed"},
	}
	for i, c := range cases {
		r := e.do("POST", "/checkout", "err-"+string(rune('a'+i)), c.body)
		p := r.json(t)
		if r.Status != c.status || p["type"] != "/problems/"+c.slug || r.Header.Get("Content-Type") != "application/problem+json" || p["title"] == "" || p["instance"] != "/checkout" {
			t.Fatalf("%s = %d %s", c.name, r.Status, r.Body)
		}
	}
}

func TestAnExpiredOfferIs409WithRequotedOffers(t *testing.T) {
	e := newEnv(t)
	delete(e.gw.Terms, "offer-2")
	r := e.checkout("expired")
	p := r.json(t)
	offers, _ := p["requotedOffers"].([]any)
	if r.Status != 409 || p["type"] != "/problems/offer-not-quotable" || len(offers) == 0 {
		t.Fatalf("expired offer = %d %s", r.Status, r.Body)
	}
	o := offers[0].(map[string]any)
	if o["offerId"] != "new-1" || o["groupNo"] == nil || o["expiresAt"] == "" || o["tier"] != "NEXT_DAY" {
		t.Fatalf("requoted offer = %v", o)
	}
	if again := e.do("GET", "/orders/anything", "", ""); again.json(t)["requotedOffers"] != nil {
		t.Fatal("only offer-not-quotable carries requotedOffers")
	}
}

func TestGetOrderAndTracking(t *testing.T) {
	e := newEnv(t)
	id := e.placed()
	g := e.do("GET", "/orders/"+id, "", "")
	if g.Status != 200 || g.json(t)["orderId"] != id {
		t.Fatalf("get = %d %s", g.Status, g.Body)
	}
	tr := e.do("GET", "/orders/"+id+"/tracking", "", "")
	body := tr.json(t)
	groups := body["groups"].([]any)
	if tr.Status != 200 || body["status"] != "PLACED" || len(groups) != 2 || groups[0].(map[string]any)["packages"] == nil {
		t.Fatalf("tracking = %d %s", tr.Status, tr.Body)
	}
	for _, p := range []string{"/orders/unknown", "/orders/unknown/tracking", "/orders/unknown/ship-to", "/orders/" + strings.Repeat("x", 65)} {
		r := e.do("GET", p, "", "")
		if r.Status != 404 || r.json(t)["type"] != "/problems/not-found" {
			t.Fatalf("%s = %d %s", p, r.Status, r.Body)
		}
	}
}

func TestShipToIsOnlyServedByItsRoute(t *testing.T) {
	e := newEnv(t)
	id := e.placed()
	r := e.do("GET", "/orders/"+id+"/ship-to", "", "")
	s := r.json(t)
	if r.Status != 200 || s["line1"] != "1 Sample Street" || s["postalCode"] != "78701" || s["phone"] != "+1 555 0100" || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("ship-to = %d %v cache %q", r.Status, s, r.Header.Get("Cache-Control"))
	}
	for _, p := range []string{"/orders/" + id, "/orders/" + id + "/tracking"} {
		if body := e.do("GET", p, "", "").Body; strings.Contains(body, "Sample Street") || strings.Contains(body, "Ana Example") {
			t.Fatalf("%s leaks the address", p)
		}
	}
}

func TestCancelOrder(t *testing.T) {
	e := newEnv(t)
	id := e.placed()
	r := e.do("POST", "/orders/"+id+":cancel", "c1", `{"reason":"changed my mind"}`)
	o := r.json(t)
	g := o["groups"].([]any)[0].(map[string]any)
	if r.Status != 200 || g["status"] != "CANCELLATION_REQUESTED" || g["cancelReason"] != "changed my mind" || o["status"] != "PLACED" {
		t.Fatalf("cancel = %d %s", r.Status, r.Body)
	}
	if again := e.do("POST", "/orders/"+id+":cancel", "c1", `{"reason":"changed my mind"}`); again.Body != r.Body || again.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay = %s", again.Body)
	}
	for _, c := range []struct {
		path, body string
		status     int
		slug       string
	}{
		{"/orders/nope:cancel", `{"reason":"x"}`, 404, "not-found"},
		{"/orders/" + id + ":cancel", `{"reason":""}`, 422, "validation-failed"},
		{"/orders/" + id + ":cancel", `{"reasn":"x"}`, 400, "malformed-request"},
	} {
		r := e.do("POST", c.path, "c-"+c.slug+c.body, c.body)
		if r.Status != c.status || r.json(t)["type"] != "/problems/"+c.slug {
			t.Fatalf("%s %s = %d %s", c.path, c.body, r.Status, r.Body)
		}
	}
}

func TestRegisterShopper(t *testing.T) {
	e := newEnv(t)
	r := e.do("POST", "/shoppers", "s1", `{"email":"New@Example.com"}`)
	s := r.json(t)
	if r.Status != 201 || s["email"] != "new@example.com" || s["status"] != "ACTIVE" || s["shopperId"] == "" || !strings.HasPrefix(r.Header.Get("Location"), "/shoppers/") {
		t.Fatalf("register = %d %s", r.Status, r.Body)
	}
	dup := e.do("POST", "/shoppers", "s2", `{"email":"new@example.com"}`)
	if dup.Status != 409 || dup.json(t)["type"] != "/problems/shopper-exists" {
		t.Fatalf("duplicate = %d %s", dup.Status, dup.Body)
	}
	bad := e.do("POST", "/shoppers", "s3", `{"email":"nope"}`)
	if bad.Status != 422 || bad.json(t)["type"] != "/problems/validation-failed" {
		t.Fatalf("invalid e-mail = %d %s", bad.Status, bad.Body)
	}
}

func TestNULIsAClientError(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/orders/a%00b", "", 404}, {"GET", "/orders/x?q=%00", "", 400},
		{"POST", "/shoppers", "{\"email\":\"a\\u0000@b.co\"}", 400},
	} {
		if r := e.do(c.method, c.path, "nul-key", c.body); r.Status != c.status {
			t.Fatalf("%s %s = %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
	if r := e.do("POST", "/shoppers", "nul-key", `{"email":"ok@example.com"}`); r.Status != 201 {
		t.Fatalf("a rejected request must not record its key: %d %s", r.Status, r.Body)
	}
}

func TestHealthAndReadiness(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/healthz", "/readyz"} {
		if r := e.do("GET", p, "", ""); r.Status != 200 {
			t.Fatalf("%s = %d", p, r.Status)
		}
	}
}
