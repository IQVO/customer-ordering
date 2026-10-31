//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	inbounda2a "github.com/claudioed/customer-ordering/internal/adapters/inbound/a2a"
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

// The binary migrates a database itself (idempotently: pgtest's databases are
// already migrated, a second run must be a no-op), then answers over the real
// JSON-RPC router exactly what checkout's domain Place persisted, and its
// reads add no outbox row: the agent is read-only, so there is nothing for a
// relay to publish from this binary.
func TestA2AAgainstPostgres_ReadsWhatTheAPIWroteAndNeverWrites(t *testing.T) {
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

	srv := httptest.NewServer(newAgent(deps, inbounda2a.Config{AgentURL: "http://placeholder.invalid/", Version: "it"}, quietLogger(), "customer-ordering-a2a"))
	t.Cleanup(srv.Close)
	client, err := a2aclient.NewFromCard(ctx, inbounda2a.Card(inbounda2a.Config{AgentURL: srv.URL + "/", Version: "it"}))
	if err != nil {
		t.Fatal(err)
	}

	assertSeededReads(t, client)

	if after := countOutbox(); after != before {
		t.Fatalf("outbox rows %d -> %d: the A2A binary wrote an event", before, after)
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

// envelopeOf sends one skill call and returns the decoded envelope (a
// test-local shape) and everything the agent put on the wire.
func envelopeOf(t *testing.T, client *a2aclient.Client, skill, orderID string) (map[string]any, string) {
	t.Helper()
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": map[string]any{"orderId": orderID}}))
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		t.Fatalf("%s: %v", skill, err)
	}
	reply, ok := resp.(*a2a.Message)
	if !ok || len(reply.Parts) < 2 {
		t.Fatalf("%s: reply = %T", skill, resp)
	}
	data, ok := reply.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("%s: first part is %T", skill, reply.Parts[0].Content)
	}
	raw, _ := json.Marshal(data.Value)
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	return env, string(raw)
}

// assertSeededReads reads back, through both skills, what seedOrder persisted.
func assertSeededReads(t *testing.T, client *a2aclient.Client) {
	t.Helper()

	env, raw := envelopeOf(t, client, "get_order", "ord-it-1")
	order, _ := env["data"].(map[string]any)["order"].(map[string]any)
	if env["problem"] != nil || order["orderId"] != "ord-it-1" || order["status"] != "PLACED" || order["shopperId"] != "shopper-1" {
		t.Fatalf("get_order = %s", raw)
	}
	if groups := order["groups"].([]any); len(groups) != 2 || groups[0].(map[string]any)["promiseId"] != "p-it-1" {
		t.Fatalf("get_order groups = %v", order["groups"])
	}
	if lines := order["lines"].([]any); len(lines) != 2 || lines[0].(map[string]any)["quantity"] != 2.0 {
		t.Fatalf("get_order lines = %v", order["lines"])
	}
	if strings.Contains(raw, "Austin") || strings.Contains(raw, "78701") || strings.Contains(raw, "Sample Street") {
		t.Fatalf("get_order leaks the ship-to: %s", raw)
	}

	env, raw = envelopeOf(t, client, "get_order_tracking", "ord-it-1")
	tracking, _ := env["data"].(map[string]any)["tracking"].(map[string]any)
	if env["problem"] != nil || tracking["status"] != "PLACED" || len(tracking["groups"].([]any)) != 2 {
		t.Fatalf("get_order_tracking = %s", raw)
	}

	env, raw = envelopeOf(t, client, "get_order", "ord-it-ghost")
	problem, _ := env["problem"].(map[string]any)
	if problem["status"] != 404.0 || problem["type"] != "/problems/not-found" {
		t.Fatalf("an unknown order must be a 404 not-found problem, got %s", raw)
	}
}
