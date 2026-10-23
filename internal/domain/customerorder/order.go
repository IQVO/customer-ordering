// Package customerorder is the CustomerOrder aggregate: a shopper's order with
// its lines, the ship-to address (synthetic PII), one promise per shipment
// group and the customer-facing status.
//
// Order states: PLACED, IN_FULFILLMENT, PARTIALLY_SHIPPED, SHIPPED,
// PARTIALLY_DELIVERED, DELIVERED, LOST, RETURNED, CANCELLED. The order status
// is derived from the group states (derive in status.go, ADR 0002 A1 and
// A23); group states are PROMISED, LOCKED, SHIPPED, DELIVERED, LOST, RETURNED,
// CANCELLATION_REQUESTED, CANCELLED.
//
// Invariants: every line is in exactly one group and every group has a promise
// (ErrLineWithoutPromise); the promise destination matches the ship-to
// (ErrPromiseDestinationMismatch); cancel only while a group is unlocked
// (ErrCancelAfterLock, advisory); status moves forward only
// (ErrStatusRegression).
package customerorder

import (
	"errors"
	"time"

	"github.com/claudioed/customer-ordering/internal/domain/shipto"
)

// The invariants of the model, by the model's names.
var (
	// ErrLineWithoutPromise: every line is in exactly one group and every group
	// has a promise (422 line-without-promise).
	ErrLineWithoutPromise = errors.New("line without promise")
	// ErrPromiseDestinationMismatch: the promise destination prefix matches the
	// ship-to (422 promise-destination-mismatch).
	ErrPromiseDestinationMismatch = errors.New("promise destination mismatch")
	// ErrCancelAfterLock: no group is unlocked any more (409 cancel-after-lock).
	ErrCancelAfterLock = errors.New("cancel after lock")
	// ErrStatusRegression: status moves forward only (409 status-regression).
	ErrStatusRegression = errors.New("status regression")
	// ErrUnknownGroup: the order has no such group (a consumed event named it).
	ErrUnknownGroup = errors.New("unknown group")
)

// Bounds (ADR 0002, A15). Plain literal constants.
const (
	// MaxLines is the most lines an order holds.
	MaxLines = 100
	// MaxGroups is the most shipment groups an order holds.
	MaxGroups = 50
	// MaxTrackingNumbers is the most tracking numbers of one group.
	MaxTrackingNumbers = 50
	// MaxTrackingNumberLen is the longest tracking number.
	MaxTrackingNumberLen = 64
	// MaxReason is the longest cancellation reason.
	MaxReason = 200
	// MaxNumber is the Postgres integer ceiling of line, group and quantity numbers.
	MaxNumber = 2147483647
)

// ID identifies a customer order (orderId).
type ID string

// Status is the customer-facing status of an order.
type Status string

// The order states.
const (
	StatusPlaced             Status = "PLACED"
	StatusInFulfillment      Status = "IN_FULFILLMENT"
	StatusPartiallyShipped   Status = "PARTIALLY_SHIPPED"
	StatusShipped            Status = "SHIPPED"
	StatusPartiallyDelivered Status = "PARTIALLY_DELIVERED"
	StatusDelivered          Status = "DELIVERED"
	StatusLost               Status = "LOST"
	StatusReturned           Status = "RETURNED"
	StatusCancelled          Status = "CANCELLED"
)

// Valid reports whether s is one of the nine order states.
func (s Status) Valid() bool {
	switch s {
	case StatusPlaced, StatusInFulfillment, StatusPartiallyShipped, StatusShipped,
		StatusPartiallyDelivered, StatusDelivered, StatusLost, StatusReturned, StatusCancelled:
		return true
	}
	return false
}

// GroupStatus is the state of one shipment group.
type GroupStatus string

