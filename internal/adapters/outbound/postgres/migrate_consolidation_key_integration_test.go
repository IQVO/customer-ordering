//go:build integration

package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/postgres"
	"github.com/claudioed/customer-ordering/internal/testing/pgtest"
)

const migrationKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func hasConsolidationKeyColumn(t *testing.T, url string) bool {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'customer_orders' AND column_name = 'consolidation_key'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// TestConsolidationKeyMigrationGoesDownAndUpKeepingTheOrders proves 0006 alone
// (ADR 0012): up adds customer_orders.consolidation_key and a key round-trips;
// the CHECK refuses a malformed value; down drops only the column (the order
// survives); up again brings it back NULL, which reads as no key.
func TestConsolidationKeyMigrationGoesDownAndUpKeepingTheOrders(t *testing.T) {
	url := pgtest.EmptyURL(t)
	if err := postgres.RunMigrations(url); err != nil {
		t.Fatalf("up: %v", err)
	}
	if !hasConsolidationKeyColumn(t, url) {
		t.Fatal("after up: customer_orders has no consolidation_key column")
	}
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := postgres.NewOrderRepository(pool)
	keyed := zonedOrderKeyed(t, "order-key", migrationKey)
	if err := repo.Save(context.Background(), keyed); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := repo.Get(context.Background(), "order-key")
	if err != nil || got.ConsolidationKey() != migrationKey {
		t.Fatalf("get = %v, key %q, want %q", err, got.ConsolidationKey(), migrationKey)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE customer_orders SET consolidation_key = $1`, strings.ToUpper(migrationKey)); err == nil {
		t.Fatal("the CHECK must refuse an upper-case key")
	}

	// 0009 (tracking_package_eta) is the top migration now: step past it, 0008
	// (group_consolidated_choice), 0007 (tracking_package_groups) and 0006.
	stepMigrations(t, url, -5)
	if hasConsolidationKeyColumn(t, url) {
		t.Fatal("after down past 0006: consolidation_key is still there")
	}
	stepMigrations(t, url, 4)
	old, err := repo.Get(context.Background(), "order-key")
	if err != nil {
		t.Fatalf("an order stored before the column must still load: %v", err)
	}
	if old.ConsolidationKey() != "" {
		t.Fatalf("an order without a stored key read back as %q, want empty", old.ConsolidationKey())
	}
}
