package usecases_test

import (
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
)

// A19: PackageLost and PackageReturned as consumer use cases.

func shipBoth(t *testing.T, f *fx, id string) {
	t.Helper()
	c := f.consumer()
	must(t, c.ApplyFulfillmentShipped(bg(), f.meta("sourcing.Shipped"), usecases.FulfillmentShipped{OrderID: id, GroupNo: 1, TrackingNumbers: []string{"TRK-1", "TRK-1b"}, ShippedAt: at(20)}))
	must(t, c.ApplyFulfillmentShipped(bg(), f.meta("sourcing.Shipped"), usecases.FulfillmentShipped{OrderID: id, GroupNo: 2, TrackingNumbers: []string{"TRK-2"}, ShippedAt: at(21)}))
}

func TestPackageLostClosesTheGroupAndRecordsThePackage(t *testing.T) {
	f := newFx(t)
	id := f.place()
	shipBoth(t, f, id)
	must(t, f.consumer().ApplyPackageLost(bg(), f.meta("shipping.Lost"), usecases.PackageLost{TrackingNumber: "TRK-1b", OrderID: id, DeclaredAt: at(50)}))
	if got := f.groupStatus(id, 1); got != customerorder.GroupLost {
		t.Fatalf("group 1 = %s", got)
	}
	g := f.order(id).Groups()[0]
	if g.OutcomeTrackingNumber != "TRK-1b" || !g.LostAt.Equal(at(50)) {
		t.Fatalf("group = %+v", g)
	}
	pkgs, err := f.store.Tracking().Packages(bg(), id)
	must(t, err)
	var found bool
	for _, p := range pkgs {
		if p.TrackingNumber == "TRK-1b" {
			found = p.State == readmodel.OutcomeLost && p.Outcome == readmodel.OutcomeLost && p.GroupNo == 1
		}
	}
	if !found {
		t.Fatalf("the package row must show LOST: %+v", pkgs)
	}
	if f.store.Counts().OutboxEvents != 1 { // only CustomerOrderPlaced: an outcome publishes nothing
		t.Fatalf("outbox = %d", f.store.Counts().OutboxEvents)
	}
}

func TestPackageReturnedFallsBackToTheEventTime(t *testing.T) {
	f := newFx(t)
	id := f.place()
	shipBoth(t, f, id)
	m := f.meta("shipping.Returned")
	must(t, f.consumer().ApplyPackageReturned(bg(), m, usecases.PackageReturned{TrackingNumber: "TRK-2", OrderID: id}))
	if g := f.order(id).Groups()[1]; g.Status != customerorder.GroupReturned || !g.ReturnedAt.Equal(m.At) {
		t.Fatalf("group 2 = %+v, want returnedAt %v", g, m.At)
	}
}

func TestAnOutcomeBeforeTheShipmentIsReplayedAndAClosedGroupSkipsTheRest(t *testing.T) {
	f := newFx(t)
	id := f.place()
	c := f.consumer()
	must(t, c.ApplyPackageLost(bg(), f.meta("shipping.Lost"), usecases.PackageLost{TrackingNumber: "TRK-1", OrderID: id, DeclaredAt: at(50)}))
	must(t, c.ApplyPackageReturned(bg(), f.meta("shipping.Returned"), usecases.PackageReturned{TrackingNumber: "TRK-1b", OrderID: id, ReturnedAt: at(40)}))
	if got := f.groupStatus(id, 1); got != customerorder.GroupPromised {
		t.Fatalf("nothing is known of the shipment, group 1 = %s", got)
	}
	shipBoth(t, f, id)
	// the oldest outcome (the return at 40) closed the group; the later loss is skipped
	if g := f.order(id).Groups()[0]; g.Status != customerorder.GroupReturned || g.OutcomeTrackingNumber != "TRK-1b" {
		t.Fatalf("group 1 = %+v", g)
	}
}

func TestAnOutcomeForAnUnknownOrderOrPackageIsRecordedAndSkipped(t *testing.T) {
	f := newFx(t)
	id := f.place()
	shipBoth(t, f, id)
	c := f.consumer()
	must(t, c.ApplyPackageLost(bg(), f.meta("shipping.Lost"), usecases.PackageLost{TrackingNumber: "TRK-1", OrderID: "order-nope"}))
	must(t, c.ApplyPackageLost(bg(), f.meta("shipping.Lost"), usecases.PackageLost{TrackingNumber: "TRK-unknown", OrderID: id}))
	if got := f.groupStatus(id, 1); got != customerorder.GroupShipped {
		t.Fatalf("group 1 = %s", got)
	}
	if got := f.order(id).Status(); got != customerorder.StatusShipped {
		t.Fatalf("order = %s", got)
	}
}

func TestTheTrackingViewShowsTheOutcome(t *testing.T) {
	f := newFx(t)
	id := f.place()
	shipBoth(t, f, id)
	must(t, f.consumer().ApplyPackageLost(bg(), f.meta("shipping.Lost"), usecases.PackageLost{TrackingNumber: "TRK-1", OrderID: id, DeclaredAt: at(50)}))
	must(t, f.consumer().ApplyPackageReturned(bg(), f.meta("shipping.Returned"), usecases.PackageReturned{TrackingNumber: "TRK-2", OrderID: id, ReturnedAt: at(51)}))
	v, err := usecases.GetOrderTracking{Deps: f.deps}.Execute(bg(), customerorder.ID(id))
	must(t, err)
	if v.Status != "LOST" || v.Groups[0].Status != "LOST" || !v.Groups[0].LostAt.Equal(at(50)) || v.Groups[0].OutcomePackage != "TRK-1" ||
		v.Groups[1].Status != "RETURNED" || !v.Groups[1].ReturnedAt.Equal(at(51)) || !v.Groups[1].LostAt.IsZero() {
		t.Fatalf("tracking = %+v", v)
	}
	for _, p := range v.Groups[0].Packages {
		if p.TrackingNumber == "TRK-1" && p.State != "LOST" {
			t.Fatalf("package = %+v", p)
		}
	}
}
