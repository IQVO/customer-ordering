//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/postgres"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/testing/pgtest"
)

func consolidatedOrderOf(t *testing.T, id string, eligible *bool) *customerorder.CustomerOrder {
	t.Helper()
	o := zonedOrder(t, id)
	snap := o.Snapshot()
	g := snap.Groups[0]
	g.Tier, g.ChosenDeliveryDay, g.ConsolidationEligible = customerorder.TierConsolidated, "2026-10-31", eligible
	in := customerorder.NewOrder{
		ID: customerorder.ID(id), ShopperID: snap.ShopperID, ShipTo: snap.ShipTo, Lines: snap.Lines,
		Groups: []customerorder.GroupInput{{
			GroupNo: g.GroupNo, PromiseID: g.PromiseID, Tier: g.Tier, WitnessNodeID: g.WitnessNodeID, ShipBy: g.ShipBy,
			DeliverByEarliest: g.DeliverByEarliest, DeliverByLatest: g.DeliverByLatest, TimeZone: g.TimeZone, LineNos: g.LineNos,
			DestinationPostalPrefix: "787", ChosenDeliveryDay: g.ChosenDeliveryDay, ConsolidationEligible: g.ConsolidationEligible,
		}},
	}
	placed, _, err := customerorder.Place(in, snap.PlacedAt)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	return placed
}

// TestConsolidatedChoiceMigration proves 0008 alone (ADR 0013): up accepts the
// fifth tier and stores the choice (a day, true, false and unknown all
// round-trip); the CHECK still refuses a sixth value; down FAILS while a
// CONSOLIDATED group exists; on a database without one it succeeds, dropping the
// new columns, and up again brings them back.
func TestConsolidatedChoiceMigration(t *testing.T) {
	url := pgtest.EmptyURL(t)
	if err := postgres.RunMigrations(url); err != nil {
		t.Fatalf("up: %v", err)
	}
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := postgres.NewOrderRepository(pool)
	yes, no := true, false
	for id, eligible := range map[string]*bool{"order-ce-yes": &yes, "order-ce-no": &no, "order-ce-unknown": nil} {
		if err := repo.Save(context.Background(), consolidatedOrderOf(t, id, eligible)); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
		got, err := repo.Get(context.Background(), customerorder.ID(id))
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		g := got.Groups()[0]
		if g.Tier != customerorder.TierConsolidated || g.ChosenDeliveryDay != "2026-10-31" || (g.ConsolidationEligible == nil) != (eligible == nil) ||
			eligible != nil && *g.ConsolidationEligible != *eligible {
			t.Fatalf("%s read back as %+v", id, g)
		}
	}
	if _, err := pool.Exec(context.Background(), `UPDATE order_groups SET tier = 'WEEKLY'`); err == nil {
		t.Fatal("the tier CHECK must still refuse a sixth value")
	}

	// Rolling back is refused while a CONSOLIDATED group exists: a promise is never rewritten.
	if err := postgres.RollbackMigrations(url); err == nil {
		t.Fatal("down must fail while a CONSOLIDATED group exists")
	}
	// The failed down leaves this database dirty; the clean down is proved on a fresh one.
	url = pgtest.EmptyURL(t)
	if err := postgres.RunMigrations(url); err != nil {
		t.Fatalf("up: %v", err)
	}
	stepMigrations(t, url, -1)
	var n int
	pool2, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool2.Close()
	if err := pool2.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'order_groups' AND column_name IN ('chosen_delivery_day', 'consolidation_eligible')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("after down: %d choice columns remain (%v)", n, err)
	}
	stepMigrations(t, url, 1)
	if err := pool2.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'order_groups' AND column_name IN ('chosen_delivery_day', 'consolidation_eligible')`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("after up again: %d choice columns (%v)", n, err)
	}
}
