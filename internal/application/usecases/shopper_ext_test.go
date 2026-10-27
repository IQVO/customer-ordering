package usecases_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

func addAddress(t *testing.T, f *fx, shopperID, key string) string {
	t.Helper()
	v, err := usecases.AddSavedAddress{Deps: f.deps}.Execute(context.Background(), usecases.AddSavedAddressInput{
		ShopperID: shopperID, Address: address(), IdempotencyKey: key,
	})
	must(t, err)
	return v.AddressID
}

func TestAddSavedAddress(t *testing.T) {
	f := newFx(t)
	id := addAddress(t, f, "shopper-1", "key-1")
	if id == "" {
		t.Fatal("no addressId returned")
	}
	s, err := f.store.Shoppers().Get(context.Background(), "shopper-1")
	must(t, err)
	if len(s.SavedAddresses()) != 1 || s.SavedAddresses()[0].ID != shopper.AddressID(id) || s.SavedAddresses()[0].ShipTo != address() {
		t.Fatalf("addresses = %+v", s.SavedAddresses())
	}
	if s.Version() != 2 {
		t.Fatalf("version = %d, want 2", s.Version())
	}
}

func TestAddSavedAddressDerivesTheIDFromTheKey(t *testing.T) {
	f := newFx(t)
	first := addAddress(t, f, "shopper-1", "same-key")
	// A replay after the idempotency record expired derives the same id and
	// stays a no-op: still one address, no concurrent-update failure.
	v, err := usecases.AddSavedAddress{Deps: f.deps}.Execute(context.Background(), usecases.AddSavedAddressInput{
		ShopperID: "shopper-1", Address: address(), IdempotencyKey: "same-key",
	})
	must(t, err)
	if v.AddressID != first {
		t.Fatalf("the same key must derive the same id: %q vs %q", v.AddressID, first)
	}
	s, err := f.store.Shoppers().Get(context.Background(), "shopper-1")
	must(t, err)
	if len(s.SavedAddresses()) != 1 {
		t.Fatalf("a replay added an address: %+v", s.SavedAddresses())
	}
}

func TestAddSavedAddressUnknownShopperIsNotFound(t *testing.T) {
	f := newFx(t)
	_, err := usecases.AddSavedAddress{Deps: f.deps}.Execute(context.Background(), usecases.AddSavedAddressInput{
		ShopperID: "shopper-nope", Address: address(), IdempotencyKey: "k",
	})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestAddSavedAddressCapAndValidation(t *testing.T) {
	f := newFx(t)
	for i := 0; i < shopper.MaxSavedAddresses; i++ {
		addAddress(t, f, "shopper-1", fmt.Sprintf("key-%d", i))
	}
	_, err := usecases.AddSavedAddress{Deps: f.deps}.Execute(context.Background(), usecases.AddSavedAddressInput{
		ShopperID: "shopper-1", Address: address(), IdempotencyKey: "key-one-too-many",
	})
	if !errors.Is(err, shopper.ErrTooManyAddresses) {
		t.Fatalf("want ErrTooManyAddresses, got %v", err)
	}
	bad := address()
	bad.Name = ""
	_, err = usecases.AddSavedAddress{Deps: f.deps}.Execute(context.Background(), usecases.AddSavedAddressInput{
		ShopperID: "shopper-1", Address: bad, IdempotencyKey: "key-bad",
	})
	if !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}

func TestGetShopperReturnsTheAddresses(t *testing.T) {
	f := newFx(t)
	addAddress(t, f, "shopper-1", "key-1")
	v, err := usecases.GetShopper{Deps: f.deps}.Execute(context.Background(), "shopper-1")
	must(t, err)
	if v.ShopperID != "shopper-1" || v.Email != "ana@example.com" || v.Status != "ACTIVE" || v.RegisteredAt.IsZero() {
		t.Fatalf("view = %+v", v)
	}
	if len(v.SavedAddresses) != 1 || v.SavedAddresses[0].PostalCode != "78701" || v.SavedAddresses[0].AddressID == "" {
		t.Fatalf("addresses = %+v", v.SavedAddresses)
	}
	if _, err := (usecases.GetShopper{Deps: f.deps}).Execute(context.Background(), "shopper-nope"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestFindShopperByEmailNormalises(t *testing.T) {
	f := newFx(t)
	v, err := usecases.FindShopperByEmail{Deps: f.deps}.Execute(context.Background(), "  ANA@Example.com ")
	must(t, err)
	if v.ShopperID != "shopper-1" {
		t.Fatalf("view = %+v", v)
	}
	for _, email := range []string{"bob@example.com", "", "   "} {
		if _, err := (usecases.FindShopperByEmail{Deps: f.deps}).Execute(context.Background(), email); !errors.Is(err, ports.ErrNotFound) {
			t.Fatalf("%q: want ErrNotFound, got %v", email, err)
		}
	}
}

// registerSecond registers one more shopper and returns its id.
func registerSecond(t *testing.T, f *fx, email string) (string, error) {
	t.Helper()
	v, err := usecases.RegisterShopper{Deps: f.deps}.Execute(context.Background(), usecases.RegisterShopperInput{Email: email})
	return v.ShopperID, err
}
