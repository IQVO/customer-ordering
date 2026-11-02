package repocontract

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/idempotency"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
)

// idemDo is Do with the Env clock as the record stamp.
func idemDo(e Env, key, hash string, fn func(ctx context.Context) (idempotency.Record, error)) (idempotency.Record, bool, error) {
	return e.Idempotency.Do(bg(), key, hash, e.Clock.Now().UTC(), fn)
}

// answerAfterSaving returns an fn that saves an order through the ctx it is
// handed (so the write joins the key's transaction) and answers status.
func answerAfterSaving(t *testing.T, e Env, orderID string, status int, calls *atomic.Int32) func(context.Context) (idempotency.Record, error) {
	t.Helper()
	return func(ctx context.Context) (idempotency.Record, error) {
		calls.Add(1)
		must(t, e.Orders.Save(ctx, newOrder(t, orderID)))
		return idempotency.Record{Status: status, ContentType: "application/json", Body: []byte(`{"a":1}`)}, nil
	}
}

func orderExists(e Env, id string) bool {
	_, err := e.Orders.Get(bg(), customerorder.ID("order-"+id))
	return err == nil
}

func testIdempotency(t *testing.T, e Env) {
	t.Run("the first call runs fn and commits it with the record, a repeat replays", func(t *testing.T) { idemReplay(t, e) })
	t.Run("a 4xx answer is stored", func(t *testing.T) { idemClientError(t, e) })
	t.Run("a 5xx, an error and a panic store nothing and undo the writes", func(t *testing.T) { idemRollback(t, e) })
	t.Run("an expired record is taken over", func(t *testing.T) { idemExpired(t, e) })
	t.Run("concurrent calls with one key run fn once", func(t *testing.T) { idemConcurrent(t, e) })
}

func idemReplay(t *testing.T, e Env) {
	var calls atomic.Int32
	rec, replayed, err := idemDo(e, "k1", "h1", answerAfterSaving(t, e, "order-i1", 201, &calls))
	must(t, err)
	if replayed || calls.Load() != 1 || rec.Status != 201 || !orderExists(e, "i1") {
		t.Fatalf("first call: replayed=%v calls=%d rec=%+v order=%v", replayed, calls.Load(), rec, orderExists(e, "i1"))
	}
	// The key is global (REST guideline 2.3): a repeat, even with another fingerprint (another
	// method, path or body), finds the stored record untouched; the middleware compares hashes and
	// turns a mismatch into 409 idempotency-key-reused.
	for _, hash := range []string{"h1", "h2"} {
		got, replayed, err := idemDo(e, "k1", hash, answerAfterSaving(t, e, "order-i1b", 500, &calls))
		must(t, err)
		if !replayed || calls.Load() != 1 || got.RequestHash != "h1" || got.Status != 201 || got.ContentType != "application/json" || string(got.Body) != `{"a":1}` {
			t.Fatalf("repeat with %s: replayed=%v calls=%d rec=%+v", hash, replayed, calls.Load(), got)
		}
	}
	if orderExists(e, "i1b") {
		t.Fatal("a replay must not run fn")
	}
}

func idemClientError(t *testing.T, e Env) {
	var calls atomic.Int32
	rec, replayed, err := idemDo(e, "k-4xx", "h", answerAfterSaving(t, e, "order-i4", 422, &calls))
	must(t, err)
	if replayed || rec.Status != 422 {
		t.Fatalf("first call: replayed=%v rec=%+v", replayed, rec)
	}
	got, replayed, err := idemDo(e, "k-4xx", "h", answerAfterSaving(t, e, "order-i4b", 201, &calls))
	must(t, err)
	if !replayed || got.Status != 422 || calls.Load() != 1 {
		t.Fatalf("a 4xx is replayed: replayed=%v rec=%+v calls=%d", replayed, got, calls.Load())
	}
}

func idemRollback(t *testing.T, e Env) {
	var calls atomic.Int32
	failures := map[string]func(ctx context.Context) (idempotency.Record, error){
		"5xx": answerAfterSaving(t, e, "order-i5", 503, &calls),
		"error": func(ctx context.Context) (idempotency.Record, error) {
			calls.Add(1)
			must(t, e.Orders.Save(ctx, newOrder(t, "order-ie")))
			return idempotency.Record{}, errBoom
		},
		"panic": func(ctx context.Context) (idempotency.Record, error) {
			calls.Add(1)
			must(t, e.Orders.Save(ctx, newOrder(t, "order-ip")))
			panic("boom")
		},
	}
	for name, id := range map[string]string{"5xx": "i5", "error": "ie", "panic": "ip"} {
		key := "k-" + name
		func() {
			defer func() { _ = recover() }()
			_, replayed, _ := idemDo(e, key, "h", failures[name])
			if replayed {
				t.Errorf("%s: nothing was stored yet", name)
			}
		}()
		if orderExists(e, id) {
			t.Fatalf("%s: the writes of fn must be undone", name)
		}
		before := calls.Load()
		_, replayed, _ := idemDo(e, key, "h", answerAfterSaving(t, e, "order-ir-"+name, 201, &calls))
		if replayed || calls.Load() != before+1 {
			t.Fatalf("%s: the retry must run fn again (replayed=%v)", name, replayed)
		}
	}
}

func idemExpired(t *testing.T, e Env) {
	var calls atomic.Int32
	old := e.Clock.Now().UTC().Add(-idempotency.TTL - time.Minute)
	if _, _, err := e.Idempotency.Do(bg(), "k-old", "old", old, answerAfterSaving(t, e, "order-io", 201, &calls)); err != nil {
		t.Fatal(err)
	}
	rec, replayed, err := idemDo(e, "k-old", "new", answerAfterSaving(t, e, "order-in", 200, &calls))
	must(t, err)
	if replayed || rec.Status != 200 || calls.Load() != 2 {
		t.Fatalf("an expired record is replaceable: replayed=%v rec=%+v calls=%d", replayed, rec, calls.Load())
	}
	got, replayed, err := idemDo(e, "k-old", "new", answerAfterSaving(t, e, "order-ix", 201, &calls))
	if err != nil || !replayed || got.RequestHash != "new" {
		t.Fatalf("the replacement is live: replayed=%v rec=%+v err=%v", replayed, got, err)
	}
}

func idemConcurrent(t *testing.T, e Env) {
	const callers = 8
	var (
		calls    atomic.Int32
		wg       sync.WaitGroup
		mu       sync.Mutex
		replays  int
		failures []string
	)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec, replayed, err := e.Idempotency.Do(bg(), "k-race", "h", e.Clock.Now().UTC(),
				func(ctx context.Context) (idempotency.Record, error) {
					calls.Add(1)
					time.Sleep(50 * time.Millisecond) // let the others queue behind the claim
					if err := e.Orders.Save(ctx, newOrder(t, fmt.Sprintf("order-race-%d", i))); err != nil {
						return idempotency.Record{}, err
					}
					return idempotency.Record{Status: 201, Body: []byte(fmt.Sprint(i))}, nil
				})
			mu.Lock()
			defer mu.Unlock()
			if err != nil || rec.Status != 201 {
				failures = append(failures, fmt.Sprintf("caller %d: %+v %v", i, rec, err))
			}
			if replayed {
				replays++
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 || replays != callers-1 || len(failures) != 0 {
		t.Fatalf("fn ran %d times, %d replays of %d, failures %v", calls.Load(), replays, callers, failures)
	}
	n := 0
	for i := range callers {
		if _, err := e.Orders.Get(bg(), customerorder.ID(fmt.Sprintf("order-race-%d", i))); err == nil {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d orders were created, want exactly one", n)
	}
}
