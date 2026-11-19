//go:build integration

package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// The replay classes (ADR 0019) over the REAL router and REAL Postgres: a
// RETRYABLE refusal rolls the request back, key row included, so the same key
// retries the work; an OUTCOME refusal commits what the command did together
// with its stored answer.

// keyRows counts the rows the store holds for key.
func (a *idemAPI) keyRows(t *testing.T, key string) int {
	t.Helper()
	var n int
	if err := a.query(fmt.Sprintf("SELECT count(*) FROM idempotency_keys WHERE key = '%s'", key), &n); err != nil {
		t.Fatal(err)
	}
	return n
}

// policyBody is a whole notification-policy document whose families all name notifier.
func policyBody(notifier string) string {
	families := []string{"DELAY_EXPECTED", "ETA_WINDOW_ASSIGNED", "DELIVERY_POINT_ADVISORY", "GROUP_SHIPPED", "GROUP_DELIVERED", "GROUP_LOST", "GROUP_RETURNED"}
	rows := make([]string, len(families))
	for i, f := range families {
		rows[i] = fmt.Sprintf(`{"family":%q,"notifier":%q}`, f, notifier)
	}
	return `{"rows":[` + strings.Join(rows, ",") + `]}`
}

// concurrent-update is RETRYABLE: the loser of an optimistic-lock race leaves
// no key row, and its retry under the same key executes, is stored, then replayed.
func TestAConcurrentUpdateRefusalIsNotStoredSoItsKeyRetries(t *testing.T) {
	a := newIdemAPI(t)
	const callers = 16
	notifiers := []string{"MARKETPLACE", "SELLER", "NOBODY"}
	var loserKey, loserBody string
	for round := 0; round < 30 && loserKey == ""; round++ {
		var (
			wg sync.WaitGroup
			mu sync.Mutex
		)
		for i := range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				key, body := fmt.Sprintf("pol-%d-%d", round, i), policyBody(notifiers[i%len(notifiers)])
				r := a.svc.call(t, "PUT", "/notification-policy", key, body)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case r.status == 200:
				case r.status == 409 && r.field(t, "type") == "/problems/concurrent-update":
					loserKey, loserBody = key, body
				default:
					t.Errorf("publish %s = %d %s", key, r.status, r.body)
				}
			}()
		}
		wg.Wait()
	}
	if loserKey == "" {
		t.Fatal("no publisher lost the optimistic-lock race in 30 rounds of 16: the test cannot prove the release")
	}
	if n := a.keyRows(t, loserKey); n != 0 {
		t.Fatalf("the refused request left %d row(s) under its key %q, want 0: a concurrent-update refusal is RETRYABLE and must release the key", n, loserKey)
	}
	retry := a.svc.call(t, "PUT", "/notification-policy", loserKey, loserBody)
	if retry.status != 200 || retry.header.Get("Idempotency-Replayed") != "" {
		t.Fatalf("the retry under the released key = %d %s replayed=%q, want a fresh 200", retry.status, retry.body, retry.header.Get("Idempotency-Replayed"))
	}
	if n := a.keyRows(t, loserKey); n != 1 {
		t.Fatalf("%d row(s) under the key after the retry succeeded, want 1 (a 2xx is stored)", n)
	}
	again := a.svc.call(t, "PUT", "/notification-policy", loserKey, loserBody)
	if again.status != 200 || again.body != retry.body || again.header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("third call = %d %s replayed=%q, want the stored 200 replayed", again.status, again.body, again.header.Get("Idempotency-Replayed"))
	}
}

