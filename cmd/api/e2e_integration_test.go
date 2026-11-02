//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/testing/kafkatest"
	"github.com/claudioed/customer-ordering/internal/testing/pgtest"
)

// The end-to-end proof that the shipped binary is really wired: the test boots
// the REAL entry point (run, in-process) against testcontainers Postgres and
// Kafka with nothing but the environment a deployment sets, and drives it only
// through REST and Kafka. delivery-promise is the one in-organization HTTP
// dependency and is a local fake server. Every Kafka read is bounded by a
// deadline; run it with -timeout 300s.

const (
	topicEvents   = "retailnetwork.customer-ordering.events"
	topicSourcing = "retailnetwork.order-sourcing.events"
	topicShipping = "retailnetwork.shipping-and-tracking.events"
	topicPromise  = "retailnetwork.delivery-promise.events"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	kafkatest.Shutdown()
	os.Exit(code)
}

// fakeDeliveryPromise answers MakePromise and ConfirmPromise like the sibling.
func fakeDeliveryPromise(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		raw, _ := io.ReadAll(r.Body)
		switch {
		case r.URL.Path == "/promises":
			var in struct {
				OfferID string `json:"offerId"`
			}
			_ = json.Unmarshal(raw, &in)
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"promiseId":"pr-%s","offerId":%q,"state":"PROVISIONAL","destinationPostalPrefix":"787",
			 "terms":{"tier":"NEXT_DAY","shipBy":"2026-10-24T10:00:00Z","deliverByEarliest":"2026-10-25T10:00:00Z","deliverByLatest":"2026-10-25T20:00:00Z"},
			 "witnessPath":{"nodeId":"fc-aus-1"}}`, in.OfferID, in.OfferID)
		case strings.HasSuffix(r.URL.Path, "/confirmations"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"promiseId":"x","state":"CONFIRMED"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

// service is a booted instance of the real entry point.
type service struct {
	base string
	stop func() error
}

func boot(t *testing.T, env map[string]string) *service {
	t.Helper()
	addr := freeAddr(t)
	for k, v := range env {
		t.Setenv(k, v)
	}
	t.Setenv("HTTP_ADDR", addr)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	var (
		stopOnce sync.Once
		stopErr  error
	)
	s := &service{base: "http://" + addr, stop: func() error {
		stopOnce.Do(func() {
			cancel()
			select {
			case stopErr = <-done:
			case <-time.After(60 * time.Second):
				stopErr = fmt.Errorf("the service did not stop")
			}
		})
		return stopErr
	}}
	t.Cleanup(func() { _ = s.stop() })
	eventually(t, 90*time.Second, "readyz to report ready", func() bool {
		resp, err := http.Get(s.base + "/readyz")
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode == 200
	})
	return s
}

func eventually(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

type reply struct {
	status int
	body   string
	header http.Header
}

func (s *service) call(t *testing.T, method, path, key, body string) reply {
	t.Helper()
	req, err := http.NewRequest(method, s.base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return reply{status: resp.StatusCode, body: string(b), header: resp.Header}
}

func (r reply) field(t *testing.T, name string) any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.body), &m); err != nil {
		t.Fatalf("not JSON: %s", r.body)
	}
	return m[name]
}

const checkout = `{"shopperId":%q,
 "shipTo":{"name":"Ana Example","line1":"1 Sample Street","city":"Austin","region":"TX","postalCode":"78701","countryCode":"US"},
 "lines":[{"lineNo":1,"catalogItemId":"sku-100","quantity":2},{"lineNo":2,"catalogItemId":"sku-200","quantity":1}],
 "groups":[{"groupNo":1,"offerId":"offer-1","lineNos":[1]},{"groupNo":2,"offerId":"offer-2","lineNos":[2]}]}`

func event(id, typ, subject string, data map[string]any) kafkatest.Message {
	b, _ := json.Marshal(map[string]any{
		"specversion": "1.0", "id": id, "source": "/retailnetwork/test", "type": typ, "subject": subject,
		"time": time.Now().UTC().Format(time.RFC3339Nano), "datacontenttype": "application/json", "data": data,
	})
	return kafkatest.Message{Key: subject, Value: b}
}

func TestTheShippedBinaryIsWiredEndToEnd(t *testing.T) {
	dsn := pgtest.URL(t)
	brokers := kafkatest.Brokers(t)
	for _, topic := range []string{topicEvents, topicSourcing, topicShipping, topicPromise} {
		kafkatest.CreateTopic(t, topic)
	}
	dp := fakeDeliveryPromise(t)
	env := map[string]string{
		"DATABASE_URL": dsn, "KAFKA_BROKERS": strings.Join(brokers, ","), "DELIVERY_PROMISE_URL": dp.URL,
		"DELIVERY_PROMISE_CONSUMER_GROUP": "co-e2e-promise", "ORDER_SOURCING_CONSUMER_GROUP": "co-e2e-sourcing", "SHIPPING_CONSUMER_GROUP": "co-e2e-shipping",
		"OUTBOX_RELAY_INTERVAL": "100ms", "SHUTDOWN_DRAIN_DELAY": "0", "LOG_LEVEL": "warn",
	}
	svc := boot(t, env)

	// A shopper registers, then checks out; a replay of the same key is the same answer.
	shopper := svc.call(t, "POST", "/shoppers", "shopper-key", `{"email":"ana@example.com"}`)
	if shopper.status != 201 {
		t.Fatalf("register = %d %s", shopper.status, shopper.body)
	}
	shopperID := shopper.field(t, "shopperId").(string)
	body := fmt.Sprintf(checkout, shopperID)
	placed := svc.call(t, "POST", "/checkout", "checkout-1", body)
	if placed.status != 201 || placed.field(t, "status") != "PLACED" {
		t.Fatalf("checkout = %d %s", placed.status, placed.body)
	}
	orderID := placed.field(t, "orderId").(string)
	if again := svc.call(t, "POST", "/checkout", "checkout-1", body); again.body != placed.body || again.header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay = %d %s", again.status, again.body)
	}

	// CustomerOrderPlaced reaches Kafka as a CloudEvent, without the address.
	msgs := kafkatest.ReadKeyed(t, topicEvents, orderID, 1, 60*time.Second)
	if msgs[0].Key != orderID || !strings.Contains(string(msgs[0].Value), "customerorder.CustomerOrderPlaced") || strings.Contains(string(msgs[0].Value), "Sample Street") {
		t.Fatalf("event = %s key %q", msgs[0].Value, msgs[0].Key)
	}

	// The address is served only by its restricted route.
	if shipTo := svc.call(t, "GET", "/orders/"+orderID+"/ship-to", "", ""); shipTo.status != 200 || shipTo.field(t, "line1") != "1 Sample Street" {
		t.Fatalf("ship-to = %d %s", shipTo.status, shipTo.body)
	}

	// order-sourcing and shipping-and-tracking events move the order.
	sourcing := func(id, name string, data map[string]any) kafkatest.Message {
		return event(id, "com.retailnetwork.commerce.order-sourcing.assignment."+name, orderID, data)
	}
	kafkatest.Produce(t, topicSourcing,
		sourcing("lock-1", "FulfillmentLocked", map[string]any{"assignmentId": "as-1", "orderId": orderID, "groupNo": 1, "nodeId": "fc-aus-1"}),
		sourcing("lock-2", "FulfillmentLocked", map[string]any{"assignmentId": "as-2", "orderId": orderID, "groupNo": 2, "nodeId": "fc-aus-1"}),
		sourcing("lock-2", "FulfillmentLocked", map[string]any{"assignmentId": "as-2", "orderId": orderID, "groupNo": 2, "nodeId": "fc-aus-1"}),
	)
	statusIs := func(want string) func() bool {
		return func() bool { return svc.call(t, "GET", "/orders/"+orderID, "", "").field(t, "status") == want }
	}
	eventually(t, 60*time.Second, "IN_FULFILLMENT", statusIs("IN_FULFILLMENT"))
	kafkatest.Produce(t, topicSourcing, sourcing("ship-1", "FulfillmentShipped", map[string]any{"assignmentId": "as-1", "orderId": orderID, "groupNo": 1, "trackingNumbers": []string{"TRK-1"}}))
	eventually(t, 60*time.Second, "PARTIALLY_SHIPPED", statusIs("PARTIALLY_SHIPPED"))
	kafkatest.Produce(t, topicShipping, event("dlv-1", "com.retailnetwork.logistics.shipping-and-tracking.journey.PackageDelivered", orderID,
		map[string]any{"trackingNumber": "TRK-1", "orderId": orderID, "deliveredAt": time.Now().UTC().Format(time.RFC3339Nano)}))
	eventually(t, 60*time.Second, "the delivered package in the tracking", func() bool {
		return strings.Contains(svc.call(t, "GET", "/orders/"+orderID+"/tracking", "", "").body, `"deliveredAt"`)
	})

	// Every group is locked or beyond: cancellation is refused.
	if refused := svc.call(t, "POST", "/orders/"+orderID+"/cancellations", "cancel-1", `{"reason":"too late"}`); refused.status != 409 || refused.field(t, "type") != "/problems/cancel-after-lock" {
		t.Fatalf("cancel = %d %s", refused.status, refused.body)
	}

	// A second order is cancelled while unlocked; the request reaches Kafka.
	second := svc.call(t, "POST", "/checkout", "checkout-2", body)
	secondID := second.field(t, "orderId").(string)
	if cancelled := svc.call(t, "POST", "/orders/"+secondID+"/cancellations", "cancel-2", `{"reason":"changed my mind"}`); cancelled.status != 200 {
		t.Fatalf("cancel = %d %s", cancelled.status, cancelled.body)
	}
	msgs = kafkatest.ReadKeyed(t, topicEvents, secondID, 2, 60*time.Second)
	if !strings.Contains(string(msgs[0].Value), "CustomerOrderPlaced") || !strings.Contains(string(msgs[1].Value), "CustomerOrderCancelled") {
		t.Fatalf("events = %s | %s", msgs[0].Value, msgs[1].Value)
	}

	// A restart keeps everything: the state lives in Postgres.
	if err := svc.stop(); err != nil && err != context.Canceled {
		t.Fatalf("stop: %v", err)
	}
	svc = boot(t, env)
	if got := svc.call(t, "GET", "/orders/"+orderID, "", ""); got.status != 200 || got.field(t, "status") != "PARTIALLY_DELIVERED" {
		t.Fatalf("after restart = %d %s", got.status, got.body)
	}

	t.Run("readyz is 503 dependency-unavailable while kafka is down", func(t *testing.T) {
		assertKafkaDownReadiness(t, svc)
	})
}

// assertKafkaDownReadiness stops the real broker and checks the contract:
// /readyz answers 503 dependency-unavailable, /healthz stays 200, a write is
// still accepted (the outbox holds the event) and the service stops cleanly.
func assertKafkaDownReadiness(t *testing.T, svc *service) {
	t.Helper()
	kafkatest.Stop(t)
	eventually(t, 60*time.Second, "readyz to report 503", func() bool {
		return svc.call(t, "GET", "/readyz", "", "").status == http.StatusServiceUnavailable
	})
	ready := svc.call(t, "GET", "/readyz", "", "")
	if ready.status != http.StatusServiceUnavailable || !strings.Contains(ready.body, "dependency-unavailable") {
		t.Fatalf("readyz = %d %s", ready.status, ready.body)
	}
	if health := svc.call(t, "GET", "/healthz", "", ""); health.status != http.StatusOK {
		t.Fatalf("healthz = %d %s", health.status, health.body)
	}
	if write := svc.call(t, "POST", "/shoppers", "shopper-offline", `{"email":"bo@example.com"}`); write.status != http.StatusCreated {
		t.Fatalf("write while kafka is down = %d %s", write.status, write.body)
	}
	if err := svc.stop(); err != nil && err != context.Canceled {
		t.Fatalf("stop: %v", err)
	}
}
