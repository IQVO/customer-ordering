package deliverypromise_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/deliverypromise"
	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/promising"
)

type seen struct {
	method, path, key, contentType string
	body                           map[string]any
}

// serve answers every request with status and body and records the last one.
func serve(t *testing.T, status int, contentType, body string) (*deliverypromise.Gateway, *seen) {
	t.Helper()
	got := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got.method, got.path, got.key, got.contentType = r.Method, r.URL.EscapedPath(), r.Header.Get("Idempotency-Key"), r.Header.Get("Content-Type")
		got.body = nil
		_ = json.Unmarshal(raw, &got.body)
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return deliverypromise.New(srv.URL+"/", nil), got
}

const promiseBody = `{"promiseId":"pr-1","offerId":"of-1","state":"PROVISIONAL","destinationPostalPrefix":"787",
 "terms":{"tier":"NEXT_DAY","shipBy":"2026-10-23T20:00:00Z","deliverByEarliest":"2026-10-24T12:00:00Z","deliverByLatest":"2026-10-24T20:00:00Z","basis":"SCHEDULED","policyVersion":"v1"},
 "witnessPath":{"nodeId":"fc-aus-1","pickupDepartureId":"d1","legs":[],"deliveryWaveId":"w1"},"lines":[],"revisions":[],"holdExpiresAt":"2026-10-23T11:00:00Z"}`

func TestMakePromiseSendsTheOfferAndMapsTheTerms(t *testing.T) {
	g, got := serve(t, 201, "application/json", promiseBody)
	terms, err := g.MakePromise(context.Background(), promising.MakeRequest{OrderID: "o-1", GroupNo: 2, OfferID: "of-1", Lines: []promising.Line{{CatalogItemID: "sku-1", Quantity: 3}}})
	if err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/promises" || got.key != "o-1:2" || got.body["offerId"] != "of-1" {
		t.Fatalf("request = %+v", got)
	}
	line := got.body["lines"].([]any)[0].(map[string]any)
	if line["catalogItemId"] != "sku-1" || line["quantity"] != float64(3) {
		t.Fatalf("lines = %v", got.body["lines"])
	}
	want := promising.Terms{
		PromiseID: "pr-1", Tier: "NEXT_DAY", WitnessNodeID: "fc-aus-1", DestinationPostalPrefix: "787",
		ShipBy: time.Date(2026, 10, 23, 20, 0, 0, 0, time.UTC), DeliverByEarliest: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC), DeliverByLatest: time.Date(2026, 10, 24, 20, 0, 0, 0, time.UTC),
	}
	if terms != want {
		t.Fatalf("terms = %+v", terms)
	}
}

func TestConfirmPromiseUsesTheConfirmationsRoute(t *testing.T) {
	g, got := serve(t, 200, "application/json", promiseBody)
	if err := g.ConfirmPromise(context.Background(), "pr 1", "o-1", 2); err != nil {
		t.Fatal(err)
	}
	if got.path != "/promises/pr%201/confirmations" || got.key != "o-1:2:confirm" || got.body["orderId"] != "o-1" || got.body["groupNo"] != float64(2) {
		t.Fatalf("request = %+v", got)
	}
}