// The group states.
const (
	GroupPromised              GroupStatus = "PROMISED"
	GroupLocked                GroupStatus = "LOCKED"
	GroupShipped               GroupStatus = "SHIPPED"
	GroupDelivered             GroupStatus = "DELIVERED"
	GroupLost                  GroupStatus = "LOST"
	GroupReturned              GroupStatus = "RETURNED"
	GroupCancellationRequested GroupStatus = "CANCELLATION_REQUESTED"
	GroupCancelled             GroupStatus = "CANCELLED"
)

// Valid reports whether s is one of the eight group states.
func (s GroupStatus) Valid() bool {
	switch s {
	case GroupPromised, GroupLocked, GroupShipped, GroupDelivered, GroupLost, GroupReturned, GroupCancellationRequested, GroupCancelled:
		return true
	}
	return false
}

// Tier is a delivery-promise service tier.
type Tier string

// The four service tiers.
const (
	TierSameDay  Tier = "SAME_DAY"
	TierNextDay  Tier = "NEXT_DAY"
	TierTwoDay   Tier = "TWO_DAY"
	TierStandard Tier = "STANDARD"
)

// Valid reports whether t is one of the four tiers.
func (t Tier) Valid() bool {
	return t == TierSameDay || t == TierNextDay || t == TierTwoDay || t == TierStandard
}

// OrderLine is one cart line.
type OrderLine struct {
	LineNo        int
	CatalogItemID string
	Quantity      int
}

// GroupPromise is the order's record of one shipment group: its promise
// snapshot, its lines and what the consumed events reported (ADR 0002, A2).
type GroupPromise struct {
	GroupNo           int
	PromiseID         string
	Tier              Tier
	WitnessNodeID     string
	ShipBy            time.Time
	DeliverByEarliest time.Time
	DeliverByLatest   time.Time
	LineNos           []int
	Status            GroupStatus
	RevisionNo        int

	AssignmentID string
	NodeID       string
	LockedAt     time.Time
	ShippedAt    time.Time

	TrackingNumbers          []string
	DeliveredTrackingNumbers []string
	DeliveredAt              time.Time

	// OutcomeTrackingNumber is the package PackageLost or PackageReturned
	// named; LostAt and ReturnedAt are the instants of the group's terminal
	// outcome (ADR 0002, A19). All three are empty unless the group is LOST
	// or RETURNED.
	OutcomeTrackingNumber string
	LostAt                time.Time
	ReturnedAt            time.Time

	CancelReason        string
	CancelRequestedAt   time.Time
	CancelledAt         time.Time
	CancelRefusedReason string
}

// closedWithoutDelivery reports a LOST or RETURNED group (terminal, A19).
func (g GroupPromise) closedWithoutDelivery() bool {
	return g.Status == GroupLost || g.Status == GroupReturned
}

// terminal reports a group no command can change any more.
func (g GroupPromise) terminal() bool {
	return g.Status == GroupCancelled || g.Status == GroupDelivered || g.closedWithoutDelivery()
}

func (g GroupPromise) clone() GroupPromise {
	g.LineNos = append([]int{}, g.LineNos...)
	g.TrackingNumbers = append([]string{}, g.TrackingNumbers...)
	g.DeliveredTrackingNumbers = append([]string{}, g.DeliveredTrackingNumbers...)
	return g
}

// CustomerOrder is the aggregate root.
type CustomerOrder struct {
	id          ID
	shopperID   string
	shipTo      shipto.ShipTo
	prefix      string
	status      Status
	placedAt    time.Time
	cancelledAt time.Time
	lines       []OrderLine
	groups      []GroupPromise
	version     int64
	loaded      int64
}

// Snapshot is the persisted form of an order. Restore validates it.
type Snapshot struct {
	ID                      ID
	ShopperID               string
	ShipTo                  shipto.ShipTo
	DestinationPostalPrefix string
	Status                  Status
	PlacedAt                time.Time
	CancelledAt             time.Time
	Lines                   []OrderLine
	Groups                  []GroupPromise
	Version                 int64
}

// ID returns the order id.
func (o *CustomerOrder) ID() ID { return o.id }

