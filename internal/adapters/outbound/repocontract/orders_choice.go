package repocontract

import (
	"reflect"
	"testing"

	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
)

// Fixture values of the choice cases, named so they are not repeated literals.
const (
	choiceZone    = "America/Chicago"
	choiceShopper = "shopper-1"
	choiceSKU     = "sku-100"
)

// consolidatedOrder places a one-group CONSOLIDATED order (ADR 0013) with the
// given chosen day and eligibility answer.
func consolidatedOrder(t *testing.T, id, day string, eligible *bool) *customerorder.CustomerOrder {
	t.Helper()
	g := groupInput(1, "p-"+id, 1)
	g.Tier, g.TimeZone, g.ChosenDeliveryDay, g.ConsolidationEligible = customerorder.TierConsolidated, choiceZone, day, eligible
	o, _, err := customerorder.Place(customerorder.NewOrder{
		ID: customerorder.ID(id), ShopperID: choiceShopper, ShipTo: address(),
		Lines:  []customerorder.OrderLine{{LineNo: 1, CatalogItemID: choiceSKU, Quantity: 2}},
		Groups: []customerorder.GroupInput{g},
	}, t0)
	if err != nil {
		t.Fatalf("Place %s: %v", id, err)
	}
	return o
}

// assertConsolidatedChoice checks that the CONSOLIDATED tier and the stored
// choice round-trip: a day with eligibility true, a day with eligibility false
// (a stated false is not "unknown"), and both unknown.
func assertConsolidatedChoice(t *testing.T, e Env) {
	t.Helper()
	yes, no := true, false
	for _, c := range []struct {
		id       string
		day      string
		eligible *bool
	}{{"order-ch-yes", "2026-10-24", &yes}, {"order-ch-no", "2026-10-23", &no}, {"order-ch-unknown", "", nil}} {
		o := consolidatedOrder(t, c.id, c.day, c.eligible)
		must(t, e.Orders.Save(bg(), o))
		got, err := e.Orders.Get(bg(), customerorder.ID(c.id))
		must(t, err)
		if !reflect.DeepEqual(got.Snapshot(), o.Snapshot()) {
			t.Fatalf("%s: round trip changed the order:\n got %+v\nwant %+v", c.id, got.Snapshot(), o.Snapshot())
		}
		g := got.Groups()[0]
		if g.Tier != customerorder.TierConsolidated || g.ChosenDeliveryDay != c.day || (g.ConsolidationEligible == nil) != (c.eligible == nil) ||
			c.eligible != nil && *g.ConsolidationEligible != *c.eligible {
			t.Fatalf("%s: group = %+v", c.id, g)
		}
	}
}
