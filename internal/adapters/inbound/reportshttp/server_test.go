package reportshttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/adapters/inbound/reportshttp"
	"github.com/claudioed/customer-ordering/internal/analytics/report"
)

// fakeReader answers fixed aggregates and records the range it was asked for.
type fakeReader struct {
	placed    []report.PlacedDay
	cancelled []report.CancelledDay
	notices   []report.NoticeDay
	last      *time.Time
	err       error
	asked     report.Range
}

func (f *fakeReader) PlacedOrders(_ context.Context, r report.Range) ([]report.PlacedDay, error) {
	f.asked = r
	return f.placed, f.err
}

func (f *fakeReader) CancelledOrders(context.Context, report.Range) ([]report.CancelledDay, error) {
	return f.cancelled, f.err
}

func (f *fakeReader) NoticesDecided(context.Context, report.Range) ([]report.NoticeDay, error) {
	return f.notices, f.err
}

func (f *fakeReader) LastEventAt(context.Context) (*time.Time, error) { return f.last, f.err }

type fakePing struct{ err error }

func (p fakePing) Ping(context.Context) error { return p.err }

var (
	now  = time.Date(2026, 11, 8, 18, 30, 0, 0, time.UTC)
	nov3 = time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
)

func serve(t *testing.T, s *reportshttp.Server, method, target string) (int, string, map[string]any) {
	t.Helper()
	if s.Now == nil {
		s.Now = func() time.Time { return now }
	}
	h := reportshttp.NewRouter(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, target, nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s %s: body %q is not JSON: %v", method, target, rec.Body.String(), err)
	}
	return rec.Code, rec.Header().Get("Content-Type"), body
}

func TestOrderingPerformanceReport(t *testing.T) {
	asOf := now.Add(-90 * time.Second)
	r := &fakeReader{
		placed:    []report.PlacedDay{{Day: nov3, Orders: 4, Consolidated: 1, Pickup: 2, Split: 1}},
		cancelled: []report.CancelledDay{{Day: nov3, Orders: 1}, {Day: nov3.Add(24 * time.Hour), Orders: 2}},
		notices:   []report.NoticeDay{{Day: nov3, Family: "GROUP_SHIPPED", Count: 3}, {Day: nov3, Family: "GROUP_LOST", Count: 1}},
		last:      &asOf,
	}
	code, ct, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, "/reports/ordering-performance?from=2026-11-01&to=2026-11-07")
	if code != http.StatusOK || ct != "application/json" {
		t.Fatalf("status %d content-type %s", code, ct)
	}
	want := map[string]any{
		"from": "2026-11-01", "to": "2026-11-07",
		"days": []any{
			map[string]any{"day": "2026-11-03", "orders_placed": 4.0, "orders_cancelled": 1.0, "consolidated_tier_orders": 1.0,
				"pickup_point_orders": 2.0, "split_shipment_orders": 1.0, "split_shipment_share": 0.25, "shopper_notices_decided": 4.0,
				"shopper_notices_by_kind": []any{map[string]any{"kind": "GROUP_SHIPPED", "count": 3.0}, map[string]any{"kind": "GROUP_LOST", "count": 1.0}},
				"shopper_notices_other":   0.0},
			// A day with only a cancellation: no placed order, so no share (null, not 0).
			map[string]any{"day": "2026-11-04", "orders_placed": 0.0, "orders_cancelled": 2.0, "consolidated_tier_orders": 0.0,
				"pickup_point_orders": 0.0, "split_shipment_orders": 0.0, "split_shipment_share": nil, "shopper_notices_decided": 0.0,
				"shopper_notices_by_kind": []any{}, "shopper_notices_other": 0.0},
		},
		"freshness": map[string]any{"as_of": "2026-11-08T18:28:30Z", "lag_seconds": 90.0},
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %v\nwant %v", body, want)
	}
	if !r.asked.From.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) || !r.asked.To.Equal(time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("asked range = %+v, want [11-01, 11-08)", r.asked)
	}
}

func TestOrderingPerformanceDefaultsToTheThirtyDaysEndingToday(t *testing.T) {
	_, _, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, "/reports/ordering-performance")
	if body["from"] != "2026-10-10" || body["to"] != "2026-11-08" {
		t.Fatalf("range = %v..%v", body["from"], body["to"])
	}
}

// An empty range is an empty days array and null freshness, never an error or
// a null array.
func TestOrderingPerformanceOfAnEmptyRange(t *testing.T) {
	code, _, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, "/reports/ordering-performance")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if days, ok := body["days"].([]any); !ok || len(days) != 0 {
		t.Fatalf("days = %#v, want []", body["days"])
	}
	want := map[string]any{"as_of": nil, "lag_seconds": nil}
	if !reflect.DeepEqual(body["freshness"], want) {
		t.Fatalf("freshness = %v, want explicit nulls", body["freshness"])
	}
}

