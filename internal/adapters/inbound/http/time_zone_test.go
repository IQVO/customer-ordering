package http_test

import (
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/promising"
)

// zoneOf reads the optional timeZone of the i-th entry of a JSON array field;
// present reports whether the key exists at all (absent = unknown, ADR 0009).
func zoneOf(t *testing.T, body map[string]any, field string, i int) (zone string, present bool) {
	t.Helper()
	list, ok := body[field].([]any)
	if !ok || len(list) <= i {
		t.Fatalf("%s[%d] missing in %v", field, i, body)
	}
	v, present := list[i].(map[string]any)["timeZone"]
	zone, _ = v.(string)
	return zone, present
}

// Every view of a group's window names the zone it is read in when the
// promise carried one, and omits the key when it did not (ADR 0009).
func TestOrderViewsExposeTheWindowTimeZone(t *testing.T) {
	e := newEnv(t)
	zoned := e.gw.Terms["offer-1"]
	zoned.TimeZone = "America/Chicago"
	e.gw.Terms["offer-1"] = zoned
	created := e.checkout("key-tz")
	if created.Status != 201 {
		t.Fatalf("checkout = %d %s", created.Status, created.Body)
	}
	id := created.json(t)["orderId"].(string)
	views := map[string]map[string]any{
		"checkout": created.json(t),
		"order":    e.do("GET", "/orders/"+id, "", "").json(t),
		"tracking": e.do("GET", "/orders/"+id+"/tracking", "", "").json(t),
		"history":  e.do("GET", "/orders?shopperId=shopper-1", "", "").json(t)["items"].([]any)[0].(map[string]any),
	}
	for name, body := range views {
		if zone, _ := zoneOf(t, body, "groups", 0); zone != "America/Chicago" {
			t.Errorf("%s: group 1 timeZone = %q, want America/Chicago", name, zone)
		}
		if _, present := zoneOf(t, body, "groups", 1); present {
			t.Errorf("%s: group 2 has no zone, so timeZone must be omitted", name)
		}
	}
}

func TestRequotedOffersExposeTheWindowTimeZone(t *testing.T) {
	e := newEnv(t)
	delete(e.gw.Terms, "offer-2")
	zoned := e.gw.Offers[0]
	zoned.TimeZone = "America/Chicago"
	e.gw.Offers = []promising.Offer{zoned, e.gw.Offers[0]}
	r := e.checkout("expired-tz")
	if r.Status != 409 {
		t.Fatalf("expired offer = %d %s", r.Status, r.Body)
	}
	body := r.json(t)
	if zone, _ := zoneOf(t, body, "requotedOffers", 0); zone != "America/Chicago" {
		t.Fatalf("requoted offer timeZone = %q, want America/Chicago", zone)
	}
	if _, present := zoneOf(t, body, "requotedOffers", 1); present {
		t.Fatal("an offer without a zone must omit timeZone")
	}
}
