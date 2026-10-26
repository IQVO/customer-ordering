// Package usecases holds the application's use cases: one per operation of
// apis/openapi.yaml and one per consumed event. Every command follows one
// shape inside ONE ports.UnitOfWork: load the aggregate, call the domain, save
// it and append the raised events to the outbox, then return a view. Domain,
// shared and port errors pass through unchanged (errors.Is keeps working): the
// HTTP adapter maps them to problem types.
package usecases

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// Deps bundles the ports the use cases need.
type Deps struct {
	Orders   ports.OrderRepository
	Shoppers ports.ShopperRepository
	Tracking ports.TrackingRepository
	Inbox    ports.Inbox
	Outbox   ports.EventOutbox
	UoW      ports.UnitOfWork
	Promises ports.PromiseGateway
	Clock    ports.Clock
	IDs      ports.IDGenerator
	// Log receives the events a consumer skipped; nil discards them.
	Log *slog.Logger
	// Metrics records the business outcome of each checkout; nil records
	// nothing, so unit tests need no wiring.
	Metrics ports.OrderMetrics
}

const (
	// commandAttempts is how often a command runs when another writer got in
	// between its load and its save.
	commandAttempts = 2
	// consumerAttempts is the same for consumed events, which contend with
	// events of sibling groups of the same order (ADR 0002, A7).
	consumerAttempts = 5
)

func (d Deps) now() time.Time { return d.Clock.Now().UTC() }

func (d Deps) logger() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.New(slog.DiscardHandler)
}

// metrics returns the configured OrderMetrics or a no-op.
func (d Deps) metrics() ports.OrderMetrics {
	if d.Metrics != nil {
		return d.Metrics
	}
	return noMetrics{}
}

// noMetrics is the OrderMetrics of a Deps without one.
type noMetrics struct{}

func (noMetrics) OrderPlaced(context.Context)           {}
func (noMetrics) OrderRejected(context.Context, string) {}

func (d Deps) publish(ctx context.Context, events []shared.Event) error {
	if err := d.Outbox.Append(ctx, events...); err != nil {
		return fmt.Errorf("append events to outbox: %w", err)
	}
	return nil
}

// transact runs attempt in a unit of work. A ports.ErrConcurrentUpdate means
// another writer got in between the load and the save, so the whole attempt
// (load, domain call, save, outbox) runs again on fresh state, up to attempts
// times, before the conflict is reported.
func transact[T any](ctx context.Context, uow ports.UnitOfWork, attempts int, attempt func(ctx context.Context) (T, error)) (T, error) {
	var out T
	run := func(ctx context.Context) error {
		v, err := attempt(ctx)
		if err != nil {
			return err
		}
		out = v
		return nil
	}
	var err error
	for i := 0; i < attempts; i++ {
		if err = uow.Do(ctx, run); !errors.Is(err, ports.ErrConcurrentUpdate) {
			break
		}
	}
	if err != nil {
		var zero T
		return zero, err
	}
	return out, nil
}
