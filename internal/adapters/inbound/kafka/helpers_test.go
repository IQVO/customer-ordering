package kafka_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	kafkaconsumer "github.com/claudioed/customer-ordering/internal/adapters/inbound/kafka"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/memory"
	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/promising"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fastRetry keeps Run-loop tests quick while still exercising backoff.
var fastRetry = kafkaconsumer.RetryPolicy{Initial: time.Millisecond, Max: 4 * time.Millisecond}

var errInjected = errors.New("injected transient database failure")

var t0 = time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)

const orderID = "order-1"

// flakyOrders fails the first `fail` Save calls AFTER saving, so the unit of
// work must roll the half-done effect (and the inbox claim) back.
type flakyOrders struct {
	ports.OrderRepository
	mu    sync.Mutex
	fail  int
	calls int
}

func (f *flakyOrders) Save(ctx context.Context, o *customerorder.CustomerOrder) error {
	if err := f.OrderRepository.Save(ctx, o); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.fail > 0 {
		f.fail--
		return errInjected
	}
	return nil
}

func (f *flakyOrders) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// countingUoW counts how often a unit of work was opened, proving a
// deterministic bad message never even touches the database.
type countingUoW struct {
	inner ports.UnitOfWork
	mu    sync.Mutex
	calls int
}

func (u *countingUoW) Do(ctx context.Context, fn func(context.Context) error) error {
	u.mu.Lock()
	u.calls++
	u.mu.Unlock()
	return u.inner.Do(ctx, fn)
}

func (u *countingUoW) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls
}

// world is a consumer over the in-memory store with one placed order of two groups.
type world struct {
	store  *memory.Store
	orders *flakyOrders
	uow    *countingUoW
	deps   usecases.Deps
}

