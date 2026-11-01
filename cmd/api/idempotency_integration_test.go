//go:build integration

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	inboundhttp "github.com/claudioed/customer-ordering/internal/adapters/inbound/http"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/clock"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/deliverypromise"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/ids"
	outboundkafka "github.com/claudioed/customer-ordering/internal/adapters/outbound/kafka"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/postgres"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/testing/pgtest"
)

// The Idempotency-Key contract (REST guideline 2) over the REAL router and REAL
// Postgres: the key row commits in the same transaction as the aggregate and its
// outbox rows, a replay is byte-identical, and concurrent requests with one key
// have exactly one effect.

// flakyPromise is a delivery-promise fake that answers 500 while fail is set.
type flakyPromise struct {
	srv  *httptest.Server
	fail atomic.Bool
}

func newFlakyPromise(t *testing.T) *flakyPromise {
	t.Helper()
	f := &flakyPromise{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		raw, _ := io.ReadAll(r.Body)
		switch {
		case f.fail.Load():
			w.WriteHeader(http.StatusInternalServerError)
		case r.URL.Path == "/promises":
			offer := "offer-1"
			if strings.Contains(string(raw), "offer-2") {
				offer = "offer-2"
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"promiseId":"pr-%s","offerId":%q,"state":"PROVISIONAL","destinationPostalPrefix":"787",
			 "terms":{"tier":"NEXT_DAY","shipBy":"2026-10-24T10:00:00Z","deliverByEarliest":"2026-10-25T10:00:00Z","deliverByLatest":"2026-10-25T20:00:00Z"},
			 "witnessPath":{"nodeId":"fc-aus-1"}}`, offer, offer)
		case strings.HasSuffix(r.URL.Path, ":confirm"), strings.HasSuffix(r.URL.Path, "/confirmations"):
			_, _ = w.Write([]byte(`{"promiseId":"x","state":"CONFIRMED"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// idemAPI is the real router over a real Postgres pool.
type idemAPI struct {
	svc   *service
	gw    *flakyPromise
	query func(sql string, dest ...any) error
}

func newIdemAPI(t *testing.T) *idemAPI {
	t.Helper()
	pool := pgtest.NewPool(t)
	gw := newFlakyPromise(t)
	logger := slog.New(slog.DiscardHandler)
	ad := adapters{pool: pool, idempotency: postgres.NewIdempotencyStore(pool), clock: clock.System{}, ids: ids.Generator{}}
	ad.outbox = postgres.NewOutboxRepo(pool, outboundkafka.NewEncoder())
	ad.deps = usecases.Deps{
		Orders: postgres.NewOrderRepository(pool), Shoppers: postgres.NewShopperRepository(pool), Tracking: postgres.NewTrackingRepository(pool),
		Inbox: postgres.NewInbox(pool), Outbox: ad.outbox, UoW: postgres.NewUnitOfWork(pool),
		Promises: deliverypromise.New(gw.srv.URL, &http.Client{}), Clock: ad.clock, IDs: ad.ids, Log: logger,
	}
	srv := httptest.NewServer(inboundhttp.NewRouter(buildServer(ad, nil), logger, ""))
	t.Cleanup(srv.Close)
	query := func(sql string, dest ...any) error { return pool.QueryRow(context.Background(), sql).Scan(dest...) }
	return &idemAPI{svc: &service{base: srv.URL}, gw: gw, query: query}
}

func (a *idemAPI) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := a.query("SELECT count(*) FROM "+table, &n); err != nil {
		t.Fatal(err)
	}
	return n
}

const registerAna = `{"email":"ana@example.com"}`

func TestIdempotencyKeyReplayAndReuseOverRealPostgres(t *testing.T) {
	a := newIdemAPI(t)
	first := a.svc.call(t, "POST", "/shoppers", "reg-1", registerAna)
	if first.status != 201 || first.header.Get("Idempotency-Replayed") != "" {
		t.Fatalf("fresh key = %d %s replayed=%q", first.status, first.body, first.header.Get("Idempotency-Replayed"))
	}
	again := a.svc.call(t, "POST", "/shoppers", "reg-1", registerAna)
	if again.status != 201 || again.body != first.body || again.header.Get("Idempotency-Replayed") != "true" ||
		again.header.Get("Content-Type") != first.header.Get("Content-Type") {
		t.Fatalf("replay = %d %s replayed=%q", again.status, again.body, again.header.Get("Idempotency-Replayed"))
	}
	if n := a.count(t, "shoppers"); n != 1 {
		t.Fatalf("%d shoppers after a replay, want 1", n)
	}
	for name, c := range map[string]struct{ path, body string }{
		"another body":  {"/shoppers", `{"email":"bia@example.com"}`},
		"another route": {"/shoppers/x/addresses", `{}`},
	} {
		r := a.svc.call(t, "POST", c.path, "reg-1", c.body)
		if r.status != 409 || r.field(t, "type") != "/problems/idempotency-key-reused" {
			t.Fatalf("%s under the same key = %d %s, want 409 idempotency-key-reused", name, r.status, r.body)
		}
	}
	if n := a.count(t, "shoppers"); n != 1 {
		t.Fatalf("%d shoppers after the refused reuse, want 1", n)
	}
}

func TestIdempotencyKeyIsRequiredAndValidatedOverRealPostgres(t *testing.T) {
	a := newIdemAPI(t)
	if r := a.svc.call(t, "POST", "/shoppers", "", registerAna); r.status != 400 || r.field(t, "type") != "/problems/idempotency-key-required" {
		t.Fatalf("missing key = %d %s", r.status, r.body)
	}
	if r := a.svc.call(t, "POST", "/shoppers", strings.Repeat("k", 129), registerAna); r.status != 400 || r.field(t, "type") != "/problems/idempotency-key-invalid" {
		t.Fatalf("129 character key = %d %s", r.status, r.body)
	}
	if n := a.count(t, "idempotency_keys"); n != 0 {
		t.Fatalf("%d key rows after refused keys, want 0", n)
	}
}

func TestConcurrentRequestsWithOneKeyHaveOneEffect(t *testing.T) {
	a := newIdemAPI(t)
	const callers = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		bodies  = map[string]int{}
		replays int
	)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := a.svc.call(t, "POST", "/shoppers", "race-1", registerAna)
			mu.Lock()
			defer mu.Unlock()
			if r.status != 201 {
				t.Errorf("concurrent request = %d %s", r.status, r.body)
			}
			bodies[r.body]++
			if r.header.Get("Idempotency-Replayed") == "true" {
				replays++
			}
		}()
	}
	wg.Wait()
	if len(bodies) != 1 || replays != callers-1 {
		t.Fatalf("%d distinct bodies, %d replays of %d callers", len(bodies), replays, callers)
	}
	if n := a.count(t, "shoppers"); n != 1 {
		t.Fatalf("%d shopper rows, want exactly one", n)
	}
	if n := a.count(t, "idempotency_keys"); n != 1 {
		t.Fatalf("%d key rows, want exactly one", n)
	}
}

