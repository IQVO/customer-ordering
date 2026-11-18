// Package report is the self-contained read-model region of the analytics read
// side (ADR 0018): the facts the projector folds in, the shape of the ordering
// performance report, the KPI rules (split-shipment, consolidated-tier and
// pickup classification, the bounded notice-kind breakdown, shares), the
// date-range rules, the freshness lag, and the writer/reader ports the
// analytical store implements. It imports no other internal package (arch
// test): the vocabulary below is hand-mirrored from apis/asyncapi.yaml, never
// imported from the domain, so the OLTP model can never leak into the
// projection.
package report

import (
	"context"
	"errors"
	"time"
)

// Kind is which of the three projected events a fact came from. The names are
// the CloudEvents event names (the last segment of `type`).
type Kind string

// The three projected events of retailnetwork.customer-ordering.events.
const (
	KindOrderPlaced    Kind = "CustomerOrderPlaced"
	KindOrderCancelled Kind = "CustomerOrderCancelled"
	KindNoticeDecided  Kind = "ShopperNoticeDecided"
)

// Event is one decoded analytics fact ready to project. It is the union of the
// three payloads; only the fields of its Kind are set. It carries ids, codes,
// counts and instants only: never a name, an address, a postal prefix, a
// consolidation key or free text (the cancellation reason is not read).
type Event struct {
	Kind    Kind
	EventID string    // CloudEvents id: the idempotency key
	At      time.Time // CloudEvents time: when the use case raised it (freshness)

	// OrderID is the order every fact is about (the CloudEvents subject).
	OrderID string
	// OccurredAt is the business instant of the fact: `placedAt`,
	// `cancelledAt` or `decidedAt`. The reports bucket days on it.
	OccurredAt time.Time

	// CustomerOrderPlaced.
	GroupCount         int  // shipment groups of the order
	ConsolidatedGroups int  // groups promised under the CONSOLIDATED tier
	PickupPoint        bool // the order goes to a pickup point

	// ShopperNoticeDecided.
	Family    string // the notice family as published; the store keeps NormalizeFamily(Family), never the raw value
	NoticeKey string // sha256 dedupe key of the notice (an opaque hash)
}

// Projection is the WRITER port: Apply records the event id and projects the
// fact in ONE transaction. applied is false when the id was already recorded
// (a replay): nothing changed. An error wrapping ErrRejected is deterministic
// (the store can never accept this event: retry is pointless); any other error
// is transient and the same event may be retried.
type Projection interface {
	Apply(ctx context.Context, e Event) (applied bool, err error)
}

// ErrRejected marks an event the analytical store deterministically refuses
// (a data-exception or integrity-violation class error).
var ErrRejected = errors.New("report: event rejected by the analytical store")

// Reader is the READER port. Every method answers for the half-open instant
// range [r.From, r.To) of the business instants, bucketed by UTC day. Results
// are ordered by day (then family) and never nil.
type Reader interface {
	PlacedOrders(ctx context.Context, r Range) ([]PlacedDay, error)
	CancelledOrders(ctx context.Context, r Range) ([]CancelledDay, error)
	NoticesDecided(ctx context.Context, r Range) ([]NoticeDay, error)
	// LastEventAt is the CloudEvents time of the newest event the projection
	// applied (nil while it applied none): the basis of the freshness lag.
	LastEventAt(ctx context.Context) (*time.Time, error)
}

// PlacedDay is what the store counts for one UTC day of placedAt: the orders
// placed, those with at least one CONSOLIDATED group, those to a pickup point
// and those with at least SplitMinGroups shipment groups.
type PlacedDay struct {
	Day          time.Time
	Orders       int
	Consolidated int
	Pickup       int
	Split        int
}

// CancelledDay is the number of DISTINCT orders with a cancellation request
// on one UTC day of cancelledAt.
type CancelledDay struct {
	Day    time.Time
	Orders int
}

// NoticeDay is the number of notices decided of one family on one UTC day of
// decidedAt.
type NoticeDay struct {
	Day    time.Time
	Family string
	Count  int
}
