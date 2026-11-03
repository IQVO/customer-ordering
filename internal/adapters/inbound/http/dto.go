package http

import (
	"time"

	"github.com/claudioed/customer-ordering/internal/application/views"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
)

// ts renders an instant as RFC 3339 UTC; the zero time is "" (omitted).
func ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

type shipToDTO struct {
	Name        string `json:"name"`
	Line1       string `json:"line1"`
	Line2       string `json:"line2,omitempty"`
	City        string `json:"city"`
	Region      string `json:"region"`
	PostalCode  string `json:"postalCode"`
	CountryCode string `json:"countryCode"`
	Phone       string `json:"phone,omitempty"`
}

func (d shipToDTO) domain() shipto.ShipTo { return shipto.ShipTo(d) }

func shipToView(v views.ShipTo) shipToDTO { return shipToDTO(v) }

type lineDTO struct {
	LineNo        int    `json:"lineNo"`
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

type checkoutGroupDTO struct {
	GroupNo int    `json:"groupNo"`
	OfferID string `json:"offerId"`
	LineNos []int  `json:"lineNos"`
}

type checkoutRequest struct {
	ShopperID string    `json:"shopperId"`
	ShipTo    shipToDTO `json:"shipTo"`
	// ShipToAddressID names a saved address of the shopper (NG1): exactly one
	// of shipTo / shipToAddressId, the address is copied into the order.
	ShipToAddressID string             `json:"shipToAddressId"`
	Lines           []lineDTO          `json:"lines"`
	Groups          []checkoutGroupDTO `json:"groups"`
}

func (c checkoutRequest) lines() []customerorder.OrderLine {
	out := make([]customerorder.OrderLine, len(c.Lines))
	for i, l := range c.Lines {
		out[i] = customerorder.OrderLine(l)
	}
	return out
}

type cancelRequest struct {
	Reason string `json:"reason"`
}

type registerShopperRequest struct {
	Email          string      `json:"email"`
	SavedAddresses []shipToDTO `json:"savedAddresses"`
}

type groupDTO struct {
	GroupNo             int    `json:"groupNo"`
	PromiseID           string `json:"promiseId"`
	Tier                string `json:"tier"`
	ShipBy              string `json:"shipBy"`
	DeliverByEarliest   string `json:"deliverByEarliest"`
	DeliverByLatest     string `json:"deliverByLatest"`
	TimeZone            string `json:"timeZone,omitempty"`
	LineNos             []int  `json:"lineNos"`
	Status              string `json:"status"`
	RevisionNo          int    `json:"revisionNo,omitempty"`
	CancelReason        string `json:"cancelReason,omitempty"`
	CancelRequestedAt   string `json:"cancelRequestedAt,omitempty"`
	CancelRefusedReason string `json:"cancelRefusedReason,omitempty"`
}

type orderDTO struct {
	OrderID                 string     `json:"orderId"`
	ShopperID               string     `json:"shopperId"`
	Status                  string     `json:"status"`
	DestinationPostalPrefix string     `json:"destinationPostalPrefix"`
	PlacedAt                string     `json:"placedAt"`
	CancelledAt             string     `json:"cancelledAt,omitempty"`
	Lines                   []lineDTO  `json:"lines"`
	Groups                  []groupDTO `json:"groups"`
}

func orderView(o views.Order) orderDTO {
	lines := make([]lineDTO, len(o.Lines))
	for i, l := range o.Lines {
		lines[i] = lineDTO(l)
	}
	groups := make([]groupDTO, len(o.Groups))
	for i, g := range o.Groups {
		nos := g.LineNos
		if nos == nil {
			nos = []int{}
		}
		groups[i] = groupDTO{
			GroupNo: g.GroupNo, PromiseID: g.PromiseID, Tier: g.Tier, ShipBy: ts(g.ShipBy), DeliverByEarliest: ts(g.DeliverByEarliest),
			DeliverByLatest: ts(g.DeliverByLatest), TimeZone: g.TimeZone, LineNos: nos, Status: g.Status, RevisionNo: g.RevisionNo, CancelReason: g.CancelReason,
			CancelRequestedAt: ts(g.CancelRequestedAt), CancelRefusedReason: g.CancelRefusedReason,
		}
	}
	return orderDTO{
		OrderID: o.OrderID, ShopperID: o.ShopperID, Status: o.Status, DestinationPostalPrefix: o.DestinationPostalPrefix,
		PlacedAt: ts(o.PlacedAt), CancelledAt: ts(o.CancelledAt), Lines: lines, Groups: groups,
	}
}

type trackingPackageDTO struct {
	TrackingNumber  string `json:"trackingNumber"`
	Carrier         string `json:"carrier,omitempty"`
	State           string `json:"state,omitempty"`
	LastScanNodeID  string `json:"lastScanNodeId,omitempty"`
	LastScanAt      string `json:"lastScanAt,omitempty"`
	DeliveredAt     string `json:"deliveredAt,omitempty"`
	ExceptionReason string `json:"exceptionReason,omitempty"`
}

type trackingGroupDTO struct {
	GroupNo             int                  `json:"groupNo"`
	PromiseID           string               `json:"promiseId"`
	Status              string               `json:"status"`
	DeliverByEarliest   string               `json:"deliverByEarliest"`
	DeliverByLatest     string               `json:"deliverByLatest"`
	TimeZone            string               `json:"timeZone,omitempty"`
	AtRiskReason        string               `json:"atRiskReason,omitempty"`
	UnsourceableReason  string               `json:"unsourceableReason,omitempty"`
	PromiseVerdict      string               `json:"promiseVerdict,omitempty"`
	LateByMinutes       *int                 `json:"lateByMinutes,omitempty"`
	LockedAt            string               `json:"lockedAt,omitempty"`
	ShippedAt           string               `json:"shippedAt,omitempty"`
	CancelRefusedReason string               `json:"cancelRefusedReason,omitempty"`
	LostAt              string               `json:"lostAt,omitempty"`
	ReturnedAt          string               `json:"returnedAt,omitempty"`
	OutcomePackage      string               `json:"outcomePackage,omitempty"`
	Packages            []trackingPackageDTO `json:"packages"`
}

type trackingDTO struct {
	OrderID string             `json:"orderId"`
	Status  string             `json:"status"`
	Groups  []trackingGroupDTO `json:"groups"`
}

func trackingView(t views.Tracking) trackingDTO {
	groups := make([]trackingGroupDTO, len(t.Groups))
	for i, g := range t.Groups {
		pkgs := make([]trackingPackageDTO, len(g.Packages))
		for j, p := range g.Packages {
			pkgs[j] = trackingPackageDTO{
				TrackingNumber: p.TrackingNumber, Carrier: p.Carrier, State: p.State, LastScanNodeID: p.LastScanNodeID,
				LastScanAt: ts(p.LastScanAt), DeliveredAt: ts(p.DeliveredAt), ExceptionReason: p.ExceptionReason,
			}
		}
		groups[i] = trackingGroupDTO{
			GroupNo: g.GroupNo, PromiseID: g.PromiseID, Status: g.Status, DeliverByEarliest: ts(g.DeliverByEarliest), DeliverByLatest: ts(g.DeliverByLatest), TimeZone: g.TimeZone,
			AtRiskReason: g.AtRiskReason, UnsourceableReason: g.UnsourceableReason, PromiseVerdict: g.PromiseVerdict, LateByMinutes: g.LateByMinutes,
			LockedAt: ts(g.LockedAt), ShippedAt: ts(g.ShippedAt), CancelRefusedReason: g.CancelRefusedReason,
			LostAt: ts(g.LostAt), ReturnedAt: ts(g.ReturnedAt), OutcomePackage: g.OutcomePackage, Packages: pkgs,
		}
	}
	return trackingDTO{OrderID: t.OrderID, Status: t.Status, Groups: groups}
}

// savedAddressDTO is a saved address with its id (NG1): the ShipTo fields the
// caller sent plus addressId.
type savedAddressDTO struct {
	AddressID   string `json:"addressId"`
	Name        string `json:"name"`
	Line1       string `json:"line1"`
	Line2       string `json:"line2,omitempty"`
	City        string `json:"city"`
	Region      string `json:"region"`
	PostalCode  string `json:"postalCode"`
	CountryCode string `json:"countryCode"`
	Phone       string `json:"phone,omitempty"`
}

func savedAddressView(a views.SavedAddress) savedAddressDTO {
	return savedAddressDTO{
		AddressID: a.AddressID, Name: a.Name, Line1: a.Line1, Line2: a.Line2, City: a.City,
		Region: a.Region, PostalCode: a.PostalCode, CountryCode: a.CountryCode, Phone: a.Phone,
	}
}

type shopperDTO struct {
	ShopperID      string            `json:"shopperId"`
	Email          string            `json:"email"`
	Status         string            `json:"status"`
	RegisteredAt   string            `json:"registeredAt"`
	SavedAddresses []savedAddressDTO `json:"savedAddresses"`
}

func shopperView(s views.Shopper) shopperDTO {
	addresses := make([]savedAddressDTO, 0, len(s.SavedAddresses))
	for _, a := range s.SavedAddresses {
		addresses = append(addresses, savedAddressView(a))
	}
	return shopperDTO{ShopperID: s.ShopperID, Email: s.Email, Status: s.Status, RegisteredAt: ts(s.RegisteredAt), SavedAddresses: addresses}
}

type requotedOfferDTO struct {
	GroupNo           int    `json:"groupNo"`
	OfferID           string `json:"offerId"`
	Tier              string `json:"tier"`
	DeliverByEarliest string `json:"deliverByEarliest"`
	DeliverByLatest   string `json:"deliverByLatest"`
	TimeZone          string `json:"timeZone,omitempty"`
	ExpiresAt         string `json:"expiresAt"`
}

func requotedOffersDTO(in []views.RequotedOffer) []requotedOfferDTO {
	out := make([]requotedOfferDTO, len(in))
	for i, o := range in {
		out[i] = requotedOfferDTO{
			GroupNo: o.GroupNo, OfferID: o.OfferID, Tier: o.Tier, DeliverByEarliest: ts(o.DeliverByEarliest),
			DeliverByLatest: ts(o.DeliverByLatest), TimeZone: o.TimeZone, ExpiresAt: ts(o.ExpiresAt),
		}
	}
	return out
}
