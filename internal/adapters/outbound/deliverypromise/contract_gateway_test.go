package deliverypromise

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/promising"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// serveAnswer answers every request with status, contentType and body.
func serveAnswer(t *testing.T, status int, contentType, body string) *Gateway {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, nil)
}

var quoteLines = []promising.Line{{CatalogItemID: "sku-1", Quantity: 2}}

// R3 GatewayDecodesSpecExamples: the testdata fixtures (R2-proven against the
// pinned schemas) are served by an httptest server through the REAL gateway,
// asserting the decoded values and the problem+json sentinel mapping.
func TestGatewayDecodesSpecExamples(t *testing.T) {
	t.Run("quote offers", func(t *testing.T) {
		g := serveAnswer(t, http.StatusOK, "application/json", fixture(t, "quote-response.example.json"))
		offers, err := g.QuoteOffers(context.Background(), promising.Destination{PostalPrefix: "787"}, quoteLines)
		if err != nil {
			t.Fatal(err)
		}
		assertQuoteDecoded(t, offers)
	})
	t.Run("promise terms", func(t *testing.T) {
		g := serveAnswer(t, http.StatusCreated, "application/json", fixture(t, "promise-response.example.json"))
		terms, err := g.MakePromise(context.Background(), promising.MakeRequest{
			OrderID: "o-1", GroupNo: 2, OfferID: "of-1", Lines: quoteLines,
		})
		if err != nil {
			t.Fatal(err)
		}
		assertPromiseDecoded(t, terms)
	})
	t.Run("confirmation", func(t *testing.T) {
		g := serveAnswer(t, http.StatusOK, "application/json", fixture(t, "promise-response.example.json"))
		if err := g.ConfirmPromise(context.Background(), "pr-1", "o-1", 2); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("problem answers map to the port sentinels", func(t *testing.T) {
		for _, c := range problemCases() {
			t.Run(c.file, func(t *testing.T) {
				g := serveAnswer(t, c.status, "application/problem+json", fixture(t, c.file))
				_, err := g.QuoteOffers(context.Background(), promising.Destination{PostalPrefix: "787"}, quoteLines)
				if !errors.Is(err, c.want) {
					t.Errorf("QuoteOffers error = %v, want %v", err, c.want)
				}
			})
		}
	})
	t.Run("a cancel refusal maps to not cancellable", func(t *testing.T) {
		g := serveAnswer(t, http.StatusConflict, "application/problem+json", fixture(t, "problem-illegal-transition.json"))
		err := g.CancelPromise(context.Background(), "pr-1", "o-1", 2)
		if !errors.Is(err, ports.ErrPromiseNotCancellable) {
			t.Errorf("CancelPromise error = %v, want %v", err, ports.ErrPromiseNotCancellable)
		}
	})
}

func assertQuoteDecoded(t *testing.T, offers []promising.Offer) {
	t.Helper()
	if len(offers) != 3 {
		t.Fatalf("offers = %d, want 3 (all QUOTED)", len(offers))
	}
	first := offers[0]
	if first.OfferID != "of-1" || first.Tier != "NEXT_DAY" || first.TimeZone != "America/Chicago" {
		t.Errorf("first offer = %+v", first)
	}
	assertInstant(t, "DeliverByEarliest", first.DeliverByEarliest, "2026-10-26T12:00:00Z")
	assertInstant(t, "ExpiresAt", first.ExpiresAt, "2026-10-25T13:30:00Z")
	// of-2 omits window.timeZone (an offer stored before the field was
	// carried, ADR 0011): absence must decode as unknown, not fail.
	if offers[1].TimeZone != "" {
		t.Errorf("of-2 timeZone = %q, want \"\" (absence tolerated)", offers[1].TimeZone)
	}
}

func assertPromiseDecoded(t *testing.T, terms promising.Terms) {
	t.Helper()
	if terms.PromiseID != "pr-1" || terms.Tier != "CONSOLIDATED" || terms.WitnessNodeID != "fc-aus-1" {
		t.Errorf("terms = %+v", terms)
	}
	if terms.TimeZone != "America/Chicago" || terms.DestinationPostalPrefix != "787" {
		t.Errorf("terms = %+v", terms)
	}
	if terms.ConsolidationEligible == nil || !*terms.ConsolidationEligible {
		t.Errorf("ConsolidationEligible = %v, want true", terms.ConsolidationEligible)
	}
	// The spec-conformant fixture carries no pickupPointId (the pinned
	// Promise schema does not declare it): absence must decode as empty.
	if terms.PickupPointID != "" {
		t.Errorf("PickupPointID = %q, want \"\" (absence tolerated)", terms.PickupPointID)
	}
}

func assertInstant(t *testing.T, field string, got time.Time, want string) {
	t.Helper()
	if got.Format(time.RFC3339) != want {
		t.Errorf("%s = %s, want %s", field, got, want)
	}
}

func problemCases() []struct {
	file   string
	status int
	want   error
} {
	return []struct {
		file   string
		status int
		want   error
	}{
		{"problem-capacity-exhausted.json", http.StatusConflict, ports.ErrPromiseCapacityExhausted},
		{"problem-book-closed.json", http.StatusConflict, ports.ErrPromiseBookClosed},
		{"problem-group-spans-nodes.json", http.StatusUnprocessableEntity, ports.ErrPromiseGroupSpansNodes},
		{"problem-not-found.json", http.StatusNotFound, ports.ErrPromiseOfferNotQuotable},
	}
}
