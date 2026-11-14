package usecases

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
)

// LabelIssued is the consumed shipping-and-tracking event of the same name.
type LabelIssued struct {
	TrackingNumber string
	AssignmentID   string
	Carrier        string
	// PickupPointID names the point of a pickup label; empty on a ship-to
	// label (ADR 0016).
	PickupPointID string
}

// ApplyLabelIssued creates or completes the package row.
func (c Consumer) ApplyLabelIssued(ctx context.Context, m Meta, in LabelIssued) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		return c.Tracking.ApplyLabel(ctx, readmodel.Label{
			TrackingNumber: in.TrackingNumber, AssignmentID: in.AssignmentID, Carrier: in.Carrier, PickupPointID: in.PickupPointID, At: c.when(m),
		})
	})
}

// LabelVoided is the consumed shipping-and-tracking event of the same name.
type LabelVoided struct{ TrackingNumber string }

// ApplyLabelVoided marks the package VOIDED (G18).
func (c Consumer) ApplyLabelVoided(ctx context.Context, m Meta, in LabelVoided) error {
	return c.handle(ctx, m, func(ctx context.Context) error { return c.Tracking.ApplyVoid(ctx, in.TrackingNumber) })
}

// PackageScanned is the consumed shipping-and-tracking event of the same name.
type PackageScanned struct {
	TrackingNumber string
	NodeID         string
	State          string
	At             time.Time
}

// ApplyPackageScanned records the scan unless a newer one is stored.
func (c Consumer) ApplyPackageScanned(ctx context.Context, m Meta, in PackageScanned) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		return c.Tracking.ApplyScan(ctx, readmodel.Scan{
			TrackingNumber: in.TrackingNumber, NodeID: in.NodeID, State: in.State, At: orElse(in.At, c.when(m)),
		})
	})
}

// PackageExceptionRaised is the consumed shipping-and-tracking event of the same name.
type PackageExceptionRaised struct {
	TrackingNumber string
	AssignmentID   string
	OrderID        string
	Reason         string
	At             time.Time
}

// ApplyPackageException records the exception reason.
func (c Consumer) ApplyPackageException(ctx context.Context, m Meta, in PackageExceptionRaised) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		return c.Tracking.ApplyException(ctx, readmodel.Exception{
			TrackingNumber: in.TrackingNumber, AssignmentID: in.AssignmentID, OrderID: in.OrderID,
			Reason: reasonOrDefault(in.Reason), At: orElse(in.At, c.when(m)),
		})
	})
}

// PackageDelivered is the consumed shipping-and-tracking event of the same name.
type PackageDelivered struct {
	TrackingNumber string
	AssignmentID   string
	OrderID        string
	DeliveredAt    time.Time
}

// ApplyPackageDelivered marks the package delivered and runs
// MarkGroupDelivered for EVERY group that named the tracking number (a package
// shared by merged groups closes all of them, ADR 0012) and for the group the
// event itself resolves. A delivery whose group is not known yet stays in the
// tracking rows and is replayed when the group's shipment arrives (A21).
func (c Consumer) ApplyPackageDelivered(ctx context.Context, m Meta, in PackageDelivered) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		at := orElse(in.DeliveredAt, c.when(m))
		if err := c.Tracking.ApplyDelivery(ctx, readmodel.Delivery{
			TrackingNumber: in.TrackingNumber, AssignmentID: in.AssignmentID, OrderID: in.OrderID, At: at,
		}); err != nil {
			return err
		}
		targets, err := c.deliveryTargets(ctx, in)
		if err != nil {
			return err
		}
		return c.eachGroup(ctx, targets, func(o *customerorder.CustomerOrder, groupNo int) error {
			n := groupNo
			if n == 0 {
				var ok bool
				if n, ok = groupOf(o, in); !ok {
					return nil
				}
			}
			return o.MarkGroupDelivered(n, in.TrackingNumber, at)
		})
	})
}

// groupTarget is one group a package event applies to; groupNo 0 means "the
// group the order resolves from the event" (the delivery of a package whose
// assignment link is not known yet).
type groupTarget struct {
	orderID string
	groupNo int
}

// refTargets turns the group set of a tracking number into targets.
func refTargets(refs []readmodel.GroupRef) []groupTarget {
	out := make([]groupTarget, len(refs))
	for i, r := range refs {
		out[i] = groupTarget{orderID: r.OrderID, groupNo: r.GroupNo}
	}
	return out
}

// deliveryTargets are the groups of the tracking number plus the group the
// event resolves through the assignment link (the carrier's, which is also in
// the set once it shipped).
func (c Consumer) deliveryTargets(ctx context.Context, in PackageDelivered) ([]groupTarget, error) {
	refs, err := c.Tracking.GroupsOfTracking(ctx, in.TrackingNumber)
	if err != nil {
		return nil, err
	}
	targets := refTargets(refs)
	orderID, groupNo, err := c.locate(ctx, in)
	if err != nil || orderID == "" {
		return targets, err
	}
	if groupNo != 0 {
		return withTarget(targets, groupTarget{orderID: orderID, groupNo: groupNo}), nil
	}
	for _, t := range targets {
		if t.orderID == orderID {
			return targets, nil // the order's groups of this package are all targets already
		}
	}
	return append(targets, groupTarget{orderID: orderID}), nil
}

