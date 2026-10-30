package customerorder

import (
	"errors"
	"fmt"
	"slices"

	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// Restore rebuilds a persisted order, validating every invariant.
func Restore(s Snapshot) (*CustomerOrder, error) {
	if err := checkSnapshot(s); err != nil {
		return nil, shared.Corrupt("order %q: %v", s.ID, err)
	}
	groups := make([]GroupPromise, len(s.Groups))
	for i, g := range s.Groups {
		g = g.clone()
		g.ShipBy, g.DeliverByEarliest, g.DeliverByLatest = shared.UTC(g.ShipBy), shared.UTC(g.DeliverByEarliest), shared.UTC(g.DeliverByLatest)
		g.LockedAt, g.ShippedAt, g.DeliveredAt = shared.UTC(g.LockedAt), shared.UTC(g.ShippedAt), shared.UTC(g.DeliveredAt)
		g.CancelRequestedAt, g.CancelledAt = shared.UTC(g.CancelRequestedAt), shared.UTC(g.CancelledAt)
		g.LostAt, g.ReturnedAt = shared.UTC(g.LostAt), shared.UTC(g.ReturnedAt)
		groups[i] = g
	}
	return &CustomerOrder{
		id: s.ID, shopperID: s.ShopperID, shipTo: s.ShipTo, prefix: s.DestinationPostalPrefix, status: s.Status,
		placedAt: shared.UTC(s.PlacedAt), cancelledAt: shared.UTC(s.CancelledAt), lines: append([]OrderLine{}, s.Lines...),
		groups: groups, version: s.Version, loaded: s.Version,
	}, nil
}

func checkSnapshot(s Snapshot) error {
	if !shared.ValidID(string(s.ID)) || !shared.ValidID(s.ShopperID) {
		return errors.New("orderId and shopperId must be valid ids")
	}
	if err := s.ShipTo.Validate(); err != nil {
		return err
	}
	if s.DestinationPostalPrefix != s.ShipTo.DestinationPrefix() {
		return errors.New("destinationPostalPrefix does not match the ship-to")
	}
	if s.PlacedAt.IsZero() || s.Version < 1 {
		return errors.New("placedAt and a version of at least 1 are required")
	}
	if err := validateLines(s.Lines); err != nil {
		return err
	}
	if err := checkGroups(s); err != nil {
		return err
	}
	if !s.Status.Valid() || s.Status != derive(s.Groups) {
		return fmt.Errorf("status %q does not match the group states", s.Status)
	}
	if (s.Status == StatusCancelled) == s.CancelledAt.IsZero() {
		return errors.New("cancelledAt is set exactly when the order is CANCELLED")
	}
	return nil
}

func checkGroups(s Snapshot) error {
	if len(s.Groups) == 0 || len(s.Groups) > MaxGroups {
		return fmt.Errorf("an order holds 1 to %d groups", MaxGroups)
	}
	known := make(map[int]bool, len(s.Lines))
	for _, l := range s.Lines {
		known[l.LineNo] = false
	}
	seen := make(map[int]bool, len(s.Groups))
	for _, g := range s.Groups {
		if g.GroupNo < 1 || g.GroupNo > MaxNumber || seen[g.GroupNo] {
			return fmt.Errorf("groupNo %d must be unique and between 1 and %d", g.GroupNo, MaxNumber)
		}
		seen[g.GroupNo] = true
		if err := checkGroupSnapshot(g); err != nil {
			return fmt.Errorf("group %d: %w", g.GroupNo, err)
		}
		if err := assignLines(GroupInput{GroupNo: g.GroupNo, LineNos: g.LineNos}, known); err != nil {
			return err
		}
	}
	for no, assigned := range known {
		if !assigned {
			return fmt.Errorf("line %d is in no group", no)
		}
	}
	return nil
}

func checkGroupSnapshot(g GroupPromise) error {
	if !shared.ValidID(g.PromiseID) || !shared.ValidID(g.WitnessNodeID) || !g.Tier.Valid() || g.RevisionNo < 0 || g.RevisionNo > MaxNumber {
		return errors.New("promiseId, witnessNodeId, tier and revisionNo must be valid")
	}
	if g.ShipBy.IsZero() || g.DeliverByEarliest.IsZero() || g.DeliverByLatest.Before(g.DeliverByEarliest) {
		return errors.New("shipBy and a delivery window with earliest <= latest are required")
	}
	if g.TimeZone != "" && !shared.ValidTimeZone(g.TimeZone) {
		return fmt.Errorf("timeZone %q is not an IANA time-zone name", g.TimeZone)
	}
	if !g.Status.Valid() {
		return fmt.Errorf("status %q is not a group state", g.Status)
	}
	if err := checkPackages(g); err != nil {
		return err
	}
	if err := checkReasons(g); err != nil {
		return err
	}
	return checkStatusFacts(g)
}

func checkPackages(g GroupPromise) error {
	for _, list := range [][]string{g.TrackingNumbers, g.DeliveredTrackingNumbers} {
		if len(list) > MaxTrackingNumbers {
			return errors.New("too many tracking numbers")
		}
		for i, tn := range list {
			if !shared.ValidTextN(tn, MaxTrackingNumberLen) || slices.Contains(list[:i], tn) {
				return fmt.Errorf("trackingNumber %q is invalid or repeated", tn)
			}
		}
	}
	if (len(g.DeliveredTrackingNumbers) > 0) == g.DeliveredAt.IsZero() {
		return errors.New("deliveredAt is set exactly when a package was delivered")
	}
	return nil
}

func checkReasons(g GroupPromise) error {
	for _, r := range []string{g.CancelReason, g.CancelRefusedReason} {
		if r != "" && !shared.ValidTextN(r, MaxReason) {
			return errors.New("a cancellation reason is invalid")
		}
	}
	if g.AssignmentID != "" && !shared.ValidID(g.AssignmentID) || g.NodeID != "" && !shared.ValidID(g.NodeID) {
		return errors.New("assignmentId and nodeId must be valid ids")
	}
	return nil
}

// checkStatusFacts verifies the timestamps each group state requires or forbids.
func checkStatusFacts(g GroupPromise) error {
	if err := checkOutcome(g); err != nil || g.closedWithoutDelivery() {
		return err
	}
	switch g.Status {
	case GroupPromised:
		return forbid(g.Status, "locked/shipped/cancel facts", !g.LockedAt.IsZero() || !g.ShippedAt.IsZero() || !g.CancelRequestedAt.IsZero() ||
			!g.CancelledAt.IsZero() || g.CancelRefusedReason != "")
	case GroupLocked:
		return forbid(g.Status, "shipped/cancelled facts", !g.ShippedAt.IsZero() || !g.CancelledAt.IsZero())
	case GroupShipped:
		return requireShipped(g, false)
	case GroupDelivered:
		return requireShipped(g, true)
	case GroupCancellationRequested:
		return requireRequested(g)
	}
	return requireCancelled(g)
}

func requireRequested(g GroupPromise) error {
	if g.CancelRequestedAt.IsZero() {
		return errors.New("CANCELLATION_REQUESTED needs cancelRequestedAt")
	}
	return forbid(g.Status, "shipped/cancelled facts", !g.ShippedAt.IsZero() || !g.CancelledAt.IsZero())
}

func requireCancelled(g GroupPromise) error {
	if g.CancelledAt.IsZero() {
		return errors.New("CANCELLED needs cancelledAt")
	}
	return forbid(g.Status, "shipped facts", !g.ShippedAt.IsZero())
}

func forbid(s GroupStatus, what string, bad bool) error {
	if bad {
		return fmt.Errorf("a %s group has no %s", s, what)
	}
	return nil
}

func requireShipped(g GroupPromise, delivered bool) error {
	if g.ShippedAt.IsZero() || !g.CancelledAt.IsZero() {
		return fmt.Errorf("a %s group needs shippedAt and has no cancelledAt", g.Status)
	}
	allDelivered := len(g.TrackingNumbers) > 0
	for _, tn := range g.TrackingNumbers {
		allDelivered = allDelivered && slices.Contains(g.DeliveredTrackingNumbers, tn)
	}
	if allDelivered != delivered {
		return fmt.Errorf("a %s group has all of its packages delivered exactly when it is DELIVERED", g.Status)
	}
	return nil
}

// checkOutcome verifies the terminal-outcome facts: a LOST group has lostAt, a
// RETURNED group has returnedAt, both name a package the group announced and
// did not deliver, it shipped and was not cancelled, and no other group carries
// any of them.
func checkOutcome(g GroupPromise) error {
	lost, returned := g.Status == GroupLost, g.Status == GroupReturned
	if !lost && !returned {
		return forbid(g.Status, "lost/returned facts", g.OutcomeTrackingNumber != "" || !g.LostAt.IsZero() || !g.ReturnedAt.IsZero())
	}
	if lost == g.LostAt.IsZero() || returned == g.ReturnedAt.IsZero() {
		return fmt.Errorf("a %s group has exactly its own lostAt or returnedAt", g.Status)
	}
	if g.ShippedAt.IsZero() || !g.CancelledAt.IsZero() {
		return fmt.Errorf("a %s group needs shippedAt and has no cancelledAt", g.Status)
	}
	if !slices.Contains(g.TrackingNumbers, g.OutcomeTrackingNumber) || slices.Contains(g.DeliveredTrackingNumbers, g.OutcomeTrackingNumber) {
		return fmt.Errorf("a %s group names an announced package that was not delivered", g.Status)
	}
	return nil
}
