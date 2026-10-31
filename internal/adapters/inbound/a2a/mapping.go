package a2a

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/views"
)

// problemTypeBase namespaces this context's problem types. It is the SAME
// namespace as the REST adapter's RFC 9457 `type` URIs
// (internal/adapters/inbound/http/errors.go), so a delegating agent sees the
// identifiers a REST caller would for the outcomes both surfaces share.
const problemTypeBase = "/problems/"

// problem is the RFC 7807-shaped rejection an agent client receives instead of
// a result: domain rejections and request-shape failures are business
// answers, not transport errors, so they travel in the message envelope.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

// summary renders the one-line form used for the reply's text part.
func (p *problem) summary() string {
	return fmt.Sprintf("%s (%d): %s", p.Title, p.Status, p.Detail)
}

func problemMalformedRequest(detail string) *problem {
	return &problem{Type: problemTypeBase + "malformed-skill-request", Title: "Malformed skill request", Detail: detail, Status: 400}
}

func problemUnknownSkill(skill string) *problem {
	return &problem{Type: problemTypeBase + "unknown-skill", Title: "Unknown skill", Detail: fmt.Sprintf("skill %q is not offered by this agent; the Agent Card lists the supported skills", skill), Status: 400}
}

func problemInvalidArgument(detail string) *problem {
	return &problem{Type: problemTypeBase + "invalid-argument", Title: "Invalid skill argument", Detail: detail, Status: 400}
}

// problemNotFound is the REST 404 `not-found` problem. The detail never echoes
// the id back: it may be arbitrary caller text.
func problemNotFound(detail string) *problem {
	return &problem{Type: problemTypeBase + "not-found", Title: "Not found", Detail: detail, Status: 404}
}

func problemInternal() *problem {
	return &problem{Type: problemTypeBase + "internal-error", Title: "Internal server error", Detail: "the skill failed; see server logs (trace id in the response metadata)", Status: 500}
}

// envelope is the structured payload of every skill answer: exactly one of
// Data (a skill DTO) or Problem (why there is no result), plus the skill id
// the answer belongs to.
type envelope struct {
	Skill   string    `json:"skill"`
	Data    *skillDTO `json:"data,omitempty"`
	Problem *problem  `json:"problem,omitempty"`
}

// skillDTO is the union of the skill result DTOs. A2A parts are
// machine-readable JSON; each skill fills its own field, keeping the envelope
// flat and versioned as one contract.
type skillDTO struct {
	Order    *orderDTO    `json:"order,omitempty"`
	Tracking *trackingDTO `json:"tracking,omitempty"`
	summary  string
}

// Summary is the human-readable one-liner for the reply's text part.
func (s skillDTO) Summary() string { return s.summary }

// --- DTOs (the REST bodies, camelCase like the REST contract) -----------------

