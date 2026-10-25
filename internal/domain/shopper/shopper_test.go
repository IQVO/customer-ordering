package shopper

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/domain/shared"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
)

var t0 = time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)

func addr() shipto.ShipTo {
	return shipto.ShipTo{Name: "Ana Example", Line1: "1 Sample Street", City: "Austin", Region: "TX", PostalCode: "78701", CountryCode: "US"}
}

func TestRegisterNormalisesAndStarts(t *testing.T) {
	brt := time.FixedZone("BRT", -3*3600)
	s, err := Register(NewShopper{ID: "shopper-1", Email: "  Ana.Example@Example.COM ", SavedAddresses: []SavedAddress{{ID: "addr-1", ShipTo: addr()}}}, t0.In(brt))
	if err != nil {
		t.Fatal(err)
	}
	if s.ID() != "shopper-1" || s.Email() != "ana.example@example.com" || s.Status() != StatusActive || s.Version() != 1 {
		t.Fatalf("unexpected shopper: %+v", s.Snapshot())
	}
	if s.RegisteredAt().Location() != time.UTC || !s.RegisteredAt().Equal(t0) {
		t.Fatalf("registeredAt must be UTC: %v", s.RegisteredAt())
	}
	if len(s.SavedAddresses()) != 1 || s.SavedAddresses()[0].ID != "addr-1" {
		t.Fatal("saved address lost")
	}
}

func TestRegisterRejects(t *testing.T) {
	bad := addr()
	bad.Name = ""
	many := make([]SavedAddress, MaxSavedAddresses+1)
	for i := range many {
		many[i] = SavedAddress{ID: AddressID(fmt.Sprintf("addr-%d", i)), ShipTo: addr()}
	}
	longLocal := strings.Repeat("a", MaxEmail-len("@e.com")+1) + "@e.com"
	cases := map[string]NewShopper{
		"bad id":          {ID: "-x", Email: "a@b.com"},
		"blank email":     {ID: "s1", Email: "  "},
		"no at":           {ID: "s1", Email: "ab.com"},
		"two ats":         {ID: "s1", Email: "a@b@c.com"},
		"no dot in host":  {ID: "s1", Email: "a@localhost"},
		"display name":    {ID: "s1", Email: "Ana <a@b.com>"},
		"too long":        {ID: "s1", Email: longLocal},
		"bad address":     {ID: "s1", Email: "a@b.com", SavedAddresses: []SavedAddress{{ID: "addr-1", ShipTo: bad}}},
		"too many saved":  {ID: "s1", Email: "a@b.com", SavedAddresses: many},
		"empty local":     {ID: "s1", Email: "@b.com"},
		"space in middle": {ID: "s1", Email: "a b@c.com"},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Register(in, t0); !errors.Is(err, shared.ErrValidation) {
				t.Fatalf("want ErrValidation, got %v", err)
			}
		})
	}
}

func TestRegisterAcceptsTheLimits(t *testing.T) {
	email := strings.Repeat("a", MaxEmail-len("@e.com")) + "@e.com"
	if len(email) != MaxEmail {
		t.Fatalf("test setup: %d", len(email))
	}
	many := make([]SavedAddress, MaxSavedAddresses)
	for i := range many {
		many[i] = SavedAddress{ID: AddressID(fmt.Sprintf("addr-%d", i)), ShipTo: addr()}
	}
	if _, err := Register(NewShopper{ID: "s1", Email: email, SavedAddresses: many}, t0); err != nil {
		t.Fatalf("limits are inclusive: %v", err)
	}
}

func TestRegisterCopiesTheSavedAddresses(t *testing.T) {
	list := []SavedAddress{{ID: "addr-1", ShipTo: addr()}}
	s, _ := Register(NewShopper{ID: "s1", Email: "a@b.com", SavedAddresses: list}, t0)
	list[0].ShipTo.Name = "changed"
	out := s.SavedAddresses()
	out[0].ShipTo.Name = "changed too"
	if s.SavedAddresses()[0].ShipTo.Name != "Ana Example" {
		t.Fatal("the aggregate must own its addresses")
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	s, _ := Register(NewShopper{ID: "s1", Email: "a@b.com", SavedAddresses: []SavedAddress{{ID: "addr-1", ShipTo: addr()}}}, t0)
	back, err := Restore(s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := back.Snapshot(), s.Snapshot(); got.ID != want.ID || got.Email != want.Email || got.Version != want.Version ||
		!got.RegisteredAt.Equal(want.RegisteredAt) || len(got.SavedAddresses) != 1 || got.Status != want.Status {
		t.Fatalf("round trip changed the shopper: %+v vs %+v", got, want)
	}
}

func TestRestoreBoundaries(t *testing.T) {
	good := Snapshot{ID: "s1", Email: "a@b.com", Status: StatusActive, RegisteredAt: t0, Version: 1}
	if _, err := Restore(good); err != nil {
		t.Fatalf("good snapshot rejected: %v", err)
	}
	badAddr := addr()
	badAddr.City = ""
	cases := map[string]func(*Snapshot){
		"bad id":         func(s *Snapshot) { s.ID = "" },
		"unnormalised":   func(s *Snapshot) { s.Email = "A@b.com" },
		"padded":         func(s *Snapshot) { s.Email = " a@b.com" },
		"invalid email":  func(s *Snapshot) { s.Email = "nope" },
		"status":         func(s *Snapshot) { s.Status = "DELETED" },
		"no registered":  func(s *Snapshot) { s.RegisteredAt = time.Time{} },
		"version zero":   func(s *Snapshot) { s.Version = 0 },
		"bad saved addr": func(s *Snapshot) { s.SavedAddresses = []SavedAddress{{ID: "addr-1", ShipTo: badAddr}} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := good
			mutate(&s)
			if _, err := Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) {
				t.Fatalf("want ErrInvalidSnapshot, got %v", err)
			}
		})
	}
	v2 := good
	v2.Version = 2
	if _, err := Restore(v2); err != nil {
		t.Fatalf("version 2 must restore: %v", err)
	}
}
