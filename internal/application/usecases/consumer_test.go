package usecases_test

import (
	"context"
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
)

func lock(t *testing.T, f *fx, id string, groupNo int) {
	t.Helper()
	must(t, f.consumer().ApplyFulfillmentLocked(bg(), f.meta("sourcing.Locked"), usecases.FulfillmentLocked{
		AssignmentID: "as-" + id + string(rune('0'+groupNo)), OrderID: id, GroupNo: groupNo, NodeID: "fc-aus-1", LockedAt: at(10),
	}))
}

func TestLockedLinksTheAssignmentAndMovesTheStatus(t *testing.T) {
	f := newFx(t)
	id := f.place()
	lock(t, f, id, 1)
	if got := f.groupStatus(id, 1); got != customerorder.GroupLocked {
		t.Fatalf("group 1 = %s", got)
	}
	if got := f.order(id).Status(); got != customerorder.StatusInFulfillment {
		t.Fatalf("order = %s", got)
	}
	l, err := f.store.Tracking().ResolveAssignment(bg(), "as-"+id+"1")
	must(t, err)
	if l.OrderID != id || l.GroupNo != 1 {
		t.Fatalf("link = %+v", l)
	}
	if f.store.Counts().OutboxEvents != 1 { // only CustomerOrderPlaced: a lock publishes nothing
		t.Fatalf("outbox = %d", f.store.Counts().OutboxEvents)
	}
}

func TestEveryConsumedEventIsAppliedOnce(t *testing.T) {
	f := newFx(t)
	id := f.place()
	c := f.consumer()
	m := f.meta("sourcing.Locked")
	in := usecases.FulfillmentLocked{AssignmentID: "as-1", OrderID: id, GroupNo: 1, NodeID: "fc"}
	must(t, c.ApplyFulfillmentLocked(bg(), m, in))
	version := f.order(id).Version()
	in.GroupNo = 2 // the same event id with another body must change nothing
	must(t, c.ApplyFulfillmentLocked(bg(), m, in))
	if f.order(id).Version() != version || f.groupStatus(id, 2) != customerorder.GroupPromised {
		t.Fatal("a redelivered event id must be ignored")
	}
}

func TestSkippedEventsAreRecordedNotFailed(t *testing.T) {
	f := newFx(t)
	id := f.place()
	c := f.consumer()
	cases := map[string]func() error{
		"unknown order": func() error {
			return c.ApplyFulfillmentLocked(bg(), f.meta("t"), usecases.FulfillmentLocked{AssignmentID: "as-9", OrderID: "order-nope", GroupNo: 1, NodeID: "fc"})
		},
		"unknown group": func() error {
			return c.ApplyFulfillmentLocked(bg(), f.meta("t"), usecases.FulfillmentLocked{AssignmentID: "as-9", OrderID: id, GroupNo: 9, NodeID: "fc"})
		},
		"cancelled for a shipped group": func() error {
			lock(t, f, id, 2)
			must(t, c.ApplyFulfillmentShipped(bg(), f.meta("t"), usecases.FulfillmentShipped{OrderID: id, GroupNo: 2, TrackingNumbers: []string{"TRK-2"}}))
			return c.ApplyFulfillmentCancelled(bg(), f.meta("t"), usecases.FulfillmentCancelled{OrderID: id, GroupNo: 2})
		},
		"refusal for an unrequested group": func() error {
			return c.ApplyFulfillmentCancelRefused(bg(), f.meta("t"), usecases.FulfillmentCancelRefused{OrderID: id, GroupNo: 1, Reason: "r"})
		},
		"unkeepable for a locked group": func() error {
			lock(t, f, id, 1)
			return c.ApplyPromiseUnkeepable(bg(), f.meta("t"), usecases.PromiseUnkeepable{OrderID: id, GroupNo: 1})
		},
		"revision for an unknown promise": func() error {
			return c.ApplyPromiseRevised(bg(), f.meta("t"), usecases.PromiseRevised{PromiseID: "nope", OrderID: id, RevisionNo: 1, DeliverByEarliest: at(1), DeliverByLatest: at(2)})
		},
		"at risk for an unknown promise": func() error {
			return c.ApplyPromiseAtRisk(bg(), f.meta("t"), usecases.PromiseAtRisk{PromiseID: "nope", OrderID: id})
		},
		"verdict for an unknown order": func() error {
			return c.ApplyPromiseKept(bg(), f.meta("t"), usecases.PromiseKept{PromiseID: "p", OrderID: "order-nope"})
		},
	}
	for name, fn := range cases {
		if err := fn(); err != nil {
			t.Errorf("%s: a skippable event must not fail: %v", name, err)
		}
	}
}

