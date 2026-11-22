//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/analyticsstore"
	"github.com/claudioed/customer-ordering/internal/analytics/report"
	"github.com/claudioed/customer-ordering/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}

func getJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		return 0, nil
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func waitReady(t *testing.T, url string) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if code, _ := getJSON(t, url); code == http.StatusOK {
			return
		}
	}
	t.Fatal("/readyz never answered 200")
}

// The real entry point over a projected analytical database: /readyz, a
// report and the freshness answer from it; a cancelled context drains cleanly.
func TestReportsServeTheProjectionAndStopCleanly(t *testing.T) {
	url := pgtest.EmptyURL(t)
	if err := analyticsstore.RunMigrations(url); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	writer, err := analyticsstore.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	placed := time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC)
	if _, err := analyticsstore.NewProjection(writer).Apply(ctx, report.Event{Kind: report.KindOrderPlaced, EventID: "e1",
		OrderID: "o1", At: placed.Add(time.Hour), OccurredAt: placed, GroupCount: 2, PickupPoint: true}); err != nil {
		t.Fatal(err)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	env := map[string]string{"ANALYTICS_READER_DATABASE_URL": url, "HTTP_ADDR": addr, "LOG_LEVEL": "error"}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(runCtx, func(k string) string { return env[k] }) }()

	waitReady(t, "http://"+addr+"/readyz")
	code, body := getJSON(t, "http://"+addr+"/reports/ordering-performance?from=2026-11-03&to=2026-11-03")
	days, _ := body["days"].([]any)
	if code != http.StatusOK || len(days) != 1 || days[0].(map[string]any)["orders_placed"] != 1.0 || days[0].(map[string]any)["split_shipment_share"] != 1.0 {
		t.Fatalf("ordering-performance = %d %v", code, body)
	}
	if code, body := getJSON(t, "http://"+addr+"/reports/freshness"); code != http.StatusOK || body["as_of"] != "2026-11-03T12:00:00Z" {
		t.Fatalf("freshness = %d %v", code, body)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a cancelled reports server must drain cleanly, got %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the reports server did not stop")
	}
}