// ShopperID returns the shopper who placed the order.
func (o *CustomerOrder) ShopperID() string { return o.shopperID }

// ShipTo returns the ship-to address (PII).
func (o *CustomerOrder) ShipTo() shipto.ShipTo { return o.shipTo }

// DestinationPostalPrefix returns the destination postal prefix.
func (o *CustomerOrder) DestinationPostalPrefix() string { return o.prefix }

// Status returns the order status.
func (o *CustomerOrder) Status() Status { return o.status }

// PlacedAt returns when the order was placed.
func (o *CustomerOrder) PlacedAt() time.Time { return o.placedAt }

// CancelledAt returns when the order became CANCELLED (zero before).
func (o *CustomerOrder) CancelledAt() time.Time { return o.cancelledAt }

// Version returns the aggregate version: it grows by one for every command
// that changed the order.
func (o *CustomerOrder) Version() int64 { return o.version }

// LoadedVersion returns the version the order had when it was loaded from the
// repository, 0 for an order that was never stored. A repository saves under
// the rule "stored version == LoadedVersion" (several commands may run between
// a load and a save, ADR 0002).
func (o *CustomerOrder) LoadedVersion() int64 { return o.loaded }

// MarkPersisted records that the current version is stored. Repositories call
// it after a successful Save.
func (o *CustomerOrder) MarkPersisted() { o.loaded = o.version }

// Lines returns a copy of the order lines.
func (o *CustomerOrder) Lines() []OrderLine { return append([]OrderLine{}, o.lines...) }

// Groups returns a copy of the shipment groups.
func (o *CustomerOrder) Groups() []GroupPromise {
	out := make([]GroupPromise, len(o.groups))
	for i, g := range o.groups {
		out[i] = g.clone()
	}
	return out
}

// GroupNoByTrackingNumber returns the group that announced the tracking number.
func (o *CustomerOrder) GroupNoByTrackingNumber(tn string) (int, bool) {
	for _, g := range o.groups {
		for _, t := range g.TrackingNumbers {
			if t == tn {
				return g.GroupNo, true
			}
		}
	}
	return 0, false
}

// GroupNoByAssignment returns the group locked under the assignment id.
func (o *CustomerOrder) GroupNoByAssignment(assignmentID string) (int, bool) {
	for _, g := range o.groups {
		if g.AssignmentID != "" && g.AssignmentID == assignmentID {
			return g.GroupNo, true
		}
	}
	return 0, false
}

// GroupNoByPromise returns the group holding the promise.
func (o *CustomerOrder) GroupNoByPromise(promiseID string) (int, bool) {
	i := o.indexByPromise(promiseID)
	if i < 0 {
		return 0, false
	}
	return o.groups[i].GroupNo, true
}

// Snapshot returns the persisted form.
func (o *CustomerOrder) Snapshot() Snapshot {
	return Snapshot{
		ID: o.id, ShopperID: o.shopperID, ShipTo: o.shipTo, DestinationPostalPrefix: o.prefix, Status: o.status,
		PlacedAt: o.placedAt, CancelledAt: o.cancelledAt, Lines: o.Lines(), Groups: o.Groups(), Version: o.version,
	}
}

func (o *CustomerOrder) indexByGroup(groupNo int) int {
	for i := range o.groups {
		if o.groups[i].GroupNo == groupNo {
			return i
		}
	}
	return -1
}

func (o *CustomerOrder) indexByPromise(promiseID string) int {
	for i := range o.groups {
		if o.groups[i].PromiseID == promiseID {
			return i
		}
	}
	return -1
}

// bump records a change: it derives the new order status, stamps cancelledAt
// when the order just became CANCELLED and raises the version.
func (o *CustomerOrder) bump(at time.Time) {
	o.status = derive(o.groups)
	if o.status == StatusCancelled && o.cancelledAt.IsZero() {
		o.cancelledAt = at
	}
	o.version++
}
