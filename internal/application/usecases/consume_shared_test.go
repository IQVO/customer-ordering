package usecases_test

import (
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
)

// ADR 0012: one package (one tracking number) carries the goods of several
// groups that order-sourcing merged. Every member group raises its own
// FulfillmentLocked and FulfillmentShipped with the SAME tracking number;
// shipping-and-tracking raises the package events once, under the carrier's
// assignment and order.

const tnShared = "TRK-SH"

// sharedFx places two orders of one shopper; group 1 of the first is the
// carrier, group 1 of the second the follower.
type sharedFx struct {
	*fx
	carrier, follower string
}

func newSharedFx(t *testing.T) sharedFx {
	t.Helper()
	f := newFx(t)
	return sharedFx{fx: f, carrier: f.place(), follower: f.place()}
}

func (s sharedFx) lockBoth(t *testing.T) {
	t.Helper()
	lock(t, s.fx, s.carrier, 1)
	lock(t, s.fx, s.follower, 1)
}

func (s sharedFx) ship(t *testing.T, orderID string) {
	t.Helper()
	must(t, s.consumer().ApplyFulfillmentShipped(bg(), s.meta("sourcing.Shipped"), usecases.FulfillmentShipped{
		AssignmentID: "as-" + orderID + "1", OrderID: orderID, GroupNo: 1, TrackingNumbers: []string{tnShared}, ShippedAt: at(20),
	}))
}

// delivered is PackageDelivered as shipping-and-tracking raises it: the carrier's ids.
func (s sharedFx) delivered(t *testing.T, minute int) {
	t.Helper()
	must(t, s.consumer().ApplyPackageDelivered(bg(), s.meta("shipping.Delivered"), usecases.PackageDelivered{
		TrackingNumber: tnShared, AssignmentID: "as-" + s.carrier + "1", OrderID: s.carrier, DeliveredAt: at(minute),
	}))
}

func (s sharedFx) wantBoth(t *testing.T, want customerorder.GroupStatus) {
	t.Helper()
	for _, id := range []string{s.carrier, s.follower} {
		if got := s.groupStatus(id, 1); got != want {
			t.Fatalf("group 1 of %s = %s, want %s", id, got, want)
		}
	}
}

func TestOnePackageDeliveredClosesEveryGroupThatNamedIt(t *testing.T) {
	s := newSharedFx(t)
	s.lockBoth(t)
	s.ship(t, s.carrier)
	s.ship(t, s.follower)
	s.wantBoth(t, customerorder.GroupShipped)
	s.delivered(t, 60)
	s.wantBoth(t, customerorder.GroupDelivered)
	if g := s.order(s.follower).Groups()[0]; len(g.DeliveredTrackingNumbers) != 1 || !g.DeliveredAt.Equal(at(60)) {
		t.Fatalf("the follower's delivery facts = %+v", g)
	}
	// The other groups of both orders are untouched.
	for _, id := range []string{s.carrier, s.follower} {
		if got := s.groupStatus(id, 2); got != customerorder.GroupPromised {
			t.Fatalf("group 2 of %s = %s, want PROMISED", id, got)
		}
	}
}

func TestADeliveryBeforeTheFollowerShippedIsReplayedForIt(t *testing.T) {
	s := newSharedFx(t)
	s.lockBoth(t)
	s.ship(t, s.carrier)
	s.delivered(t, 60)
	if got := s.groupStatus(s.carrier, 1); got != customerorder.GroupDelivered {
		t.Fatalf("carrier = %s", got)
	}
	if got := s.groupStatus(s.follower, 1); got != customerorder.GroupLocked {
		t.Fatalf("the follower has not shipped yet: %s", got)
	}
	s.ship(t, s.follower)
	if got := s.groupStatus(s.follower, 1); got != customerorder.GroupDelivered {
		t.Fatalf("the follower's shipment must replay the delivery, got %s", got)
	}
}

func TestTwoGroupsOfOneOrderSharingAPackageAreBothDelivered(t *testing.T) {
	f := newFx(t)
	id := f.place()
	lock(t, f, id, 1)
	lock(t, f, id, 2)
	c := f.consumer()
	for _, g := range []int{1, 2} {
		must(t, c.ApplyFulfillmentShipped(bg(), f.meta("sourcing.Shipped"), usecases.FulfillmentShipped{
			AssignmentID: "as-" + id + string(rune('0'+g)), OrderID: id, GroupNo: g, TrackingNumbers: []string{tnShared}, ShippedAt: at(20),
		}))
	}
	must(t, c.ApplyPackageDelivered(bg(), f.meta("shipping.Delivered"), usecases.PackageDelivered{TrackingNumber: tnShared, AssignmentID: "as-" + id + "1", OrderID: id, DeliveredAt: at(50)}))
	for _, g := range []int{1, 2} {
		if got := f.groupStatus(id, g); got != customerorder.GroupDelivered {
			t.Fatalf("group %d = %s", g, got)
		}
	}
	if got := f.order(id).Status(); got != customerorder.StatusDelivered {
		t.Fatalf("order = %s", got)
	}
}

