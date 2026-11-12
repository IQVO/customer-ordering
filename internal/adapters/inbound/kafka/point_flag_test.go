package kafka_test

import (
	"testing"

	kafkaconsumer "github.com/claudioed/customer-ordering/internal/adapters/inbound/kafka"
)

// ADR 0015: PackageDeliveryPointFlagged stores the advisory on the package row;
// the tracking view carries it until the package ends.

const flagAt = etaDay + "16:30:00Z"

func pointFlagEvent(id, tn, reason, at string) []byte {
	return event(id, kafkaconsumer.TypePackageDeliveryPointFlagged, map[string]any{
		"trackingNumber": tn, "assignmentId": "as-1", "orderId": orderID,
		"reason": reason, "flaggedAt": at,
	})
}

func TestDeliveryPointAdvisoryIsShownUntilThePackageEnds(t *testing.T) {
	w := newWorld(t)
	apply(t, w.sourcing(), locked("l1", 1), shipped("e1", 1, "TRK-1"))
	apply(t, w.shipping(),
		scanEvent("s1", "TRK-1", "OUT_FOR_DELIVERY", etaDay+"09:00:00Z"),
		pointFlagEvent("f1", "TRK-1", "NOT_HOME", flagAt))
	p := w.trackedPackage(t)
	if p.DeliveryPointAdvisory == nil || p.DeliveryPointAdvisory.Reason != "NOT_HOME" {
		t.Fatalf("the advisory must be shown: %+v", p.DeliveryPointAdvisory)
	}
	if !p.DeliveryPointAdvisory.FlaggedAt.Equal(mustTime(t, flagAt)) {
		t.Fatalf("flaggedAt = %v", p.DeliveryPointAdvisory.FlaggedAt)
	}
	// A delivered package ends: the advisory disappears with it.
	apply(t, w.shipping(), event("d1", kafkaconsumer.TypePackageDelivered, map[string]any{"trackingNumber": "TRK-1", "assignmentId": "as-1", "orderId": orderID, "at": etaDay + "18:00:00Z"}))
	if p := w.trackedPackage(t); p.DeliveryPointAdvisory != nil {
		t.Fatalf("a delivered package carries no advisory: %+v", p.DeliveryPointAdvisory)
	}
}

func TestANewerPointFlagWinsAndAnOlderOneIsIgnored(t *testing.T) {
	w := newWorld(t)
	apply(t, w.sourcing(), locked("l1", 1), shipped("e1", 1, "TRK-1"))
	apply(t, w.shipping(), scanEvent("s1", "TRK-1", "OUT_FOR_DELIVERY", etaDay+"09:00:00Z"),
		pointFlagEvent("f1", "TRK-1", "NOT_HOME", etaDay+"10:00:00Z"),
		pointFlagEvent("f0", "TRK-1", "ACCESS_PROBLEM", etaDay+"09:30:00Z"))
	p := w.trackedPackage(t)
	if p.DeliveryPointAdvisory == nil || p.DeliveryPointAdvisory.Reason != "NOT_HOME" {
		t.Fatalf("the newer flag must win: %+v", p.DeliveryPointAdvisory)
	}
}

func TestAPointFlagWithAnUnknownReasonIsRejected(t *testing.T) {
	w := newWorld(t)
	apply(t, w.sourcing(), locked("l1", 1), shipped("e1", 1, "TRK-1"))
	apply(t, w.shipping(), scanEvent("s1", "TRK-1", "OUT_FOR_DELIVERY", etaDay+"09:00:00Z"))
	if err := handle(t, w.shipping(), pointFlagEvent("f1", "TRK-1", "SNOWSTORM", flagAt)); err == nil {
		t.Fatal("a reason outside the two published must surface to the loop so it is dead-lettered")
	}
	if p := w.trackedPackage(t); p.DeliveryPointAdvisory != nil {
		t.Fatalf("an unknown reason must not be stored: %+v", p.DeliveryPointAdvisory)
	}
	if w.claimed(t, "f1") {
		t.Fatal("a bad payload must not be claimed")
	}
}

func TestAPointFlagIsIdempotentOnTheEventID(t *testing.T) {
	w := newWorld(t)
	apply(t, w.sourcing(), locked("l1", 1), shipped("e1", 1, "TRK-1"))
	apply(t, w.shipping(), scanEvent("s1", "TRK-1", "OUT_FOR_DELIVERY", etaDay+"09:00:00Z"),
		pointFlagEvent("same", "TRK-1", "NOT_HOME", etaDay+"10:00:00Z"))
	apply(t, w.shipping(), pointFlagEvent("same", "TRK-1", "ACCESS_PROBLEM", etaDay+"11:00:00Z"))
	p := w.trackedPackage(t)
	if p.DeliveryPointAdvisory == nil || p.DeliveryPointAdvisory.Reason != "NOT_HOME" {
		t.Fatalf("a replayed event id must have no effect: %+v", p.DeliveryPointAdvisory)
	}
	if !w.claimed(t, "same") {
		t.Fatal("the id must be claimed")
	}
}
