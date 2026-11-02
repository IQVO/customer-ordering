package http_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	httpadapter "github.com/claudioed/customer-ordering/internal/adapters/inbound/http"
	"github.com/claudioed/customer-ordering/internal/application/idempotency"
	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

// failingStore makes the idempotency store the broken dependency.
type failingStore struct{ err error }

func (f failingStore) Do(ctx context.Context, _, _ string, _ time.Time,
	fn func(context.Context) (idempotency.Record, error),
) (idempotency.Record, bool, error) {
	if f.err != nil {
		return idempotency.Record{}, false, f.err
	}
	rec, err := fn(ctx)
	return rec, false, err
}

func serve(t *testing.T, store ports.IdempotencyStore, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	mw := httpadapter.RequireIdempotencyKey(store, fixedClock{})(h)
	req := httptest.NewRequest("POST", "/x", strings.NewReader(`{}`))
	req.Header.Set("Idempotency-Key", "k")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	return rec
}

func TestAServerFailureIsNeverStored(t *testing.T) {
	e := newEnv(t)
	calls := 0
	boom := func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(http.StatusInternalServerError) }
	mw := httpadapter.RequireIdempotencyKey(e.store.Idempotency(), fixedClock{})(http.HandlerFunc(boom))
	for range 2 {
		req := httptest.NewRequest("POST", "/x", strings.NewReader(`{}`))
		req.Header.Set("Idempotency-Key", "k")
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != 500 || rec.Header().Get("Idempotency-Replayed") != "" {
			t.Fatalf("code %d replayed %q", rec.Code, rec.Header().Get("Idempotency-Replayed"))
		}
	}
	if calls != 2 {
		t.Fatalf("a 5xx must not be stored: the handler ran %d times, want 2", calls)
	}
}

func TestStoreFailuresAreInternalErrorsWithoutLeaks(t *testing.T) {
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) }
	rec := serve(t, failingStore{err: errors.New("secret dsn")}, ok)
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "secret") || !strings.Contains(rec.Body.String(), "/problems/internal-error") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestControlCharactersInTheKeyAreInvalid(t *testing.T) {
	for _, key := range []string{"k\x01k", "k\x7fk", "k\nk"} {
		mw := httpadapter.RequireIdempotencyKey(failingStore{}, fixedClock{})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		req := httptest.NewRequest("POST", "/x", strings.NewReader(`{}`))
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), "/problems/idempotency-key-invalid") {
			t.Fatalf("key %q = %d %s", key, rec.Code, rec.Body)
		}
	}
}

func TestAnOversizedBodyIsMalformed(t *testing.T) {
	mw := httpadapter.RequireIdempotencyKey(failingStore{}, fixedClock{})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequest("POST", "/x", strings.NewReader(strings.Repeat("a", 64<<10+1)))
	req.Header.Set("Idempotency-Key", "k")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "malformed-request") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestEveryTypedErrorHasItsStatus(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		slug   string
	}{
		{ports.ErrNotFound, 404, "not-found"},
		{ports.ErrPromiseOfferNotQuotable, 409, "offer-not-quotable"},
		{ports.ErrPromiseCapacityExhausted, 409, "promise-capacity-exhausted"},
		{ports.ErrPromiseBookClosed, 409, "book-closed"},
		{ports.ErrPromiseUnavailable, 503, "delivery-promise-unavailable"},
		{ports.ErrPromiseGroupSpansNodes, 422, "group-spans-nodes"},
		{customerorder.ErrLineWithoutPromise, 422, "line-without-promise"},
		{customerorder.ErrPromiseDestinationMismatch, 422, "promise-destination-mismatch"},
		{customerorder.ErrStatusRegression, 409, "status-regression"},
		{customerorder.ErrCancelAfterLock, 409, "cancel-after-lock"},
		{shopper.ErrShopperExists, 409, "shopper-exists"},
		{shopper.ErrTooManyAddresses, 422, "too-many-addresses"},
		{ports.ErrConcurrentUpdate, 409, "concurrent-update"},
		{shared.ErrIllegalTransition, 409, "illegal-transition"},
		{shared.ErrValidation, 422, "validation-failed"},
		{errors.New("anything else"), 500, "internal-error"},
	} {
		status, slug := httpadapter.ProblemOf(fmt.Errorf("wrapped: %w", c.err))
		if status != c.status || slug != c.slug {
			t.Fatalf("%v = %d %s, want %d %s", c.err, status, slug, c.status, c.slug)
		}
	}
}

func TestReadinessReflectsDrainAndDependencies(t *testing.T) {
	var down bool
	g := httpadapter.NewReadiness(time.Millisecond, 0, httpadapter.Dependency{Name: "postgres", Check: func(context.Context) error {
		if down {
			return errors.New("refused")
		}
		return nil
	}})
	srv := httptest.NewServer(httpadapter.NewRouter(&httpadapter.Server{Readiness: g}, nil, ""))
	defer srv.Close()
	get := func() (int, string) {
		resp, err := http.Get(srv.URL + "/readyz")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, _ := get(); code != 200 {
		t.Fatalf("ready = %d", code)
	}
	down = true
	time.Sleep(5 * time.Millisecond)
	if code, body := get(); code != 503 || !strings.Contains(body, "postgres") || !strings.Contains(body, "dependency-unavailable") {
		t.Fatalf("dependency down = %d %s", code, body)
	}
	down = false
	time.Sleep(5 * time.Millisecond)
	g.SetNotReady()
	if code, body := get(); code != 503 || !strings.Contains(body, "not_ready") {
		t.Fatalf("draining = %d %s", code, body)
	}
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return httpadapter.FixedNow }
