//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/postgres"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
	"github.com/claudioed/customer-ordering/internal/testing/pgtest"
)

// stepMigrations moves the schema n migrations (negative = down) from the
// same files the binary embeds.
func stepMigrations(t *testing.T, url string, n int) {
	t.Helper()
	src, err := iofs.New(os.DirFS("migrations"), ".")
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Steps(n); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("steps %d: %v", n, err)
	}
}

func hasTimeZoneColumn(t *testing.T, url string) bool {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'order_groups' AND column_name = 'time_zone'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func zonedOrder(t *testing.T, id string) *customerorder.CustomerOrder {
	t.Helper()
	at := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	o, _, err := customerorder.Place(customerorder.NewOrder{
		ID: customerorder.ID(id), ShopperID: "shopper-1",
		ShipTo: shipto.ShipTo{Name: "Ana Example", Line1: "1 Sample Street", City: "Austin", Region: "TX", PostalCode: "78701", CountryCode: "US"},
		Lines:  []customerorder.OrderLine{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 1}},
		Groups: []customerorder.GroupInput{{
			GroupNo: 1, PromiseID: "p-" + id, Tier: customerorder.TierNextDay, WitnessNodeID: "fc-aus-1", ShipBy: at.Add(6 * time.Hour),
			DeliverByEarliest: time.Date(2026, 10, 31, 14, 0, 0, 0, time.UTC), DeliverByLatest: time.Date(2026, 11, 1, 2, 0, 0, 0, time.UTC),
			TimeZone: "America/Chicago", LineNos: []int{1}, DestinationPostalPrefix: "787",
		}},
	}, at)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	return o
}

// TestTimeZoneMigrationGoesDownAndUpKeepingTheGroups proves 0004 alone
// (ADR 0009): up adds order_groups.time_zone and a zone round-trips; down drops
// only the column (the group survives); up again brings it back NULL, which
// reads as an unknown zone (empty), the shape of a group stored before it.
func TestTimeZoneMigrationGoesDownAndUpKeepingTheGroups(t *testing.T) {
	url := pgtest.EmptyURL(t)
	if err := postgres.RunMigrations(url); err != nil {
		t.Fatalf("up: %v", err)
	}
	if !hasTimeZoneColumn(t, url) {
		t.Fatal("after up: order_groups has no time_zone column")
	}
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := postgres.NewOrderRepository(pool)
	if err := repo.Save(context.Background(), zonedOrder(t, "order-tz")); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := repo.Get(context.Background(), "order-tz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if z := got.Groups()[0].TimeZone; z != "America/Chicago" {
		t.Fatalf("stored zone read back as %q", z)
	}

	stepMigrations(t, url, -1)
	if hasTimeZoneColumn(t, url) {
		t.Fatal("after down of 0004: time_zone is still there")
	}
	stepMigrations(t, url, 1)
	if !hasTimeZoneColumn(t, url) {
		t.Fatal("after up again: order_groups has no time_zone column")
	}
	old, err := repo.Get(context.Background(), "order-tz")
	if err != nil {
		t.Fatalf("a group stored before the column must still load: %v", err)
	}
	if z := old.Groups()[0].TimeZone; z != "" {
		t.Fatalf("a group without a stored zone read back as %q, want empty (unknown)", z)
	}
}
