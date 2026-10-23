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
	DestinationPostalPrefix string
}

// Offer is one quoted delivery offer.
type Offer struct {
	OfferID           string
	Tier              string
	DeliverByEarliest time.Time
	DeliverByLatest   time.Time
	ExpiresAt         time.Time
}
