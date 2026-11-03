//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/postgres"
	"github.com/claudioed/customer-ordering/internal/application/compensation"
	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/testing/pgtest"
	"github.com/jackc/pgx/v5/pgxpool"
	// The database itself is booted by pgtest; this import keeps the
	// testcontainers rule of internal/architecture satisfied for a file
	// that queries pgx directly.
	_ "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var cancels = []compensation.Cancel{
	{OrderID: "co-1", GroupNo: 1, PromiseID: "pc-1"},
	{OrderID: "co-1", GroupNo: 2, PromiseID: "pc-2"},
}

func rowState(t *testing.T, pool *pgxpool.Pool, promiseID string) (attempts int, lastError *string, released bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT attempts, last_error, released_at IS NOT NULL FROM compensation_cancels WHERE promise_id = $1`, promiseID).
		Scan(&attempts, &lastError, &released)
	if err != nil {
		t.Fatalf("read row %s: %v", promiseID, err)
	}
	return attempts, lastError, released
}

// A failed retry stops the pass, is recorded on the row and is retried on a
// later pass; a refused cancel and a succeeded one release their rows.
func TestCompensationQueueRetriesUntilReleased(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewCompensationRepo(pool)
	ctx := context.Background()
	if err := q.Enqueue(ctx, cancels...); err != nil {
		t.Fatal(err)
	}

	// Pass 1: the retry of the first cancel fails, so the pass stops there
	// and nothing is released yet.
	flaky := errors.New("delivery-promise is down")
	n, err := q.Drain(ctx, 10, 0, func(context.Context, compensation.Cancel) error { return flaky })
	if n != 0 || err == nil {
		t.Fatalf("released = %d err = %v, want the pass to stop at the failure", n, err)
	}
	var rf *compensation.RetryFailureError
	if !errors.As(err, &rf) || rf.Cancel.PromiseID != "pc-1" || rf.Attempts != 1 || !errors.Is(rf, flaky) {
		t.Fatalf("err = %v, want the first cancel with its attempt count", err)
	}
	if attempts, last, released := rowState(t, pool, "pc-1"); attempts != 1 || last == nil || released {
		t.Fatalf("the failure was recorded: attempts = %d last = %v released = %v", attempts, last, released)
	}

	// Pass 2: both cancels succeed and are released.
	n, err = q.Drain(ctx, 10, 0, func(context.Context, compensation.Cancel) error { return nil })
	if n != 2 || err != nil {
		t.Fatalf("released = %d err = %v, want both cancels released", n, err)
	}
	// pc-1 failed once before succeeding (attempts 2); pc-2 was first tried
	// by this pass, because pass 1 stopped at pc-1's failure (attempts 1).
	for id, want := range map[string]int{"pc-1": 2, "pc-2": 1} {
		if attempts, last, released := rowState(t, pool, id); !released || attempts != want || last != nil {
			t.Fatalf("%s: attempts = %d last = %v released = %v, want attempts %d released", id, attempts, last, released, want)
		}
	}

	// A released queue stays drained.
	if n, err := q.Drain(ctx, 10, 0, func(context.Context, compensation.Cancel) error { return flaky }); n != 0 || err != nil {
		t.Fatalf("released = %d err = %v, want an empty pass", n, err)
	}
}

// A cancel delivery-promise refuses releases its row: the promise is closed,
// there is nothing left to release.
func TestARefusedCancelReleasesItsRow(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewCompensationRepo(pool)
	if err := q.Enqueue(context.Background(), cancels[0]); err != nil {
		t.Fatal(err)
	}
	n, err := q.Drain(context.Background(), 10, 0, func(context.Context, compensation.Cancel) error {
		return ports.ErrPromiseNotCancellable
	})
	if n != 1 || err != nil {
		t.Fatalf("released = %d err = %v, want the refused cancel released", n, err)
	}
	if _, _, released := rowState(t, pool, "pc-1"); !released {
		t.Fatal("the refused cancel's row is released")
	}
}

// With maxAttempts > 0 a row whose failures reached the cap is not claimed.
func TestACappedRowIsNotClaimed(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewCompensationRepo(pool)
	ctx := context.Background()
	if err := q.Enqueue(ctx, cancels[0]); err != nil {
		t.Fatal(err)
	}
	flaky := func(context.Context, compensation.Cancel) error { return errors.New("down") }
	if _, err := q.Drain(ctx, 10, 1, flaky); err == nil {
		t.Fatal("the first pass fails")
	}
	if n, err := q.Drain(ctx, 10, 1, flaky); n != 0 || err != nil {
		t.Fatalf("released = %d err = %v, want the capped row skipped", n, err)
	}
	if _, _, released := rowState(t, pool, "pc-1"); released {
		t.Fatal("the capped row stays queued for inspection")
	}
}