// not-found is RETRYABLE: the order a cancellation names may be created by
// another request (its id derives from a checkout's Idempotency-Key).
func TestANotFoundRefusalIsNotStoredSoTheCancelRetriesOnceTheOrderExists(t *testing.T) {
	a := newIdemAPI(t)
	const cancelKey, checkoutKey, reason = "cancel-late", "co-late", `{"reason":"changed my mind"}`
	orderID := string(ids.Generator{}.OrderIDFor(checkoutKey))
	path := "/orders/" + orderID + "/cancellations"

	refused := a.svc.call(t, "POST", path, cancelKey, reason)
	if refused.status != 404 || refused.field(t, "type") != "/problems/not-found" {
		t.Fatalf("cancel of an order that does not exist yet = %d %s, want 404 not-found", refused.status, refused.body)
	}
	if n := a.keyRows(t, cancelKey); n != 0 {
		t.Fatalf("the 404 left %d row(s) under its key, want 0: not-found is RETRYABLE and must release the key", n)
	}

	reg := a.svc.call(t, "POST", "/shoppers", "reg-late", registerAna)
	shopperID, _ := reg.field(t, "shopperId").(string)
	if placed := a.svc.call(t, "POST", "/checkout", checkoutKey, fmt.Sprintf(checkout, shopperID)); placed.status != 201 || placed.field(t, "orderId") != orderID {
		t.Fatalf("checkout = %d %s, want 201 for order %s", placed.status, placed.body, orderID)
	}

	retry := a.svc.call(t, "POST", path, cancelKey, reason)
	if retry.status >= 300 || retry.header.Get("Idempotency-Replayed") != "" {
		t.Fatalf("the retry under the released key = %d %s replayed=%q, want a fresh 2xx", retry.status, retry.body, retry.header.Get("Idempotency-Replayed"))
	}
	again := a.svc.call(t, "POST", path, cancelKey, reason)
	if again.status != retry.status || again.body != retry.body || again.header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("third call = %d %s replayed=%q, want the stored answer replayed", again.status, again.body, again.header.Get("Idempotency-Replayed"))
	}
}

// offer-not-quotable is OUTCOME: when a later confirm fails and the cancel of
// the confirmed promise fails too, the checkout enqueues that cancel in the
// request's transaction (usecases/checkout.go compensate) and then refuses;
// the row must commit with the stored answer, and a replay must not run again.
func TestAnOutcomeRefusalCommitsItsCompensationRowWithTheStoredAnswer(t *testing.T) {
	pool := pgtest.NewPool(t)
	dp := &recordingDP{cancelsFail: true}
	logger := slog.New(slog.DiscardHandler)
	ad := adapters{pool: pool, idempotency: postgres.NewIdempotencyStore(pool), clock: clock.System{}, ids: ids.Generator{}}
	ad.outbox = postgres.NewOutboxRepo(pool, outboundkafka.NewEncoder())
	ad.compensations = postgres.NewCompensationRepo(pool)
	ad.deps = usecases.Deps{
		Orders: postgres.NewOrderRepository(pool), Shoppers: postgres.NewShopperRepository(pool), Tracking: postgres.NewTrackingRepository(pool),
		Inbox: postgres.NewInbox(pool), Outbox: ad.outbox, UoW: postgres.NewUnitOfWork(pool), Compensations: ad.compensations,
		Promises: deliverypromise.New(dp.serve(t).URL, &http.Client{}), Clock: ad.clock, IDs: ad.ids, Log: logger,
	}
	srv := httptest.NewServer(inboundhttp.NewRouter(buildServer(ad, nil), logger, ""))
	t.Cleanup(srv.Close)
	svc := &service{base: srv.URL}
	count := func(sql string) int {
		var n int
		if err := pool.QueryRow(t.Context(), sql).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	reg := svc.call(t, "POST", "/shoppers", "reg-out", registerAna)
	shopperID, _ := reg.field(t, "shopperId").(string)
	body := fmt.Sprintf(checkout, shopperID)
	first := svc.call(t, "POST", "/checkout", "co-out", body)
	if first.status != 409 || first.field(t, "type") != "/problems/offer-not-quotable" {
		t.Fatalf("checkout whose second confirm is refused = %d %s, want 409 offer-not-quotable", first.status, first.body)
	}
	if n := count(`SELECT count(*) FROM idempotency_keys WHERE key = 'co-out'`); n != 1 {
		t.Fatalf("%d key row(s) after the refusal, want 1 (an OUTCOME answer is stored)", n)
	}
	if n := count(`SELECT count(*) FROM compensation_cancels`); n != 1 {
		t.Fatalf("%d compensation row(s) after the refusal, want 1: the enqueued cancel must commit with the stored answer", n)
	}
	cancelsBefore := len(dp.cancels())

	again := svc.call(t, "POST", "/checkout", "co-out", body)
	if again.status != 409 || again.header.Get("Idempotency-Replayed") != "true" || again.body != first.body {
		t.Fatalf("replay = %d %s replayed=%q, want the stored 409", again.status, again.body, again.header.Get("Idempotency-Replayed"))
	}
	if got := len(dp.cancels()); got != cancelsBefore {
		t.Fatalf("the replay sent %d more cancel(s) to delivery-promise, want 0", got-cancelsBefore)
	}
	if n := count(`SELECT count(*) FROM compensation_cancels`); n != 1 {
		t.Fatalf("%d compensation row(s) after the replay, want 1", n)
	}
}
