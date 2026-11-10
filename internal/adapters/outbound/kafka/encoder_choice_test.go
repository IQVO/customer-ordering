package kafka_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
)

// consolidatedOrder places an order whose group 1 is a CONSOLIDATED choice with
// a day and a true answer, group 2 a CONSOLIDATED choice with a stated false and
// no day, group 3 a classic tier (ADR 0013).
func consolidatedOrder(t *testing.T) []shared.Event {
	t.Helper()
	yes, no := true, false
	group := func(no int, promise string, tier customerorder.Tier, line int) customerorder.GroupInput {
		return customerorder.GroupInput{
			GroupNo: no, PromiseID: promise, Tier: tier, WitnessNodeID: "fc-aus-1", ShipBy: t0.Add(12 * time.Hour),
			DeliverByEarliest: t0.Add(72 * time.Hour), DeliverByLatest: t0.Add(84 * time.Hour), LineNos: []int{line}, DestinationPostalPrefix: "787",
		}
	}
	first := group(1, "p-2001", customerorder.TierConsolidated, 1)
	first.TimeZone, first.ChosenDeliveryDay, first.ConsolidationEligible = "America/Chicago", "2026-10-26", &yes
	second := group(2, "p-2002", customerorder.TierConsolidated, 2)
	second.ConsolidationEligible = &no
	_, evs, err := customerorder.Place(customerorder.NewOrder{
		ID: "7a1e6c52-0d2b-4f0e-9d6a-5c6f1b8e2a10", ShopperID: "3b0c1f0e-6f0a-4a39-8a55-2b7f0d8c9e11",
		ShipTo: shipto.ShipTo{Name: "Ana Example", Line1: "1 Sample Street", City: "Austin", Region: "TX", PostalCode: "78701", CountryCode: "US"},
		Lines:  []customerorder.OrderLine{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2}, {LineNo: 2, CatalogItemID: "sku-200", Quantity: 1}, {LineNo: 3, CatalogItemID: "sku-300", Quantity: 5}},
		Groups: []customerorder.GroupInput{first, second, group(3, "p-2003", customerorder.TierStandard, 3)},
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func TestCustomerOrderPlacedConsolidatedGolden(t *testing.T) {
	msg := encodeOne(t, consolidatedOrder(t)[0])
	assertGolden(t, "CustomerOrderPlaced.consolidated", msg.Value)
	assertEnvelope(t, msg, "CustomerOrderPlaced")
}

// TestAnUnknownChoiceIsOmittedAndAStatedFalseIsNot pins the reading rule of
// ADR 0013: an absent consolidationEligible is unknown, never false.
func TestAnUnknownChoiceIsOmittedAndAStatedFalseIsNot(t *testing.T) {
	var env struct {
		Data struct {
			Groups []map[string]json.RawMessage `json:"groups"`
		} `json:"data"`
	}
	if err := json.Unmarshal(encodeOne(t, consolidatedOrder(t)[0]).Value, &env); err != nil {
		t.Fatal(err)
	}
	g := env.Data.Groups
	if string(g[0]["tier"]) != `"CONSOLIDATED"` || string(g[0]["chosenDeliveryDay"]) != `"2026-10-26"` || string(g[0]["consolidationEligible"]) != "true" {
		t.Errorf("group 1 = %v", g[0])
	}
	if string(g[1]["consolidationEligible"]) != "false" {
		t.Errorf("a stated false must be on the wire: %v", g[1])
	}
	if _, ok := g[1]["chosenDeliveryDay"]; ok {
		t.Errorf("an unknown day must be omitted: %v", g[1])
	}
	for _, field := range []string{"chosenDeliveryDay", "consolidationEligible"} {
		if _, ok := g[2][field]; ok {
			t.Errorf("a classic group carries %s: %v", field, g[2])
		}
	}
	_, evs := placedOrder(t)
	if raw := string(encodeOne(t, evs[0]).Value); strings.Contains(raw, "chosenDeliveryDay") || strings.Contains(raw, "consolidationEligible") {
		t.Errorf("an order without a choice must not mention it: %s", raw)
	}
}
