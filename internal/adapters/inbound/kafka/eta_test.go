package kafka_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	kafkaconsumer "github.com/claudioed/customer-ordering/internal/adapters/inbound/kafka"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/application/views"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
)

// ADR 0014: PackageEtaWindowAssigned is stored on the package row and the
// tracking view shows it under one rule.

const (
	etaDay   = "2026-10-26T"
	etaStart = etaDay + "13:00:00Z"
	etaEnd   = etaDay + "15:00:00Z"
)

func etaEvent(id, tn, assigned string, extra map[string]any) []byte {
	data := map[string]any{"trackingNumber": tn, "assignmentId": "as-1", "orderId": orderID, "etaStart": etaStart, "etaEnd": etaEnd, "assignedAt": assigned}
	for k, v := range extra {
		data[k] = v
	}
	return event(id, kafkaconsumer.TypePackageEtaWindowAssigned, data)
}

func scanEvent(id, tn, state, at string) []byte {
	return event(id, kafkaconsumer.TypePackageScanned, map[string]any{"trackingNumber": tn, "nodeId": "ds-1", "state": state, "at": at})
}

// trackedPackage returns the first package of group 1 as the tracking view shows it.
func (w *world) trackedPackage(t *testing.T) views.TrackingPackage {
	t.Helper()
	v, err := usecases.GetOrderTracking{Deps: w.deps}.Execute(context.Background(), orderID)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Groups[0].Packages) != 1 {
		t.Fatalf("group 1 packages = %+v", v.Groups[0].Packages)
	}
	return v.Groups[0].Packages[0]
}

func shownEta(p views.TrackingPackage) bool { return !p.EtaStart.IsZero() && !p.EtaEnd.IsZero() }

func TestEtaWindowIsShownWhileOutForDeliveryAndHiddenAfterwards(t *testing.T) {
	w := newWorld(t)
	apply(t, w.sourcing(), locked("l1", 1), shipped("e1", 1, "TRK-1"))
	apply(t, w.shipping(), scanEvent("s1", "TRK-1", "AT_STATION", etaDay+"07:00:00Z"))
	if p := w.trackedPackage(t); shownEta(p) {
		t.Fatalf("no window yet: %+v", p)
	}
	apply(t, w.shipping(), scanEvent("s2", "TRK-1", "OUT_FOR_DELIVERY", etaDay+"09:00:00Z"), etaEvent("w1", "TRK-1", etaDay+"09:00:00Z", nil))
	p := w.trackedPackage(t)
	if !shownEta(p) || !p.EtaStart.Equal(mustTime(t, etaStart)) || !p.EtaEnd.Equal(mustTime(t, etaEnd)) {
		t.Fatalf("window must be shown while out for delivery: %+v", p)
	}
	// A failed attempt sends the package back to the station: the window is hidden.
	apply(t, w.shipping(), scanEvent("s3", "TRK-1", "AT_STATION", etaDay+"16:00:00Z"))
	if p := w.trackedPackage(t); shownEta(p) {
		t.Fatalf("a failed attempt must hide the window: %+v", p)
	}
	// The next day's out-for-delivery scan is newer than yesterday's window: still hidden.
	apply(t, w.shipping(), scanEvent("s4", "TRK-1", "OUT_FOR_DELIVERY", "2026-10-27T09:00:00Z"))
	if p := w.trackedPackage(t); shownEta(p) {
		t.Fatalf("yesterday's window must stay hidden: %+v", p)
	}
	apply(t, w.shipping(), etaEvent("w2", "TRK-1", "2026-10-27T09:00:00Z", map[string]any{"etaStart": "2026-10-27T10:00:00Z", "etaEnd": "2026-10-27T12:00:00Z"}))
	if p := w.trackedPackage(t); !shownEta(p) || !p.EtaStart.Equal(mustTime(t, "2026-10-27T10:00:00Z")) {
		t.Fatalf("the new window must be shown: %+v", p)
	}
	apply(t, w.shipping(), event("d1", kafkaconsumer.TypePackageDelivered, map[string]any{"trackingNumber": "TRK-1", "assignmentId": "as-1", "orderId": orderID, "deliveredAt": "2026-10-27T11:00:00Z"}))
	if p := w.trackedPackage(t); shownEta(p) {
		t.Fatalf("a delivered package shows no window: %+v", p)
	}
}

func TestEtaWindowMayArriveBeforeTheScanAndTheShipment(t *testing.T) {
	w := newWorld(t)
	apply(t, w.shipping(), etaEvent("w1", "TRK-1", etaDay+"09:00:00Z", nil)) // before lock, shipment and scan
	apply(t, w.sourcing(), locked("l1", 1), shipped("e1", 1, "TRK-1"))
	if p := w.trackedPackage(t); shownEta(p) {
		t.Fatalf("hidden until the package is out for delivery: %+v", p)
	}
	apply(t, w.shipping(), scanEvent("s1", "TRK-1", "OUT_FOR_DELIVERY", etaDay+"09:00:00Z")) // equal instant: current
	if p := w.trackedPackage(t); !shownEta(p) {
		t.Fatalf("window must show once the scan agrees: %+v", p)
	}
}