func TestShippedThenDeliveredCompletesTheOrder(t *testing.T) {
	f := newFx(t)
	id := f.place()
	c := f.consumer()
	lock(t, f, id, 1)
	lock(t, f, id, 2)
	must(t, c.ApplyFulfillmentShipped(bg(), f.meta("t"), usecases.FulfillmentShipped{OrderID: id, GroupNo: 1, TrackingNumbers: []string{"TRK-1", "TRK-1b"}, ShippedAt: at(20)}))
	if got := f.order(id).Status(); got != customerorder.StatusPartiallyShipped {
		t.Fatalf("order = %s", got)
	}
	must(t, c.ApplyFulfillmentShipped(bg(), f.meta("t"), usecases.FulfillmentShipped{OrderID: id, GroupNo: 2, TrackingNumbers: []string{"TRK-2"}}))
	if got := f.order(id).Status(); got != customerorder.StatusShipped {
		t.Fatalf("order = %s", got)
	}
	deliver := func(tn string, minute int) {
		must(t, c.ApplyPackageDelivered(bg(), f.meta("t"), usecases.PackageDelivered{TrackingNumber: tn, OrderID: id, DeliveredAt: at(minute)}))
	}
	deliver("TRK-2", 60)
	if got := f.order(id).Status(); got != customerorder.StatusPartiallyDelivered {
		t.Fatalf("order = %s", got)
	}
	deliver("TRK-1", 61)
	if f.groupStatus(id, 1) != customerorder.GroupShipped {
		t.Fatal("group 1 needs both of its packages")
	}
	deliver("TRK-1b", 62)
	if got := f.order(id).Status(); got != customerorder.StatusDelivered {
		t.Fatalf("order = %s", got)
	}
}

func TestADeliveryBeforeTheLockOrShipmentIsReplayed(t *testing.T) {
	f := newFx(t)
	id := f.place()
	c := f.consumer()
	// Delivered before anything is known about the group: only remembered.
	must(t, c.ApplyPackageDelivered(bg(), f.meta("t"), usecases.PackageDelivered{TrackingNumber: "TRK-1", AssignmentID: "as-x", OrderID: id, DeliveredAt: at(70)}))
	if f.order(id).Version() != 1 {
		t.Fatal("the group is unknown: the order must not change")
	}
	// A delivery with neither order nor assignment is also only remembered.
	must(t, c.ApplyPackageDelivered(bg(), f.meta("t"), usecases.PackageDelivered{TrackingNumber: "TRK-lost", DeliveredAt: at(71)}))
	// The lock links the assignment; the shipment replays the delivery.
	must(t, c.ApplyFulfillmentLocked(bg(), f.meta("t"), usecases.FulfillmentLocked{AssignmentID: "as-x", OrderID: id, GroupNo: 1, NodeID: "fc"}))
	if f.groupStatus(id, 1) != customerorder.GroupLocked {
		t.Fatal("the lock must apply")
	}
	must(t, c.ApplyFulfillmentShipped(bg(), f.meta("t"), usecases.FulfillmentShipped{AssignmentID: "as-x", OrderID: id, GroupNo: 1, TrackingNumbers: []string{"TRK-1"}, ShippedAt: at(80)}))
	if f.groupStatus(id, 1) != customerorder.GroupDelivered {
		t.Fatalf("group 1 = %s, want the early delivery replayed", f.groupStatus(id, 1))
	}
}

