package repocontract

import (
	"reflect"
	"testing"

	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
)

// ordersCancelledRoundTrip is the cancelledAt contract: the derived status and
// its instant survive a save and a read.
func ordersCancelledRoundTrip(t *testing.T, e Env) {
	t.Helper()
	o := newOrder(t, "order-cancelled")
	must(t, e.Orders.Save(bg(), o))
	_, err := o.CancelOrder("x", at(5))
	must(t, err)
	must(t, o.ConfirmGroupCancelled(1, at(6)))
	must(t, o.ConfirmGroupCancelled(2, at(7)))
	must(t, e.Orders.Save(bg(), o))
	got, err := e.Orders.Get(bg(), "order-cancelled")
	must(t, err)
	if got.Status() != customerorder.StatusCancelled || !got.CancelledAt().Equal(at(7)) {
		t.Fatalf("status/cancelledAt = %s %v", got.Status(), got.CancelledAt())
	}
}

// ordersPickupRoundTrip is the destination contract of ADR 0016: a pickup
// order (a point, no ship-to, the promise's prefix) reads back identically.
func ordersPickupRoundTrip(t *testing.T, e Env) {
	t.Helper()
	o, _, err := customerorder.Place(customerorder.NewOrder{
		ID: "order-pickup-rt", ShopperID: choiceShopper, PickupPointID: "node-pnt-7",
		Lines:  []customerorder.OrderLine{{LineNo: 1, CatalogItemID: choiceSKU, Quantity: 2}},
		Groups: []customerorder.GroupInput{pickupGroupInput(1, "p-order-pickup-rt-1", 1)},
	}, at(0))
	must(t, err)
	must(t, e.Orders.Save(bg(), o))
	got, err := e.Orders.Get(bg(), "order-pickup-rt")
	must(t, err)
	if !reflect.DeepEqual(got.Snapshot(), o.Snapshot()) {
		t.Fatalf("round trip changed the pickup order:\n got %+v\nwant %+v", got.Snapshot(), o.Snapshot())
	}
	if got.PickupPointID() != "node-pnt-7" || got.ShipTo() != (shipto.ShipTo{}) {
		t.Fatalf("the pickup destination did not survive: %+v", got.Snapshot())
	}
}