func newWorld(t *testing.T) *world {
	t.Helper()
	s := memory.NewStore()
	gw := memory.NewPromiseGateway()
	w := &world{store: s, orders: &flakyOrders{OrderRepository: s.Orders()}, uow: &countingUoW{inner: s.UnitOfWork()}}
	w.deps = usecases.Deps{
		Orders: w.orders, Shoppers: s.Shoppers(), Tracking: s.Tracking(), Inbox: s.Inbox(), Outbox: s.Outbox(), UoW: w.uow,
		Promises: gw, Clock: memory.NewClock(t0), IDs: &memory.SequentialIDs{}, Log: testLogger(),
	}
	sh, err := shopper.Register(shopper.NewShopper{ID: "shopper-1", Email: "ana@example.com"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Shoppers().Save(context.Background(), sh); err != nil {
		t.Fatal(err)
	}
	for i, offer := range []string{"offer-1", "offer-2"} {
		gw.Terms[offer] = promising.Terms{
			PromiseID: fmt.Sprintf("p-%d", i+1), Tier: "NEXT_DAY", WitnessNodeID: "fc-1", ShipBy: t0.Add(10 * time.Hour),
			DeliverByEarliest: t0.Add(20 * time.Hour), DeliverByLatest: t0.Add(30 * time.Hour), DestinationPostalPrefix: "787",
		}
	}
	_, err = usecases.PlaceOrder{Deps: w.deps}.Execute(context.Background(), usecases.PlaceOrderInput{
		OrderID: orderID, ShopperID: "shopper-1",
		ShipTo: shipto.ShipTo{Name: "Ana Example", Line1: "1 Sample Street", City: "Austin", Region: "TX", PostalCode: "78701", CountryCode: "US"},
		Lines:  []customerorder.OrderLine{{LineNo: 1, CatalogItemID: "sku-1", Quantity: 1}, {LineNo: 2, CatalogItemID: "sku-2", Quantity: 1}},
		Groups: []usecases.CheckoutGroup{{GroupNo: 1, OfferID: "offer-1", LineNos: []int{1}}, {GroupNo: 2, OfferID: "offer-2", LineNos: []int{2}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	w.orders.calls = 0
	return w
}

func (w *world) consumer() usecases.Consumer { return usecases.Consumer{Deps: w.deps} }

func (w *world) sourcing() *kafkaconsumer.Consumer {
	return &kafkaconsumer.Consumer{
		Name: "order-sourcing", Topic: kafkaconsumer.OrderSourcingTopic, Handlers: kafkaconsumer.OrderSourcingHandlers(w.consumer()),
		Logger: testLogger(), Retry: fastRetry, DLQ: &fakeDLQWriter{}, DeadLetterInvalid: true,
	}
}

func (w *world) status(t *testing.T) string {
	t.Helper()
	o, err := w.store.Orders().Get(context.Background(), orderID)
	if err != nil {
		t.Fatal(err)
	}
	return string(o.Status())
}

// claimed reports whether the inbox already holds the event id (committed).
func (w *world) claimed(t *testing.T, id string) bool {
	t.Helper()
	var first bool
	err := w.store.UnitOfWork().Do(context.Background(), func(ctx context.Context) error {
		var err error
		first, err = w.store.Inbox().Claim(ctx, id, "probe")
		if err == nil {
			return errInjected // roll the probe back
		}
		return err
	})
	if !errors.Is(err, errInjected) {
		t.Fatal(err)
	}
	return !first
}

// event renders a structured-mode CloudEvent of another service.
func event(id, typ string, data map[string]any) []byte {
	b, err := json.Marshal(map[string]any{
		"specversion": "1.0", "id": id, "source": "/retailnetwork/test", "type": typ, "subject": orderID,
		"time": t0.Add(time.Hour).Format(time.RFC3339Nano), "datacontenttype": "application/json", "data": data,
	})
	if err != nil {
		panic(err)
	}
	return b
}

func locked(id string, group int) []byte {
	return event(id, kafkaconsumer.TypeFulfillmentLocked, map[string]any{"assignmentId": fmt.Sprintf("as-%d", group), "orderId": orderID, "groupNo": group, "nodeId": "fc-1"})
}

// fakeReader is a scripted kafkaconsumer.Reader. It records every
// FetchMessage/CommitMessages in order so a test can assert that the offset
// was committed only after the handler succeeded, and exactly once. After the
// scripted messages it blocks until ctx is cancelled, like a real reader on an
// idle topic.
type fakeReader struct {
	mu       sync.Mutex
	msgs     []kafkago.Message
	next     int
	events   []string
	onCommit func()
}

func newFakeReader(values ...[]byte) *fakeReader {
	r := &fakeReader{}
	for i, v := range values {
		r.msgs = append(r.msgs, kafkago.Message{Partition: 0, Offset: int64(i), Value: v, Topic: "t"})
	}
	return r
}

func (r *fakeReader) FetchMessage(ctx context.Context) (kafkago.Message, error) {
	r.mu.Lock()
	if r.next < len(r.msgs) {
		m := r.msgs[r.next]
		r.next++
		r.events = append(r.events, fmt.Sprintf("fetch:%d", m.Offset))
		r.mu.Unlock()
		return m, nil
	}
	r.mu.Unlock()
	<-ctx.Done()
	return kafkago.Message{}, ctx.Err()
}

func (r *fakeReader) CommitMessages(_ context.Context, msgs ...kafkago.Message) error {
	r.mu.Lock()
	for _, m := range msgs {
		r.events = append(r.events, fmt.Sprintf("commit:%d", m.Offset))
	}
	fn := r.onCommit
	r.mu.Unlock()
	if fn != nil {
		fn()
	}
	return nil
}

func (r *fakeReader) Close() error { return nil }

func (r *fakeReader) log() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func (r *fakeReader) commits() int {
	n := 0
	for _, e := range r.log() {
		if len(e) > 6 && e[:6] == "commit" {
			n++
		}
	}
	return n
}

// fakeDLQWriter records every message WriteMessages publishes.
type fakeDLQWriter struct {
	mu   sync.Mutex
	msgs []kafkago.Message
}

func (w *fakeDLQWriter) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.msgs = append(w.msgs, msgs...)
	return nil
}

func (w *fakeDLQWriter) Close() error { return nil }

func (w *fakeDLQWriter) published() []kafkago.Message {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]kafkago.Message(nil), w.msgs...)
}

// runUntilCommits runs c.Run until the reader saw `want` commits (or fails
// after 10s), then cancels and returns Run's error.
func runUntilCommits(t *testing.T, c *kafkaconsumer.Consumer, reader *fakeReader, want int) error {
	t.Helper()
	c.Reader = reader
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	deadline := time.After(10 * time.Second)
	for reader.commits() < want {
		select {
		case err := <-done:
			return err
		case <-deadline:
			t.Fatalf("timed out waiting for %d commits; events=%v", want, reader.log())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
		return nil
	}
}