func TestDeliveryResolvesTheGroupByTrackingNumberOrAssignment(t *testing.T) {
	f := newFx(t)
	id := f.place()
	c := f.consumer()
	must(t, c.ApplyFulfillmentShipped(bg(), f.meta("t"), usecases.FulfillmentShipped{OrderID: id, GroupNo: 2, TrackingNumbers: []string{"TRK-2"}, ShippedAt: at(20)}))
	// No assignment link exists: the order's own tracking numbers resolve the group.
	must(t, c.ApplyPackageDelivered(bg(), f.meta("t"), usecases.PackageDelivered{TrackingNumber: "TRK-2", OrderID: id, DeliveredAt: at(30)}))
	if f.groupStatus(id, 2) != customerorder.GroupDelivered {
		t.Fatalf("group 2 = %s", f.groupStatus(id, 2))
	}
	// The assignment recorded on the group resolves a package that is on no list.
	lock(t, f, id, 1)
	must(t, c.ApplyPackageDelivered(bg(), f.meta("t"), usecases.PackageDelivered{TrackingNumber: "TRK-x", AssignmentID: "as-" + id + "1", DeliveredAt: at(31)}))
	if g := f.order(id).Groups()[0]; len(g.DeliveredTrackingNumbers) != 1 || g.DeliveredTrackingNumbers[0] != "TRK-x" {
		t.Fatalf("group 1 = %+v", g)
	}
	// An order id without a group clue (no link, no list, no assignment) changes nothing.
	v := f.order(id).Version()
	must(t, c.ApplyPackageDelivered(bg(), f.meta("t"), usecases.PackageDelivered{TrackingNumber: "TRK-y", OrderID: id, DeliveredAt: at(32)}))
	if f.order(id).Version() != v {
		t.Fatal("an unresolvable delivery must not change the order")
	}
}

func TestCancellationOutcomes(t *testing.T) {
	f := newFx(t)
	id := f.place()
	c := f.consumer()
	_, err := usecases.CancelOrder{Deps: f.deps}.Execute(bg(), usecases.CancelOrderInput{OrderID: customerorder.ID(id), Reason: "changed my mind"})
	must(t, err)
	must(t, c.ApplyFulfillmentCancelRefused(bg(), f.meta("t"), usecases.FulfillmentCancelRefused{OrderID: id, GroupNo: 1, Reason: "VENDOR_ACKNOWLEDGED"}))
	if f.groupStatus(id, 1) != customerorder.GroupLocked || f.order(id).Groups()[0].CancelRefusedReason != "VENDOR_ACKNOWLEDGED" {
		t.Fatalf("group 1 = %+v", f.order(id).Groups()[0])
	}
	must(t, c.ApplyFulfillmentCancelled(bg(), f.meta("t"), usecases.FulfillmentCancelled{OrderID: id, GroupNo: 2}))
	if f.groupStatus(id, 2) != customerorder.GroupCancelled || f.order(id).Status() != customerorder.StatusInFulfillment {
		t.Fatalf("group 2 = %s order = %s", f.groupStatus(id, 2), f.order(id).Status())
	}
}

