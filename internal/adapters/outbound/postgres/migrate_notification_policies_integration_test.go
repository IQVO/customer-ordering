//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/postgres"
	"github.com/claudioed/customer-ordering/internal/domain/notice"
	"github.com/claudioed/customer-ordering/internal/testing/pgtest"
)

// t0 is an arbitrary FIXED instant for the published version.
var t0 = time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)

// tableExists reports whether the table is there.
func tableExists(t *testing.T, url, table string) bool {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var ok bool
	if err := pool.QueryRow(context.Background(), `SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

// TestNotificationPoliciesMigration proves 0012 alone (ADR 0017): the table
// appears, the repository round-trips a version over it, and down removes it.
func TestNotificationPoliciesMigration(t *testing.T) {
	url := pgtest.EmptyURL(t)
	stepMigrations(t, url, 12) // 0001..0012: the schema with the policy table
	if !tableExists(t, url, "notification_policies") {
		t.Fatal("notification_policies must exist after 0012")
	}
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := postgres.NewPolicyRepository(pool)
	rows := notice.DefaultRows()
	for i, r := range rows {
		if r.Family == notice.FamilyGroupDelivered {
			rows[i].Notifier = notice.NotifierSeller
		}
	}
	p, err := notice.Publish(0, rows, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Version() != 1 || got.Notifier(notice.FamilyGroupDelivered) != notice.NotifierSeller {
		t.Fatalf("policy = v%d %+v", got.Version(), got.Snapshot().Rows)
	}
	stepMigrations(t, url, -1) // down: the table goes
	if tableExists(t, url, "notification_policies") {
		t.Fatal("notification_policies must be gone after down")
	}
	stepMigrations(t, url, 1) // and up brings it back
	if !tableExists(t, url, "notification_policies") {
		t.Fatal("notification_policies must exist after up")
	}
}
