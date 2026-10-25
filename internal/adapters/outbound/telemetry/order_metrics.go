package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/claudioed/customer-ordering/internal/application/ports"
)

// meterName scopes this service's own instruments, keeping them distinct from
// the ones otelchi and the runtime collector register.
const meterName = "github.com/claudioed/customer-ordering"

// ordersPlacedName is the Tier 2 business metric of the context (ADR 0007, the
// fleet's standard metrics convention): every checkout attempt, by outcome.
const ordersPlacedName = "ordering.orders.placed"

const (
	outcomeKey = attribute.Key("outcome")
	reasonKey  = attribute.Key("reason")

	outcomePlaced   = "placed"
	outcomeRejected = "rejected"
)

// OrderMetrics implements ports.OrderMetrics against the global MeterProvider.
// Until Setup installs a real provider the global one is a no-op, so recording
// is cheap and safe in tests and local runs.
type OrderMetrics struct {
	counter metric.Int64Counter
}

var _ ports.OrderMetrics = (*OrderMetrics)(nil)

// NewOrderMetrics registers the counter. It only fails if the instrument name
// is invalid, which is a programming error, not a runtime condition. Call it
// after Setup so the instrument binds to the real MeterProvider.
func NewOrderMetrics() (*OrderMetrics, error) {
	counter, err := otel.Meter(meterName).Int64Counter(
		ordersPlacedName,
		metric.WithDescription("Checkout attempts by outcome (placed or rejected; reason says why a rejection happened, "+
			"as a problem slug such as offer-not-quotable or promise-capacity-exhausted). "+
			"A falling placed share with a rising offer-not-quotable or promise-capacity-exhausted means shoppers are refused "+
			"at checkout by delivery promising, not by their own input; a rising internal-error means the service itself is failing."),
		metric.WithUnit("{order}"),
	)
	if err != nil {
		return nil, err
	}
	return &OrderMetrics{counter: counter}, nil
}

// OrderPlaced counts an order that was placed.
func (m *OrderMetrics) OrderPlaced(ctx context.Context) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomePlaced)))
}

// OrderRejected counts a checkout that placed no order, with the reason why.
func (m *OrderMetrics) OrderRejected(ctx context.Context, reason string) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeRejected), reasonKey.String(reason)))
}