func TestQuoteOffersKeepsQuotedOffersOnly(t *testing.T) {
	g, got := serve(t, 200, "application/json", `{"offers":[
	 {"offerId":"a","state":"QUOTED","tier":"NEXT_DAY","window":{"earliest":"2026-10-24T12:00:00Z","latest":"2026-10-24T20:00:00Z"},"expiresAt":"2026-10-23T11:00:00Z"},
	 {"offerId":"b","state":"EXPIRED","tier":"STANDARD","window":{"earliest":"2026-10-26T12:00:00Z","latest":"2026-10-26T20:00:00Z"},"expiresAt":"2026-10-23T09:00:00Z"}],"unavailable":[]}`)
	offers, err := g.QuoteOffers(context.Background(), "787", []promising.Line{{CatalogItemID: "sku-1", Quantity: 1}})
	if err != nil || len(offers) != 1 || offers[0].OfferID != "a" || offers[0].Tier != "NEXT_DAY" || !offers[0].ExpiresAt.Equal(time.Date(2026, 10, 23, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("offers = %+v err %v", offers, err)
	}
	if got.path != "/delivery-quotes" || got.body["destinationPostalPrefix"] != "787" || !validQuoteKey(got.key) {
		t.Fatalf("request = %+v", got)
	}
}

// validQuoteKey reports whether key is a client-generated UUID (REST guideline 2.2).
func validQuoteKey(key string) bool {
	_, err := uuid.Parse(key)
	return err == nil
}

// TestEveryQuoteIsItsOwnLogicalSubmit: a quote has no natural key, so each call
// mints a fresh Idempotency-Key; reusing one would make delivery-promise refuse
// the second quote with 409 idempotency-key-reused.
func TestEveryQuoteIsItsOwnLogicalSubmit(t *testing.T) {
	g, got := serve(t, 200, "application/json", `{"offers":[]}`)
	lines := []promising.Line{{CatalogItemID: "sku-1", Quantity: 1}}
	keys := map[string]bool{}
	for range 3 {
		if _, err := g.QuoteOffers(context.Background(), "787", lines); err != nil {
			t.Fatal(err)
		}
		keys[got.key] = true
	}
	if len(keys) != 3 {
		t.Fatalf("3 quotes used %d distinct keys: %v", len(keys), keys)
	}
}

func TestRefusalsAreTyped(t *testing.T) {
	problem := func(slug string) string { return `{"type":"/problems/` + slug + `","title":"t","detail":"d"}` }
	for _, c := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"offer gone 404", 404, problem("offer-not-found"), ports.ErrPromiseOfferNotQuotable},
		{"offer expired 409", 409, problem("offer-expired"), ports.ErrPromiseOfferNotQuotable},
		{"invalid 422", 422, problem("validation-failed"), ports.ErrPromiseOfferNotQuotable},
		{"capacity", 409, problem("promise-capacity-exhausted"), ports.ErrPromiseCapacityExhausted},
		{"book closed", 409, problem("book-closed"), ports.ErrPromiseBookClosed},
		{"group spans nodes", 422, problem("group-spans-nodes"), ports.ErrPromiseGroupSpansNodes},
		{"server error", 500, `oops`, ports.ErrPromiseUnavailable},
		{"bad gateway", 502, ``, ports.ErrPromiseUnavailable},
		{"unexpected 400", 400, problem("malformed-request"), ports.ErrPromiseUnavailable},
	} {
		g, _ := serve(t, c.status, "application/problem+json", c.body)
		if err := g.ConfirmPromise(context.Background(), "p", "o", 1); !errors.Is(err, c.want) {
			t.Fatalf("%s: %v, want %v", c.name, err, c.want)
		}
		if _, err := g.MakePromise(context.Background(), promising.MakeRequest{OrderID: "o", GroupNo: 1}); !errors.Is(err, c.want) {
			t.Fatalf("%s (make): %v", c.name, err)
		}
		if _, err := g.QuoteOffers(context.Background(), "787", nil); !errors.Is(err, c.want) {
			t.Fatalf("%s (quote): %v", c.name, err)
		}
	}
}

func TestUndecodableAndEmptyPromisesAreUnavailable(t *testing.T) {
	for _, body := range []string{`not json`, `{}`} {
		g, _ := serve(t, 201, "application/json", body)
		if _, err := g.MakePromise(context.Background(), promising.MakeRequest{OrderID: "o", GroupNo: 1}); !errors.Is(err, ports.ErrPromiseUnavailable) {
			t.Fatalf("%q: %v", body, err)
		}
	}
	g, _ := serve(t, 200, "application/json", `not json`)
	if _, err := g.QuoteOffers(context.Background(), "787", nil); !errors.Is(err, ports.ErrPromiseUnavailable) {
		t.Fatalf("quote: %v", err)
	}
}

func TestATimeoutOrADeadConnectionIsUnavailable(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer slow.Close()
	g := deliverypromise.New(slow.URL, &http.Client{Timeout: 50 * time.Millisecond})
	if err := g.ConfirmPromise(context.Background(), "p", "o", 1); !errors.Is(err, ports.ErrPromiseUnavailable) {
		t.Fatalf("timeout: %v", err)
	}
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	if err := deliverypromise.New(url, nil).ConfirmPromise(context.Background(), "p", "o", 1); !errors.Is(err, ports.ErrPromiseUnavailable) {
		t.Fatalf("refused connection: %v", err)
	}
}

