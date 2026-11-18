package kafka

import (
	"context"
	"strings"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// runDLQ records the dead letters an analytics-consumer test publishes.
type runDLQ struct {
	msgs []kafkago.Message
}

func (d *runDLQ) WriteMessages(_ context.Context, m ...kafkago.Message) error {
	d.msgs = append(d.msgs, m...)
	return nil
}

func (d *runDLQ) Close() error { return nil }

// waitFor polls the loop recorder until its log mentions want.
func waitFor(t *testing.T, rec *loopRecorder, want, msg string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if strings.Contains(strings.Join(rec.log(), " "), want) {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("%s; events: %v", msg, rec.log())
		case <-time.After(5 * time.Millisecond):
		}
	}
}