func TestUnkeepablePromiseCancelsTheGroupAndPublishes(t *testing.T) {
	f := newFx(t)
	id := f.place()
	c := f.consumer()
	order := f.order(id)
	must(t, c.ApplyPromiseUnkeepable(bg(), f.meta("t"), usecases.PromiseUnkeepable{PromiseID: order.Groups()[1].PromiseID, OrderID: id, GroupNo: 2, Reason: "NO_FEASIBLE_PATH"}))
	if f.groupStatus(id, 2) != customerorder.GroupCancelled {
		t.Fatalf("group 2 = %s", f.groupStatus(id, 2))
	}
	events := f.store.Outbox().Events()
	last, ok := events[len(events)-1].(customerorder.CustomerOrderCancelled)
	if !ok || last.Reason != "PROMISE_UNKEEPABLE: NO_FEASIBLE_PATH" || len(last.GroupNos) != 1 || last.GroupNos[0] != 2 {
		t.Fatalf("event = %+v", events[len(events)-1])
	}
	must(t, c.ApplyPromiseUnkeepable(bg(), f.meta("t"), usecases.PromiseUnkeepable{OrderID: id, GroupNo: 1, Reason: "x"}))
	if f.order(id).Status() != customerorder.StatusCancelled {
		t.Fatalf("order = %s, want CANCELLED when nothing remains", f.order(id).Status())
	}
}

func TestPromiseSignalsAndRevisions(t *testing.T) {
	f := newFx(t)
	id := f.place()
	c := f.consumer()
	p1 := f.order(id).Groups()[0].PromiseID
	p2 := f.order(id).Groups()[1].PromiseID
	must(t, c.ApplyPromiseRevised(bg(), f.meta("t"), usecases.PromiseRevised{
		PromiseID: p1, OrderID: id, RevisionNo: 1, DeliverByEarliest: at(2000), DeliverByLatest: at(2300), ShipBy: at(1900), NodeID: "fc-aus-2",
	}))
	g := f.order(id).Groups()[0]
	if g.RevisionNo != 1 || !g.DeliverByLatest.Equal(at(2300)) || !g.ShipBy.Equal(at(1900)) || g.WitnessNodeID != "fc-aus-2" {
		t.Fatalf("group = %+v", g)
	}
	// A revision without shipBy keeps the group's own.
	must(t, c.ApplyPromiseRevised(bg(), f.meta("t"), usecases.PromiseRevised{PromiseID: p2, OrderID: id, RevisionNo: 1, DeliverByEarliest: at(2000), DeliverByLatest: at(2300)}))
	if g2 := f.order(id).Groups()[1]; !g2.ShipBy.Equal(at(600)) || !g2.DeliverByLatest.Equal(at(2300)) {
		t.Fatalf("group 2 = %+v", g2)
	}
	late := 660
	must(t, c.ApplyPromiseBroken(bg(), f.meta("t"), usecases.PromiseBroken{PromiseID: p1, OrderID: id, LateByMinutes: &late}))
	must(t, c.ApplyPromiseKept(bg(), f.meta("t"), usecases.PromiseKept{PromiseID: p2, OrderID: id}))
	signals, err := f.store.Tracking().GroupSignals(bg(), id)
	must(t, err)
	if len(signals) != 2 || signals[0].PromiseVerdict != "BROKEN" || signals[0].LateByMinutes == nil || *signals[0].LateByMinutes != 660 || signals[1].PromiseVerdict != "KEPT" {
		t.Fatalf("signals = %+v", signals)
	}
}

func TestTrackingEventsFeedTheReadModel(t *testing.T) {
	f := newFx(t)
	id := f.place()
	c := f.consumer()
	lock(t, f, id, 1)
	as := "as-" + id + "1"
	must(t, c.ApplyLabelIssued(bg(), f.meta("t"), usecases.LabelIssued{TrackingNumber: "TRK-1", AssignmentID: as, Carrier: "NETWORK"}))
	must(t, c.ApplyLabelIssued(bg(), f.meta("t"), usecases.LabelIssued{TrackingNumber: "TRK-2", AssignmentID: as}))
	must(t, c.ApplyLabelVoided(bg(), f.meta("t"), usecases.LabelVoided{TrackingNumber: "TRK-2"}))
	must(t, c.ApplyPackageScanned(bg(), f.meta("t"), usecases.PackageScanned{TrackingNumber: "TRK-1", NodeID: "ds-1", State: "AT_STATION"}))
	must(t, c.ApplyPackageException(bg(), f.meta("t"), usecases.PackageExceptionRaised{TrackingNumber: "TRK-1", OrderID: id, Reason: ""}))
	pkgs, err := f.store.Tracking().Packages(bg(), id)
	must(t, err)
	if len(pkgs) != 2 {
		t.Fatalf("packages = %+v", pkgs)
	}
	p1, p2 := pkgs[0], pkgs[1]
	if p1.State != "AT_STATION" || p1.Carrier != "NETWORK" || p1.ExceptionReason != "UNSPECIFIED" || p1.LastScanAt.IsZero() || p1.GroupNo != 1 {
		t.Fatalf("TRK-1 = %+v", p1)
	}
	if p2.State != "VOIDED" {
		t.Fatalf("TRK-2 = %+v", p2)
	}
}

