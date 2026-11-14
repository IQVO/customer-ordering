package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/notice"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// targetOfAssignment resolves the group through the assignment link, with the
// event's own orderId as the fallback of a link that names no order.
func (c Consumer) targetOfAssignment(ctx context.Context, assignmentID, orderID string) ([]groupTarget, error) {
	link, err := c.Tracking.ResolveAssignment(ctx, assignmentID)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return nil, nil // an unknown link leaves the fact unattributed (ADR 0017)
		}
		return nil, err
	}
	order, group := link.OrderID, link.GroupNo
	if order == "" {
		order = orderID
	}
	if order == "" || group == 0 {
		return nil, nil
	}
	return []groupTarget{{orderID: order, groupNo: group}}, nil
}

// noticeTargetsOfTracking resolves the groups a package fact attributes to:
// every group that named the tracking number (a shared package of merged
// groups, ADR 0012), else the assignment link, else nothing (the fact cannot
// be attributed yet and emits no notice, ADR 0017).
func (c Consumer) noticeTargetsOfTracking(ctx context.Context, trackingNumber, assignmentID, orderID string) ([]groupTarget, error) {
	refs, err := c.Tracking.GroupsOfTracking(ctx, trackingNumber)
	if err != nil {
		return nil, err
	}
	if targets := dedupeTargets(refTargets(refs)); len(targets) > 0 {
		return targets, nil
	}
	if assignmentID == "" {
		return nil, nil
	}
	return c.targetOfAssignment(ctx, assignmentID, orderID)
}

// dedupeTargets removes duplicate (order, group) pairs, keeping first order.
func dedupeTargets(in []groupTarget) []groupTarget {
	out := make([]groupTarget, 0, len(in))
	for _, t := range in {
		dup := false
		for _, seen := range out {
			if seen == t {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, t)
		}
	}
	return out
}

// decideNotices loads the current policy and returns one ShopperNoticeDecided
// per target, REGARDLESS of the notifier: the event is the fact, `notifier` is
// the instruction to the marketplace side and NOBODY means drop (ADR 0017).
func (c Consumer) decideNotices(ctx context.Context, at time.Time, family notice.Family, fact notice.Fact, targets []groupTarget) ([]shared.Event, error) {
	if len(targets) == 0 {
		return nil, nil
	}
	p, err := currentNoticePolicy(ctx, c.Policies)
	if err != nil {
		return nil, err
	}
	out := make([]shared.Event, 0, len(targets))
	for _, t := range targets {
		out = append(out, notice.ShopperNoticeDecided{
			OrderID: t.orderID, GroupNo: t.groupNo, Family: family, Notifier: p.Notifier(family),
			PolicyVersion: p.Version(), NoticeKey: notice.NoticeKey(t.orderID, t.groupNo, family, fact),
			DecidedAt: shared.UTC(at), Fact: fact,
		})
	}
	return out, nil
}

// publishNotices decides and appends the notice events in the caller's unit of
// work (the same transaction as the effect, ADR 0017). No targets is a no-op.
func (c Consumer) publishNotices(ctx context.Context, at time.Time, family notice.Family, fact notice.Fact, targets []groupTarget) error {
	events, err := c.decideNotices(ctx, at, family, fact, targets)
	if err != nil {
		return err
	}
	return c.publish(ctx, events)
}

// statusOf reads the group's status (empty when the order has no such group).
func statusOf(o *customerorder.CustomerOrder, groupNo int) customerorder.GroupStatus {
	for _, g := range o.Groups() {
		if g.GroupNo == groupNo {
			return g.Status
		}
	}
	return customerorder.GroupStatus("")
}

// shippedOrLater reports whether the status is one the group only reaches by
// shipping (A19: LOST and RETURNED groups did ship).
func shippedOrLater(s customerorder.GroupStatus) bool {
	switch s {
	case customerorder.GroupShipped, customerorder.GroupDelivered, customerorder.GroupLost, customerorder.GroupReturned:
		return true
	}
	return false
}

// groupNotices returns the notice events for the group-status transitions
// crossed between before and after ONE command (ADR 0017): GROUP_SHIPPED when
// the group crossed into the shipped-or-later set, plus the terminal family it
// reached. Nothing crossed returns nil; the state machine moving only forward
// makes a repeat a no-op (the dedupe of the wire).
func (c Consumer) groupNotices(ctx context.Context, at time.Time, orderID string, groupNo int, before, after customerorder.GroupStatus) ([]shared.Event, error) {
	target := []groupTarget{{orderID: orderID, groupNo: groupNo}}
	var events []shared.Event
	if !shippedOrLater(before) && shippedOrLater(after) {
		ev, err := c.decideNotices(ctx, at, notice.FamilyGroupShipped, notice.StatusFact(string(customerorder.GroupShipped)), target)
		if err != nil {
			return nil, err
		}
		events = append(events, ev...)
	}
	var terminal notice.Family
	switch {
	case before != customerorder.GroupDelivered && after == customerorder.GroupDelivered:
		terminal = notice.FamilyGroupDelivered
	case before != customerorder.GroupLost && after == customerorder.GroupLost:
		terminal = notice.FamilyGroupLost
	case before != customerorder.GroupReturned && after == customerorder.GroupReturned:
		terminal = notice.FamilyGroupReturned
	}
	if terminal == "" {
		return events, nil
	}
	ev, err := c.decideNotices(ctx, at, terminal, notice.StatusFact(string(after)), target)
	if err != nil {
		return nil, err
	}
	return append(events, ev...), nil
}
