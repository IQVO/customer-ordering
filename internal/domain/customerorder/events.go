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
	Lines             []OrderLine
}

// CustomerOrderPlaced is raised by Place. Its fields are the model's data list.
// It carries the destination postal prefix, never the address.
type CustomerOrderPlaced struct {
	shared.Header
	OrderID                 ID
	ShopperID               string
	DestinationPostalPrefix string
	PlacedAt                time.Time
	Groups                  []PlacedGroup
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
