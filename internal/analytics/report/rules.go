package report

import (
	"errors"
	"fmt"
	"slices"
)

// The KPI rules of ADR 0018. They are functions, not package variables or
// constants, so the vocabularies cannot be mutated by a caller and mutation
// testing reaches them.

// ConsolidatedTier is the group tier of the opt-in later delivery day the
// network may merge (ADR 0013). An order is a consolidated-tier order when at
// least one of its groups carries it.
func ConsolidatedTier() string { return "CONSOLIDATED" }

// SplitMinGroups is the number of shipment groups from which an order is a
// split shipment: every group has its own promise and its own shipment, so an
// order with two or more is shipped apart.
func SplitMinGroups() int { return 2 }

// IsSplit reports whether an order of groupCount shipment groups is a split
// shipment.
func IsSplit(groupCount int) bool { return groupCount >= SplitMinGroups() }

// NoticeFamilies is the notice vocabulary of ADR 0017, in the order the ADR
// lists it.
func NoticeFamilies() []string {
	return []string{
		"DELAY_EXPECTED", "ETA_WINDOW_ASSIGNED", "DELIVERY_POINT_ADVISORY",
		"GROUP_SHIPPED", "GROUP_DELIVERED", "GROUP_LOST", "GROUP_RETURNED",
	}
}

// UnrecognisedFamily is the kind a notice of a family outside NoticeFamilies
// is stored under: the read side keeps a bounded, code-only vocabulary, so a
// family added later is counted, never dropped, and never stored verbatim.
func UnrecognisedFamily() string { return "UNRECOGNISED" }

// NormalizeFamily is family when it is in NoticeFamilies, else
// UnrecognisedFamily.
func NormalizeFamily(family string) string {
	if slices.Contains(NoticeFamilies(), family) {
		return family
	}
	return UnrecognisedFamily()
}

// MaxNoticeKinds bounds the by-kind breakdown of one day: at most this many
// kinds are listed, the rest is folded into one remainder count.
func MaxNoticeKinds() int { return 5 }

// ErrInvalidEvent marks a fact that can never be projected: a required field is
// missing. It is deterministic, so the projector dead-letters it.
var ErrInvalidEvent = errors.New("report: invalid analytics event")

// Validate checks the fields every fact needs and the fields its Kind needs.
func (e Event) Validate() error {
	missing := firstMissing(
		required{"id", e.EventID != ""},
		required{"time", !e.At.IsZero()},
		required{"orderId", e.OrderID != ""},
		required{"the business instant", !e.OccurredAt.IsZero()},
	)
	if missing == "" {
		missing = e.kindMissing()
	}
	if missing != "" {
		return fmt.Errorf("%w: %s %s: %s is required", ErrInvalidEvent, e.Kind, e.EventID, missing)
	}
	return nil
}

// kindMissing names the first missing or inconsistent field of the Kind's own
// payload, or rejects a Kind the projection does not know.
func (e Event) kindMissing() string {
	switch e.Kind {
	case KindOrderPlaced:
		return firstMissing(
			required{"groups", e.GroupCount >= 1},
			required{"consistent consolidated group count", e.ConsolidatedGroups >= 0 && e.ConsolidatedGroups <= e.GroupCount},
		)
	case KindOrderCancelled:
		return ""
	case KindNoticeDecided:
		return firstMissing(required{"family", e.Family != ""}, required{"noticeKey", e.NoticeKey != ""})
	default:
		return "a known kind"
	}
}

// required is one field check: name and whether it is present.
type required struct {
	name    string
	present bool
}

// firstMissing names the first absent field, or "" when every one is present.
func firstMissing(fields ...required) string {
	for _, f := range fields {
		if !f.present {
			return f.name
		}
	}
	return ""
}
