package usecases_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
)

// recordingMetrics counts the ports.OrderMetrics calls of a use case.
type recordingMetrics struct {
	mu       sync.Mutex
	placed   int
	rejected []string
}

var _ ports.OrderMetrics = (*recordingMetrics)(nil)

func (m *recordingMetrics) OrderPlaced(context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.placed++
}

func (m *recordingMetrics) OrderRejected(_ context.Context, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rejected = append(m.rejected, reason)
}

func TestPlaceOrderCountsEachAttemptOnceByOutcome(t *testing.T) {
	f := newFx(t)
	m := &recordingMetrics{}
	f.deps.Metrics = m
	place := usecases.PlaceOrder{Deps: f.deps}

	if _, err := place.Execute(bg(), checkout("order-1")); err != nil {
		t.Fatal(err)
	}
	if m.placed != 1 || len(m.rejected) != 0 {
		t.Fatalf("after a placement: placed=%d rejected=%v, want 1 / none", m.placed, m.rejected)
	}

	// The same checkout again is a replay of the stored order, not a new attempt.
	if _, err := place.Execute(bg(), checkout("order-1")); err != nil {
		t.Fatal(err)
	}
	if m.placed != 1 || len(m.rejected) != 0 {
		t.Fatalf("after a replay: placed=%d rejected=%v, want unchanged", m.placed, m.rejected)
	}

	bad := checkout("order-2")
	bad.ShipToAddressID = "addr-1" // both an inline ship-to and a saved address: refused
	if _, err := place.Execute(context.Background(), bad); err == nil {
		t.Fatal("a checkout with two addresses must be refused")
	}
	if m.placed != 1 || len(m.rejected) != 1 || m.rejected[0] != "validation-failed" {
		t.Fatalf("after a refusal: placed=%d rejected=%v, want 1 / [validation-failed]", m.placed, m.rejected)
	}
}

func TestPlaceOrderRejectionReasonIsTheProblemSlug(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"an unavailable delivery-promise", ports.ErrPromiseUnavailable, "delivery-promise-unavailable"},
		{"exhausted capacity", ports.ErrPromiseCapacityExhausted, "promise-capacity-exhausted"},
		{"a closed book", ports.ErrPromiseBookClosed, "book-closed"},
		{"an unexpected error", errors.New("disk on fire"), "internal-error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFx(t)
			m := &recordingMetrics{}
			f.deps.Metrics = m
			f.gw.MakeErr["offer-1"] = tt.err
			_, err := usecases.PlaceOrder{Deps: f.deps}.Execute(bg(), checkout("order-1"))
			if !errors.Is(err, tt.err) {
				t.Fatalf("Execute = %v, want %v", err, tt.err)
			}
			if len(m.rejected) != 1 || m.rejected[0] != tt.want || m.placed != 0 {
				t.Fatalf("placed=%d rejected=%v, want one rejection %q", m.placed, m.rejected, tt.want)
			}
		})
	}
}
