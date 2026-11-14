// Package promising holds the data exchanged with delivery-promise through
// ports.PromiseGateway (the model's sync_calls QuoteDeliveryOffers and
// MakePromise, plus ConfirmPromise of E7).
package promising

import "time"

// Line is one line of a shipment group.
type Line struct {
	CatalogItemID string
	Quantity      int
}

// Destination is the checkout's destination as a quote names it (ADR 0016):
// exactly one of PostalPrefix and PickupPointID.
type Destination struct {
	PostalPrefix  string
	PickupPointID string
}

// Pickup reports whether the destination is the named pickup point.
func (d Destination) Pickup() bool { return d.PickupPointID != "" }

// MakeRequest asks delivery-promise for a provisional promise for one group,
// from the offer the shopper saw. OrderID and GroupNo make the call
// idempotent.
type MakeRequest struct {
	OrderID string
	GroupNo int
	OfferID string
	Lines   []Line
}

// Terms is the promise delivery-promise made.
type Terms struct {
	PromiseID               string
	Tier                    string
	WitnessNodeID           string
	ShipBy                  time.Time
	DeliverByEarliest       time.Time
	DeliverByLatest         time.Time
	TimeZone                string // optional IANA zone of the window; empty = unknown (ADR 0009)
	DestinationPostalPrefix string
	// PickupPointID is the promise's own point when it names one (ADR 0016):
	// an optional answer field, empty when the promise carries none.
	PickupPointID string
	// ConsolidationEligible is delivery-promise's answer, present only for a
	// CONSOLIDATED promise: whether the option leaves the network time to merge.
	// nil = absent or not a boolean (unknown, never false; ADR 0013).
	ConsolidationEligible *bool
}

// Offer is one quoted delivery offer.
type Offer struct {
	OfferID           string
	Tier              string
	DeliverByEarliest time.Time
	DeliverByLatest   time.Time
	TimeZone          string // optional IANA zone of the window; empty = unknown (ADR 0009)
	ExpiresAt         time.Time
}
