package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
)

func TestCheckoutBySavedAddressCopiesItIntoTheOrder(t *testing.T) {
	f := newFx(t)
	addrID := addAddress(t, f, "shopper-1", "home")
	in := checkout("order-saved")
	in.ShipTo = shipto.ShipTo{}
	in.ShipToAddressID = addrID
	v, err := usecases.PlaceOrder{Deps: f.deps}.Execute(context.Background(), in)
	must(t, err)
	if v.OrderID != "order-saved" {
		t.Fatalf("view = %+v", v)
	}
	// The address is COPIED into the order: the stored order carries it even
	// though the request never named it.
	stored := f.order("order-saved")
	if stored.ShipTo() != address() {
		t.Fatalf("stored ship-to = %+v, want the saved address", stored.ShipTo())
	}
}

func TestCheckoutBySavedAddressRejects(t *testing.T) {
	f := newFx(t)
	other, err := registerSecond(t, f, "bob@example.com")
	must(t, err)
	foreign := addAddress(t, f, other, "theirs")
	cases := map[string]func(in *usecases.PlaceOrderInput){
		"unknown address id": func(in *usecases.PlaceOrderInput) { in.ShipTo = shipto.ShipTo{}; in.ShipToAddressID = "address-nope" },
		"another shopper's":  func(in *usecases.PlaceOrderInput) { in.ShipTo = shipto.ShipTo{}; in.ShipToAddressID = foreign },
		"both given":         func(in *usecases.PlaceOrderInput) { in.ShipToAddressID = foreign },
		"neither given":      func(in *usecases.PlaceOrderInput) { in.ShipTo = shipto.ShipTo{} },
		"unknown shopper": func(in *usecases.PlaceOrderInput) {
			in.ShopperID = "shopper-nope"
			in.ShipTo = shipto.ShipTo{}
			in.ShipToAddressID = foreign
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := checkout("order-" + name)
			mutate(&in)
			_, err := usecases.PlaceOrder{Deps: f.deps}.Execute(context.Background(), in)
			if !errors.Is(err, shared.ErrValidation) {
				t.Fatalf("want ErrValidation, got %v", err)
			}
			if got := f.store.Counts(); got.Orders != 0 || got.OutboxEvents != 0 {
				t.Fatalf("a refused checkout stored something: %+v", got)
			}
		})
	}
}
