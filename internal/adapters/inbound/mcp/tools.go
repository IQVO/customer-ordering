package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/application/views"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
)

// Deps is everything the MCP tools need, injected by the composition root.
// It carries the SAME read use cases the REST adapter uses (GET
// /orders/{orderId}, /orders/{orderId}/tracking, /orders/{orderId}/ship-to);
// the adapter never constructs an outbound adapter itself, and it holds no
// write use case at all (docs/adr/0005). POST /checkout,
// /orders/{orderId}:cancel and /shoppers stay REST-only.
type Deps struct {
	GetOrder         *usecases.GetOrder
	GetOrderTracking *usecases.GetOrderTracking
	GetShipTo        *usecases.GetShipTo
}

// NewDeps wires the three read use cases over the two read repositories they
// need. It is the only construction path the composition root needs: handing
// the repositories over this way keeps cmd/mcp from ever naming the shared
// usecases.Deps bundle, which also carries the write seams (a unit of work,
// the outbox, the promise gateway) no MCP binary may reach.
func NewDeps(orders ports.OrderRepository, tracking ports.TrackingRepository) Deps {
	deps := usecases.Deps{Orders: orders, Tracking: tracking}
	return Deps{
		GetOrder:         &usecases.GetOrder{Deps: deps},
		GetOrderTracking: &usecases.GetOrderTracking{Deps: deps},
		GetShipTo:        &usecases.GetShipTo{Deps: deps},
	}
}

// --- inputs -------------------------------------------------------------------

// maxOrderIDLength mirrors the REST adapter's {orderId} guard
// (internal/adapters/inbound/http/handlers.go): an id that cannot name an
// order is a miss, never a server error.
const maxOrderIDLength = 64

type orderIDInput struct {
	OrderID string `json:"orderId" jsonschema:"the order id, e.g. ord-3f9c1a2b"`
}

// orderID applies the REST adapter's id guard: an empty or over-long id can
// never name an order and reads as not-found, exactly like GET /orders/{id}.
func orderID(id string) (customerorder.ID, error) {
	if id == "" || len(id) > maxOrderIDLength {
		return "", toolError("not-found", "an order id is 1 to 64 characters; this one cannot name an order")
	}
	return customerorder.ID(id), nil
}

// --- outputs (the REST bodies, camelCase like the REST contract) --------------

