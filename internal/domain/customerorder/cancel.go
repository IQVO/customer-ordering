package customerorder

import (
	"fmt"
	"strings"
	"time"

	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// UnkeepableReasonPrefix starts the reason of a group cancelled by the policy
// CancelOnUnfulfillable (A5).
const UnkeepableReasonPrefix = "PROMISE_UNKEEPABLE: "

// CancelOrder is the shopper's cancellation REQUEST (A3): every PROMISED group
// becomes CANCELLATION_REQUESTED and CustomerOrderCancelled lists exactly those
// groups. Locked, shipped and delivered groups are untouched. With nothing
// left to request, a repeat (every group already requested or cancelled)
// changes nothing and raises nothing; a DELIVERED order is a status
// regression; otherwise ErrCancelAfterLock (A4).
func (o *CustomerOrder) CancelOrder(reason string, now time.Time) ([]shared.Event, error) {
	if !shared.ValidTextN(reason, MaxReason) {
		return nil, shared.Invalid("reason must be 1 to %d trimmed characters without control characters", MaxReason)
	}
	if o.status == StatusDelivered {
		return nil, fmt.Errorf("%w: order %s is %s", ErrStatusRegression, o.id, o.status)
	}
	at := shared.UTC(now)
	var nos []int
	repeat := false
	for i := range o.groups {
		switch o.groups[i].Status {
		case GroupPromised:
			g := &o.groups[i]
			g.Status, g.CancelReason, g.CancelRequestedAt = GroupCancellationRequested, reason, at
			nos = append(nos, g.GroupNo)
		case GroupCancellationRequested, GroupCancelled:
			repeat = true
		}
	}
	if len(nos) == 0 {
		if repeat {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: every group of order %s is locked, shipped or delivered", ErrCancelAfterLock, o.id)
	}
	o.bump(at)
	return []shared.Event{o.cancelled(reason, at, nos)}, nil
}

func (o *CustomerOrder) cancelled(reason string, at time.Time, nos []int) shared.Event {
	return CustomerOrderCancelled{
		Header: shared.Header{Version: o.version, At: at}, OrderID: o.id, Reason: reason, CancelledAt: at, GroupNos: nos,
	}
}

// CancelUnkeepableGroup applies the policy CancelOnUnfulfillable
// (PromiseUnkeepable): a PROMISED group is CANCELLED at once and
// CustomerOrderCancelled lists it. The order becomes CANCELLED when nothing
// remains. Any other group state is illegal (A5).
func (o *CustomerOrder) CancelUnkeepableGroup(groupNo int, reason string, now time.Time) ([]shared.Event, error) {
	g, err := o.group(groupNo)
	if err != nil {
		return nil, err
	}
	if g.Status != GroupPromised {
		return nil, illegal(g, "CancelUnkeepableGroup")
	}
	at := shared.UTC(now)
	g.Status, g.CancelReason, g.CancelRequestedAt, g.CancelledAt = GroupCancelled, clean(UnkeepableReasonPrefix+reason), at, at
	o.bump(at)
	return []shared.Event{o.cancelled(g.CancelReason, at, []int{groupNo})}, nil
}

// ConfirmGroupCancelled records order-sourcing's acceptance
// (FulfillmentCancelled): PROMISED, CANCELLATION_REQUESTED or LOCKED ->
// CANCELLED. CANCELLED again is a no-op; SHIPPED and DELIVERED are a status
// regression.
func (o *CustomerOrder) ConfirmGroupCancelled(groupNo int, at time.Time) error {
	if at.IsZero() {
		return shared.Invalid("cancelledAt is required")
	}
	g, err := o.group(groupNo)
	if err != nil {
		return err
	}
	switch g.Status {
	case GroupCancelled:
		return nil
	case GroupShipped, GroupDelivered:
		return regression(g, string(GroupCancelled))
	}
	g.Status, g.CancelledAt = GroupCancelled, shared.UTC(at)
	o.bump(g.CancelledAt)
	return nil
}

// RefuseGroupCancellation records order-sourcing's refusal
// (FulfillmentCancelRefused): CANCELLATION_REQUESTED -> LOCKED, the vendor
// ships. For an already LOCKED, SHIPPED or DELIVERED group the reason is only
// recorded. PROMISED and CANCELLED are illegal (A3).
func (o *CustomerOrder) RefuseGroupCancellation(groupNo int, reason string, at time.Time) error {
	if at.IsZero() {
		return shared.Invalid("refusedAt is required")
	}
	g, err := o.group(groupNo)
	if err != nil {
		return err
	}
	if g.Status == GroupPromised || g.Status == GroupCancelled {
		return illegal(g, "RefuseGroupCancellation")
	}
	reason = clean(reason)
	if reason == "" {
		reason = "REFUSED"
	}
	if g.Status != GroupCancellationRequested && g.CancelRefusedReason == reason {
		return nil
	}
	g.CancelRefusedReason = reason
	if g.Status == GroupCancellationRequested {
		g.Status = GroupLocked
	}
	o.bump(shared.UTC(at))
	return nil
}

// clean drops control characters, trims and cuts s to MaxReason characters, so
// text from another context always satisfies the reason rule.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	runes := []rune(strings.TrimSpace(s))
	return strings.TrimSpace(string(runes[:min(len(runes), MaxReason)]))
}
