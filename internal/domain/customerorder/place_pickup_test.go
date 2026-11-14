package customerorder

import (
	"errors"
	"testing"

	"github.com/claudioed/customer-ordering/internal/domain/shipto"
)

// pickupOrder is a one-group pickup checkout whose promise was quoted against
// point node-pnt-7 (prefix 070, ADR 0016).
func pickupOrder() NewOrder {
	g := groupInput(1, "p-1", 1)
	g.DestinationPostalPrefix = "070"
	return NewOrder{
		ID: "order-1", ShopperID: "shopper-1", PickupPointID: "node-pnt-7",
		Lines: []OrderLine{{1, "sku-100", 2}}, Groups: []GroupInput{g},
	}
}

func TestPlacePickupOrderTakesThePromisePrefixAndRaisesThePoint(t *testing.T) {
	o, evs, err := Place(pickupOrder(), t0)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if o.PickupPointID() != "node-pnt-7" || o.DestinationKind() != "PICKUP_POINT" {
		t.Fatalf("pickup identity lost: %+v", o.Snapshot())
	}
	if o.ShipTo() != (shipto.ShipTo{}) {
		t.Fatalf("a pickup order carries no ship-to: %+v", o.ShipTo())
	}
	if o.DestinationPostalPrefix() != "070" {
		t.Fatalf("the prefix is the promise's (the point's own), got %q", o.DestinationPostalPrefix())
	}
	ev, ok := evs[0].(CustomerOrderPlaced)
	if !ok || ev.PickupPointID != "node-pnt-7" || ev.DestinationPostalPrefix != "070" {
		t.Fatalf("the placed event must carry the point and the promise's prefix: %+v", ev)
	}
	// Snapshot/Restore keep the destination (the four-places rule, ADR 0016).
	r, err := Restore(o.Snapshot())
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if r.PickupPointID() != "node-pnt-7" || r.DestinationPostalPrefix() != "070" || r.ShipTo() != (shipto.ShipTo{}) {
		t.Fatalf("restore lost the pickup destination: %+v", r.Snapshot())
	}
}

func TestPlacePickupRefusesAWrongDestination(t *testing.T) {
	cases := []struct {
		name string
		in   func(NewOrder) NewOrder
		want error
	}{
		{"no destination at all", func(in NewOrder) NewOrder {
			in.PickupPointID = ""
			return in
		}, nil},
		{"both destinations", func(in NewOrder) NewOrder {
			in.ShipTo = goodShipTo()
			return in
		}, nil},
		{"bad point id", func(in NewOrder) NewOrder {
			in.PickupPointID = "not a valid id"
			return in
		}, nil},
		{"promise names no prefix", func(in NewOrder) NewOrder {
			in.Groups[0].DestinationPostalPrefix = ""
			return in
		}, nil},
		{"promise names another point", func(in NewOrder) NewOrder {
			in.Groups[0].PickupPointID = "node-pnt-9"
			return in
		}, ErrPromiseDestinationMismatch},
		{"promise names a point on a ship-to order", func(in NewOrder) NewOrder {
			in.PickupPointID = ""
			in.ShipTo = goodShipTo()
			in.Groups[0].PickupPointID = "node-pnt-7"
			return in
		}, ErrPromiseDestinationMismatch},
	}
	for _, c := range cases {
		_, _, err := Place(c.in(pickupOrder()), t0)
		switch {
		case c.want == nil && err == nil:
			t.Fatalf("%s: want an error", c.name)
		case c.want == nil:
			if errors.Is(err, ErrPromiseDestinationMismatch) {
				t.Fatalf("%s: a shape error is fine but not a destination mismatch: %v", c.name, err)
			}
		case !errors.Is(err, c.want):
			t.Fatalf("%s: want %v, got %v", c.name, c.want, err)
		}
	}
}

func TestPlacePickupGroupsMustShareThePointsPrefix(t *testing.T) {
	in := pickupOrder()
	second := groupInput(2, "p-2", 2)
	second.DestinationPostalPrefix = "999" // quoted against another destination
	in.Lines = append(in.Lines, OrderLine{2, "sku-200", 1})
	in.Groups = append(in.Groups, second)
	if _, _, err := Place(in, t0); !errors.Is(err, ErrPromiseDestinationMismatch) {
		t.Fatalf("two promises of two prefixes must refuse, got %v", err)
	}
	second.DestinationPostalPrefix = "070"
	in.Groups[1] = second
	o, _, err := Place(in, t0)
	if err != nil {
		t.Fatalf("Place with agreeing prefixes: %v", err)
	}
	if o.DestinationPostalPrefix() != "070" {
		t.Fatalf("prefix wrong: %q", o.DestinationPostalPrefix())
	}
}

func TestRestoreRefusesTwoDestinations(t *testing.T) {
	o, _, err := Place(pickupOrder(), t0)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	s := o.Snapshot()
	s.ShipTo = goodShipTo()
	if _, err := Restore(s); err == nil {
		t.Fatal("a snapshot with both destinations must be refused")
	}
	s = o.Snapshot()
	s.PickupPointID = ""
	if _, err := Restore(s); err == nil {
		t.Fatal("a pickup snapshot without the point (and without a ship-to) must be refused")
	}
}
