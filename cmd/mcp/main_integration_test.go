//go:build integration

package main

import (
	"context"
	"os"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/customer-ordering/internal/adapters/inbound/mcp"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/postgres"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
	"github.com/claudioed/customer-ordering/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}

// The binary migrates a database itself (idempotently: pgtest's databases
// are already migrated, a second run must be a no-op), then reads through MCP
// tools exactly what checkout's domain Place persisted, and its reads add no
// outbox row.
func TestMCPAgainstPostgres_ReadsWhatTheAPIWroteAndNeverWrites(t *testing.T) {
	url := pgtest.URL(t) // a fresh, already migrated database
	ctx := context.Background()

	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	countOutbox := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events`).Scan(&n); err != nil {
			t.Fatalf("count outbox_events: %v", err)
		}
		return n
	}

	seedOrder(t, url)
	before := countOutbox()

	deps, closeFn, err := buildDeps(ctx, quietLogger(), url, url)
	if err != nil {
		t.Fatalf("buildDeps: %v", err)
	}
	t.Cleanup(closeFn)

	ct, st := sdk.NewInMemoryTransports()
	ss, err := inboundmcp.NewServer(deps).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	assertSeededReads(t, session)

	if after := countOutbox(); after != before {
		t.Fatalf("outbox rows %d -> %d: the MCP binary wrote an event", before, after)
	}
}

// seedOrder places ord-it-1 the way checkout does (the domain Place the
// PlaceOrder use case drives, persisted through the Postgres repository the
// command side owns).
func seedOrder(t *testing.T, url string) {
	t.Helper()
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	t0 := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	group := func(no int, promise string, lineNos ...int) customerorder.GroupInput {
		return customerorder.GroupInput{
			GroupNo: no, PromiseID: promise, Tier: customerorder.TierNextDay, WitnessNodeID: "fc-aus-1",
			ShipBy: t0.Add(600 * time.Second), DeliverByEarliest: t0.Add(1200 * time.Second),
			DeliverByLatest: t0.Add(1500 * time.Second), LineNos: lineNos, DestinationPostalPrefix: "787",
		}
	}
	o, _, err := customerorder.Place(customerorder.NewOrder{
		ID: "ord-it-1", ShopperID: "shopper-1",
		ShipTo: shipto.ShipTo{
			Name: "Ana Example", Line1: "1 Sample Street", City: "Austin", Region: "TX",
			PostalCode: "78701", CountryCode: "US",
		},
		Lines:  []customerorder.OrderLine{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2}, {LineNo: 2, CatalogItemID: "sku-200", Quantity: 1}},
		Groups: []customerorder.GroupInput{group(1, "p-it-1", 1), group(2, "p-it-2", 2)},
	}, t0)
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	if err := postgres.NewOrderRepository(pool).Save(ctx, o); err != nil {
		t.Fatalf("save order: %v", err)
	}
}

// assertSeededReads reads back, through every MCP tool, what seedOrder
// persisted.
func assertSeededReads(t *testing.T, session *sdk.ClientSession) {
	t.Helper()
	ctx := context.Background()
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s: err=%v res=%+v", name, err, res)
		}
		return res.StructuredContent.(map[string]any)
	}

	order := call("get_order", map[string]any{"orderId": "ord-it-1"})
	if order["orderId"] != "ord-it-1" || order["status"] != "PLACED" || order["shopperId"] != "shopper-1" {
		t.Fatalf("get_order = %v", order)
	}
	if groups := order["groups"].([]any); len(groups) != 2 || groups[0].(map[string]any)["promiseId"] != "p-it-1" {
		t.Fatalf("get_order groups = %v", order["groups"])
	}
	if lines := order["lines"].([]any); len(lines) != 2 || lines[0].(map[string]any)["quantity"] != 2.0 {
		t.Fatalf("get_order lines = %v", order["lines"])
	}

	tracking := call("get_order_tracking", map[string]any{"orderId": "ord-it-1"})
	if tracking["status"] != "PLACED" {
		t.Fatalf("get_order_tracking = %v", tracking)
	}
	if groups := tracking["groups"].([]any); len(groups) != 2 {
		t.Fatalf("get_order_tracking groups = %v", tracking["groups"])
	}

	shipTo := call("get_ship_to", map[string]any{"orderId": "ord-it-1"})
	if shipTo["city"] != "Austin" || shipTo["postalCode"] != "78701" || shipTo["name"] != "Ana Example" {
		t.Fatalf("get_ship_to = %v", shipTo)
	}
}
