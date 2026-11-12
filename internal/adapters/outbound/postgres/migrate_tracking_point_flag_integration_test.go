//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/postgres"
	"github.com/claudioed/customer-ordering/internal/testing/pgtest"
)

func pointFlagColumnCount(t *testing.T, url string) int {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'tracking_packages' AND column_name IN ('point_flag_reason', 'point_flagged_at')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestTrackingPointFlagMigration proves 0010 alone (ADR 0015): a package stored
// before it keeps loading with no flag, the CHECK refuses a half-set flag, a
// reason outside the two published and a missing instant, down drops the two
// columns and up brings them back.
func TestTrackingPointFlagMigration(t *testing.T) {
	url := pgtest.EmptyURL(t)
	stepMigrations(t, url, 9) // 0001..0009: the schema before the advisory
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(context.Background(), `INSERT INTO tracking_packages (tracking_number, state) VALUES ('TRK-OLD', 'LABELLED')`); err != nil {
		t.Fatal(err)
	}
	stepMigrations(t, url, 1)
	if n := pointFlagColumnCount(t, url); n != 2 {
		t.Fatalf("after up: %d point-flag columns, want 2", n)
	}
	var nulls int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM tracking_packages WHERE tracking_number = 'TRK-OLD' AND point_flag_reason IS NULL AND point_flagged_at IS NULL`).Scan(&nulls); err != nil || nulls != 1 {
		t.Fatalf("a package stored before 0010 must have no flag (%d, %v)", nulls, err)
	}
	for name, q := range map[string]string{
		"half set":     `INSERT INTO tracking_packages (tracking_number, point_flag_reason) VALUES ('TRK-H', 'NOT_HOME')`,
		"no instant":   `INSERT INTO tracking_packages (tracking_number, point_flag_reason, point_flagged_at) VALUES ('TRK-N', 'NOT_HOME', NULL)`,
		"bad reason":   `INSERT INTO tracking_packages (tracking_number, point_flag_reason, point_flagged_at) VALUES ('TRK-B', 'SNOWSTORM', now())`,
		"reason alone": `INSERT INTO tracking_packages (tracking_number, point_flagged_at) VALUES ('TRK-T', now())`,
	} {
		if _, err := pool.Exec(context.Background(), q); err == nil {
			t.Fatalf("%s: the CHECK must refuse the flag", name)
		}
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO tracking_packages (tracking_number, point_flag_reason, point_flagged_at) VALUES ('TRK-OK', 'ACCESS_PROBLEM', now())`); err != nil {
		t.Fatalf("a proper flag must be accepted: %v", err)
	}
	stepMigrations(t, url, -1)
	if n := pointFlagColumnCount(t, url); n != 0 {
		t.Fatalf("after down: %d point-flag columns, want 0", n)
	}
	stepMigrations(t, url, 1)
	if n := pointFlagColumnCount(t, url); n != 2 {
		t.Fatalf("after up again: %d point-flag columns, want 2", n)
	}
}
