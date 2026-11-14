package usecases_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/promising"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
)

// pickupTerms is what delivery-promise answers for a promise quoted against
// the point node-pnt-7 (its own prefix 070, ADR 0016).
func pickupTerms(promise string) promising.Terms {
	t := terms(promise, "fc-pnt-7")
	t.DestinationPostalPrefix = "070"
	return t
}

// pickupCheckout is checkout() with the point as the destination.
func pickupCheckout(orderID string) usecases.PlaceOrderInput {
	in := checkout(orderID)
	in.ShipTo = shipto.ShipTo{}
	in.PickupPointID = "node-pnt-7"
	return in
}

// placePickup checks a pickup order out and returns its id.
func (f *fx) placePickup() string {
	f.t.Helper()
	f.seq++
	id := fmt.Sprintf("order-pickup-%d", f.seq)
	f.gw.Terms["offer-1"] = pickupTerms(fmt.Sprintf("pp-%d-1", f.seq))
	f.gw.Terms["offer-2"] = pickupTerms(fmt.Sprintf("pp-%d-2", f.seq))
	_, err := usecases.PlaceOrder{Deps: f.deps}.Execute(context.Background(), pickupCheckout(id))
	must(f.t, err)
	return id
}

func TestCheckoutToAPickupPointPlacesTheOrderOnThePromisePrefix(t *testing.T) {
	f := newFx(t)
	id := f.placePickup()
	o := f.order(id)
	if o.PickupPointID() != "node-pnt-7" || o.DestinationKind() != "PICKUP_POINT" {
		t.Fatalf("pickup destination lost: %+v", o.Snapshot())
	}
	if o.ShipTo() != (shipto.ShipTo{}) || o.DestinationPostalPrefix() != "070" {
		t.Fatalf("a pickup order has no ship-to and the promise's prefix: %+v", o.Snapshot())
	}
	if ev := placedEvent(t, f); ev.PickupPointID != "node-pnt-7" || ev.DestinationPostalPrefix != "070" {
		t.Fatalf("CustomerOrderPlaced carries the point and the promise's prefix: %+v", ev)
	}
}

func TestCheckoutPickupRefusesAPromiseOfAnotherPoint(t *testing.T) {
	f := newFx(t)
	other := pickupTerms("pp-other")
	other.PickupPointID = "node-pnt-9" // the promise names another point
	f.gw.Terms["offer-1"] = other
	_, err := usecases.PlaceOrder{Deps: f.deps}.Execute(context.Background(), pickupCheckout("order-mismatch"))
	if !errors.Is(err, customerorder.ErrPromiseDestinationMismatch) {
		t.Fatalf("want promise-destination-mismatch, got %v", err)
	}
	if _, err := f.store.Orders().Get(context.Background(), "order-mismatch"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("no order may be stored, got %v", err)
	}
}

func TestCheckoutPickupDerivesTheKeyOverShopperAndPoint(t *testing.T) {
	f := newFx(t)
	keyer := &recordingKeyer{}
	f.deps.ConsolidationKeys = keyer
	id := f.placePickup()
	if keyer.shopper != "shopper-1" || keyer.address != "point:node-pnt-7" {
		t.Fatalf("the pickup key runs over shopper + point, saw %q / %q", keyer.shopper, keyer.address)
	}
	if o := f.order(id); o.ConsolidationKey() != strings.Repeat("c0", 32) {
		t.Fatalf("stored key = %q", o.ConsolidationKey())
	}
}

func TestCheckoutDestinationChoiceIsExactlyOne(t *testing.T) {
	base := pickupCheckout("order-choice")
	cases := []struct {
		name string
		set  func(in usecases.PlaceOrderInput) usecases.PlaceOrderInput
	}{
		{"no destination at all", func(in usecases.PlaceOrderInput) usecases.PlaceOrderInput {
			in.PickupPointID = ""
			return in
		}},
		{"an inline address too", func(in usecases.PlaceOrderInput) usecases.PlaceOrderInput {
			in.ShipTo = address()
			return in
		}},
		{"a saved address too", func(in usecases.PlaceOrderInput) usecases.PlaceOrderInput {
			in.ShipToAddressID = "addr-1"
			return in
		}},
		{"all three", func(in usecases.PlaceOrderInput) usecases.PlaceOrderInput {
			in.ShipTo = address()
			in.ShipToAddressID = "addr-1"
			return in
		}},
	}
	for _, c := range cases {
		u := usecases.PlaceOrder{Deps: newFx(t).deps}
		if _, err := u.Execute(context.Background(), c.set(base)); !errors.Is(err, shared.ErrValidation) {
			t.Fatalf("%s: want validation-failed, got %v", c.name, err)
		}
	}
}
