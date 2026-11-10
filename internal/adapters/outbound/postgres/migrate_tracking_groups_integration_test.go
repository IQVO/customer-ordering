//go:build integration

package postgres_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/postgres"
	"github.com/claudioed/customer-ordering/internal/testing/pgtest"
)

func queryGroups(t *testing.T, url string) []string {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	rows, err := pool.Query(context.Background(), `SELECT tracking_number || '/' || order_id || '/' || group_no FROM tracking_package_groups ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// TestTrackingGroupsMigrationBackfillsAndGoesDown proves 0007 alone (ADR 0012):
// up creates tracking_package_groups and copies the group of every package that
// knows one (a package with no group, or only an order, gets no row); down
// drops only that table, the package rows keep their first group.
func TestTrackingGroupsMigrationBackfillsAndGoesDown(t *testing.T) {
	url := pgtest.EmptyURL(t)
	stepMigrations(t, url, 6) // 0001..0006: the schema before the group set
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, q := range []string{
		`INSERT INTO tracking_packages (tracking_number, order_id, group_no) VALUES ('TRK-1', 'o-1', 1)`,
		`INSERT INTO tracking_packages (tracking_number, order_id, group_no) VALUES ('TRK-2', 'o-1', 2)`,
		`INSERT INTO tracking_packages (tracking_number, order_id) VALUES ('TRK-3', 'o-9')`,
		`INSERT INTO tracking_packages (tracking_number) VALUES ('TRK-4')`,
	} {
		if _, err := pool.Exec(context.Background(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	stepMigrations(t, url, 1)
	want := []string{"TRK-1/o-1/1", "TRK-2/o-1/2"}
	if got := queryGroups(t, url); !reflect.DeepEqual(got, want) {
		t.Fatalf("backfilled groups = %v, want %v", got, want)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO tracking_package_groups VALUES ('TRK-1', 'o-1', 1)`); err == nil {
		t.Fatal("the primary key must refuse a repeated (number, order, group)")
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO tracking_package_groups VALUES ('TRK-1', 'o-2', 1)`); err != nil {
		t.Fatalf("a second order may share the number: %v", err)
	}

	stepMigrations(t, url, -1)
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.tables WHERE table_name = 'tracking_package_groups'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("after down: %d tables named tracking_package_groups (%v)", n, err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM tracking_packages WHERE group_no IS NOT NULL`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("down must keep the first group of each package: %d rows (%v)", n, err)
	}
	stepMigrations(t, url, 1)
	if got := queryGroups(t, url); !reflect.DeepEqual(got, want) {
		t.Fatalf("up again re-derives the groups from the packages: %v, want %v", got, want)
	}
}
