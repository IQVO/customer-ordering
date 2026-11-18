package kafka

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/customer-ordering/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/customer-ordering/internal/analytics/report"
)

// fakeProjection records the applied events; errs are returned one per Apply
// call first (nil entries succeed).
type fakeProjection struct {
	mu     sync.Mutex
	events []report.Event
	errs   []error
}

func (f *fakeProjection) Apply(_ context.Context, e report.Event) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		if err != nil {
			return false, err
		}
	}
	f.events = append(f.events, e)
	return true, nil
}

var (
	eventTime   = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	businessAt  = time.Date(2026, 11, 3, 11, 30, 0, 0, time.UTC)
	businessRFC = "2026-11-03T11:30:00Z"
)

// ownEvent encodes one of this context's events through the shared envelope
// builder (the one the outbox encoder uses).
func ownEvent(t *testing.T, entity, name, subject string, data map[string]any) []byte {
	t.Helper()
	b, err := cloudevents.New(cloudevents.Spec{ID: "evt-" + name, Entity: entity, EventName: name, Subject: subject,
		Time: eventTime, Stream: cloudevents.StreamEvents, Version: 1, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func analyticsConsumer(p report.Projection, dlq DeadLetterWriter, logger *slog.Logger) *AnalyticsConsumer {
	if logger == nil {
		logger = quietLogger()
	}
	return &AnalyticsConsumer{Projection: p, DLQ: dlq, Topic: OwnEventsTopic, DLQTopic: AnalyticsDLQTopic, Logger: logger}
}

func TestAnalyticsConsumerProjectsTheThreeTypes(t *testing.T) {
	base := report.Event{At: eventTime, OrderID: "o-1", OccurredAt: businessAt}
	placedData := func(extra map[string]any) map[string]any {
		d := map[string]any{"orderId": "o-1", "shopperId": "s-1", "destinationPostalPrefix": "787", "placedAt": businessRFC,
			"consolidationKey": "k", "groups": []any{
				map[string]any{"groupNo": 1, "tier": "NEXT_DAY", "lines": []any{}},
				map[string]any{"groupNo": 2, "tier": "CONSOLIDATED", "lines": []any{}},
				map[string]any{"groupNo": 3, "tier": "CONSOLIDATED", "lines": []any{}},
			}}
		for k, v := range extra {
			d[k] = v
		}
		return d
	}
	cases := []struct {
		entity, name string
		data         map[string]any
		want         func(e report.Event) report.Event
	}{
		{entityCustomerOrder, "CustomerOrderPlaced", placedData(nil),
			func(e report.Event) report.Event {
				e.Kind, e.GroupCount, e.ConsolidatedGroups = report.KindOrderPlaced, 3, 2
				return e
			}},
		{entityCustomerOrder, "CustomerOrderPlaced", placedData(map[string]any{"pickupPointId": "pp-9"}),
			func(e report.Event) report.Event {
				e.Kind, e.GroupCount, e.ConsolidatedGroups, e.PickupPoint = report.KindOrderPlaced, 3, 2, true
				return e
			}},
		{entityCustomerOrder, "CustomerOrderCancelled", map[string]any{"orderId": "o-1", "reason": "Ana wants to cancel", "cancelledAt": businessRFC, "groupNos": []any{1, 2}},
			func(e report.Event) report.Event { e.Kind = report.KindOrderCancelled; return e }},
		{entityOrder, "ShopperNoticeDecided", map[string]any{"orderId": "o-1", "groupNo": 2, "family": "GROUP_LOST", "notifier": "NOBODY",
			"policyVersion": 0, "noticeKey": "nk-1", "decidedAt": businessRFC, "fact": map[string]any{"status": "LOST"}},
			func(e report.Event) report.Event {
				e.Kind, e.Family, e.NoticeKey = report.KindNoticeDecided, "GROUP_LOST", "nk-1"
				return e
			}},
	}
	for _, c := range cases {
		p, dlq := &fakeProjection{}, &runDLQ{}
		err := analyticsConsumer(p, dlq, nil).HandleMessage(context.Background(),
			kafkago.Message{Value: ownEvent(t, c.entity, c.name, "o-1", c.data)})
		if err != nil || len(dlq.msgs) != 0 || len(p.events) != 1 {
			t.Fatalf("%s: err=%v dlq=%d applied=%d", c.name, err, len(dlq.msgs), len(p.events))
		}
		want := c.want(base)
		want.EventID = "evt-" + c.name
		if !reflect.DeepEqual(p.events[0], want) {
			t.Errorf("%s projected %+v\nwant %+v", c.name, p.events[0], want)
		}
	}
}

// The projected event carries no free text and no personal data, whatever the
// payload holds: the cancellation reason, the shopper, the postal prefix, the
// consolidation key and the pickup point id are never read into it.
func TestAnalyticsConsumerProjectsNoPII(t *testing.T) {
	data := map[string]any{"orderId": "o-1", "shopperId": "shopper-secret", "destinationPostalPrefix": "787-secret", "placedAt": businessRFC,
		"consolidationKey": "key-secret", "pickupPointId": "pp-secret", "groups": []any{map[string]any{"tier": "NEXT_DAY", "witnessNodeId": "node-secret"}}}
	p := &fakeProjection{}
	if err := analyticsConsumer(p, &runDLQ{}, nil).HandleMessage(context.Background(),
		kafkago.Message{Value: ownEvent(t, entityCustomerOrder, "CustomerOrderPlaced", "o-1", data)}); err != nil {
		t.Fatal(err)
	}
	cancel := map[string]any{"orderId": "o-1", "reason": "reason-secret", "cancelledAt": businessRFC, "groupNos": []any{1}}
	if err := analyticsConsumer(p, &runDLQ{}, nil).HandleMessage(context.Background(),
		kafkago.Message{Value: ownEvent(t, entityCustomerOrder, "CustomerOrderCancelled", "o-1", cancel)}); err != nil {
		t.Fatal(err)
	}
	notice := map[string]any{"orderId": "o-1", "family": "DELIVERY_POINT_ADVISORY", "noticeKey": "nk", "decidedAt": businessRFC,
		"fact": map[string]any{"reason": "fact-secret"}}
	if err := analyticsConsumer(p, &runDLQ{}, nil).HandleMessage(context.Background(),
		kafkago.Message{Value: ownEvent(t, entityOrder, "ShopperNoticeDecided", "o-1", notice)}); err != nil {
		t.Fatal(err)
	}
	if len(p.events) != 3 {
		t.Fatalf("applied = %d", len(p.events))
	}
	if dump := fmt.Sprintf("%+v", p.events); strings.Contains(dump, "secret") {
		t.Fatalf("a projected event leaks payload content: %s", dump)
	}
}

func TestAnalyticsConsumerDeadLettersPoisonOnce(t *testing.T) {
	cases := []struct {
		name string
		msg  func(t *testing.T) []byte
		errs []error
	}{
		{"undecodable timestamp", func(t *testing.T) []byte {
			return ownEvent(t, entityCustomerOrder, "CustomerOrderCancelled", "o-1", map[string]any{"orderId": "o-1", "cancelledAt": "yesterday"})
		}, nil},
		{"subject is not the order", func(t *testing.T) []byte {
			return ownEvent(t, entityCustomerOrder, "CustomerOrderCancelled", "o-9", map[string]any{"orderId": "o-1", "cancelledAt": businessRFC})
		}, nil},
		{"no order id", func(t *testing.T) []byte {
			return ownEvent(t, entityCustomerOrder, "CustomerOrderCancelled", "o-1", map[string]any{"cancelledAt": businessRFC})
		}, nil},
		{"a placed order without groups", func(t *testing.T) []byte {
			return ownEvent(t, entityCustomerOrder, "CustomerOrderPlaced", "o-1", map[string]any{"orderId": "o-1", "placedAt": businessRFC, "groups": []any{}})
		}, nil},
		{"a notice without its key", func(t *testing.T) []byte {
			return ownEvent(t, entityOrder, "ShopperNoticeDecided", "o-1", map[string]any{"orderId": "o-1", "family": "GROUP_LOST", "decidedAt": businessRFC})
		}, nil},
		{"a notice without its instant", func(t *testing.T) []byte {
			return ownEvent(t, entityOrder, "ShopperNoticeDecided", "o-1", map[string]any{"orderId": "o-1", "family": "GROUP_LOST", "noticeKey": "k"})
		}, nil},
		{"the store rejects it", func(t *testing.T) []byte {
			return ownEvent(t, entityCustomerOrder, "CustomerOrderCancelled", "o-1", map[string]any{"orderId": "o-1", "cancelledAt": businessRFC})
		}, []error{fmt.Errorf("%w: check violation", report.ErrRejected)}},
	}
	for _, c := range cases {
		p, dlq := &fakeProjection{errs: c.errs}, &runDLQ{}
		value := c.msg(t)
		msg := kafkago.Message{Topic: OwnEventsTopic, Partition: 2, Offset: 7, Key: []byte("o-1"), Value: value}
		if err := analyticsConsumer(p, dlq, nil).HandleMessage(context.Background(), msg); err != nil {
			t.Fatalf("%s: poison must be dead-lettered and committed past, got %v", c.name, err)
		}
		if len(dlq.msgs) != 1 || len(p.events) != 0 {
			t.Fatalf("%s: dlq=%d applied=%d, want exactly one dead letter", c.name, len(dlq.msgs), len(p.events))
		}
		d := dlq.msgs[0]
		if d.Topic != AnalyticsDLQTopic || !bytes.Equal(d.Value, value) || string(d.Key) != "o-1" || dlqHeader(d, "x-dlq-source-topic") != OwnEventsTopic {
			t.Errorf("%s: dead letter = topic %s key %s headers %v", c.name, d.Topic, d.Key, d.Headers)
		}
		if dlqHeader(d, "x-dlq-error") == "" {
			t.Errorf("%s: the dead letter must carry the cause", c.name)
		}
	}
}

func dlqHeader(m kafkago.Message, key string) string {
	for _, h := range m.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

func TestAnalyticsConsumerTransientFailureIsReturnedNotDeadLettered(t *testing.T) {
	p, dlq := &fakeProjection{errs: []error{errors.New("connection refused")}}, &runDLQ{}
	msg := kafkago.Message{Value: ownEvent(t, entityCustomerOrder, "CustomerOrderCancelled", "o-1", map[string]any{"orderId": "o-1", "cancelledAt": businessRFC})}
	if err := analyticsConsumer(p, dlq, nil).HandleMessage(context.Background(), msg); err == nil {
		t.Fatal("a transient failure must be returned for the loop to retry")
	}
	if len(dlq.msgs) != 0 {
		t.Fatal("a transient failure must never be dead-lettered")
	}
}

// failingDLQ refuses every write.
type failingDLQ struct{}

func (failingDLQ) WriteMessages(context.Context, ...kafkago.Message) error {
	return errors.New("broker down")
}
func (failingDLQ) Close() error { return nil }

func TestAnalyticsConsumerFailedDeadLetterIsRetried(t *testing.T) {
	msg := kafkago.Message{Value: ownEvent(t, entityCustomerOrder, "CustomerOrderCancelled", "o-9", map[string]any{"orderId": "o-1", "cancelledAt": businessRFC})}
	err := analyticsConsumer(&fakeProjection{}, failingDLQ{}, nil).HandleMessage(context.Background(), msg)
	if err == nil || !strings.Contains(err.Error(), AnalyticsDLQTopic) {
		t.Fatalf("a failed dead-letter write must be returned (so the message is retried), got %v", err)
	}
}

func TestAnalyticsConsumerRunRetriesTransientFailuresForever(t *testing.T) {
	p, dlq := &fakeProjection{errs: []error{errors.New("down"), errors.New("down"), errors.New("down"), errors.New("down"),
		errors.New("down"), errors.New("down"), nil}}, &runDLQ{}
	c := analyticsConsumer(p, dlq, nil)
	rec := &loopRecorder{}
	c.Reader = &scriptedReader{rec: rec, msgs: []kafkago.Message{{Offset: 1, Value: ownEvent(t, entityCustomerOrder, "CustomerOrderCancelled", "o-1",
		map[string]any{"orderId": "o-1", "cancelledAt": businessRFC})}}}
	var sleeps []time.Duration
	c.Retry = RetryPolicy{Initial: time.Millisecond, Max: 4 * time.Millisecond}
	c.sleep = func(_ context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	waitFor(t, rec, "commit:1", "the message is committed once the projection succeeds")
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	if len(p.events) != 1 || len(dlq.msgs) != 0 {
		t.Fatalf("applied=%d dlq=%d: six transient failures must be retried past the domain cap, never dead-lettered", len(p.events), len(dlq.msgs))
	}
	want := []time.Duration{time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond, 4 * time.Millisecond, 4 * time.Millisecond, 4 * time.Millisecond}
	if !reflect.DeepEqual(sleeps, want) {
		t.Fatalf("backoff = %v, want the capped sequence %v", sleeps, want)
	}
}

// LEGACY ONLY. Until the notice use case stamped its events with the decision
// instant, every ShopperNoticeDecided was published with the zero CloudEvents
// time (0001-01-01T00:00:00Z); those messages are still on the stream. Such a
// notice is projected with its decision instant as the freshness basis instead
// of dead-lettered; the same defect on any other type is still poison. No
// current publisher produces a zero time (the encoder time table pins that in
// internal/adapters/outbound/kafka).
func TestAnalyticsConsumerAcceptsALegacyNoticeWithAZeroCloudEventsTime(t *testing.T) {
	zeroTime := func(entity, name string, data map[string]any) []byte {
		b, err := cloudevents.New(cloudevents.Spec{ID: "evt-zero", Entity: entity, EventName: name, Subject: "o-1",
			Stream: cloudevents.StreamEvents, Version: 1, Data: data})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	p, dlq := &fakeProjection{}, &runDLQ{}
	notice := map[string]any{"orderId": "o-1", "family": "GROUP_LOST", "noticeKey": "nk", "decidedAt": businessRFC}
	if err := analyticsConsumer(p, dlq, nil).HandleMessage(context.Background(), kafkago.Message{Value: zeroTime(entityOrder, "ShopperNoticeDecided", notice)}); err != nil {
		t.Fatal(err)
	}
	if len(p.events) != 1 || len(dlq.msgs) != 0 || !p.events[0].At.Equal(businessAt) {
		t.Fatalf("applied=%+v dlq=%d, want the notice projected with At = decidedAt", p.events, len(dlq.msgs))
	}
	cancelled := map[string]any{"orderId": "o-1", "cancelledAt": businessRFC}
	if err := analyticsConsumer(p, dlq, nil).HandleMessage(context.Background(), kafkago.Message{Value: zeroTime(entityCustomerOrder, "CustomerOrderCancelled", cancelled)}); err != nil {
		t.Fatal(err)
	}
	if len(p.events) != 1 || len(dlq.msgs) != 1 {
		t.Fatalf("a cancellation without a time must be dead-lettered: applied=%d dlq=%d", len(p.events), len(dlq.msgs))
	}
}

// The zero-time fallback is for the legacy messages only: a notice that carries
// a real CloudEvents time (every notice published since the fix) is projected
// at THAT time, even when it differs from decidedAt; the fallback never
// applies to it.
func TestAnalyticsConsumerUsesTheEnvelopeTimeOfANoticeThatHasOne(t *testing.T) {
	p, dlq := &fakeProjection{}, &runDLQ{}
	notice := map[string]any{"orderId": "o-1", "family": "GROUP_LOST", "noticeKey": "nk", "decidedAt": businessRFC}
	msg := kafkago.Message{Value: ownEvent(t, entityOrder, "ShopperNoticeDecided", "o-1", notice)}
	if err := analyticsConsumer(p, dlq, nil).HandleMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if len(p.events) != 1 || len(dlq.msgs) != 0 || !p.events[0].At.Equal(eventTime) || p.events[0].At.Equal(businessAt) {
		t.Fatalf("applied=%+v dlq=%d, want the notice projected at its envelope time %s, not at decidedAt", p.events, len(dlq.msgs), eventTime)
	}
}
