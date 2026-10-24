// Package repocontract is the repository contract suite: the behaviours every
// persistence adapter (the in-memory doubles and Postgres) must show. It is
// test support (it imports "testing") kept out of _test.go files so both
// adapters can run it; the shipped binary never links it (enforced by
// internal/architecture).
package repocontract

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

// Env is what an adapter hands to the suite. Every test gets a fresh Env.
type Env struct {
	Orders      ports.OrderRepository
	Shoppers    ports.ShopperRepository
	Tracking    ports.TrackingRepository
	Inbox       ports.Inbox
	Outbox      ports.EventOutbox
	UoW         ports.UnitOfWork
	Idempotency ports.IdempotencyStore
	// Clock is the store's time authority: testIdempotency stamps records
	// with it, so the expired-record case expires relative to the SAME clock
	// the implementation under test judges TTL with (the memory double is
	// driven by a frozen clock; the Postgres store by the database's now()).
	Clock ports.Clock
	// OutboxCount returns how many events the outbox holds.
	OutboxCount func(t *testing.T) int
}

// Run executes the whole suite; newEnv must return an empty Env each time.
func Run(t *testing.T, newEnv func(t *testing.T) Env) {
	t.Helper()
	suites := map[string]func(*testing.T, Env){
		"orders":      testOrders,
		"shoppers":    testShoppers,
		"tracking":    testTracking,
		"inbox":       testInbox,
		"unitofwork":  testUnitOfWork,
		"idempotency": testIdempotency,
	}
	for name, fn := range suites {
		t.Run(name, func(t *testing.T) { fn(t, newEnv(t)) })
	}
}

var t0 = time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)

// at returns t0 plus m minutes.
func at(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }

func address() shipto.ShipTo {
	return shipto.ShipTo{
		Name: "Ana Example", Line1: "1 Sample Street", Line2: "Apt 2", City: "Austin", Region: "TX",
		PostalCode: "78701", CountryCode: "US", Phone: "+1 555 0100",
	}
}

func groupInput(no int, promise string, lineNos ...int) customerorder.GroupInput {
	return customerorder.GroupInput{
		GroupNo: no, PromiseID: promise, Tier: customerorder.TierNextDay, WitnessNodeID: "fc-aus-1", ShipBy: at(600),
		DeliverByEarliest: at(1200), DeliverByLatest: at(1500), LineNos: lineNos, DestinationPostalPrefix: "787",
	}
}

// newOrder builds a PLACED order of three lines in two groups.
func newOrder(t *testing.T, id string) *customerorder.CustomerOrder {
	t.Helper()
	o, _, err := customerorder.Place(customerorder.NewOrder{
		ID: customerorder.ID(id), ShopperID: "shopper-1", ShipTo: address(),
		Lines:  []customerorder.OrderLine{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2}, {LineNo: 2, CatalogItemID: "sku-200", Quantity: 1}, {LineNo: 3, CatalogItemID: "sku-300", Quantity: 5}},
		Groups: []customerorder.GroupInput{groupInput(1, "p-"+id+"-1", 1, 2), groupInput(2, "p-"+id+"-2", 3)},
	}, t0)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	return o
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("want %v, got %v", want, got)
	}
}

var bg = context.Background

func newShopper(t *testing.T, id, email string) *shopper.Shopper {
	t.Helper()
	s, err := shopper.Register(shopper.NewShopper{ID: shopper.ID(id), Email: email, SavedAddresses: []shipto.ShipTo{address()}}, t0)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return s
}
