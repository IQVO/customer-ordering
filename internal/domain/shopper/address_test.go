package shopper

import (
	"errors"
	"fmt"
	"testing"

	"github.com/claudioed/customer-ordering/internal/domain/shared"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
)

func registered(t *testing.T) *Shopper {
	t.Helper()
	s, err := Register(NewShopper{ID: "shopper-1", Email: "ana@example.com"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAddSavedAddressAppendsAndBumps(t *testing.T) {
	s := registered(t)
	if err := s.AddSavedAddress("addr-1", addr()); err != nil {
		t.Fatal(err)
	}
	if s.Version() != 2 {
		t.Fatalf("version = %d, want 2", s.Version())
	}
	got := s.SavedAddresses()
	if len(got) != 1 || got[0].ID != "addr-1" || got[0].ShipTo != addr() {
		t.Fatalf("addresses = %+v", got)
	}
	back, err := Restore(s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if len(back.SavedAddresses()) != 1 || back.SavedAddresses()[0].ID != "addr-1" {
		t.Fatalf("restored addresses = %+v", back.SavedAddresses())
	}
}

func TestAddSavedAddressIsIdempotentPerID(t *testing.T) {
	s := registered(t)
	if err := s.AddSavedAddress("addr-1", addr()); err != nil {
		t.Fatal(err)
	}
	other := addr()
	other.City = "Dallas"
	// A replay of the same Idempotency-Key derives the same address id (A24):
	// the second add is a no-op, even with a different body.
	if err := s.AddSavedAddress("addr-1", other); err != nil {
		t.Fatal(err)
	}
	if s.Version() != 2 || len(s.SavedAddresses()) != 1 || s.SavedAddresses()[0].ShipTo.City != "Austin" {
		t.Fatalf("replay changed the shopper: %+v", s.Snapshot())
	}
}

func TestAddSavedAddressRejectsTheTwentyFirst(t *testing.T) {
	s := registered(t)
	for i := 0; i < MaxSavedAddresses; i++ {
		if err := s.AddSavedAddress(AddressID(fmt.Sprintf("addr-%d", i)), addr()); err != nil {
			t.Fatalf("address %d of %d must fit: %v", i+1, MaxSavedAddresses, err)
		}
	}
	err := s.AddSavedAddress("addr-one-too-many", addr())
	if !errors.Is(err, ErrTooManyAddresses) {
		t.Fatalf("want ErrTooManyAddresses, got %v", err)
	}
	if len(s.SavedAddresses()) != MaxSavedAddresses || s.Version() != int64(1+MaxSavedAddresses) {
		t.Fatalf("the refusal must not change the shopper: %+v", s.Snapshot())
	}
}

func TestAddSavedAddressValidates(t *testing.T) {
	bad := addr()
	bad.Name = ""
	cases := map[string]struct {
		id AddressID
		a  shipto.ShipTo
	}{
		"bad id":      {"", addr()},
		"bad address": {"addr-1", bad},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := registered(t)
			if err := s.AddSavedAddress(c.id, c.a); !errors.Is(err, shared.ErrValidation) {
				t.Fatalf("want ErrValidation, got %v", err)
			}
			if s.Version() != 1 || len(s.SavedAddresses()) != 0 {
				t.Fatal("a refused add must not change the shopper")
			}
		})
	}
}

func TestAddressLooksUpByID(t *testing.T) {
	s := registered(t)
	if err := s.AddSavedAddress("addr-1", addr()); err != nil {
		t.Fatal(err)
	}
	a, ok := s.Address("addr-1")
	if !ok || a != addr() {
		t.Fatalf("Address = %v %v", a, ok)
	}
	if _, ok := s.Address("addr-nope"); ok {
		t.Fatal("an unknown id must miss")
	}
}

func TestRegisterRejectsBadAddressIDs(t *testing.T) {
	dup := []SavedAddress{{ID: "addr-1", ShipTo: addr()}, {ID: "addr-1", ShipTo: addr()}}
	cases := map[string][]SavedAddress{
		"empty id":   {{ID: "", ShipTo: addr()}},
		"invalid id": {{ID: "-x", ShipTo: addr()}},
		"duplicated": dup,
	}
	for name, list := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Register(NewShopper{ID: "s1", Email: "a@b.com", SavedAddresses: list}, t0); !errors.Is(err, shared.ErrValidation) {
				t.Fatalf("want ErrValidation, got %v", err)
			}
		})
	}
}

func TestRestoreRejectsBadAddressIDs(t *testing.T) {
	good := Snapshot{ID: "s1", Email: "a@b.com", Status: StatusActive, RegisteredAt: t0, Version: 1}
	cases := map[string][]SavedAddress{
		"empty id":   {{ID: "", ShipTo: addr()}},
		"duplicated": {{ID: "addr-1", ShipTo: addr()}, {ID: "addr-1", ShipTo: addr()}},
	}
	for name, list := range cases {
		t.Run(name, func(t *testing.T) {
			s := good
			s.SavedAddresses = list
			if _, err := Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) {
				t.Fatalf("want ErrInvalidSnapshot, got %v", err)
			}
		})
	}
}
