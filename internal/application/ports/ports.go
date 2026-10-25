// Package ports declares the application's OUT ports: interfaces only
// (enforced by internal/architecture). Their non-interface vocabulary is the
// sentinels of errors.go and the data packages readmodel, promising and
// idempotency.
//
// Optimistic concurrency: an order is saved under the rule "stored version ==
// o.LoadedVersion()" (several commands may run between a load and a save,
// ADR 0002); a create (LoadedVersion 0) expects that the id is not stored. A
// shopper, which has one command, is saved under "stored version ==
// Version()-1". Any mismatch, including an update of a row that no longer
// exists, is ErrConcurrentUpdate. After a successful order Save the adapter
// calls o.MarkPersisted(). Inside a UnitOfWork every repository joins
// the transaction carried by the ctx the unit handed to its function.
package ports

import (
	"context"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/idempotency"
	"github.com/claudioed/customer-ordering/internal/application/promising"
	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

// OrderRepository persists CustomerOrder aggregates.
type OrderRepository interface {
	// Get returns the order, or ErrNotFound.
	Get(ctx context.Context, id customerorder.ID) (*customerorder.CustomerOrder, error)
	// Save persists the order snapshot under the concurrency rule of the
	// package comment.
	Save(ctx context.Context, o *customerorder.CustomerOrder) error
	// ListByShopper returns up to limit orders of the shopper, newest first
	// (placedAt DESC, orderId ASC as the tiebreak, ADR 0002 A26), starting
	// strictly after (afterPlacedAt, afterOrderID) when afterPlacedAt is not
	// zero. A shopper without orders is an empty page, never ErrNotFound.
	ListByShopper(ctx context.Context, shopperID string, afterPlacedAt time.Time, afterOrderID string, limit int) ([]*customerorder.CustomerOrder, error)
}

// ShopperRepository persists Shopper aggregates.
type ShopperRepository interface {
	// Get returns the shopper, or ErrNotFound.
	Get(ctx context.Context, id shopper.ID) (*shopper.Shopper, error)
	// FindByEmail returns the shopper of the NORMALISED e-mail (trimmed and
	// lower-cased by the caller, ADR 0002 A16), or ErrNotFound.
	FindByEmail(ctx context.Context, email string) (*shopper.Shopper, error)
	// Save persists the shopper under the concurrency rule of the package
	// comment. A second shopper with the same e-mail (lower-cased) is
	// shopper.ErrShopperExists (unique index).
	Save(ctx context.Context, s *shopper.Shopper) error
}

// TrackingRepository stores the event-fed read models behind
// OrderTrackingView (ADR 0003). Writes are upserts and tolerate any arrival
// order: a scan may precede the label, a label the lock.
type TrackingRepository interface {
	// LinkAssignment stores the assignment link and fills orderId and groupNo
	// of every package row that carries the assignment id.
	LinkAssignment(ctx context.Context, link readmodel.AssignmentLink) error
	// ResolveAssignment returns the link of the assignment, or ErrNotFound.
	ResolveAssignment(ctx context.Context, assignmentID string) (readmodel.AssignmentLink, error)
	// AttachTrackingNumbers sets orderId and groupNo on the package rows of
	// the tracking numbers, creating the missing rows.
	AttachTrackingNumbers(ctx context.Context, orderID string, groupNo int, trackingNumbers []string) error
	// ApplyLabel upserts the package row from a label; the order link comes
	// from the assignment link when it is known.
	ApplyLabel(ctx context.Context, l readmodel.Label) error
	// ApplyScan records the scan when it is not older than the last one.
	ApplyScan(ctx context.Context, s readmodel.Scan) error
	// ApplyDelivery marks the package delivered (state DELIVERED).
	ApplyDelivery(ctx context.Context, d readmodel.Delivery) error
	// ApplyException records the exception reason.
	ApplyException(ctx context.Context, e readmodel.Exception) error
	// ApplyVoid marks a LABELLED (or unscanned) package VOIDED.
	ApplyVoid(ctx context.Context, trackingNumber string) error
	// ApplyOutcome records PackageLost or PackageReturned on the package row,
	// creating it: the first outcome wins, the package state becomes the
	// outcome unless the package was reported delivered, and a later scan
	// no longer changes the state.
	ApplyOutcome(ctx context.Context, o readmodel.Outcome) error
	// OutcomePackages returns the packages of the group that have an outcome,
	// ordered by instant then tracking number.
	OutcomePackages(ctx context.Context, orderID string, groupNo int) ([]readmodel.Outcome, error)
	// DeliveredPackages returns the packages of the group reported delivered.
	DeliveredPackages(ctx context.Context, orderID string, groupNo int) ([]readmodel.Delivery, error)
	// PatchGroupSignal sets the non-nil fields of the group's signal row.
	PatchGroupSignal(ctx context.Context, p readmodel.GroupSignalPatch) error
	// GroupSignals returns the signal rows of the order ordered by groupNo.
	GroupSignals(ctx context.Context, orderID string) ([]readmodel.GroupSignal, error)
	// Packages returns the package rows of the order ordered by group and
	// tracking number.
	Packages(ctx context.Context, orderID string) ([]readmodel.Package, error)
}

// Inbox makes consumers idempotent on the CloudEvents id.
type Inbox interface {
	// Claim records the event id and reports whether this is the first time
	// it is seen. Called with the ctx of a unit of work, the claim rolls back
	// with it.
	Claim(ctx context.Context, eventID, eventType string) (first bool, err error)
}

// EventOutbox is the write side of the transactional outbox. Called with the
// ctx handed to UnitOfWork.Do it joins the SAME transaction as the aggregate
// Save calls. Appending no events is a no-op.
type EventOutbox interface {
	Append(ctx context.Context, events ...shared.Event) error
}

// UnitOfWork runs fn as ONE atomic unit: every repository, inbox and outbox
// call fn makes with the ctx it is handed commits together when fn returns
// nil, or rolls back together when fn returns an error (or panics). Do is
// re-entrant; the error fn returns is returned unchanged.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// PromiseGateway is the synchronous in-organization client of
// delivery-promise (sync_calls QuoteDeliveryOffers and MakePromise, and
// ConfirmPromise, E7, and CancelPromise for its compensation). Failures are the ErrPromise... sentinels.
type PromiseGateway interface {
	// QuoteOffers quotes the offers (all tiers) for a destination prefix and a
	// group's lines.
	QuoteOffers(ctx context.Context, destinationPostalPrefix string, lines []promising.Line) ([]promising.Offer, error)
	// MakePromise makes a provisional promise for one group (idempotent per
	// order and group).
	MakePromise(ctx context.Context, req promising.MakeRequest) (promising.Terms, error)
	// ConfirmPromise confirms a promise for an order group (idempotent).
	ConfirmPromise(ctx context.Context, promiseID, orderID string, groupNo int) error
	// CancelPromise releases the hold of a promise (compensation of a
	// checkout whose later confirm failed, ADR 0002 A14). Idempotent: a repeat
	// on a CANCELLED promise succeeds. ErrPromiseNotCancellable when the
	// promise is unknown or is not CONFIRMED or AT_RISK (PROVISIONAL, KEPT,
	// BROKEN, RELEASED).
	CancelPromise(ctx context.Context, promiseID, orderID string, groupNo int) error
}

// IdempotencyStore remembers the first response per (route, key) for 24 hours.
type IdempotencyStore interface {
	// Get returns the stored record, or ErrNotFound (expired records are
	// treated as absent).
	Get(ctx context.Context, route, key string) (idempotency.Record, error)
	// Put stores the record unless one exists; it reports whether it stored.
	Put(ctx context.Context, route, key string, rec idempotency.Record) (bool, error)
}

// Clock is the service's notion of "now"; use cases stamp aggregates with it.
type Clock interface {
	Now() time.Time
}

// IDGenerator mints the ids of new aggregates.
type IDGenerator interface {
	// NewShopperID mints a shopper id.
	NewShopperID() shopper.ID
	// NewAddressID mints the id of a registration-time saved address.
	NewAddressID() shopper.AddressID
	// AddressIDFor derives the id of an address added under an
	// Idempotency-Key (ADR 0002, A24): the same (shopper, key) always yields
	// the same id, so a replayed add is a no-op.
	AddressIDFor(shopperID shopper.ID, key string) shopper.AddressID
	// OrderIDFor derives the order id of a checkout from its Idempotency-Key
	// (ADR 0002, A13): the same key always yields the same id.
	OrderIDFor(idempotencyKey string) customerorder.ID
}
