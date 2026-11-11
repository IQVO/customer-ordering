//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/postgres"
	"github.com/claudioed/customer-ordering/internal/testing/pgtest"
)

func etaColumnCount(t *testing.T, url string) int {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'tracking_packages' AND column_name IN ('eta_start', 'eta_end', 'eta_assigned_at')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestTrackingEtaMigration proves 0009 alone (ADR 0014): a package stored before
// it keeps loading with no window, the CHECK refuses a half-set or inverted
// window, down drops the three columns and up brings them back.
func TestTrackingEtaMigration(t *testing.T) {
	url := pgtest.EmptyURL(t)
	stepMigrations(t, url, 8) // 0001..0008: the schema before the window
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(context.Background(), `INSERT INTO tracking_packages (tracking_number, state) VALUES ('TRK-OLD', 'LABELLED')`); err != nil {
		t.Fatal(err)
	}
	stepMigrations(t, url, 1)
	if n := etaColumnCount(t, url); n != 3 {
		t.Fatalf("after up: %d eta columns, want 3", n)
	}
	var nulls int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM tracking_packages WHERE tracking_number = 'TRK-OLD' AND eta_start IS NULL AND eta_end IS NULL AND eta_assigned_at IS NULL`).Scan(&nulls); err != nil || nulls != 1 {
		t.Fatalf("a package stored before 0009 must have no window (%d, %v)", nulls, err)
	}
	for name, q := range map[string]string{
		"half set": `INSERT INTO tracking_packages (tracking_number, eta_start) VALUES ('TRK-H', now())`,
		"inverted": `INSERT INTO tracking_packages (tracking_number, eta_start, eta_end, eta_assigned_at) VALUES ('TRK-I', now() + interval '1 hour', now(), now())`,
		"empty":    `INSERT INTO tracking_packages (tracking_number, eta_start, eta_end, eta_assigned_at) VALUES ('TRK-E', now(), now(), now())`,
	} {
		if _, err := pool.Exec(context.Background(), q); err == nil {
			t.Fatalf("%s: the CHECK must refuse the window", name)
		}
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO tracking_packages (tracking_number, eta_start, eta_end, eta_assigned_at) VALUES ('TRK-OK', now(), now() + interval '2 hours', now())`); err != nil {
		t.Fatalf("a proper window must be accepted: %v", err)
	}
	stepMigrations(t, url, -1)
	if n := etaColumnCount(t, url); n != 0 {
		t.Fatalf("after down: %d eta columns remain", n)
	}
	stepMigrations(t, url, 1)
	if n := etaColumnCount(t, url); n != 3 {
		t.Fatalf("after up again: %d eta columns, want 3", n)
	}
}
