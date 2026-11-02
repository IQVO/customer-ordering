//go:build integration

package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	inboundhttp "github.com/claudioed/customer-ordering/internal/adapters/inbound/http"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/clock"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/postgres"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/postgres/pgtx"
	"github.com/claudioed/customer-ordering/internal/testing/pgtest"
)

// poisonTx simulates what a mapped conflict does to the middleware's
// transaction: a statement fails on the server (a unique violation, like a lost
// race on a unique index), the handler maps it to a typed answer, and the
// transaction is ABORTED from then on. The middleware must still record and
// return a truthful 4xx, never a 500.
func poisonTx(t *testing.T, r *http.Request) {
	t.Helper()
	tx, ok := pgtx.From(r.Context())
	if !ok {
		t.Error("the store did not bind a transaction into the request context")
		return
	}
	for range 2 {
		if _, err := tx.Exec(r.Context(), `INSERT INTO idempotency_keys (route, key, request_hash, status, content_type, body, created_at) VALUES ('poison', 'dup', 'h', 200, '', ''::bytea, now())`); err != nil {
			return // the second insert aborts the transaction (23505)
		}
	}
	t.Error("the duplicate insert did not abort the transaction")
}

func poisonKeyed(key string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/checkout", nil)
	req.Header.Set(inboundhttp.IdempotencyKeyHeader, key)
	return req
}

// A 409 from an aborted transaction is committed and replayed; the handler
// never re-runs for that key.
func TestIdempotencyA409FromAnAbortedTransactionCommitsTheOutcome(t *testing.T) {
	pool := pgtest.NewPool(t)
	var calls atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		poisonTx(t, r)
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"type":"/problems/some-conflict","title":"Conflict","status":409}`))
	})
	mw := inboundhttp.RequireIdempotencyKey(postgres.NewIdempotencyStore(pool), clock.System{})(handler)

	first := httptest.NewRecorder()
	mw.ServeHTTP(first, poisonKeyed("k-poison"))
	if first.Code != http.StatusConflict {
		t.Fatalf("first = %d %s, want the truthful 409", first.Code, first.Body.String())
	}
	var status int
	if err := pool.QueryRow(t.Context(), `SELECT status FROM idempotency_keys WHERE key = 'k-poison'`).Scan(&status); err != nil || status != http.StatusConflict {
		t.Fatalf("the 409 outcome was not committed: status=%d err=%v", status, err)
	}
	var leaked int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM idempotency_keys WHERE route = 'poison'`).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("the handler's partial writes survived: %d rows, err=%v", leaked, err)
	}

	replay := httptest.NewRecorder()
	mw.ServeHTTP(replay, poisonKeyed("k-poison"))
	if replay.Code != http.StatusConflict || replay.Header().Get(inboundhttp.ReplayedHeader) != "true" {
		t.Fatalf("replay = %d replayed=%q, want the cached 409", replay.Code, replay.Header().Get(inboundhttp.ReplayedHeader))
	}
	if replay.Body.String() != first.Body.String() {
		t.Fatalf("replay body = %s, want %s", replay.Body.String(), first.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1", calls.Load())
	}
}

// A 2xx from an aborted transaction is a lie: 500, nothing cached, and the same
// key retries the real work.
func TestIdempotencyA2xxFromAnAbortedTransactionIsNeverCached(t *testing.T) {
	pool := pgtest.NewPool(t)
	var calls atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			poisonTx(t, r) // only the first attempt dies inside the transaction
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"orderId":"o1"}`))
	})
	mw := inboundhttp.RequireIdempotencyKey(postgres.NewIdempotencyStore(pool), clock.System{})(handler)

	first := httptest.NewRecorder()
	mw.ServeHTTP(first, poisonKeyed("k-lie"))
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("first = %d %s, want 500 (the writes never happened)", first.Code, first.Body.String())
	}
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM idempotency_keys WHERE key = 'k-lie'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a failed success left %d idempotency rows (err=%v): the key could not retry", n, err)
	}

	second := httptest.NewRecorder()
	mw.ServeHTTP(second, poisonKeyed("k-lie"))
	if second.Code != http.StatusCreated {
		t.Fatalf("retry = %d %s, want the real work to succeed", second.Code, second.Body.String())
	}
	if calls.Load() != 2 {
		t.Fatalf("handler ran %d times, want 2", calls.Load())
	}
}
