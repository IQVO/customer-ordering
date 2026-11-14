// Package views holds the read DTOs the use cases return. They are plain data
// (no behaviour beyond the From... mappers) so the ports package can stay
// interfaces-only and the HTTP adapter can encode them directly.
package views

import (
	"time"

	"github.com/claudioed/customer-ordering/internal/application/pagination"
	"github.com/claudioed/customer-ordering/internal/application/promising"
	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/notice"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

// OrderLine is one cart line.
type OrderLine struct {
	LineNo        int
	CatalogItemID string
	Quantity      int
}

// OrderGroup is one shipment group with its promise and status.
type OrderGroup struct {
	GroupNo           int
	PromiseID         string
	Tier              string
	ShipBy            time.Time
	DeliverByEarliest time.Time
	DeliverByLatest   time.Time
	TimeZone          string // optional IANA zone of the window; empty = unknown (ADR 0009)
	LineNos           []int
	Status            string
	RevisionNo        int
	// ChosenDeliveryDay and ConsolidationEligible: CONSOLIDATED groups only;
	// empty / nil = unknown (ADR 0013).
	ChosenDeliveryDay     string
	ConsolidationEligible *bool
	CancelReason          string
	CancelRequestedAt     time.Time
	CancelRefusedReason   string
}

// Order is the customer order without the ship-to address.
type Order struct {
	OrderID                 string
	ShopperID               string
	Status                  string
	DestinationPostalPrefix string
	PlacedAt                time.Time
	CancelledAt             time.Time
	Lines                   []OrderLine
	Groups                  []OrderGroup
}

// OrderFrom maps an order aggregate. The ship-to is not part of it.
func OrderFrom(o *customerorder.CustomerOrder) Order {
	lines := make([]OrderLine, 0, len(o.Lines()))
	for _, l := range o.Lines() {
		lines = append(lines, OrderLine{LineNo: l.LineNo, CatalogItemID: l.CatalogItemID, Quantity: l.Quantity})
	}
	groups := make([]OrderGroup, 0, len(o.Groups()))
	for _, g := range o.Groups() {
		groups = append(groups, OrderGroup{
			GroupNo: g.GroupNo, PromiseID: g.PromiseID, Tier: string(g.Tier), ShipBy: g.ShipBy,
			DeliverByEarliest: g.DeliverByEarliest, DeliverByLatest: g.DeliverByLatest, TimeZone: g.TimeZone, LineNos: g.LineNos,
			Status: string(g.Status), RevisionNo: g.RevisionNo, CancelReason: g.CancelReason,
			ChosenDeliveryDay: g.ChosenDeliveryDay, ConsolidationEligible: g.ConsolidationEligible,
			CancelRequestedAt: g.CancelRequestedAt, CancelRefusedReason: g.CancelRefusedReason,
		})
	}
	return Order{
		OrderID: string(o.ID()), ShopperID: o.ShopperID(), Status: string(o.Status()),
		DestinationPostalPrefix: o.DestinationPostalPrefix(), PlacedAt: o.PlacedAt(), CancelledAt: o.CancelledAt(),
		Lines: lines, Groups: groups,
	}
}

// ShipTo is the delivery address (synthetic PII).
type ShipTo struct {
	Name        string
	Line1       string
	Line2       string
	City        string
	Region      string
	PostalCode  string
	CountryCode string
	Phone       string
}

// ShipToFrom maps the value object.
func ShipToFrom(s shipto.ShipTo) ShipTo { return ShipTo(s) }

// Destination is the order's destination as the restricted ship-to route
// answers it (ADR 0016): the address of a ship-to order, or the pickup point
// id of a pickup order, which carries no PII at all.
type Destination struct {
	ShipTo        ShipTo
	PickupPointID string
}

// DestinationFrom maps the aggregate's destination.
func DestinationFrom(o *customerorder.CustomerOrder) Destination {
	if o.PickupPointID() != "" {
		return Destination{PickupPointID: o.PickupPointID()}
	}
	return Destination{ShipTo: ShipToFrom(o.ShipTo())}
}

// SavedAddress is one saved address of a shopper: its id plus the ship-to
// fields (NG1; synthetic PII, returned only on the shopper routes).
type SavedAddress struct {
	AddressID   string
	Name        string
	Line1       string
	Line2       string
	City        string
	Region      string
	PostalCode  string
	CountryCode string
	Phone       string
}

// SavedAddressFrom maps a domain saved address.
func SavedAddressFrom(a shopper.SavedAddress) SavedAddress {
	return SavedAddress{
		AddressID: string(a.ID), Name: a.ShipTo.Name, Line1: a.ShipTo.Line1, Line2: a.ShipTo.Line2,
		City: a.ShipTo.City, Region: a.ShipTo.Region, PostalCode: a.ShipTo.PostalCode,
		CountryCode: a.ShipTo.CountryCode, Phone: a.ShipTo.Phone,
	}
}

// Shopper is a registered shopper with its saved addresses (NG1).
type Shopper struct {
	ShopperID      string
	Email          string
	Status         string
	RegisteredAt   time.Time
	SavedAddresses []SavedAddress
}

// ShopperFrom maps a shopper aggregate.
func ShopperFrom(s *shopper.Shopper) Shopper {
	addresses := make([]SavedAddress, 0, len(s.SavedAddresses()))
	for _, a := range s.SavedAddresses() {
		addresses = append(addresses, SavedAddressFrom(a))
	}
	return Shopper{
		ShopperID: string(s.ID()), Email: s.Email(), Status: string(s.Status()), RegisteredAt: s.RegisteredAt(),
		SavedAddresses: addresses,
	}
}

// OrderList is one page of a shopper's order history (NG2). Next is the keyset
// the next page starts after, nil when the page is the last.
type OrderList struct {
	Items []Order
	Next  *pagination.Keyset
}

// TrackingPackage is one package of a group.
type TrackingPackage struct {
	TrackingNumber  string
	Carrier         string
	State           string
	LastScanNodeID  string
	LastScanAt      time.Time
	DeliveredAt     time.Time
	ExceptionReason string
	// DestinationKind is "PICKUP_POINT" for a package labelled to a pickup
	// point and "SHIP_TO" otherwise; PickupPointID names the point (ADR 0016).
	DestinationKind string
	PickupPointID   string
	// PickupPoint carries the collection facts of a pickup package — what the
	// shopper sees next (ADR 0016); nil on a ship-to package.
	PickupPoint *PickupPoint
	// EtaStart and EtaEnd are the day-of delivery window (ADR 0014); zero
	// unless the window is current (see etaWindow).
	EtaStart time.Time
	EtaEnd   time.Time
	// DeliveryPointAdvisory is the delivery-point flag (ADR 0015); nil unless
	// the package has not ended (see pointAdvisory).
	DeliveryPointAdvisory *DeliveryPointAdvisory
}

// PickupPoint is the collection state of a pickup package (ADR 0016).
type PickupPoint struct {
	ReadyForCollectionAt time.Time
	HoldUntil            time.Time
	// CollectedAt is zero until the shopper collected the package.
	CollectedAt time.Time
	// ProofCode is the collection proof, empty when none was taken.
	ProofCode string
}

// pickupView is the ONE rule that decides whether a package shows its pickup
// point (ADR 0016): the label or the deposit named a point. The collection
// object waits for the deposit (readyForCollectionAt is its first fact);
// collectedAt zero means still collectable.
func pickupView(p readmodel.Package) (string, string, *PickupPoint) {
	if p.PickupPointID == "" {
		return "SHIP_TO", "", nil
	}
	if p.ReadyForCollectionAt.IsZero() {
		return "PICKUP_POINT", p.PickupPointID, nil
	}
	return "PICKUP_POINT", p.PickupPointID, &PickupPoint{
		ReadyForCollectionAt: p.ReadyForCollectionAt, HoldUntil: p.HoldUntil,
		CollectedAt: p.CollectedAt, ProofCode: p.ProofCode,
	}
}

// DeliveryPointAdvisory tells the shopper that the delivery point of a package
// that is still on its way failed twice. Codes only: the words belong to the
// storefront.
type DeliveryPointAdvisory struct {
	Reason    string
	FlaggedAt time.Time
}

// TrackingGroup is the tracking of one shipment group.
type TrackingGroup struct {
	GroupNo             int
	PromiseID           string
	Status              string
	DeliverByEarliest   time.Time
	DeliverByLatest     time.Time
	TimeZone            string // optional IANA zone of the window; empty = unknown (ADR 0009)
	AtRiskReason        string
	UnsourceableReason  string
	PromiseVerdict      string
	LateByMinutes       *int
	LockedAt            time.Time
	ShippedAt           time.Time
	CancelRefusedReason string
	// LostAt / ReturnedAt and OutcomePackage describe a LOST or RETURNED group
	// (PackageLost, PackageReturned); zero or empty otherwise.
	LostAt         time.Time
	ReturnedAt     time.Time
	OutcomePackage string
	Packages       []TrackingPackage
}

// Tracking is the OrderTrackingView of one order.
type Tracking struct {
	OrderID string
	Status  string
	Groups  []TrackingGroup
}

// stateOutForDelivery is the journey state in which a day-of window is current.
const stateOutForDelivery = "OUT_FOR_DELIVERY"

// etaWindow is the ONE rule that decides whether a package shows its day-of
// window (ADR 0014): the state is OUT_FOR_DELIVERY, a window was assigned, and
// it was assigned at or after the last scan. Same definition as `EtaCurrent` in
// shipping-and-tracking. A failed attempt (state back to AT_STATION), a newer
// scan or any terminal state hides it. The window is never clipped to the
// promise: it is shown as received.
func etaWindow(p readmodel.Package) (start, end time.Time) {
	if p.State != stateOutForDelivery || p.EtaAssignedAt.IsZero() || p.EtaAssignedAt.Before(p.LastScanAt) {
		return time.Time{}, time.Time{}
	}
	return p.EtaStart, p.EtaEnd
}

// pointAdvisory is the ONE rule that decides whether a package carries the
// delivery-point advisory (ADR 0015): a flag is stored and the package has not
// ended (state not DELIVERED, RETURNED, LOST or VOIDED). The advisory ends when
// the package ends; there is no "cleared" event.
func pointAdvisory(p readmodel.Package) *DeliveryPointAdvisory {
	if p.PointFlagReason == "" || p.PointFlaggedAt.IsZero() {
		return nil
	}
	switch p.State {
	case "DELIVERED", "RETURNED", "LOST", "VOIDED":
		return nil
	}
	return &DeliveryPointAdvisory{Reason: p.PointFlagReason, FlaggedAt: p.PointFlaggedAt}
}

// TrackingFrom joins the aggregate with the read-model rows. Packages whose
// group is unknown are not shown.
func TrackingFrom(o *customerorder.CustomerOrder, signals []readmodel.GroupSignal, packages []readmodel.Package) Tracking {
	byGroup := make(map[int]readmodel.GroupSignal, len(signals))
	for _, s := range signals {
		byGroup[s.GroupNo] = s
	}
	pkgs := make(map[int][]TrackingPackage)
	for _, p := range packages {
		start, end := etaWindow(p)
		advisory := pointAdvisory(p)
		kind, point, pickup := pickupView(p)
		pkgs[p.GroupNo] = append(pkgs[p.GroupNo], TrackingPackage{
			TrackingNumber: p.TrackingNumber, Carrier: p.Carrier, State: p.State, LastScanNodeID: p.LastScanNodeID,
			LastScanAt: p.LastScanAt, DeliveredAt: p.DeliveredAt, ExceptionReason: p.ExceptionReason,
			DestinationKind: kind, PickupPointID: point, PickupPoint: pickup,
			EtaStart: start, EtaEnd: end, DeliveryPointAdvisory: advisory,
		})
	}
	groups := make([]TrackingGroup, 0, len(o.Groups()))
	for _, g := range o.Groups() {
		sig := byGroup[g.GroupNo]
		list := pkgs[g.GroupNo]
		if list == nil {
			list = []TrackingPackage{}
		}
		groups = append(groups, TrackingGroup{
			GroupNo: g.GroupNo, PromiseID: g.PromiseID, Status: string(g.Status), DeliverByEarliest: g.DeliverByEarliest,
			DeliverByLatest: g.DeliverByLatest, TimeZone: g.TimeZone, AtRiskReason: sig.AtRiskReason, UnsourceableReason: sig.UnsourceableReason,
			PromiseVerdict: sig.PromiseVerdict, LateByMinutes: sig.LateByMinutes, LockedAt: g.LockedAt, ShippedAt: g.ShippedAt,
			CancelRefusedReason: g.CancelRefusedReason, LostAt: g.LostAt, ReturnedAt: g.ReturnedAt, OutcomePackage: g.OutcomeTrackingNumber,
			Packages: list,
		})
	}
	return Tracking{OrderID: string(o.ID()), Status: string(o.Status()), Groups: groups}
}

// RequotedOffer is a fresh offer shown after an expired one.
type RequotedOffer struct {
	GroupNo           int
	OfferID           string
	Tier              string
	DeliverByEarliest time.Time
	DeliverByLatest   time.Time
	TimeZone          string // optional IANA zone of the window; empty = unknown (ADR 0009)
	ExpiresAt         time.Time
}

// RequotedFrom maps the offers quoted for one group.
func RequotedFrom(groupNo int, offers []promising.Offer) []RequotedOffer {
	out := make([]RequotedOffer, 0, len(offers))
	for _, o := range offers {
		out = append(out, RequotedOffer{
			GroupNo: groupNo, OfferID: o.OfferID, Tier: o.Tier, DeliverByEarliest: o.DeliverByEarliest,
			DeliverByLatest: o.DeliverByLatest, TimeZone: o.TimeZone, ExpiresAt: o.ExpiresAt,
		})
	}
	return out
}

// NotificationPolicy is the read view of the notification policy (GET and PUT
// /notification-policy, ADR 0017). Version 0 is the built-in default: every
// family NOBODY and PublishedAt the zero time.
type NotificationPolicy struct {
	Version     int
	Rows        []notice.Row
	PublishedAt time.Time
}

// NotificationPolicyFrom builds the view of a policy.
func NotificationPolicyFrom(p *notice.Policy) NotificationPolicy {
	s := p.Snapshot()
	return NotificationPolicy{Version: s.Version, Rows: append([]notice.Row{}, s.Rows...), PublishedAt: s.PublishedAt}
}
