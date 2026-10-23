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