func TestA4xxIsCachedAndA5xxIsNotStored(t *testing.T) {
	a := newIdemAPI(t)
	bad := a.svc.call(t, "POST", "/shoppers", "bad-1", `{"email":"not-an-email"}`)
	if bad.status != 422 {
		t.Fatalf("invalid e-mail = %d %s", bad.status, bad.body)
	}
	if again := a.svc.call(t, "POST", "/shoppers", "bad-1", `{"email":"not-an-email"}`); again.status != 422 || again.body != bad.body || again.header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("a 4xx must be replayed: %d %s replayed=%q", again.status, again.body, again.header.Get("Idempotency-Replayed"))
	}

	reg := a.svc.call(t, "POST", "/shoppers", "reg-ok", registerAna)
	shopperID, _ := reg.field(t, "shopperId").(string)
	if reg.status != 201 || shopperID == "" {
		t.Fatalf("register = %d %s", reg.status, reg.body)
	}
	a.gw.fail.Store(true)
	body := fmt.Sprintf(checkout, shopperID)
	if down := a.svc.call(t, "POST", "/checkout", "co-1", body); down.status < 500 {
		t.Fatalf("delivery-promise down = %d %s, want a 5xx", down.status, down.body)
	}
	if n := a.count(t, "idempotency_keys"); n != 2 {
		t.Fatalf("%d key rows, want 2 (the cached 4xx and the registration; a 5xx is never stored)", n)
	}
	if n := a.count(t, "customer_orders") + a.count(t, "outbox_events"); n != 0 {
		t.Fatalf("a 5xx left %d aggregate/outbox rows behind", n)
	}
	a.gw.fail.Store(false)
	retry := a.svc.call(t, "POST", "/checkout", "co-1", body)
	if retry.status != 201 || retry.header.Get("Idempotency-Replayed") != "" {
		t.Fatalf("the retry after a 5xx must run again: %d %s replayed=%q", retry.status, retry.body, retry.header.Get("Idempotency-Replayed"))
	}
	if n := a.count(t, "customer_orders"); n != 1 {
		t.Fatalf("%d orders after the retry, want 1", n)
	}
	if n := a.count(t, "outbox_events"); n != 1 {
		t.Fatalf("%d outbox rows after the retry, want 1 (committed with the order)", n)
	}
}