func withTarget(targets []groupTarget, t groupTarget) []groupTarget {
	if slices.Contains(targets, t) {
		return targets
	}
	return append(targets, t)
}

// eachGroup applies one command to every target, each in its own mutate inside
// the event's unit of work. A target the order cannot take (unknown order,
// illegal for the group's state) is recorded and skipped without stopping the
// others: one shared package must still reach every group that can take it (A7).
func (c Consumer) eachGroup(ctx context.Context, targets []groupTarget, apply func(o *customerorder.CustomerOrder, groupNo int) error) error {
	for _, t := range targets {
		err := c.mutate(ctx, t.orderID, quiet(func(o *customerorder.CustomerOrder) error { return apply(o, t.groupNo) }))
		if err == nil {
			continue
		}
		if !skippable(err) {
			return err
		}
		c.logger().InfoContext(ctx, "consumed event skipped for one group of a shared package",
			"orderId", sanitizeLog(t.orderID), "groupNo", sanitizeLog(strconv.Itoa(t.groupNo)), "reason", err.Error())
	}
	return nil
}

// locate resolves the order and, when the assignment link is known, the group.
func (c Consumer) locate(ctx context.Context, in PackageDelivered) (string, int, error) {
	orderID := in.OrderID
	link, err := c.Tracking.ResolveAssignment(ctx, in.AssignmentID)
	switch {
	case err == nil:
		if orderID == "" {
			orderID = link.OrderID
		}
		return orderID, link.GroupNo, nil
	case errors.Is(err, ports.ErrNotFound):
		return orderID, 0, nil
	}
	return "", 0, err
}

func groupOf(o *customerorder.CustomerOrder, in PackageDelivered) (int, bool) {
	if n, ok := o.GroupNoByTrackingNumber(in.TrackingNumber); ok {
		return n, true
	}
	return o.GroupNoByAssignment(in.AssignmentID)
}

// PackageLost is the consumed shipping-and-tracking event of the same name.
type PackageLost struct {
	TrackingNumber string
	OrderID        string
	DeclaredAt     time.Time
}

// ApplyPackageLost records the outcome on the package row and runs
// MarkGroupLost for the group that announced the package (A19). A loss for a
// package whose shipment is not known yet stays on the row and is replayed
// when FulfillmentShipped arrives.
func (c Consumer) ApplyPackageLost(ctx context.Context, m Meta, in PackageLost) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		at := orElse(in.DeclaredAt, c.when(m))
		return c.applyOutcome(ctx, readmodel.Outcome{TrackingNumber: in.TrackingNumber, OrderID: in.OrderID, Kind: readmodel.OutcomeLost, At: at})
	})
}

// PackageReturned is the consumed shipping-and-tracking event of the same name.
type PackageReturned struct {
	TrackingNumber string
	OrderID        string
	ReturnedAt     time.Time
}

// ApplyPackageReturned records the outcome on the package row and runs
// MarkGroupReturned for the group that announced the package (A19).
func (c Consumer) ApplyPackageReturned(ctx context.Context, m Meta, in PackageReturned) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		at := orElse(in.ReturnedAt, c.when(m))
		return c.applyOutcome(ctx, readmodel.Outcome{TrackingNumber: in.TrackingNumber, OrderID: in.OrderID, Kind: readmodel.OutcomeReturned, At: at})
	})
}

// applyOutcome records the outcome on the package row and marks EVERY group of
// the tracking number (ADR 0012): the package is one, so a merged shipment is
// lost or returned for all of its groups. When the event's own order is not in
// the set yet, the group that announced the package in that order is marked, as
// before.
func (c Consumer) applyOutcome(ctx context.Context, out readmodel.Outcome) error {
	if err := c.Tracking.ApplyOutcome(ctx, out); err != nil {
		return err
	}
	refs, err := c.Tracking.GroupsOfTracking(ctx, out.TrackingNumber)
	if err != nil {
		return err
	}
	targets := refTargets(refs)
	if !slices.ContainsFunc(targets, func(t groupTarget) bool { return t.orderID == out.OrderID }) {
		targets = append(targets, groupTarget{orderID: out.OrderID})
	}
	return c.eachGroup(ctx, targets, func(o *customerorder.CustomerOrder, groupNo int) error {
		n := groupNo
		if n == 0 {
			var ok bool
			if n, ok = o.GroupNoByTrackingNumber(out.TrackingNumber); !ok {
				return nil // the shipment is not known yet: replayed by reconcile
			}
		}
		return markOutcome(o, n, out)
	})
}

func markOutcome(o *customerorder.CustomerOrder, groupNo int, out readmodel.Outcome) error {
	if out.Kind == readmodel.OutcomeLost {
		return o.MarkGroupLost(groupNo, out.TrackingNumber, out.At)
	}
	return o.MarkGroupReturned(groupNo, out.TrackingNumber, out.At)
}

// reconcileOutcomes replays the outcomes reported for the group's packages
// before its shipment was known (A19). An outcome the group can no longer take
// (it was delivered first, or another outcome came first) is skipped.
func (c Consumer) reconcileOutcomes(ctx context.Context, o *customerorder.CustomerOrder, groupNo int) error {
	outcomes, err := c.Tracking.OutcomePackages(ctx, string(o.ID()), groupNo)
	if err != nil {
		return err
	}
	for _, out := range outcomes {
		if err := markOutcome(o, groupNo, out); err != nil && !skippable(err) {
			return err
		}
	}
	return nil
}
