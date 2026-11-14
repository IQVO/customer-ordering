// Package notice is the notification policy of the shopper relationship
// (G62, ADR 0017): a versioned NotificationPolicy document that says, per
// notice family, who notifies the shopper, and the ShopperNoticeDecided fact
// its decisions raise. The policy is DATA and this context sends nothing: the
// notice is delivered outside the network (the storefront consumes the event),
// and a future customer-notification context stays a potential consumer.
package notice

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// entity is the aggregate segment of the published type: the shopper-facing
// view of the order (ADR 0017).
const entity = "order"

// MaxVersion bounds the policy version to what the wire and the table hold.
const MaxVersion = 2147483647

// Family is a notice family: the kind of fact a notice is about. The triggers
// are facts customer-ordering already consumes or produces (ADR 0017).
type Family string

// Every family, in catalogue order.
const (
	FamilyDelayExpected         Family = "DELAY_EXPECTED"
	FamilyEtaWindowAssigned     Family = "ETA_WINDOW_ASSIGNED"
	FamilyDeliveryPointAdvisory Family = "DELIVERY_POINT_ADVISORY"
	FamilyGroupShipped          Family = "GROUP_SHIPPED"
	FamilyGroupDelivered        Family = "GROUP_DELIVERED"
	FamilyGroupLost             Family = "GROUP_LOST"
	FamilyGroupReturned         Family = "GROUP_RETURNED"
)

// Families is the closed family set, in catalogue order.
var Families = []Family{
	FamilyDelayExpected, FamilyEtaWindowAssigned, FamilyDeliveryPointAdvisory,
	FamilyGroupShipped, FamilyGroupDelivered, FamilyGroupLost, FamilyGroupReturned,
}

// IsKnownFamily reports whether f is one of Families.
func IsKnownFamily(f Family) bool { return slices.Contains(Families, f) }

// Notifier is who delivers the notice (ADR 0017). NOBODY is an instruction to
// the marketplace side to drop it, not an order to stay silent on the wire.
type Notifier string

// Every notifier.
const (
	NotifierMarketplace Notifier = "MARKETPLACE"
	NotifierSeller      Notifier = "SELLER"
	NotifierNobody      Notifier = "NOBODY"
)

// IsKnownNotifier reports whether n is one of the notifiers.
func IsKnownNotifier(n Notifier) bool {
	switch n {
	case NotifierMarketplace, NotifierSeller, NotifierNobody:
		return true
	}
	return false
}

// Row is one family's decision of the document.
type Row struct {
	Family   Family
	Notifier Notifier
}

// DefaultRows is the built-in document: every family NOBODY (version 0).
func DefaultRows() []Row {
	rows := make([]Row, len(Families))
	for i, f := range Families {
		rows[i] = Row{Family: f, Notifier: NotifierNobody}
	}
	return rows
}

// ErrVersionNotNext marks a publish whose version is not the stored one + 1.
var ErrVersionNotNext = errors.New("policy version is not the next one")

// Policy is the aggregate root: the current document.
type Policy struct {
	version     int
	rows        map[Family]Notifier
	publishedAt time.Time
}

// Snapshot is the persisted form of one version. Restore validates it.
type Snapshot struct {
	Version     int
	Rows        []Row
	PublishedAt time.Time
}

// Default returns version 0: every family NOBODY, the policy in force before
// anything was published (the dormant posture of ADR 0017).
func Default() *Policy { return &Policy{rows: toRows(DefaultRows())} }

func toRows(rows []Row) map[Family]Notifier {
	m := make(map[Family]Notifier, len(rows))
	for _, r := range rows {
		m[r.Family] = r.Notifier
	}
	return m
}

func validate(rows []Row) error {
	if len(rows) != len(Families) {
		return shared.Invalid("the document needs exactly %d rows, one per family", len(Families))
	}
	seen := make(map[Family]bool, len(rows))
	for _, r := range rows {
		if !IsKnownFamily(r.Family) {
			return shared.Invalid("unknown family %q", r.Family)
		}
		if seen[r.Family] {
			return shared.Invalid("family %q appears twice", r.Family)
		}
		seen[r.Family] = true
		if !IsKnownNotifier(r.Notifier) {
			return shared.Invalid("family %q: unknown notifier %q", r.Family, r.Notifier)
		}
	}
	return nil
}

// Publish creates policy version current+1 (current is the highest stored
// version, 0 when none). No event is raised: v1 is analytics-free (ADR 0017).
func Publish(current int, rows []Row, now time.Time) (*Policy, error) {
	if current < 0 || current >= MaxVersion {
		return nil, fmt.Errorf("%w: current version %d", ErrVersionNotNext, current)
	}
	if err := validate(rows); err != nil {
		return nil, err
	}
	if now.IsZero() {
		return nil, shared.Invalid("publishedAt is required")
	}
	return &Policy{version: current + 1, rows: toRows(rows), publishedAt: shared.UTC(now)}, nil
}

