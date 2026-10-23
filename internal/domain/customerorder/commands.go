package customerorder

import (
	"fmt"
	"slices"
	"time"

	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

func (o *CustomerOrder) group(groupNo int) (*GroupPromise, error) {
	i := o.indexByGroup(groupNo)
	if i < 0 {
		return nil, wrapUnknownGroup("order %s has no group %d", o.id, groupNo)
	}
	return &o.groups[i], nil
}

func regression(g *GroupPromise, to string) error {
	return fmt.Errorf("%w: group %d is %s, cannot move to %s", ErrStatusRegression, g.GroupNo, g.Status, to)
}

func illegal(g *GroupPromise, command string) error {
	return fmt.Errorf("%w: %s on group %d in state %s", shared.ErrIllegalTransition, command, g.GroupNo, g.Status)
}

// MarkInFulfillment records that order-sourcing locked the group to a node
// (FulfillmentLocked). PROMISED -> LOCKED. A lock for a group whose
// cancellation is requested is recorded but leaves the state: order-sourcing's
// answer decides (A3). LOCKED again is a no-op; SHIPPED and DELIVERED are a
// regression; CANCELLED is illegal.
func (o *CustomerOrder) MarkInFulfillment(groupNo int, assignmentID, nodeID string, lockedAt time.Time) error {
	if !shared.ValidID(assignmentID) || !shared.ValidID(nodeID) || lockedAt.IsZero() {
		return shared.Invalid("lock needs assignmentId, nodeId and lockedAt")
	}
	g, err := o.group(groupNo)
	if err != nil {
		return err
	}
	switch g.Status {
	case GroupLocked:
		return nil
	case GroupShipped, GroupDelivered, GroupLost, GroupReturned:
		return regression(g, string(GroupLocked))
	case GroupCancelled:
		return illegal(g, "MarkInFulfillment")
	case GroupPromised:
		g.Status = GroupLocked
	}
	g.AssignmentID, g.NodeID, g.LockedAt = assignmentID, nodeID, shared.UTC(lockedAt)
	o.bump(g.LockedAt)
	return nil
}

func validTrackingNumbers(list []string) ([]string, error) {
	out := make([]string, 0, len(list))
	for _, tn := range list {
		if !shared.ValidTextN(tn, MaxTrackingNumberLen) {
			return nil, shared.Invalid("trackingNumber %q must be 1 to %d trimmed characters", tn, MaxTrackingNumberLen)
		}
		if !slices.Contains(out, tn) {
			out = append(out, tn)
		}
	}
	if len(out) > MaxTrackingNumbers {
		return nil, shared.Invalid("a group holds at most %d tracking numbers", MaxTrackingNumbers)
	}
	return out, nil
}

// MarkGroupShipped records that the group shipped (FulfillmentShipped).
// PROMISED, LOCKED or CANCELLATION_REQUESTED -> SHIPPED (a vendor that ships
// wins over a cancel request); SHIPPED again is a no-op; DELIVERED is a
// regression, and so are LOST and RETURNED (they did ship); CANCELLED is
// illegal. If every announced package was already
// reported delivered the group is DELIVERED at once (A21).
func (o *CustomerOrder) MarkGroupShipped(groupNo int, trackingNumbers []string, shippedAt time.Time) error {
	tns, err := validTrackingNumbers(trackingNumbers)
	if err != nil {
		return err
	}
	if shippedAt.IsZero() {
		return shared.Invalid("shippedAt is required")
	}
	g, err := o.group(groupNo)
	if err != nil {
		return err
	}
	switch g.Status {
	case GroupShipped:
		return nil
	case GroupDelivered, GroupLost, GroupReturned:
		return regression(g, string(GroupShipped))
	case GroupCancelled:
		return illegal(g, "MarkGroupShipped")
	}
	g.Status, g.TrackingNumbers, g.ShippedAt = GroupShipped, tns, shared.UTC(shippedAt)
	g.settleDelivery()
	o.bump(g.ShippedAt)
	return nil
}

// settleDelivery moves a SHIPPED group to DELIVERED once every announced
// tracking number has been reported delivered.
func (g *GroupPromise) settleDelivery() {
	if g.Status != GroupShipped || len(g.TrackingNumbers) == 0 {
		return
	}
	for _, tn := range g.TrackingNumbers {
		if !slices.Contains(g.DeliveredTrackingNumbers, tn) {
			return
		}
	}
	g.Status = GroupDelivered
}

// MarkGroupDelivered records that one package of the group was delivered
// (PackageDelivered). The group is DELIVERED when it is SHIPPED and every
// announced package is delivered. A delivery that arrives before the shipment
// is remembered (A21); a repeat is a no-op; CANCELLED, LOST and RETURNED are
// illegal (terminal).
func (o *CustomerOrder) MarkGroupDelivered(groupNo int, trackingNumber string, deliveredAt time.Time) error {
	if !shared.ValidTextN(trackingNumber, MaxTrackingNumberLen) || deliveredAt.IsZero() {
		return shared.Invalid("delivery needs trackingNumber and deliveredAt")
	}
	g, err := o.group(groupNo)
	if err != nil {
		return err
	}
	if g.Status == GroupCancelled || g.closedWithoutDelivery() {
		return illegal(g, "MarkGroupDelivered")
	}
	if slices.Contains(g.DeliveredTrackingNumbers, trackingNumber) || len(g.DeliveredTrackingNumbers) >= MaxTrackingNumbers {
		return nil
	}
	at := shared.UTC(deliveredAt)
	g.DeliveredTrackingNumbers = append(g.DeliveredTrackingNumbers, trackingNumber)
	if at.After(g.DeliveredAt) {
		g.DeliveredAt = at
	}
	g.settleDelivery()
	o.bump(at)
	return nil
}

// UpdateGroupPromise applies a revised promise (PromiseRevised): a later
// window, shipBy and node for the group holding the promise. It never changes
// the group state. A revision no newer than the stored one is a no-op; a
// CANCELLED, DELIVERED, LOST or RETURNED group is illegal (A8, A19).
func (o *CustomerOrder) UpdateGroupPromise(promiseID string, revisionNo int, earliest, latest, shipBy time.Time, nodeID string) error {
	i := o.indexByPromise(promiseID)
	if i < 0 {
		return wrapUnknownGroup("order %s has no group with promise %q", o.id, promiseID)
	}
	if revisionNo < 1 || revisionNo > MaxNumber || earliest.IsZero() || latest.IsZero() || latest.Before(earliest) || shipBy.IsZero() {
		return shared.Invalid("a revision needs revisionNo >= 1, shipBy and a window with earliest <= latest")
	}
	if nodeID != "" && !shared.ValidID(nodeID) {
		return shared.Invalid("nodeId %q is not a valid id", nodeID)
	}
	g := &o.groups[i]
	if g.terminal() {
		return illegal(g, "UpdateGroupPromise")
	}
	if revisionNo <= g.RevisionNo {
		return nil
	}
	g.RevisionNo = revisionNo
	g.DeliverByEarliest, g.DeliverByLatest, g.ShipBy = shared.UTC(earliest), shared.UTC(latest), shared.UTC(shipBy)
	if nodeID != "" {
		g.WitnessNodeID = nodeID
	}
	o.bump(o.placedAt)
	return nil
}

// MarkGroupLost records that a package of the group was declared lost
// (PackageLost, A19): SHIPPED -> LOST, terminal. One lost package loses the
// group. LOST again is a no-op; a group that did not ship, is DELIVERED,
// RETURNED or CANCELLED is illegal; a tracking number the group never
// announced is an unknown group.
func (o *CustomerOrder) MarkGroupLost(groupNo int, trackingNumber string, declaredAt time.Time) error {
	return o.markPackageOutcome("MarkGroupLost", groupNo, trackingNumber, declaredAt, GroupLost)
}

// MarkGroupReturned records that a package of the group was returned to the
// sender (PackageReturned, A19): SHIPPED -> RETURNED, terminal, with the same
// rules as MarkGroupLost.
func (o *CustomerOrder) MarkGroupReturned(groupNo int, trackingNumber string, returnedAt time.Time) error {
	return o.markPackageOutcome("MarkGroupReturned", groupNo, trackingNumber, returnedAt, GroupReturned)
}

func (o *CustomerOrder) markPackageOutcome(command string, groupNo int, trackingNumber string, at time.Time, to GroupStatus) error {
	if !shared.ValidTextN(trackingNumber, MaxTrackingNumberLen) || at.IsZero() {
		return shared.Invalid("a %s package needs trackingNumber and its instant", to)
	}
	g, err := o.group(groupNo)
	if err != nil {
		return err
	}
	switch g.Status {
	case to:
		return nil
	case GroupShipped:
	default:
		return illegal(g, command)
	}
	if !slices.Contains(g.TrackingNumbers, trackingNumber) {
		return wrapUnknownGroup("group %d of order %s did not announce package %q", groupNo, o.id, trackingNumber)
	}
	if slices.Contains(g.DeliveredTrackingNumbers, trackingNumber) {
		return fmt.Errorf("%w: %s on group %d: package %q was already delivered", shared.ErrIllegalTransition, command, groupNo, trackingNumber)
	}
	g.Status, g.OutcomeTrackingNumber = to, trackingNumber
	if to == GroupLost {
		g.LostAt = shared.UTC(at)
	} else {
		g.ReturnedAt = shared.UTC(at)
	}
	o.bump(shared.UTC(at))
	return nil
}
