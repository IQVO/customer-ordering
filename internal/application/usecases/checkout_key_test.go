package usecases_test

import (
	"context"
	"strings"
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
)

// recordingKeyer returns a fixed valid key and remembers what it was asked.
type recordingKeyer struct{ shopper, address string }

func (k *recordingKeyer) KeyFor(shopperID, normalizedShipTo string) string {
	k.shopper, k.address = shopperID, normalizedShipTo
	return strings.Repeat("c0", 32)
}

func placedEvent(t *testing.T, f *fx) customerorder.CustomerOrderPlaced {
	t.Helper()
	events := f.store.Outbox().Events()
	if len(events) != 1 {
		t.Fatalf("outbox holds %d events, want 1", len(events))
	}
	placed, ok := events[0].(customerorder.CustomerOrderPlaced)
	if !ok {
		t.Fatalf("event %T is not CustomerOrderPlaced", events[0])
	}
	return placed
}

func TestCheckoutDerivesTheConsolidationKeyFromShopperAndShipTo(t *testing.T) {
	f := newFx(t)
	keyer := &recordingKeyer{}
	f.deps.ConsolidationKeys = keyer
	if _, err := (usecases.PlaceOrder{Deps: f.deps}).Execute(context.Background(), checkout("order-key")); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if keyer.shopper != "shopper-1" || keyer.address != address().NormalizedAddress() {
		t.Fatalf("keyer saw %q / %q", keyer.shopper, keyer.address)
	}
	if got := placedEvent(t, f).ConsolidationKey; got != strings.Repeat("c0", 32) {
		t.Fatalf("CustomerOrderPlaced.ConsolidationKey = %q", got)
	}
	o, err := f.store.Orders().Get(context.Background(), "order-key")
	must(t, err)
	if o.ConsolidationKey() != strings.Repeat("c0", 32) {
		t.Fatalf("stored key = %q", o.ConsolidationKey())
	}
}

func TestCheckoutWithoutAKeyerDerivesNone(t *testing.T) {
	f := newFx(t)
	if _, err := (usecases.PlaceOrder{Deps: f.deps}).Execute(context.Background(), checkout("order-nokey")); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := placedEvent(t, f).ConsolidationKey; got != "" {
		t.Fatalf("a service without a secret derived key %q", got)
	}
}

func TestCheckoutBySavedAddressKeysTheCopiedAddress(t *testing.T) {
	f := newFx(t)
	keyer := &recordingKeyer{}
	f.deps.ConsolidationKeys = keyer
	addrID := addAddress(t, f, "shopper-1", "home")
	in := checkout("order-saved-key")
	in.ShipTo, in.ShipToAddressID = shipto.ShipTo{}, addrID
	if _, err := (usecases.PlaceOrder{Deps: f.deps}).Execute(context.Background(), in); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if keyer.address != address().NormalizedAddress() {
		t.Fatalf("the keyer must see the copied saved address %q, saw %q", address().NormalizedAddress(), keyer.address)
	}
}
