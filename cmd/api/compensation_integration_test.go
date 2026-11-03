//go:build integration

package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/claudioed/customer-ordering/internal/testing/kafkatest"
	"github.com/claudioed/customer-ordering/internal/testing/pgtest"
)

// The A14 compensation over the real HTTP gateway: a delivery-promise fake
// refuses the confirm of group 2 and records every cancel it receives.

type dpCall struct{ method, path, key, body string }

type recordingDP struct {
	mu          sync.Mutex
	calls       []dpCall
	cancelsFail bool
}

func (d *recordingDP) cancels() []dpCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []dpCall
	for _, c := range d.calls {
		if strings.HasSuffix(c.path, "/cancellations") {
			out = append(out, c)
		}
	}
	return out
}

func (d *recordingDP) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		d.calls = append(d.calls, dpCall{r.Method, r.URL.Path, r.Header.Get("Idempotency-Key"), string(raw)})
		failCancel := d.cancelsFail
		d.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/promises":
			offer := strings.Split(strings.Split(string(raw), `"offerId":"`)[1], `"`)[0]
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"promiseId":"pr-%s","offerId":%q,"state":"PROVISIONAL","destinationPostalPrefix":"787",
			 "terms":{"tier":"NEXT_DAY","shipBy":"2026-10-24T10:00:00Z","deliverByEarliest":"2026-10-25T10:00:00Z","deliverByLatest":"2026-10-25T20:00:00Z"},
			 "witnessPath":{"nodeId":"fc-aus-1"}}`, offer, offer)
		case r.URL.Path == "/promises/pr-offer-2/confirmations":
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"type":"/problems/offer-expired","title":"Offer expired","detail":"the hold is gone"}`))
		case strings.HasSuffix(r.URL.Path, "/confirmations"):
			_, _ = w.Write([]byte(`{"promiseId":"x","state":"CONFIRMED"}`))
		case strings.HasSuffix(r.URL.Path, "/cancellations") && failCancel:
			w.WriteHeader(http.StatusInternalServerError)
		case strings.HasSuffix(r.URL.Path, "/cancellations"):
			_, _ = w.Write([]byte(`{"promiseId":"x","state":"CANCELLED"}`))
		case r.URL.Path == "/delivery-quotes":
			_, _ = w.Write([]byte(`{"offers":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAFailedLaterConfirmCancelsTheConfirmedPromiseOverHTTP(t *testing.T) {
	dsn := pgtest.URL(t)
	brokers := kafkatest.Brokers(t)
	for _, topic := range []string{topicEvents, topicSourcing, topicShipping, topicPromise} {
		kafkatest.CreateTopic(t, topic)
	}
	dp := &recordingDP{}
	srv := dp.serve(t)
	svc := boot(t, map[string]string{
		"DATABASE_URL": dsn, "KAFKA_BROKERS": strings.Join(brokers, ","), "DELIVERY_PROMISE_URL": srv.URL,
		"DELIVERY_PROMISE_CONSUMER_GROUP": "co-comp-promise", "ORDER_SOURCING_CONSUMER_GROUP": "co-comp-sourcing", "SHIPPING_CONSUMER_GROUP": "co-comp-shipping",
		"OUTBOX_RELAY_INTERVAL": "100ms", "SHUTDOWN_DRAIN_DELAY": "0", "LOG_LEVEL": "warn",
	})
	shopper := svc.call(t, "POST", "/shoppers", "shopper-key", `{"email":"ana@example.com"}`)
	body := fmt.Sprintf(checkout, shopper.field(t, "shopperId").(string))

	refused := svc.call(t, "POST", "/checkout", "checkout-comp-1", body)
	if refused.status != 409 || refused.field(t, "type") != "/problems/offer-not-quotable" {
		t.Fatalf("checkout = %d %s", refused.status, refused.body)
	}
	cancels := dp.cancels()
	if len(cancels) != 1 || cancels[0].method != "POST" || cancels[0].path != "/promises/pr-offer-1/cancellations" ||
		!strings.HasSuffix(cancels[0].key, ":1:cancel") || cancels[0].body != "" {
		t.Fatalf("the confirmed promise of group 1 must be cancelled once, without a body: %+v", cancels)
	}
	dp.mu.Lock()
	for _, c := range dp.calls {
		if c.key == "" || len(c.key) > 128 {
			t.Fatalf("every POST to delivery-promise carries an Idempotency-Key of 1 to 128 characters: %+v", c)
		}
	}
	dp.mu.Unlock()

	t.Run("a cancel that fails still ends in the same answer", func(t *testing.T) {
		dp.mu.Lock()
		dp.cancelsFail = true
		dp.mu.Unlock()
		again := svc.call(t, "POST", "/checkout", "checkout-comp-2", body)
		if again.status != 409 || again.field(t, "type") != "/problems/offer-not-quotable" {
			t.Fatalf("checkout = %d %s", again.status, again.body)
		}
		if got := len(dp.cancels()); got != 2 {
			t.Fatalf("the cancel was attempted (%d cancels in total)", got)
		}
	})
}
