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

// pickupShipToDTO is the pickup answer of the ship-to route (ADR 0016): the
// destination kind and the point, and NO address fields — the restricted
// route carries no PII at all for these orders.
type pickupShipToDTO struct {
	DestinationKind string `json:"destinationKind"`
	PickupPointID   string `json:"pickupPointId"`
}

// destinationView shapes the ship-to route's answer by destination kind
// (ADR 0016): PICKUP_POINT answers {destinationKind, pickupPointId}; a
// ship-to order keeps today's address shape (an absent kind IS ship-to).
func destinationView(d views.Destination) any {
	if d.PickupPointID != "" {
		return pickupShipToDTO{DestinationKind: "PICKUP_POINT", PickupPointID: d.PickupPointID}
	}
	return shipToDTO(d.ShipTo)
}

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
	// ShipToAddressID names a saved address of the shopper (NG1); exactly one
	// of shipTo / shipToAddressId / pickupPointId, the address is copied into
	// the order.
	ShipToAddressID string `json:"shipToAddressId"`
	// PickupPointID checks the order out to a pickup point instead of an
	// address (ADR 0016): no ship-to is held for the order at all.
	PickupPointID string             `json:"pickupPointId"`
	Lines         []lineDTO          `json:"lines"`
	Groups        []checkoutGroupDTO `json:"groups"`
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
	GroupNo           int    `json:"groupNo"`
	PromiseID         string `json:"promiseId"`
	Tier              string `json:"tier"`
	ShipBy            string `json:"shipBy"`
	DeliverByEarliest string `json:"deliverByEarliest"`
	DeliverByLatest   string `json:"deliverByLatest"`
	TimeZone          string `json:"timeZone,omitempty"`
	LineNos           []int  `json:"lineNos"`
	Status            string `json:"status"`
	RevisionNo        int    `json:"revisionNo,omitempty"`
	// ChosenDeliveryDay and ConsolidationEligible: CONSOLIDATED groups only,
	// omitted when unknown; an absent eligibility is never false (ADR 0013).
	ChosenDeliveryDay     string `json:"chosenDeliveryDay,omitempty"`
	ConsolidationEligible *bool  `json:"consolidationEligible,omitempty"`
	CancelReason          string `json:"cancelReason,omitempty"`
	CancelRequestedAt     string `json:"cancelRequestedAt,omitempty"`
	CancelRefusedReason   string `json:"cancelRefusedReason,omitempty"`
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
			ChosenDeliveryDay: g.ChosenDeliveryDay, ConsolidationEligible: g.ConsolidationEligible,
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
	// DestinationKind and PickupPointID name the pickup destination of the
	// package; the kind is always answered, the point only for a pickup
	// package (ADR 0016).
	DestinationKind string          `json:"destinationKind"`
	PickupPointID   string          `json:"pickupPointId,omitempty"`
	PickupPoint     *pickupPointDTO `json:"pickupPoint,omitempty"`
	EtaStart        string          `json:"etaStart,omitempty"`
	EtaEnd          string          `json:"etaEnd,omitempty"`

	DeliveryPointAdvisory *deliveryPointAdvisoryDTO `json:"deliveryPointAdvisory,omitempty"`
}

// pickupPointDTO is the collection state of a pickup package (ADR 0016).
type pickupPointDTO struct {
	ReadyForCollectionAt string `json:"readyForCollectionAt"`
	HoldUntil            string `json:"holdUntil"`
	CollectedAt          string `json:"collectedAt,omitempty"`
	ProofCode            string `json:"proofCode,omitempty"`
}

func pickupDTO(p *views.PickupPoint) *pickupPointDTO {
	if p == nil {
		return nil
	}
	return &pickupPointDTO{
		ReadyForCollectionAt: ts(p.ReadyForCollectionAt), HoldUntil: ts(p.HoldUntil),
		CollectedAt: ts(p.CollectedAt), ProofCode: p.ProofCode,
	}
}

// deliveryPointAdvisoryDTO is the code-only advisory of ADR 0015.
type deliveryPointAdvisoryDTO struct {
	Reason    string `json:"reason"`
	FlaggedAt string `json:"flaggedAt"`
}

func advisoryDTO(a *views.DeliveryPointAdvisory) *deliveryPointAdvisoryDTO {
	if a == nil {
		return nil
	}
	return &deliveryPointAdvisoryDTO{Reason: a.Reason, FlaggedAt: ts(a.FlaggedAt)}
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
				DestinationKind: p.DestinationKind, PickupPointID: p.PickupPointID, PickupPoint: pickupDTO(p.PickupPoint),
				EtaStart: ts(p.EtaStart), EtaEnd: ts(p.EtaEnd), DeliveryPointAdvisory: advisoryDTO(p.DeliveryPointAdvisory),
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
