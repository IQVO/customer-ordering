package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/application/views"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
)

func TestCancelOrderRequestsAndPublishes(t *testing.T) {
	f := newFx(t)
	id := f.place()
	before := f.store.Counts().OutboxEvents
	v, err := usecases.CancelOrder{Deps: f.deps}.Execute(bg(), usecases.CancelOrderInput{OrderID: customerorder.ID(id), Reason: "changed my mind"})
	must(t, err)
	assertOrder(t, v, "PLACED")
	if v.Groups[0].Status != "CANCELLATION_REQUESTED" || v.Groups[1].Status != "CANCELLATION_REQUESTED" || v.Groups[0].CancelReason != "changed my mind" {
		t.Fatalf("groups = %+v", v.Groups)
	}
	events := f.store.Outbox().Events()
	if len(events) != before+1 || events[len(events)-1].EventName() != "CustomerOrderCancelled" {
		t.Fatalf("events = %+v", events)
	}
	// A repeat changes and publishes nothing.
	v2, err := usecases.CancelOrder{Deps: f.deps}.Execute(bg(), usecases.CancelOrderInput{OrderID: customerorder.ID(id), Reason: "again"})
	must(t, err)
	if v2.Groups[0].CancelReason != "changed my mind" || f.store.Counts().OutboxEvents != before+1 {
		t.Fatal("a repeated cancellation must change and publish nothing")
	}
}

func TestCancelOrderRefusals(t *testing.T) {
	f := newFx(t)
	uc := usecases.CancelOrder{Deps: f.deps}
	if _, err := uc.Execute(bg(), usecases.CancelOrderInput{OrderID: "order-nope", Reason: "x"}); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	id := f.place()
	c := f.consumer()
	must(t, c.ApplyFulfillmentLocked(bg(), f.meta("t"), usecases.FulfillmentLocked{AssignmentID: "as-1", OrderID: id, GroupNo: 1, NodeID: "fc"}))
	must(t, c.ApplyFulfillmentLocked(bg(), f.meta("t"), usecases.FulfillmentLocked{AssignmentID: "as-2", OrderID: id, GroupNo: 2, NodeID: "fc"}))
	before := f.store.Counts()
	if _, err := uc.Execute(bg(), usecases.CancelOrderInput{OrderID: customerorder.ID(id), Reason: "x"}); !errors.Is(err, customerorder.ErrCancelAfterLock) {
		t.Fatalf("want ErrCancelAfterLock, got %v", err)
	}
	if _, err := uc.Execute(bg(), usecases.CancelOrderInput{OrderID: customerorder.ID(id), Reason: ""}); !isValidation(err) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
	if f.store.Counts() != before {
		t.Fatal("a refused cancellation stores nothing")
	}
}

func TestCancelOrderRetriesAConcurrentSave(t *testing.T) {
	f := newFx(t)
	id := f.place()
	failing := &failingOrders{OrderRepository: f.store.Orders(), failures: 1}
	f.deps.Orders = failing
	_, err := usecases.CancelOrder{Deps: f.deps}.Execute(bg(), usecases.CancelOrderInput{OrderID: customerorder.ID(id), Reason: "x"})
	must(t, err)
	if failing.saves != 2 {
		t.Fatalf("saves = %d, want 2", failing.saves)
	}
}

