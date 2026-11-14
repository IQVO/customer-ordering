package http_test

import (
	"strings"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/promising"
)

// ADR 0016: a pickup checkout answers its point on the ship-to route (no
// address fields at all) and the exactly-one-of validation names all three
// destinations.

const pickupCheckoutBody = `{"shopperId":"shopper-1",
 "pickupPointId":"node-pnt-7",
 "lines":[{"lineNo":1,"catalogItemId":"sku-100","quantity":2},{"lineNo":2,"catalogItemId":"sku-200","quantity":1},{"lineNo":3,"catalogItemId":"sku-300","quantity":5}],
 "groups":[{"groupNo":1,"offerId":"offer-1","lineNos":[1,2]},{"groupNo":2,"offerId":"offer-2","lineNos":[3]}]}`

// placedPickup checks a pickup order out (the promises quoted to the point's
// prefix 070) and returns the order id.
func (e *env) placedPickup() string {
	e.t.Helper()
	pickupTerms := func(p, n string) promising.Terms {
		t := promising.Terms{PromiseID: p, Tier: "NEXT_DAY", WitnessNodeID: n, ShipBy: t0.Add(10 * time.Hour),
			DeliverByEarliest: t0.Add(20 * time.Hour), DeliverByLatest: t0.Add(30 * time.Hour), DestinationPostalPrefix: "070"}
		return t
	}
	e.gw.Terms["offer-1"] = pickupTerms("pp-1", "fc-pnt-1")
	e.gw.Terms["offer-2"] = pickupTerms("pp-2", "fc-pnt-2")
	r := e.do("POST", "/checkout", "key-pickup", pickupCheckoutBody)
	if r.Status != 201 {
		e.t.Fatalf("pickup checkout = %d %s", r.Status, r.Body)
	}
	return r.json(e.t)["orderId"].(string)
}

func TestTheShipToRouteAnswersThePickupPoint(t *testing.T) {
	e := newEnv(t)
	id := e.placedPickup()
	r := e.do("GET", "/orders/"+id+"/ship-to", "", "")
	s := r.json(t)
	if r.Status != 200 || s["destinationKind"] != "PICKUP_POINT" || s["pickupPointId"] != "node-pnt-7" {
		t.Fatalf("the pickup answer = %d %v", r.Status, s)
	}
	for _, field := range []string{"name", "line1", "line2", "city", "region", "postalCode", "countryCode", "phone"} {
		if _, present := s[field]; present {
			t.Fatalf("the pickup answer carries the address field %q: %v", field, s)
		}
	}
	if r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control = %q", r.Header.Get("Cache-Control"))
	}
	// A ship-to order keeps today's shape: the address fields, no kind.
	shipE := newEnv(t)
	shipID := shipE.placed()
	ship := shipE.do("GET", "/orders/"+shipID+"/ship-to", "", "").json(t)
	if ship["line1"] != "1 Sample Street" || ship["destinationKind"] != nil {
		t.Fatalf("the ship-to answer = %v", ship)
	}
}

func TestCheckoutDestinationChoiceIsExactlyOneOfThree(t *testing.T) {
	e := newEnv(t)
	withAddress := strings.Replace(pickupCheckoutBody, `"pickupPointId":"node-pnt-7",`, `"pickupPointId":"node-pnt-7","shipTo":{"name":"Ana Example","line1":"1 Sample Street","city":"Austin","region":"TX","postalCode":"78701","countryCode":"US"},`, 1)
	withSaved := strings.Replace(pickupCheckoutBody, `"pickupPointId":"node-pnt-7",`, `"pickupPointId":"node-pnt-7","shipToAddressId":"addr-1",`, 1)
	none := strings.Replace(pickupCheckoutBody, ` "pickupPointId":"node-pnt-7",`, ``, 1)
	for i, body := range []string{withAddress, withSaved, none} {
		r := e.do("POST", "/checkout", "pickup-choice-"+string(rune('a'+i)), body)
		p := r.json(t)
		if r.Status != 422 || p["type"] != "/problems/validation-failed" || !strings.Contains(p["detail"].(string), "pickupPointId") {
			t.Fatalf("case %d = %d %s", i, r.Status, r.Body)
		}
	}
}
