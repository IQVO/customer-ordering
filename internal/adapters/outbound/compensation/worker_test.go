package compensation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	compensationworker "github.com/claudioed/customer-ordering/internal/adapters/outbound/compensation"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/memory"
	"github.com/claudioed/customer-ordering/internal/application/compensation"
	"github.com/claudioed/customer-ordering/internal/application/ports"
)

// Retry, the adapter of the promise gateway, carries the queue's core
// judgement: a cancel is retried until it succeeds, and a cancel
// delivery-promise refuses is done -- the promise is closed, nothing is left
// to release.
func TestRetryPassesARealFailureThrough(t *testing.T) {
	gw := memory.NewPromiseGateway()
	gw.CancelErr["p-1"] = ports.ErrPromiseUnavailable
	err := compensationworker.Retry(gw)(context.Background(), compensation.Cancel{OrderID: "o-1", GroupNo: 1, PromiseID: "p-1"})
	if !errors.Is(err, ports.ErrPromiseUnavailable) {
		t.Fatalf("err = %v, want the cancel error", err)
	}
}

func TestRetryTreatsARefusedCancelAsDone(t *testing.T) {
	gw := memory.NewPromiseGateway()
	gw.CancelErr["p-1"] = ports.ErrPromiseNotCancellable
	if err := compensationworker.Retry(gw)(context.Background(), compensation.Cancel{OrderID: "o-1", GroupNo: 1, PromiseID: "p-1"}); err != nil {
		t.Fatalf("a refused cancel has nothing to release, err = %v", err)
	}
}

func TestRetryPassesASucceededCancelThrough(t *testing.T) {
	gw := memory.NewPromiseGateway()
	cancel := compensation.Cancel{OrderID: "o-1", GroupNo: 1, PromiseID: "p-1"}
	if err := compensationworker.Retry(gw)(context.Background(), cancel); err != nil {
		t.Fatalf("err = %v", err)
	}
	if got := gw.Cancelled(); len(got) != 1 || got[0].PromiseID != "p-1" {
		t.Fatalf("the promise was cancelled, got %+v", got)
	}
}

// fakeStore counts the passes of a Run loop.
type fakeStore struct {
	passes int
}

func (f *fakeStore) Drain(ctx context.Context, limit, maxAttempts int, retry func(context.Context, compensation.Cancel) error) (int, error) {
	f.passes++
	return 0, nil
}

func TestRunDrainsUntilCancelled(t *testing.T) {
	store := &fakeStore{}
	w := compensationworker.NewWorker(store, func(context.Context, compensation.Cancel) error { return nil }, nil,
		compensationworker.WithInterval(time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	if err := w.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	if store.passes < 2 {
		t.Fatalf("the loop kept draining, passes = %d", store.passes)
	}
}