type lineView struct {
	LineNo        int    `json:"lineNo"`
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

type groupView struct {
	GroupNo             int    `json:"groupNo"`
	PromiseID           string `json:"promiseId"`
	Tier                string `json:"tier"`
	ShipBy              string `json:"shipBy"`
	DeliverByEarliest   string `json:"deliverByEarliest"`
	DeliverByLatest     string `json:"deliverByLatest"`
	LineNos             []int  `json:"lineNos"`
	Status              string `json:"status"`
	RevisionNo          int    `json:"revisionNo,omitempty"`
	CancelReason        string `json:"cancelReason,omitempty"`
	CancelRequestedAt   string `json:"cancelRequestedAt,omitempty"`
	CancelRefusedReason string `json:"cancelRefusedReason,omitempty"`
}

type orderView struct {
	OrderID                 string      `json:"orderId"`
	ShopperID               string      `json:"shopperId"`
	Status                  string      `json:"status"`
	DestinationPostalPrefix string      `json:"destinationPostalPrefix"`
	PlacedAt                string      `json:"placedAt"`
	CancelledAt             string      `json:"cancelledAt,omitempty"`
	Lines                   []lineView  `json:"lines"`
	Groups                  []groupView `json:"groups"`
}

type trackingPackageView struct {
	TrackingNumber  string `json:"trackingNumber"`
	Carrier         string `json:"carrier,omitempty"`
	State           string `json:"state,omitempty"`
	LastScanNodeID  string `json:"lastScanNodeId,omitempty"`
	LastScanAt      string `json:"lastScanAt,omitempty"`
	DeliveredAt     string `json:"deliveredAt,omitempty"`
	ExceptionReason string `json:"exceptionReason,omitempty"`
}

type trackingGroupView struct {
	GroupNo             int                   `json:"groupNo"`
	PromiseID           string                `json:"promiseId"`
	Status              string                `json:"status"`
	DeliverByEarliest   string                `json:"deliverByEarliest"`
	DeliverByLatest     string                `json:"deliverByLatest"`
	AtRiskReason        string                `json:"atRiskReason,omitempty"`
	UnsourceableReason  string                `json:"unsourceableReason,omitempty"`
	PromiseVerdict      string                `json:"promiseVerdict,omitempty"`
	LateByMinutes       *int                  `json:"lateByMinutes,omitempty"`
	LockedAt            string                `json:"lockedAt,omitempty"`
	ShippedAt           string                `json:"shippedAt,omitempty"`
	CancelRefusedReason string                `json:"cancelRefusedReason,omitempty"`
	LostAt              string                `json:"lostAt,omitempty"`
	ReturnedAt          string                `json:"returnedAt,omitempty"`
	OutcomePackage      string                `json:"outcomePackage,omitempty"`
	Packages            []trackingPackageView `json:"packages"`
}

type trackingView struct {
	OrderID string              `json:"orderId"`
	Status  string              `json:"status"`
	Groups  []trackingGroupView `json:"groups"`
}

type shipToView struct {
	Name        string `json:"name"`
	Line1       string `json:"line1"`
	Line2       string `json:"line2,omitempty"`
	City        string `json:"city"`
	Region      string `json:"region"`
	PostalCode  string `json:"postalCode"`
	CountryCode string `json:"countryCode"`
	Phone       string `json:"phone,omitempty"`
}

// --- mapping ------------------------------------------------------------------

// instant formats an instant as RFC 3339 UTC; the zero time is "" (omitted).
func instant(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// lineNos never reads as null: the REST body always carries an array.
func lineNos(in []int) []int {
	if in == nil {
		return []int{}
	}
	return in
}

func toOrder(o views.Order) orderView {
	lines := make([]lineView, 0, len(o.Lines))
	for _, l := range o.Lines {
		lines = append(lines, lineView{LineNo: l.LineNo, CatalogItemID: l.CatalogItemID, Quantity: l.Quantity})
	}
	groups := make([]groupView, 0, len(o.Groups))
	for _, g := range o.Groups {
		groups = append(groups, groupView{
			GroupNo: g.GroupNo, PromiseID: g.PromiseID, Tier: g.Tier, ShipBy: instant(g.ShipBy),
			DeliverByEarliest: instant(g.DeliverByEarliest), DeliverByLatest: instant(g.DeliverByLatest),
			LineNos: lineNos(g.LineNos), Status: g.Status, RevisionNo: g.RevisionNo, CancelReason: g.CancelReason,
			CancelRequestedAt: instant(g.CancelRequestedAt), CancelRefusedReason: g.CancelRefusedReason,
		})
	}
	return orderView{
		OrderID: o.OrderID, ShopperID: o.ShopperID, Status: o.Status, DestinationPostalPrefix: o.DestinationPostalPrefix,
		PlacedAt: instant(o.PlacedAt), CancelledAt: instant(o.CancelledAt), Lines: lines, Groups: groups,
	}
}

func toTracking(t views.Tracking) trackingView {
	groups := make([]trackingGroupView, 0, len(t.Groups))
	for _, g := range t.Groups {
		packages := make([]trackingPackageView, 0, len(g.Packages))
		for _, p := range g.Packages {
			packages = append(packages, trackingPackageView{
				TrackingNumber: p.TrackingNumber, Carrier: p.Carrier, State: p.State, LastScanNodeID: p.LastScanNodeID,
				LastScanAt: instant(p.LastScanAt), DeliveredAt: instant(p.DeliveredAt), ExceptionReason: p.ExceptionReason,
			})
		}
		groups = append(groups, trackingGroupView{
			GroupNo: g.GroupNo, PromiseID: g.PromiseID, Status: g.Status,
			DeliverByEarliest: instant(g.DeliverByEarliest), DeliverByLatest: instant(g.DeliverByLatest),
			AtRiskReason: g.AtRiskReason, UnsourceableReason: g.UnsourceableReason, PromiseVerdict: g.PromiseVerdict,
			LateByMinutes: g.LateByMinutes, LockedAt: instant(g.LockedAt), ShippedAt: instant(g.ShippedAt),
			CancelRefusedReason: g.CancelRefusedReason, LostAt: instant(g.LostAt), ReturnedAt: instant(g.ReturnedAt),
			OutcomePackage: g.OutcomePackage, Packages: packages,
		})
	}
	return trackingView{OrderID: t.OrderID, Status: t.Status, Groups: groups}
}

func toShipTo(s views.ShipTo) shipToView {
	return shipToView{
		Name: s.Name, Line1: s.Line1, Line2: s.Line2, City: s.City, Region: s.Region,
		PostalCode: s.PostalCode, CountryCode: s.CountryCode, Phone: s.Phone,
	}
}

// --- handlers -----------------------------------------------------------------

// getOrder mirrors GET /orders/{orderId}: the order without its ship-to.
func (d Deps) getOrder(ctx context.Context, in orderIDInput) (orderView, error) {
	id, err := orderID(in.OrderID)
	if err != nil {
		return orderView{}, err
	}
	view, err := d.GetOrder.Execute(ctx, id)
	if err != nil {
		return orderView{}, mapError(err)
	}
	return toOrder(view), nil
}

// getOrderTracking mirrors GET /orders/{orderId}/tracking: the OrderTrackingView.
func (d Deps) getOrderTracking(ctx context.Context, in orderIDInput) (trackingView, error) {
	id, err := orderID(in.OrderID)
	if err != nil {
		return trackingView{}, err
	}
	view, err := d.GetOrderTracking.Execute(ctx, id)
	if err != nil {
		return trackingView{}, mapError(err)
	}
	return toTracking(view), nil
}

// getShipTo mirrors GET /orders/{orderId}/ship-to: the only exit of the
// synthetic ship-to address (restricted, label printing only).
func (d Deps) getShipTo(ctx context.Context, in orderIDInput) (shipToView, error) {
	id, err := orderID(in.OrderID)
	if err != nil {
		return shipToView{}, err
	}
	view, err := d.GetShipTo.Execute(ctx, id)
	if err != nil {
		return shipToView{}, mapError(err)
	}
	return toShipTo(view), nil
}

// --- registry -----------------------------------------------------------------

// registerTools adds the three read-only tools. Every tool is annotated
// read-only, idempotent and closed-world; TestToolSurface pins the set and
// fails the build on any write-verb name (docs/adr/0005).
func (d Deps) registerTools(server *mcp.Server) {
	closedWorld := false
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld}

	addTool(server, &mcp.Tool{
		Name: "get_order",
		Description: "Read a placed customer order by id: its status (PLACED, IN_FULFILLMENT, PARTIALLY_SHIPPED, SHIPPED, PARTIALLY_DELIVERED, DELIVERED, LOST, RETURNED or CANCELLED), the " +
			"shopper, the destination postal prefix and when it was placed, the cart lines (lineNo, catalogItemId, " +
			"quantity) and every shipment group with its promise (promiseId, tier, shipBy, deliverByEarliest, " +
			"deliverByLatest), the line numbers it ships, its status and revision, and the cancellation request or " +
			"refusal facts when there were any. The ship-to address is NOT part of it (use get_ship_to). Read-only; " +
			"an unknown or malformed order id fails with not-found.",
		Annotations: readOnly,
	}, d.getOrder)

	addTool(server, &mcp.Tool{
		Name: "get_order_tracking",
		Description: "Read how one order is travelling: for every shipment group its promise window " +
			"(deliverByEarliest, deliverByLatest), its status (PROMISED, LOCKED, SHIPPED, DELIVERED, LOST, RETURNED, " +
			"CANCELLATION_REQUESTED or CANCELLED), when it locked and shipped, the at-risk and " +
			"unsourceable signals, the promise verdict (and how many minutes late it ran), and its packages with " +
			"carrier, state, last scan node and time, delivery time and exception reason; a lost or returned group " +
			"also carries lostAt/returnedAt and the outcome package. Read-only; an unknown or malformed order id " +
			"fails with not-found.",
		Annotations: readOnly,
	}, d.getOrderTracking)

	addTool(server, &mcp.Tool{
		Name: "get_ship_to",
		Description: "Read the ship-to delivery address of one order (name, line1, line2, city, region, postalCode, " +
			"countryCode, phone). It is synthetic PII held only by this context and served by this one read, which " +
			"REST restricts to label printing; it never appears in an event. Read-only; an unknown or malformed " +
			"order id fails with not-found.",
		Annotations: readOnly,
	}, d.getShipTo)
}

// addTool registers one tool. A handler error is returned to the SDK as the
// handler's error, which the SDK turns into an isError tool result (never a
// transport/protocol failure), carrying the error text.
func addTool[In, Out any](
	server *mcp.Server,
	tool *mcp.Tool,
	handle func(context.Context, In) (Out, error),
) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := handle(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, out, nil
	})
}
