// Package views holds the read DTOs the use cases return. They are plain data
// (no behaviour beyond the From... mappers) so the ports package can stay
// interfaces-only and the HTTP adapter can encode them directly.
package views

import (
	"time"

	"github.com/claudioed/customer-ordering/internal/application/promising"
	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
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
	GroupNo             int
	PromiseID           string
	Tier                string
	ShipBy              time.Time
	DeliverByEarliest   time.Time
	DeliverByLatest     time.Time
	LineNos             []int
	Status              string
	RevisionNo          int
	CancelReason        string
	CancelRequestedAt   time.Time
	CancelRefusedReason string
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
			DeliverByEarliest: g.DeliverByEarliest, DeliverByLatest: g.DeliverByLatest, LineNos: g.LineNos,
			Status: string(g.Status), RevisionNo: g.RevisionNo, CancelReason: g.CancelReason,
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

// OrderList is one page of a shopper's order history (NG2). NextCursor is
// empty when the page is the last.
type OrderList struct {
	Items      []Order
	NextCursor string
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
}

// TrackingGroup is the tracking of one shipment group.
type TrackingGroup struct {
	GroupNo             int
	PromiseID           string
	Status              string
	DeliverByEarliest   time.Time
	DeliverByLatest     time.Time
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

// TrackingFrom joins the aggregate with the read-model rows. Packages whose
// group is unknown are not shown.
func TrackingFrom(o *customerorder.CustomerOrder, signals []readmodel.GroupSignal, packages []readmodel.Package) Tracking {
	byGroup := make(map[int]readmodel.GroupSignal, len(signals))
	for _, s := range signals {
		byGroup[s.GroupNo] = s
	}
	pkgs := make(map[int][]TrackingPackage)
	for _, p := range packages {
		pkgs[p.GroupNo] = append(pkgs[p.GroupNo], TrackingPackage{
			TrackingNumber: p.TrackingNumber, Carrier: p.Carrier, State: p.State, LastScanNodeID: p.LastScanNodeID,
			LastScanAt: p.LastScanAt, DeliveredAt: p.DeliveredAt, ExceptionReason: p.ExceptionReason,
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
			DeliverByLatest: g.DeliverByLatest, AtRiskReason: sig.AtRiskReason, UnsourceableReason: sig.UnsourceableReason,
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
	ExpiresAt         time.Time
}

// RequotedFrom maps the offers quoted for one group.
func RequotedFrom(groupNo int, offers []promising.Offer) []RequotedOffer {
	out := make([]RequotedOffer, 0, len(offers))
	for _, o := range offers {
		out = append(out, RequotedOffer{
			GroupNo: groupNo, OfferID: o.OfferID, Tier: o.Tier, DeliverByEarliest: o.DeliverByEarliest,
			DeliverByLatest: o.DeliverByLatest, ExpiresAt: o.ExpiresAt,
		})
	}
	return out
}