// Restore rebuilds a persisted version, validating every invariant. Version 0
// is only valid as the default (use Default).
func Restore(s Snapshot) (*Policy, error) {
	if err := validate(s.Rows); err != nil {
		return nil, shared.Corrupt("policy %d: %v", s.Version, err)
	}
	if s.Version < 1 || s.Version > MaxVersion || s.PublishedAt.IsZero() {
		return nil, shared.Corrupt("policy %d: a version of at least 1 and publishedAt are required", s.Version)
	}
	return &Policy{version: s.Version, rows: toRows(s.Rows), publishedAt: shared.UTC(s.PublishedAt)}, nil
}

// Snapshot returns the persisted form, rows in catalogue order.
func (p *Policy) Snapshot() Snapshot {
	rows := make([]Row, 0, len(p.rows))
	for _, f := range Families {
		rows = append(rows, Row{Family: f, Notifier: p.rows[f]})
	}
	return Snapshot{Version: p.version, Rows: rows, PublishedAt: p.publishedAt}
}

// Version returns the version number (0 for the default).
func (p *Policy) Version() int { return p.version }

// PublishedAt returns the publish instant (zero for the default).
func (p *Policy) PublishedAt() time.Time { return p.publishedAt }

// Notifier returns the notifier of the family. An unknown family is NOBODY:
// the family set is closed, so this is defence, not a decision.
func (p *Policy) Notifier(f Family) Notifier {
	if n, ok := p.rows[f]; ok {
		return n
	}
	return NotifierNobody
}

// Fact carries only codes already public on the events customer-ordering
// consumes, one shape per family (ADR 0017); the zero fields of the other
// shapes stay zero.
type Fact struct {
	// DeliverByLatest is the revised window end (DELAY_EXPECTED).
	DeliverByLatest time.Time
	// EtaStart and EtaEnd are the day-of window (ETA_WINDOW_ASSIGNED).
	EtaStart time.Time
	EtaEnd   time.Time
	// Reason is the delivery-point advisory reason (DELIVERY_POINT_ADVISORY).
	Reason string
	// Status is the group status of a terminal family.
	Status string
}

// DelayFact is the DELAY_EXPECTED fact: the revised deliverByLatest.
func DelayFact(deliverByLatest time.Time) Fact {
	return Fact{DeliverByLatest: shared.UTC(deliverByLatest)}
}

// EtaFact is the ETA_WINDOW_ASSIGNED fact.
func EtaFact(etaStart, etaEnd time.Time) Fact {
	return Fact{EtaStart: shared.UTC(etaStart), EtaEnd: shared.UTC(etaEnd)}
}

// PointFlagFact is the DELIVERY_POINT_ADVISORY fact.
func PointFlagFact(reason string) Fact { return Fact{Reason: reason} }

// StatusFact is a terminal family's fact: the group status.
func StatusFact(status string) Fact { return Fact{Status: status} }

// digestOf renders one field as it appears on the wire (RFC 3339 UTC).
func digestOf(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// Digest is the canonical form of the fact, deterministic per family: the
// wire values in family order (an instant as RFC 3339 UTC).
func (f Fact) Digest() string {
	var b strings.Builder
	if !f.DeliverByLatest.IsZero() {
		b.WriteString("deliverByLatest=" + digestOf(f.DeliverByLatest))
	}
	if !f.EtaStart.IsZero() || !f.EtaEnd.IsZero() {
		b.WriteString("etaStart=" + digestOf(f.EtaStart) + ";etaEnd=" + digestOf(f.EtaEnd))
	}
	if f.Reason != "" {
		b.WriteString("reason=" + f.Reason)
	}
	if f.Status != "" {
		b.WriteString("status=" + f.Status)
	}
	return b.String()
}

// NoticeKey is the consumer dedupe key of one family occurrence:
// sha256(orderId|groupNo|family|factDigest) (ADR 0017). A re-assignment of the
// window is a new fact (a new key); a replay of the same fact is not.
func NoticeKey(orderID string, groupNo int, family Family, fact Fact) string {
	sum := sha256.Sum256([]byte(orderID + "|" + fmt.Sprintf("%d", groupNo) + "|" + string(family) + "|" + fact.Digest()))
	return hex.EncodeToString(sum[:])
}

// ShopperNoticeDecided is the decided notice fact of one family occurrence,
// raised from the existing consumer use cases in their unit of work. The
// notice itself is delivered outside the network; this event is the fact and
// the notifier instruction (ADR 0017).
type ShopperNoticeDecided struct {
	shared.Header
	OrderID       string
	GroupNo       int
	Family        Family
	Notifier      Notifier
	PolicyVersion int
	NoticeKey     string
	DecidedAt     time.Time
	Fact          Fact
}

// EventName returns "ShopperNoticeDecided".
func (ShopperNoticeDecided) EventName() string { return "ShopperNoticeDecided" }

// Entity returns "order" (the shopper-facing entity of ADR 0017).
func (ShopperNoticeDecided) Entity() string { return entity }

// SubjectID returns the orderId.
func (e ShopperNoticeDecided) SubjectID() string { return e.OrderID }

var _ shared.Event = ShopperNoticeDecided{}
