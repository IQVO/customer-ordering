package ids_test

import (
	"regexp"
	"testing"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/ids"
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-([1-8])[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestOrderIDForIsTheStableUUIDv5OfTheKey(t *testing.T) {
	g := ids.Generator{}
	a, b := g.OrderIDFor("key-1"), g.OrderIDFor("key-1")
	if a != b || a == g.OrderIDFor("key-2") {
		t.Fatalf("ids = %q %q", a, g.OrderIDFor("key-2"))
	}
	if m := uuidRe.FindStringSubmatch(string(a)); m == nil || m[1] != "5" {
		t.Fatalf("%q is not a UUID v5", a)
	}
	// The namespace is part of the contract: retried checkouts must keep their order id.
	if a != "00f6d601-65ca-521b-887f-715cbd55458c" {
		t.Fatalf("the derived id changed: %s", a)
	}
}

func TestNewShopperIDIsAFreshUUIDv4(t *testing.T) {
	g := ids.Generator{}
	a, b := g.NewShopperID(), g.NewShopperID()
	if a == b {
		t.Fatal("two shoppers share an id")
	}
	if m := uuidRe.FindStringSubmatch(string(a)); m == nil || m[1] != "4" {
		t.Fatalf("%q is not a UUID v4", a)
	}
}