func TestAConcurrentSaveIsRetriedAndAFailureRollsTheClaimBack(t *testing.T) {
	f := newFx(t)
	id := f.place()
	failing := &failingOrders{OrderRepository: f.store.Orders(), failures: 2}
	f.deps.Orders = failing
	m := f.meta("t")
	must(t, f.consumer().ApplyFulfillmentLocked(bg(), m, usecases.FulfillmentLocked{AssignmentID: "as-1", OrderID: id, GroupNo: 1, NodeID: "fc"}))
	if failing.saves != 3 || f.groupStatus(id, 1) != customerorder.GroupLocked {
		t.Fatalf("saves = %d: the event must be retried until it applies", failing.saves)
	}
	g := newFx(t)
	id2 := g.place()
	always := &failingOrders{OrderRepository: g.store.Orders(), failures: 99}
	g.deps.Orders = always
	m2 := g.meta("t")
	err := g.consumer().ApplyFulfillmentLocked(bg(), m2, usecases.FulfillmentLocked{AssignmentID: "as-1", OrderID: id2, GroupNo: 1, NodeID: "fc"})
	if err == nil || always.saves != 5 {
		t.Fatalf("want the conflict after 5 attempts, got %v after %d", err, always.saves)
	}
	if first, _ := g.store.Inbox().Claim(bg(), m2.EventID, m2.Type); !first {
		t.Fatal("a failed event must leave its inbox claim free for the redelivery")
	}
}

type brokenInbox struct{ ports.Inbox }

func (brokenInbox) Claim(context.Context, string, string) (bool, error) { return false, errDisk }

type brokenLinks struct{ ports.TrackingRepository }

func (brokenLinks) LinkAssignment(context.Context, readmodel.AssignmentLink) error { return errDisk }
func (brokenLinks) ResolveAssignment(context.Context, string) (readmodel.AssignmentLink, error) {
	return readmodel.AssignmentLink{}, errDisk
}
func (brokenLinks) DeliveredPackages(context.Context, string, int) ([]readmodel.Delivery, error) {
	return nil, errDisk
}

func TestInfrastructureFailuresFailTheEvent(t *testing.T) {
	f := newFx(t)
	id := f.place()
	f.deps.Inbox = brokenInbox{f.store.Inbox()}
	if err := f.consumer().ApplyPromiseKept(bg(), f.meta("t"), usecases.PromiseKept{PromiseID: "p", OrderID: id}); err == nil {
		t.Fatal("an inbox failure must fail the event")
	}
	g := newFx(t)
	id = g.place()
	g.deps.Tracking = brokenLinks{g.store.Tracking()}
	if err := g.consumer().ApplyFulfillmentLocked(bg(), g.meta("t"), usecases.FulfillmentLocked{AssignmentID: "a", OrderID: id, GroupNo: 1, NodeID: "n"}); err == nil {
		t.Fatal("a read-model failure must fail the event")
	}
	if err := g.consumer().ApplyPackageDelivered(bg(), g.meta("t"), usecases.PackageDelivered{TrackingNumber: "T", AssignmentID: "a", OrderID: id}); err == nil {
		t.Fatal("a failing assignment lookup must fail the event")
	}
}