func TestFreshness(t *testing.T) {
	_, _, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, "/reports/freshness")
	if v, ok := body["as_of"]; !ok || v != nil || body["lag_seconds"] != nil {
		t.Fatalf("before the first event = %v, want explicit nulls", body)
	}
	asOf := now.Add(-90 * time.Second)
	_, _, body = serve(t, &reportshttp.Server{Reader: &fakeReader{last: &asOf}}, http.MethodGet, "/reports/freshness")
	if body["as_of"] != "2026-11-08T18:28:30Z" || body["lag_seconds"] != 90.0 {
		t.Fatalf("freshness = %v", body)
	}
}

func assertProblem(t *testing.T, code int, ct string, body map[string]any, wantCode int, wantType string) {
	t.Helper()
	if code != wantCode || ct != "application/problem+json" || body["type"] != wantType || body["status"] != float64(wantCode) {
		t.Fatalf("got %d %s %v, want %d problem %s", code, ct, body, wantCode, wantType)
	}
}

func TestInvalidRangeIsAProblem(t *testing.T) {
	for _, q := range []string{"?from=2026-11-05&to=2026-11-04", "?from=nope", "?to=2026-13-01", "?from=2025-01-01&to=2026-11-01"} {
		code, ct, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, "/reports/ordering-performance"+q)
		assertProblem(t, code, ct, body, http.StatusBadRequest, "invalid-report-range")
	}
}

func TestReaderFailureIsAnOpaque500(t *testing.T) {
	r := &fakeReader{err: errors.New("pq: password authentication failed for secret-role")}
	for _, path := range []string{"/reports/ordering-performance", "/reports/freshness"} {
		code, ct, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, path)
		assertProblem(t, code, ct, body, http.StatusInternalServerError, "internal-error")
		if strings.Contains(body["detail"].(string), "secret") {
			t.Fatalf("%s leaked the cause: %v", path, body)
		}
	}
}

// Each of the four reads can fail on its own; none may produce a partial 200.
func TestEveryReadFailureFailsTheWholeReport(t *testing.T) {
	boom := errors.New("down")
	for name, r := range map[string]*failingReader{
		"placed": {placed: boom}, "cancelled": {cancelled: boom}, "notices": {notices: boom}, "last event": {last: boom},
	} {
		code, ct, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, "/reports/ordering-performance")
		if code != http.StatusInternalServerError {
			t.Errorf("%s: status %d, want 500", name, code)
		}
		assertProblem(t, code, ct, body, http.StatusInternalServerError, "internal-error")
	}
}

type failingReader struct{ placed, cancelled, notices, last error }

func (f *failingReader) PlacedOrders(context.Context, report.Range) ([]report.PlacedDay, error) {
	return nil, f.placed
}

func (f *failingReader) CancelledOrders(context.Context, report.Range) ([]report.CancelledDay, error) {
	return nil, f.cancelled
}

func (f *failingReader) NoticesDecided(context.Context, report.Range) ([]report.NoticeDay, error) {
	return nil, f.notices
}

func (f *failingReader) LastEventAt(context.Context) (*time.Time, error) { return nil, f.last }

func TestUnknownPathAndWriteMethodsAreProblems(t *testing.T) {
	code, ct, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, "/reports/nope")
	assertProblem(t, code, ct, body, http.StatusNotFound, "not-found")
	code, ct, body = serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodPost, "/reports/ordering-performance")
	assertProblem(t, code, ct, body, http.StatusMethodNotAllowed, "method-not-allowed")
}

func TestHealthAndReadiness(t *testing.T) {
	if code, _, body := serve(t, &reportshttp.Server{}, http.MethodGet, "/healthz"); code != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("healthz = %d %v", code, body)
	}
	if code, _, body := serve(t, &reportshttp.Server{Ping: fakePing{}}, http.MethodGet, "/readyz"); code != http.StatusOK || body["status"] != "ready" {
		t.Fatalf("readyz = %d %v", code, body)
	}
	code, ct, body := serve(t, &reportshttp.Server{Ping: fakePing{err: errors.New("down")}}, http.MethodGet, "/readyz")
	assertProblem(t, code, ct, body, http.StatusServiceUnavailable, "dependency-unavailable")
	draining := &reportshttp.Server{}
	draining.SetNotReady()
	if code, _, body := serve(t, draining, http.MethodGet, "/readyz"); code != http.StatusServiceUnavailable || body["status"] != "not_ready" {
		t.Fatalf("draining readyz = %d %v", code, body)
	}
}
