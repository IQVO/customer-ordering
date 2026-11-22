package kafka

import (
	"fmt"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/customer-ordering/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/customer-ordering/internal/analytics/report"
)

// The CloudEvents entity segments of the projected types: the CustomerOrder
// aggregate's two events and the shopper-facing notice (ADR 0017).
const (
	entityCustomerOrder = "customerorder"
	entityOrder         = "order"
)

// projectedKinds maps the FULL CloudEvents type of each projected event
// (ADR 0018) to its report.Kind. Every type this context publishes on its
// events topic is projected.
func projectedKinds() map[string]report.Kind {
	return map[string]report.Kind{
		cloudevents.Type(entityCustomerOrder, string(report.KindOrderPlaced)):    report.KindOrderPlaced,
		cloudevents.Type(entityCustomerOrder, string(report.KindOrderCancelled)): report.KindOrderCancelled,
		cloudevents.Type(entityOrder, string(report.KindNoticeDecided)):          report.KindNoticeDecided,
	}
}

// analyticsData is the union of the payload fields the projection reads,
// hand-mirrored from apis/asyncapi.yaml (never imported from the encoder).
// Everything else is ignored by design: the ship-to prefix, the shopper, the
// consolidation key, the cancellation reason (free text), the pickup point id
// and the notice fact never reach the analytical model. An absent timestamp
// stays zero.
type analyticsData struct {
	OrderID       string      `json:"orderId"`
	PickupPointID string      `json:"pickupPointId"`
	PlacedAt      time.Time   `json:"placedAt"`
	CancelledAt   time.Time   `json:"cancelledAt"`
	DecidedAt     time.Time   `json:"decidedAt"`
	Groups        []groupTier `json:"groups"`
	Family        string      `json:"family"`
	NoticeKey     string      `json:"noticeKey"`
}

// groupTier is the only field of a placed group the projection reads.
type groupTier struct {
	Tier string `json:"tier"`
}

// toEvent decodes and validates a CloudEvent of a projected kind. Every error
// is deterministic: the same bytes can never pass.
func toEvent(kind report.Kind, evt ce.Event) (report.Event, error) {
	var d analyticsData
	if err := evt.DataAs(&d); err != nil {
		return report.Event{}, fmt.Errorf("decode %s %s data: %w", kind, evt.ID(), err)
	}
	if d.OrderID == "" || d.OrderID != evt.Subject() {
		return report.Event{}, fmt.Errorf("%s %s: the aggregate id %q does not match the subject %q", kind, evt.ID(), d.OrderID, evt.Subject())
	}
	e := report.Event{Kind: kind, EventID: evt.ID(), At: evt.Time().UTC(), OrderID: d.OrderID, OccurredAt: d.instant(kind).UTC()}
	if kind == report.KindNoticeDecided && e.At.IsZero() {
		// LEGACY ONLY. Notices published before the notice use case stamped its
		// events carry the zero CloudEvents `time` (0001-01-01) and are still on
		// the stream: dead-lettering them would blind the report, so decidedAt,
		// the instant the use case decided, is their freshness basis. Current
		// publishers always set the time (ADR 0018); this branch is dead for them.
		e.At = e.OccurredAt
	}
	switch kind {
	case report.KindOrderPlaced:
		e.GroupCount, e.ConsolidatedGroups, e.PickupPoint = len(d.Groups), d.consolidatedGroups(), d.PickupPointID != ""
	case report.KindNoticeDecided:
		e.Family, e.NoticeKey = d.Family, d.NoticeKey
	}
	if err := e.Validate(); err != nil {
		return report.Event{}, err
	}
	return e, nil
}

// instant is the business instant of a fact: `placedAt`, `cancelledAt` or
// `decidedAt`.
func (d analyticsData) instant(kind report.Kind) time.Time {
	switch kind {
	case report.KindOrderPlaced:
		return d.PlacedAt
	case report.KindOrderCancelled:
		return d.CancelledAt
	default:
		return d.DecidedAt
	}
}

// consolidatedGroups counts the groups promised under the CONSOLIDATED tier.
func (d analyticsData) consolidatedGroups() int {
	n := 0
	for _, g := range d.Groups {
		if g.Tier == report.ConsolidatedTier() {
			n++
		}
	}
	return n
}
