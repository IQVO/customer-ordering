// Package compensation is the retry worker of the durable compensation
// queue: a background loop that drains the promise cancels a failed checkout
// could not perform (A14) and retries them against delivery-promise until the
// capacity holds are released. It is never switched off.
//
// The loop mirrors the outbox relay: it drains every interval (default 5s),
// a failed pass is logged and retried after the interval, and a row whose
// failures reached the attempt cap is logged once and excluded from future
// claims until an operator re-arms it.
package compensation

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/compensation"
	"github.com/claudioed/customer-ordering/internal/application/ports"
)

// Defaults for the worker's tuning knobs.
const (
	DefaultInterval  = 5 * time.Second
	DefaultBatchSize = 100
	// DefaultMaxAttempts is the attempt cap a composition root uses when
	// COMPENSATION_MAX_ATTEMPTS is unset (fleet convention, gap fix D12, ADR
	// 0008). The worker's own zero value stays 0 (unlimited), so the cap is
	// always an explicit WithMaxAttempts at the composition root.
	DefaultMaxAttempts = 20
)

// AttemptCapReachedMsg is the ERROR log message written once for a row whose
// failure reached the attempt cap.
const AttemptCapReachedMsg = "compensation worker: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0, released_at = NULL after fixing the cause"

// Store is the worker's view of the compensation queue: Drain claims up to
// limit unreleased rows in id order -- skipping rows that already failed
// maxAttempts times when maxAttempts > 0 -- calls retry for each one at a
// time, releases a row only after retry returned nil, and stops at the first
// retry error, which it returns as a *compensation.RetryFailureError.
// postgres.CompensationRepo implements it.
type Store interface {
	Drain(ctx context.Context, limit, maxAttempts int, retry func(context.Context, compensation.Cancel) error) (int, error)
}

// Retry adapts a promise gateway to the worker's retry call: the cancel is
// retried until it succeeds; a cancel delivery-promise refuses
// (ErrPromiseNotCancellable) is done -- the promise is closed, nothing is
// left to release.
func Retry(gateway ports.PromiseGateway) func(context.Context, compensation.Cancel) error {
	return func(ctx context.Context, c compensation.Cancel) error {
		if err := gateway.CancelPromise(ctx, c.PromiseID, c.OrderID, c.GroupNo); err != nil && !errors.Is(err, ports.ErrPromiseNotCancellable) {
			return err
		}
		return nil
	}
}

// Worker drains a Store by retrying its cancels.
type Worker struct {
	store       Store
	retry       func(context.Context, compensation.Cancel) error
	logger      *slog.Logger
	interval    time.Duration
	batchSize   int
	maxAttempts int
}

// Option customises a Worker.
type Option func(*Worker)

// WithInterval sets how long the worker sleeps after a pass that did not
// fill a batch (or failed). Non-positive values are ignored.
func WithInterval(d time.Duration) Option {
	return func(w *Worker) {
		if d > 0 {
			w.interval = d
		}
	}
}

// WithBatchSize caps how many rows one pass claims. Non-positive values
// are ignored.
func WithBatchSize(n int) Option {
	return func(w *Worker) {
		if n > 0 {
			w.batchSize = n
		}
	}
}

// WithMaxAttempts caps how many times a row may fail before it is excluded
// from future claims. 0 means unlimited (the default); negative values are
// ignored. Every cmd/* that builds a worker must pass it (fleet convention).
func WithMaxAttempts(n int) Option {
	return func(w *Worker) {
		if n >= 0 {
			w.maxAttempts = n
		}
	}
}

// NewWorker constructs a worker draining store with retry.
func NewWorker(store Store, retry func(context.Context, compensation.Cancel) error, logger *slog.Logger, opts ...Option) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	w := &Worker{store: store, retry: retry, logger: logger, interval: DefaultInterval, batchSize: DefaultBatchSize}
	for _, o := range opts {
		o(w)
	}
	return w
}

// RetryOnce performs a single pass and returns how many rows it released.
// When the pass failed on a row whose failure count reached the attempt
// cap, that row is logged once at ERROR (AttemptCapReachedMsg).
func (w *Worker) RetryOnce(ctx context.Context) (int, error) {
	n, err := w.store.Drain(ctx, w.batchSize, w.maxAttempts, w.retry)
	w.logAttemptCap(ctx, err)
	return n, err
}

// logAttemptCap logs the row whose failure has just reached the cap: from
// the next pass on it is excluded from claims and stays queued until an
// operator re-arms it.
func (w *Worker) logAttemptCap(ctx context.Context, err error) {
	var rf *compensation.RetryFailureError
	if w.maxAttempts == 0 || !errors.As(err, &rf) || rf.Attempts < w.maxAttempts {
		return
	}
	w.logger.ErrorContext(ctx, AttemptCapReachedMsg,
		"orderId", rf.Cancel.OrderID, "groupNo", rf.Cancel.GroupNoText(), "promiseId", rf.Cancel.PromiseID,
		"attempts", rf.Attempts, "max_attempts", w.maxAttempts, "error", rf.Err)
}

// Run drains the queue until ctx is cancelled and then returns ctx.Err().
// A pass that releases a full batch is followed immediately by another; any
// other pass sleeps for the interval. A failing pass is logged and retried
// after the interval -- the rows stay unreleased, so nothing is lost.
func (w *Worker) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := w.RetryOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			w.logger.ErrorContext(ctx, "compensation worker pass failed", "error", err)
		}
		if n == w.batchSize && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(w.interval):
		}
	}
}
