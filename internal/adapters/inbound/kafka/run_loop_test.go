package kafka_test

import (
	"context"
	"errors"
	"testing"

	kafkago "github.com/segmentio/kafka-go"

	kafkaconsumer "github.com/claudioed/customer-ordering/internal/adapters/inbound/kafka"
)

func TestRun_CommitsEveryMessageInOrderAfterHandlingIt(t *testing.T) {
	w := newWorld(t)
	consumer := w.sourcing()
	reader := newFakeReader(locked("e1", 1), locked("e2", 2))
	if err := runUntilCommits(t, consumer, reader, 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	if got := reader.log(); len(got) != 4 || got[0] != "fetch:0" || got[1] != "commit:0" || got[2] != "fetch:1" || got[3] != "commit:1" {
		t.Fatalf("events = %v", got)
	}
	if w.status(t) != "IN_FULFILLMENT" || !w.claimed(t, "e1") || !w.claimed(t, "e2") {
		t.Fatal("both locks must be applied and claimed")
	}
}

func TestRun_ATransientFailureRetriesTheSameMessageThenCommits(t *testing.T) {
	w := newWorld(t)
	w.orders.fail = 2
	reader := newFakeReader(locked("e1", 1))
	if err := runUntilCommits(t, w.sourcing(), reader, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
	if w.orders.callCount() < 3 {
		t.Fatalf("attempts = %d, want at least 3 (two rolled back, one committed)", w.orders.callCount())
	}
	if !w.claimed(t, "e1") || w.status(t) != "IN_FULFILLMENT" {
		t.Fatal("a later attempt must commit the effect and the claim")
	}
}

func TestRun_AMessageThatNeverSucceedsIsDeadLetteredAndTheLoopMovesOn(t *testing.T) {
	w := newWorld(t)
	w.orders.fail = 1 << 30
	consumer := w.sourcing()
	dlq := &fakeDLQWriter{}
	consumer.DLQ = dlq
	consumer.Topic = "t"
	reader := newFakeReader(locked("e1", 1))
	if err := runUntilCommits(t, consumer, reader, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
	msgs := dlq.published()
	if len(msgs) != 1 || msgs[0].Topic != "t.dlq" {
		t.Fatalf("dead letters = %+v", msgs)
	}
	if w.claimed(t, "e1") || w.status(t) != "PLACED" {
		t.Fatal("a rolled-back message leaves neither a claim nor an effect")
	}
}

func TestRun_APoisonPayloadIsDeadLetteredWithoutTouchingTheDatabase(t *testing.T) {
	w := newWorld(t)
	consumer := w.sourcing()
	dlq := &fakeDLQWriter{}
	consumer.DLQ, consumer.Topic = dlq, "t"
	before := w.uow.count()
	poison := event("bad", kafkaconsumer.TypeFulfillmentLocked, map[string]any{"orderId": orderID, "groupNo": 0})
	reader := newFakeReader(poison)
	if err := runUntilCommits(t, consumer, reader, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
	if got := dlq.published(); len(got) != 1 || headerOf(got[0], "x-dlq-error") == "" {
		t.Fatalf("dead letters = %+v", got)
	}
	if w.uow.count() != before {
		t.Fatalf("a poison message opened %d units of work", w.uow.count()-before)
	}
}

func TestClose_ReleasesReaderAndDeadLetterWriter(t *testing.T) {
	w := newWorld(t)
	consumer := w.sourcing()
	consumer.Reader, consumer.DLQ = newFakeReader(), &fakeDLQWriter{}
	if err := consumer.Close(); err != nil {
		t.Fatal(err)
	}
}

func headerOf(m kafkago.Message, key string) string {
	for _, h := range m.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}