type lineDTO struct {
	LineNo        int    `json:"lineNo"`
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

type groupDTO struct {
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

type packageDTO struct {
	TrackingNumber  string `json:"trackingNumber"`
	Carrier         string `json:"carrier,omitempty"`
	State           string `json:"state,omitempty"`
	LastScanNodeID  string `json:"lastScanNodeId,omitempty"`
	LastScanAt      string `json:"lastScanAt,omitempty"`
	DeliveredAt     string `json:"deliveredAt,omitempty"`
	ExceptionReason string `json:"exceptionReason,omitempty"`
}

type trackingGroupDTO struct {
	GroupNo             int          `json:"groupNo"`
	PromiseID           string       `json:"promiseId"`
	Status              string       `json:"status"`
	DeliverByEarliest   string       `json:"deliverByEarliest"`
	DeliverByLatest     string       `json:"deliverByLatest"`
	AtRiskReason        string       `json:"atRiskReason,omitempty"`
	UnsourceableReason  string       `json:"unsourceableReason,omitempty"`
	PromiseVerdict      string       `json:"promiseVerdict,omitempty"`
	LateByMinutes       *int         `json:"lateByMinutes,omitempty"`
	LockedAt            string       `json:"lockedAt,omitempty"`
	ShippedAt           string       `json:"shippedAt,omitempty"`
	CancelRefusedReason string       `json:"cancelRefusedReason,omitempty"`
	LostAt              string       `json:"lostAt,omitempty"`
	ReturnedAt          string       `json:"returnedAt,omitempty"`
	OutcomePackage      string       `json:"outcomePackage,omitempty"`
	Packages            []packageDTO `json:"packages"`
}

type trackingDTO struct {
	OrderID string             `json:"orderId"`
	Status  string             `json:"status"`
	Groups  []trackingGroupDTO `json:"groups"`
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

func toOrderDTO(o views.Order) orderDTO {
	lines := make([]lineDTO, 0, len(o.Lines))
	for _, l := range o.Lines {
		lines = append(lines, lineDTO{LineNo: l.LineNo, CatalogItemID: l.CatalogItemID, Quantity: l.Quantity})
	}
	groups := make([]groupDTO, 0, len(o.Groups))
	for _, g := range o.Groups {
		groups = append(groups, groupDTO{
			GroupNo: g.GroupNo, PromiseID: g.PromiseID, Tier: g.Tier, ShipBy: instant(g.ShipBy),
			DeliverByEarliest: instant(g.DeliverByEarliest), DeliverByLatest: instant(g.DeliverByLatest),
			LineNos: lineNos(g.LineNos), Status: g.Status, RevisionNo: g.RevisionNo, CancelReason: g.CancelReason,
			CancelRequestedAt: instant(g.CancelRequestedAt), CancelRefusedReason: g.CancelRefusedReason,
		})
	}
	return orderDTO{
		OrderID: o.OrderID, ShopperID: o.ShopperID, Status: o.Status, DestinationPostalPrefix: o.DestinationPostalPrefix,
		PlacedAt: instant(o.PlacedAt), CancelledAt: instant(o.CancelledAt), Lines: lines, Groups: groups,
	}
}

func toPackageDTOs(in []views.TrackingPackage) []packageDTO {
	out := make([]packageDTO, 0, len(in))
	for _, p := range in {
		out = append(out, packageDTO{
			TrackingNumber: p.TrackingNumber, Carrier: p.Carrier, State: p.State, LastScanNodeID: p.LastScanNodeID,
			LastScanAt: instant(p.LastScanAt), DeliveredAt: instant(p.DeliveredAt), ExceptionReason: p.ExceptionReason,
		})
	}
	return out
}

func toTrackingDTO(t views.Tracking) trackingDTO {
	groups := make([]trackingGroupDTO, 0, len(t.Groups))
	for _, g := range t.Groups {
		groups = append(groups, trackingGroupDTO{
			GroupNo: g.GroupNo, PromiseID: g.PromiseID, Status: g.Status,
			DeliverByEarliest: instant(g.DeliverByEarliest), DeliverByLatest: instant(g.DeliverByLatest),
			AtRiskReason: g.AtRiskReason, UnsourceableReason: g.UnsourceableReason, PromiseVerdict: g.PromiseVerdict,
			LateByMinutes: g.LateByMinutes, LockedAt: instant(g.LockedAt), ShippedAt: instant(g.ShippedAt),
			CancelRefusedReason: g.CancelRefusedReason, LostAt: instant(g.LostAt), ReturnedAt: instant(g.ReturnedAt),
			OutcomePackage: g.OutcomePackage, Packages: toPackageDTOs(g.Packages),
		})
	}
	return trackingDTO{OrderID: t.OrderID, Status: t.Status, Groups: groups}
}

// plural renders "1 line" / "2 lines".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// summarizeOrder is the one-line text form of an order.
func summarizeOrder(o orderDTO) string {
	return fmt.Sprintf("%s: %s, %s in %s", o.OrderID, o.Status, plural(len(o.Lines), "line"), plural(len(o.Groups), "shipment group"))
}

// summarizeTracking is the one-line text form of an order's tracking: the
// order status and how many groups sit in each group status.
func summarizeTracking(t trackingDTO) string {
	counts := map[string]int{}
	for _, g := range t.Groups {
		counts[g.Status]++
	}
	statuses := make([]string, 0, len(counts))
	for s := range counts {
		statuses = append(statuses, s)
	}
	sort.Strings(statuses)
	parts := make([]string, 0, len(statuses))
	for _, s := range statuses {
		parts = append(parts, fmt.Sprintf("%d %s", counts[s], s))
	}
	return fmt.Sprintf("%s tracking: %s, %s (%s)", t.OrderID, t.Status, plural(len(t.Groups), "shipment group"), strings.Join(parts, ", "))
}