func TestCancelPromiseUsesTheCancellationsRouteWithoutABody(t *testing.T) {
	g, got := serve(t, 200, "application/json", `{"promiseId":"pr 1","state":"CANCELLED"}`)
	if err := g.CancelPromise(context.Background(), "pr 1", "o-1", 2); err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/promises/pr%201/cancellations" || got.key != "o-1:2:cancel" || got.body != nil || got.contentType != "" {
		t.Fatalf("request = %+v", got)
	}
}

func TestCancelPromiseRefusalsAreTyped(t *testing.T) {
	problem := func(slug string) string { return `{"type":"/problems/` + slug + `","title":"t","detail":"d"}` }
	for _, c := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"unknown promise", 404, problem("not-found"), ports.ErrPromiseNotCancellable},
		{"kept or broken", 409, problem("illegal-transition"), ports.ErrPromiseNotCancellable},
		{"server error", 500, `oops`, ports.ErrPromiseUnavailable},
		{"bad gateway", 502, ``, ports.ErrPromiseUnavailable},
	} {
		g, _ := serve(t, c.status, "application/problem+json", c.body)
		if err := g.CancelPromise(context.Background(), "p", "o", 1); !errors.Is(err, c.want) {
			t.Fatalf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	if err := deliverypromise.New("http://127.0.0.1:1", nil).CancelPromise(context.Background(), "p", "o", 1); !errors.Is(err, ports.ErrPromiseUnavailable) {
		t.Fatalf("a dead connection is unavailable: %v", err)
	}
}

// The optional timeZone of the window (delivery-promise ADR 0011) is carried on
// the made terms and on every quoted offer; an absent or malformed name is an
// unknown zone (empty), never a failed checkout (ADR 0009).
func TestTheWindowTimeZoneIsCarriedAndAMalformedOneIsUnknown(t *testing.T) {
	zoned := `{"promiseId":"pr-1","destinationPostalPrefix":"787",
 "terms":{"tier":"NEXT_DAY","shipBy":"2026-10-30T23:00:00Z","deliverByEarliest":"2026-10-31T14:00:00Z","deliverByLatest":"2026-11-01T02:00:00Z","timeZone":"America/Chicago"},
 "witnessPath":{"nodeId":"fc-aus-1"}}`
	g, _ := serve(t, 201, "application/json", zoned)
	terms, err := g.MakePromise(context.Background(), promising.MakeRequest{OrderID: "o-1", GroupNo: 1, OfferID: "of-1"})
	if err != nil || terms.TimeZone != "America/Chicago" {
		t.Fatalf("terms = %+v, %v; want timeZone America/Chicago", terms, err)
	}
	g, _ = serve(t, 201, "application/json", promiseBody)
	if terms, err = g.MakePromise(context.Background(), promising.MakeRequest{OrderID: "o-1", GroupNo: 1, OfferID: "of-1"}); err != nil || terms.TimeZone != "" {
		t.Fatalf("a promise without timeZone: %+v, %v; want an empty (unknown) zone", terms, err)
	}
	g, _ = serve(t, 200, "application/json", `{"offers":[
	 {"offerId":"a","state":"QUOTED","tier":"NEXT_DAY","window":{"earliest":"2026-10-31T14:00:00Z","latest":"2026-11-01T02:00:00Z","timeZone":"America/Chicago"},"expiresAt":"2026-10-30T11:00:00Z"},
	 {"offerId":"b","state":"QUOTED","tier":"STANDARD","window":{"earliest":"2026-11-02T14:00:00Z","latest":"2026-11-03T02:00:00Z"},"expiresAt":"2026-10-30T11:00:00Z"},
	 {"offerId":"c","state":"QUOTED","tier":"TWO_DAY","window":{"earliest":"2026-11-01T14:00:00Z","latest":"2026-11-02T02:00:00Z","timeZone":"not a zone"},"expiresAt":"2026-10-30T11:00:00Z"}]}`)
	offers, err := g.QuoteOffers(context.Background(), "787", []promising.Line{{CatalogItemID: "sku-1", Quantity: 1}})
	if err != nil || len(offers) != 3 {
		t.Fatalf("offers = %+v, %v", offers, err)
	}
	if offers[0].TimeZone != "America/Chicago" || offers[1].TimeZone != "" || offers[2].TimeZone != "" {
		t.Fatalf("zones = %q %q %q, want America/Chicago, empty, empty", offers[0].TimeZone, offers[1].TimeZone, offers[2].TimeZone)
	}
}