func TestOnePackageLostOrReturnedClosesEveryGroupThatNamedIt(t *testing.T) {
	cases := []struct {
		name string
		want customerorder.GroupStatus
		run  func(s sharedFx) error
	}{
		{"lost", customerorder.GroupLost, func(s sharedFx) error {
			return s.consumer().ApplyPackageLost(bg(), s.meta("shipping.Lost"), usecases.PackageLost{TrackingNumber: tnShared, OrderID: s.carrier, DeclaredAt: at(70)})
		}},
		{"returned", customerorder.GroupReturned, func(s sharedFx) error {
			return s.consumer().ApplyPackageReturned(bg(), s.meta("shipping.Returned"), usecases.PackageReturned{TrackingNumber: tnShared, OrderID: s.carrier, ReturnedAt: at(70)})
		}},
	}
	for _, c := range cases {
		s := newSharedFx(t)
		s.lockBoth(t)
		s.ship(t, s.carrier)
		s.ship(t, s.follower)
		must(t, c.run(s))
		s.wantBoth(t, c.want)
		if g := s.order(s.follower).Groups()[0]; g.OutcomeTrackingNumber != tnShared {
			t.Fatalf("%s: the follower's outcome package = %q", c.name, g.OutcomeTrackingNumber)
		}
	}
}

func TestAnOutcomeBeforeTheFollowerShippedIsReplayedForIt(t *testing.T) {
	s := newSharedFx(t)
	s.lockBoth(t)
	s.ship(t, s.carrier)
	must(t, s.consumer().ApplyPackageLost(bg(), s.meta("shipping.Lost"), usecases.PackageLost{TrackingNumber: tnShared, OrderID: s.carrier, DeclaredAt: at(70)}))
	if got := s.groupStatus(s.carrier, 1); got != customerorder.GroupLost {
		t.Fatalf("carrier = %s", got)
	}
	s.ship(t, s.follower)
	if got := s.groupStatus(s.follower, 1); got != customerorder.GroupLost {
		t.Fatalf("the follower's shipment must replay the loss, got %s", got)
	}
}

func TestAGroupThatCannotTakeTheDeliveryIsSkippedWithoutAnError(t *testing.T) {
	s := newSharedFx(t)
	s.lockBoth(t)
	s.ship(t, s.carrier)
	s.ship(t, s.follower)
	// The package was declared lost first: both groups are LOST.
	must(t, s.consumer().ApplyPackageLost(bg(), s.meta("shipping.Lost"), usecases.PackageLost{TrackingNumber: tnShared, OrderID: s.carrier, DeclaredAt: at(41)}))
	// A delivery of the same package now is illegal for a LOST group: it is
	// skipped for each group, without an error and without stopping the rest.
	s.delivered(t, 60)
	s.wantBoth(t, customerorder.GroupLost)
}

func TestTheFollowersTrackingViewListsTheSharedPackageUnderItsOwnGroup(t *testing.T) {
	s := newSharedFx(t)
	s.lockBoth(t)
	s.ship(t, s.carrier)
	s.ship(t, s.follower)
	s.delivered(t, 60)
	v, err := usecases.GetOrderTracking{Deps: s.deps}.Execute(bg(), customerorder.ID(s.follower))
	must(t, err)
	pk := v.Groups[0].Packages
	if len(pk) != 1 || pk[0].TrackingNumber != tnShared || pk[0].State != "DELIVERED" {
		t.Fatalf("follower group 1 packages = %+v", pk)
	}
	if len(v.Groups[1].Packages) != 0 {
		t.Fatalf("group 2 has no package: %+v", v.Groups[1].Packages)
	}
}

func TestAReplayedSharedDeliveryIsANoOp(t *testing.T) {
	s := newSharedFx(t)
	s.lockBoth(t)
	s.ship(t, s.carrier)
	s.ship(t, s.follower)
	s.delivered(t, 60)
	before := [2]int64{s.order(s.carrier).Version(), s.order(s.follower).Version()}
	s.delivered(t, 61) // a second event for the same package (other CloudEvents id)
	if after := [2]int64{s.order(s.carrier).Version(), s.order(s.follower).Version()}; after != before {
		t.Fatalf("a repeated delivery changed the orders: %v -> %v", before, after)
	}
}

func TestAGroupThatCannotTakeTheDeliveryDoesNotStopTheOthers(t *testing.T) {
	s := newSharedFx(t)
	s.lockBoth(t)
	// The carrier's group ships two packages; its other one is lost first, which
	// closes the carrier group as LOST (A19).
	must(t, s.consumer().ApplyFulfillmentShipped(bg(), s.meta("sourcing.Shipped"), usecases.FulfillmentShipped{
		AssignmentID: "as-" + s.carrier + "1", OrderID: s.carrier, GroupNo: 1, TrackingNumbers: []string{tnShared, "TRK-X"}, ShippedAt: at(20),
	}))
	s.ship(t, s.follower)
	must(t, s.consumer().ApplyPackageLost(bg(), s.meta("shipping.Lost"), usecases.PackageLost{TrackingNumber: "TRK-X", OrderID: s.carrier, DeclaredAt: at(40)}))
	if got := s.groupStatus(s.carrier, 1); got != customerorder.GroupLost {
		t.Fatalf("carrier = %s", got)
	}
	// The shared package is delivered: illegal for the LOST carrier group (skipped),
	// but the follower, listed after it, is still delivered.
	s.delivered(t, 60)
	if got := s.groupStatus(s.carrier, 1); got != customerorder.GroupLost {
		t.Fatalf("carrier = %s, want LOST kept", got)
	}
	if got := s.groupStatus(s.follower, 1); got != customerorder.GroupDelivered {
		t.Fatalf("follower = %s, want DELIVERED", got)
	}
}
