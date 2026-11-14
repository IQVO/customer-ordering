package customerorder

import (
	"time"

	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

const entity = "customerorder"

// PlacedGroup is one shipment group of CustomerOrderPlaced.
type PlacedGroup struct {
	GroupNo           int
	PromiseID         string
	Tier              Tier
	WitnessNodeID     string
	ShipBy            time.Time
	DeliverByEarliest time.Time
	DeliverByLatest   time.Time
	TimeZone          string // optional IANA zone of the window (ADR 0009)
	Lines             []OrderLine
	// ChosenDeliveryDay and ConsolidationEligible: CONSOLIDATED groups only,
	// omitted on the wire when unknown (ADR 0013).
	ChosenDeliveryDay     string
	ConsolidationEligible *bool
}

// CustomerOrderPlaced is raised by Place. Its fields are the model's data list.
// It carries the destination postal prefix and, for a pickup order, the pickup
// point id (ADR 0016) — never the address.
type CustomerOrderPlaced struct {
	shared.Header
	OrderID                 ID
	ShopperID               string
	PickupPointID           string // the point of a pickup order; "" for a ship-to order (ADR 0016)
	DestinationPostalPrefix string
	PlacedAt                time.Time
	Groups                  []PlacedGroup
	// ConsolidationKey is the keyed hash of shopper and ship-to (ADR 0012),
	// "" when the service has no secret: the wire field is then omitted.
	ConsolidationKey string
}

// EventName returns "CustomerOrderPlaced".
func (CustomerOrderPlaced) EventName() string { return "CustomerOrderPlaced" }

// Entity returns "customerorder".
func (CustomerOrderPlaced) Entity() string { return entity }

// SubjectID returns the orderId.
func (e CustomerOrderPlaced) SubjectID() string { return string(e.OrderID) }

// CustomerOrderCancelled is raised by CancelOrder and CancelUnkeepableGroup.
// It is a request to order-sourcing (ADR 0002, A3).
type CustomerOrderCancelled struct {
	shared.Header
	OrderID     ID
	Reason      string
	CancelledAt time.Time
	GroupNos    []int
}

// EventName returns "CustomerOrderCancelled".
func (CustomerOrderCancelled) EventName() string { return "CustomerOrderCancelled" }

// Entity returns "customerorder".
func (CustomerOrderCancelled) Entity() string { return entity }

// SubjectID returns the orderId.
func (e CustomerOrderCancelled) SubjectID() string { return string(e.OrderID) }

var (
	_ shared.Event = CustomerOrderPlaced{}
	_ shared.Event = CustomerOrderCancelled{}
)
