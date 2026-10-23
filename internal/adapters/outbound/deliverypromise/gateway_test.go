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

func TestConfirmPromiseUsesTheConfirmRoute(t *testing.T) {
	g, got := serve(t, 200, "application/json", promiseBody)
	if err := g.ConfirmPromise(context.Background(), "pr 1", "o-1", 2); err != nil {
		t.Fatal(err)
	}
	if got.path != "/promises/pr%201:confirm" || got.key != "o-1:2:confirm" || got.body["orderId"] != "o-1" || got.body["groupNo"] != float64(2) {
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
	if got.path != "/delivery-offers:quote" || got.body["destinationPostalPrefix"] != "787" || got.key != "" {
		t.Fatalf("request = %+v", got)
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

func TestCancelPromiseUsesTheCancelRouteWithoutABody(t *testing.T) {
	g, got := serve(t, 200, "application/json", `{"promiseId":"pr 1","state":"CANCELLED"}`)
	if err := g.CancelPromise(context.Background(), "pr 1", "o-1", 2); err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/promises/pr%201:cancel" || got.key != "o-1:2:cancel" || got.body != nil || got.contentType != "" {
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
