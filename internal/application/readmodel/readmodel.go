// Package readmodel holds the plain data of the event-fed local read models
// (ADR 0003): assignment links, tracking packages and per-group promise
// signals. The ports package stays interfaces-only, so the structs live here.
package readmodel

import "time"

// AssignmentLink resolves an order-sourcing assignment id to the order group
// it fulfils. It is written by FulfillmentLocked.
type AssignmentLink struct {
	AssignmentID string
	OrderID      string
	GroupNo      int
	PromiseID    string
}

// GroupRef names one shipment group of one order. A tracking number belongs to
// a set of them once order-sourcing merges groups into one package (ADR 0012).
type GroupRef struct {
	OrderID string
	GroupNo int
}

// Label is the effect of LabelIssued on a package row.
type Label struct {
	TrackingNumber string
	AssignmentID   string
	Carrier        string
	At             time.Time
}

// Scan is the effect of PackageScanned.
type Scan struct {
	TrackingNumber string
	NodeID         string
	State          string
	At             time.Time
}

// Delivery is the effect of PackageDelivered.
type Delivery struct {
	TrackingNumber string
	AssignmentID   string
	OrderID        string
	At             time.Time
}

// Exception is the effect of PackageExceptionRaised.
type Exception struct {
	TrackingNumber string
	AssignmentID   string
	OrderID        string
	Reason         string
	At             time.Time
}

// Eta is the effect of PackageEtaWindowAssigned (ADR 0014): the day-of delivery
// window of one package. AssignedAt orders windows (the latest wins).
type Eta struct {
	TrackingNumber string
	AssignmentID   string
	OrderID        string
	Start          time.Time
	End            time.Time
	AssignedAt     time.Time
}

// The reasons of a delivery-point flag (ADR 0015).
const (
	PointFlagNotHome       = "NOT_HOME"
	PointFlagAccessProblem = "ACCESS_PROBLEM"
)

// PointFlag is the effect of PackageDeliveryPointFlagged (ADR 0015): the
// delivery point of the package failed twice. FlaggedAt orders flags (the
// newest wins).
type PointFlag struct {
	TrackingNumber string
	AssignmentID   string
	OrderID        string
	Reason         string
	FlaggedAt      time.Time
}

// The terminal package outcomes (PackageLost, PackageReturned), also the state
// the tracking view shows for the package.
const (
	OutcomeLost     = "LOST"
	OutcomeReturned = "RETURNED"
)

// Outcome is the effect of PackageLost or PackageReturned: Kind is
// OutcomeLost or OutcomeReturned and At is declaredAt or returnedAt.
type Outcome struct {
	TrackingNumber string
	OrderID        string
	Kind           string
	At             time.Time
}

// Package is one tracked package of an order, as the tracking view shows it.
// Optional fields are empty or zero when unknown.
type Package struct {
	TrackingNumber  string
	AssignmentID    string
	OrderID         string
	GroupNo         int
	Carrier         string
	State           string
	LastScanNodeID  string
	LastScanAt      time.Time
	DeliveredAt     time.Time
	ExceptionReason string
	// Outcome is OutcomeLost or OutcomeReturned once PackageLost or
	// PackageReturned was applied (first one wins), with its instant.
	Outcome   string
	OutcomeAt time.Time
	// EtaStart, EtaEnd and EtaAssignedAt are the stored day-of window of the
	// package (PackageEtaWindowAssigned, ADR 0014); all zero when none arrived.
	// Whether it is still current is the view's rule, not the store's.
	EtaStart      time.Time
	EtaEnd        time.Time
	EtaAssignedAt time.Time
	// PointFlagReason and PointFlaggedAt are the stored delivery-point flag of
	// the package (PackageDeliveryPointFlagged, ADR 0015); empty and zero when
	// none arrived. Whether it is shown is the view's rule.
	PointFlagReason string
	PointFlaggedAt  time.Time
}

// GroupSignalPatch sets the signal fields that are non-nil for one group.
type GroupSignalPatch struct {
	OrderID            string
	GroupNo            int
	AtRiskReason       *string
	UnsourceableReason *string
	PromiseVerdict     *string
	LateByMinutes      *int
}

// GroupSignal is the stored signal of one group (promise and sourcing facts
// that change no aggregate).
type GroupSignal struct {
	OrderID            string
	GroupNo            int
	AtRiskReason       string
	UnsourceableReason string
	PromiseVerdict     string
	LateByMinutes      *int
}