func TestAnOlderEtaWindowIsIgnored(t *testing.T) {
	w := newWorld(t)
	apply(t, w.sourcing(), locked("l1", 1), shipped("e1", 1, "TRK-1"))
	apply(t, w.shipping(), scanEvent("s1", "TRK-1", "OUT_FOR_DELIVERY", etaDay+"08:00:00Z"),
		etaEvent("w1", "TRK-1", etaDay+"09:00:00Z", nil),
		etaEvent("w0", "TRK-1", etaDay+"08:30:00Z", map[string]any{"etaStart": etaDay + "09:00:00Z", "etaEnd": etaDay + "11:00:00Z"}))
	if p := w.trackedPackage(t); !p.EtaStart.Equal(mustTime(t, etaStart)) {
		t.Fatalf("the older window must not replace the newer one: %+v", p)
	}
}

func TestEtaWindowIsIdempotentOnTheEventID(t *testing.T) {
	w := newWorld(t)
	apply(t, w.sourcing(), locked("l1", 1), shipped("e1", 1, "TRK-1"))
	apply(t, w.shipping(), scanEvent("s1", "TRK-1", "OUT_FOR_DELIVERY", etaDay+"08:00:00Z"), etaEvent("same", "TRK-1", etaDay+"09:00:00Z", nil))
	// A redelivery of the same id with another window changes nothing.
	apply(t, w.shipping(), etaEvent("same", "TRK-1", etaDay+"10:00:00Z", map[string]any{"etaStart": etaDay + "16:00:00Z", "etaEnd": etaDay + "18:00:00Z"}))
	if p := w.trackedPackage(t); !p.EtaStart.Equal(mustTime(t, etaStart)) {
		t.Fatalf("a replayed event id must have no effect: %+v", p)
	}
	if !w.claimed(t, "same") {
		t.Fatal("the id must be claimed")
	}
}

func TestEtaWindowDecodingIsTolerantAndFallsBackToTheEventTime(t *testing.T) {
	w := newWorld(t)
	apply(t, w.sourcing(), locked("l1", 1), shipped("e1", 1, "TRK-1"))
	// No assignedAt (event time is t0+1h, before the scan below is NOT the case: the scan is older), unknown fields ignored.
	apply(t, w.shipping(), scanEvent("s1", "TRK-1", "OUT_FOR_DELIVERY", "2026-10-23T10:30:00Z"),
		event("w1", kafkaconsumer.TypePackageEtaWindowAssigned, map[string]any{
			"trackingNumber": "TRK-1", "etaStart": etaStart, "etaEnd": etaEnd, "confidence": 0.9, "stopNo": 3,
		}))
	if p := w.trackedPackage(t); !shownEta(p) {
		t.Fatalf("window must be stored with the event time as assignedAt: %+v", p)
	}
}

func TestEtaWindowBadPayloadsAreRefused(t *testing.T) {
	w := newWorld(t)
	c := w.shipping()
	for name, data := range map[string]map[string]any{
		"no tracking number": {"etaStart": etaStart, "etaEnd": etaEnd},
		"no window":          {"trackingNumber": "TRK-1"},
		"inverted window":    {"trackingNumber": "TRK-1", "etaStart": etaEnd, "etaEnd": etaStart},
		"empty window":       {"trackingNumber": "TRK-1", "etaStart": etaStart, "etaEnd": etaStart},
		"wrong field type":   {"trackingNumber": "TRK-1", "etaStart": "tomorrow", "etaEnd": etaEnd},
	} {
		if err := handle(t, c, event("bad-"+name, kafkaconsumer.TypePackageEtaWindowAssigned, data)); err == nil {
			t.Fatalf("%s: a payload that can never be applied must surface to be dead-lettered", name)
		}
	}
}

// The group's promise snapshot, status and verdict are byte-identical before
// and after a window event: the path never reads or writes them.
func TestEtaWindowLeavesThePromiseAndTheOrderUntouched(t *testing.T) {
	w := newWorld(t)
	apply(t, w.sourcing(), locked("l1", 1), shipped("e1", 1, "TRK-1"))
	apply(t, w.shipping(), scanEvent("s1", "TRK-1", "OUT_FOR_DELIVERY", etaDay+"08:00:00Z"))
	before := w.order(t)
	apply(t, w.shipping(), etaEvent("w1", "TRK-1", etaDay+"09:00:00Z", map[string]any{"etaStart": etaDay + "20:00:00Z", "etaEnd": etaDay + "22:00:00Z"}))
	after := w.order(t)
	if !reflect.DeepEqual(before.Groups(), after.Groups()) || before.Status() != after.Status() || before.LoadedVersion() != after.LoadedVersion() {
		t.Fatalf("the order changed:\nbefore %+v\nafter  %+v", before.Groups(), after.Groups())
	}
}

func (w *world) order(t *testing.T) *customerorder.CustomerOrder {
	t.Helper()
	o, err := w.store.Orders().Get(context.Background(), orderID)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