func TestGetOrderAndShipTo(t *testing.T) {
	f := newFx(t)
	id := f.place()
	v, err := usecases.GetOrder{Deps: f.deps}.Execute(bg(), customerorder.ID(id))
	must(t, err)
	if v.OrderID != id || len(v.Groups) != 2 {
		t.Fatalf("view = %+v", v)
	}
	to, err := usecases.GetShipTo{Deps: f.deps}.Execute(bg(), customerorder.ID(id))
	must(t, err)
	if to.Name != "Ana Example" || to.Line1 != "1 Sample Street" || to.PostalCode != "78701" || to.CountryCode != "US" || to.City != "Austin" || to.Region != "TX" {
		t.Fatalf("ship-to = %+v", to)
	}
	if _, err := (usecases.GetOrder{Deps: f.deps}).Execute(bg(), "nope"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := (usecases.GetShipTo{Deps: f.deps}).Execute(bg(), "nope"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := (usecases.GetOrderTracking{Deps: f.deps}).Execute(bg(), "nope"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestGetOrderTrackingJoinsOrderAndReadModels(t *testing.T) {
	f := newFx(t)
	id := f.place()
	c := f.consumer()
	order := f.order(id)
	p1, _ := order.GroupNoByPromise(order.Groups()[0].PromiseID)
	if p1 != 1 {
		t.Fatalf("group of the first promise = %d", p1)
	}
	must(t, c.ApplyFulfillmentLocked(bg(), f.meta("t"), usecases.FulfillmentLocked{AssignmentID: "as-1", OrderID: id, GroupNo: 1, NodeID: "fc-aus-1", LockedAt: at(10)}))
	must(t, c.ApplyLabelIssued(bg(), f.meta("t"), usecases.LabelIssued{TrackingNumber: "TRK-1", AssignmentID: "as-1", Carrier: "NETWORK"}))
	must(t, c.ApplyPackageScanned(bg(), f.meta("t"), usecases.PackageScanned{TrackingNumber: "TRK-1", NodeID: "ds-1", State: "AT_STATION", At: at(50)}))
	must(t, c.ApplyPromiseAtRisk(bg(), f.meta("t"), usecases.PromiseAtRisk{PromiseID: order.Groups()[0].PromiseID, OrderID: id, Reason: "NODE_DELAYED"}))
	must(t, c.ApplyFulfillmentUnsourceable(bg(), f.meta("t"), usecases.FulfillmentUnsourceable{OrderID: id, GroupNo: 2, Reason: ""}))
	v, err := usecases.GetOrderTracking{Deps: f.deps}.Execute(bg(), customerorder.ID(id))
	must(t, err)
	if v.OrderID != id || v.Status != "IN_FULFILLMENT" || len(v.Groups) != 2 {
		t.Fatalf("tracking = %+v", v)
	}
	assertTrackedGroup1(t, v.Groups[0])
	assertTrackedGroup2(t, v.Groups[1])
}

func assertTrackedGroup1(t *testing.T, g views.TrackingGroup) {
	t.Helper()
	if g.GroupNo != 1 || g.Status != "LOCKED" || g.AtRiskReason != "NODE_DELAYED" || !g.LockedAt.Equal(at(10)) || len(g.Packages) != 1 {
		t.Fatalf("group 1 = %+v", g)
	}
	p := g.Packages[0]
	if p.TrackingNumber != "TRK-1" || p.Carrier != "NETWORK" || p.State != "AT_STATION" || p.LastScanNodeID != "ds-1" || !p.LastScanAt.Equal(at(50)) {
		t.Fatalf("package = %+v", p)
	}
}

func assertTrackedGroup2(t *testing.T, g views.TrackingGroup) {
	t.Helper()
	if g.UnsourceableReason != "UNSPECIFIED" || g.Packages == nil || len(g.Packages) != 0 || g.Status != "PROMISED" {
		t.Fatalf("group 2 = %+v", g)
	}
}

type brokenTracking struct{ ports.TrackingRepository }

func (brokenTracking) GroupSignals(context.Context, string) ([]readmodel.GroupSignal, error) {
	return nil, errDisk
}

type brokenPackages struct{ ports.TrackingRepository }

func (brokenPackages) Packages(context.Context, string) ([]readmodel.Package, error) {
	return nil, errDisk
}

func TestGetOrderTrackingPropagatesReadModelFailures(t *testing.T) {
	f := newFx(t)
	id := f.place()
	f.deps.Tracking = brokenTracking{f.store.Tracking()}
	if _, err := (usecases.GetOrderTracking{Deps: f.deps}).Execute(bg(), customerorder.ID(id)); !errors.Is(err, errDisk) {
		t.Fatalf("want the read-model error, got %v", err)
	}
	f.deps.Tracking = brokenPackages{f.store.Tracking()}
	if _, err := (usecases.GetOrderTracking{Deps: f.deps}).Execute(bg(), customerorder.ID(id)); !errors.Is(err, errDisk) {
		t.Fatalf("want the read-model error, got %v", err)
	}
}
