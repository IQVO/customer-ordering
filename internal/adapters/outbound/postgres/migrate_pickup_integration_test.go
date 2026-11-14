//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/postgres"
	"github.com/claudioed/customer-ordering/internal/testing/pgtest"
)

func pickupColumnCount(t *testing.T, url, table string) int {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns
		WHERE table_name = $1 AND column_name IN ('pickup_point_id', 'ready_for_collection_at', 'hold_until', 'collected_at', 'proof_code')`, table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestOrderPickupDestinationMigration proves 0011 alone (ADR 0016): an order
// stored before it keeps loading with no point, the CHECK refuses two
// destinations and none at all, the tracking CHECKs refuse a half-set deposit
// and a proof without a collection, down removes the point (and the pickup
// orders) and up brings them back.
func TestOrderPickupDestinationMigration(t *testing.T) {
	url := pgtest.EmptyURL(t)
	stepMigrations(t, url, 10) // 0001..0010: the schema before the pickup destination
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const oldOrder = `INSERT INTO customer_orders (order_id, shopper_id, ship_to, destination_postal_prefix, status, placed_at, version)
		VALUES ('ord-old', 'shopper-1', '{"name":"Ana Example","line1":"1 Sample Street","city":"Austin","region":"TX","postalCode":"78701","countryCode":"US"}', '787', 'PLACED', now(), 1)`
	if _, err := pool.Exec(context.Background(), oldOrder); err != nil {
		t.Fatal(err)
	}
	stepMigrations(t, url, 1)
	if n := pickupColumnCount(t, url, "customer_orders"); n != 1 {
		t.Fatalf("after up: %d pickup columns on customer_orders, want 1", n)
	}
	if n := pickupColumnCount(t, url, "tracking_packages"); n != 5 {
		t.Fatalf("after up: %d pickup columns on tracking_packages, want 5", n)
	}
	var untouched int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM customer_orders WHERE order_id = 'ord-old' AND pickup_point_id IS NULL AND ship_to IS NOT NULL`).Scan(&untouched); err != nil || untouched != 1 {
		t.Fatalf("an order stored before 0011 must keep its ship-to (%d, %v)", untouched, err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO customer_orders (order_id, shopper_id, ship_to, pickup_point_id, destination_postal_prefix, status, placed_at, version)
		VALUES ('ord-both', 'shopper-1', '{}'::jsonb, 'node-pnt-7', '070', 'PLACED', now(), 1)`); err == nil {
		t.Fatal("the CHECK must refuse two destinations")
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO customer_orders (order_id, shopper_id, ship_to, pickup_point_id, destination_postal_prefix, status, placed_at, version)
		VALUES ('ord-none', 'shopper-1', NULL, NULL, '070', 'PLACED', now(), 1)`); err == nil {
		t.Fatal("the CHECK must refuse no destination at all")
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO customer_orders (order_id, shopper_id, ship_to, pickup_point_id, destination_postal_prefix, status, placed_at, version)
		VALUES ('ord-pickup', 'shopper-1', NULL, 'node-pnt-7', '070', 'PLACED', now(), 1)`); err != nil {
		t.Fatalf("a pickup order must be accepted: %v", err)
	}
	for name, q := range map[string]string{
		"ready without hold":       `INSERT INTO tracking_packages (tracking_number, pickup_point_id, ready_for_collection_at) VALUES ('TRK-H', 'node-pnt-7', now())`,
		"proof without collection": `INSERT INTO tracking_packages (tracking_number, proof_code) VALUES ('TRK-P', '4821')`,
	} {
		if _, err := pool.Exec(context.Background(), q); err == nil {
			t.Fatalf("%s: the CHECK must refuse the row", name)
		}
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO tracking_packages (tracking_number, pickup_point_id, ready_for_collection_at, hold_until, state)
		VALUES ('TRK-OK', 'node-pnt-7', now(), now() + interval '7 days', 'READY_FOR_COLLECTION')`); err != nil {
		t.Fatalf("a proper deposit must be accepted: %v", err)
	}
	stepMigrations(t, url, -1)
	if n := pickupColumnCount(t, url, "customer_orders"); n != 0 {
		t.Fatalf("after down: %d pickup columns on customer_orders, want 0", n)
	}
	var left int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM customer_orders`).Scan(&left); err != nil || left != 1 {
		t.Fatalf("after down only the ship-to order remains (%d, %v)", left, err)
	}
	stepMigrations(t, url, 1)
	if n := pickupColumnCount(t, url, "tracking_packages"); n != 5 {
		t.Fatalf("after up again: %d pickup columns on tracking_packages, want 5", n)
	}
}
