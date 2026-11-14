package kafka_test

import (
	"testing"

	kafkaconsumer "github.com/claudioed/customer-ordering/internal/adapters/inbound/kafka"
)

// ADR 0016: a pickup package's label names the point; the deposit
// (PackageReadyForCollection) and the collection (PackageCollected) are facts
// of the tracking view, and neither changes the order.

func labelIssuedPickupEvent(id, tn, point string) []byte {
	return event(id, kafkaconsumer.TypeLabelIssued, map[string]any{
		"trackingNumber": tn, "assignmentId": "as-1", "carrier": "c-1", "pickupPointId": point,
	})
}

func readyEvent(id, tn, point, ready, hold string) []byte {
	return event(id, kafkaconsumer.TypePackageReadyForCollection, map[string]any{
		"trackingNumber": tn, "assignmentId": "as-1", "orderId": orderID, "pickupPointId": point,
		"readyForCollectionAt": ready, "holdUntil": hold,
	})
}

func collectedEvent(id, tn, point, at, proof string) []byte {
	return event(id, kafkaconsumer.TypePackageCollected, map[string]any{
		"trackingNumber": tn, "assignmentId": "as-1", "orderId": orderID, "pickupPointId": point,
		"collectedAt": at, "proofCode": proof,
	})
}

func TestAPickupPackageShowsItsPointAndCollection(t *testing.T) {
	w := newWorld(t)
	apply(t, w.sourcing(), locked("l1", 1), shipped("e1", 1, "TRK-1"))
	apply(t, w.shipping(), labelIssuedPickupEvent("lab", "TRK-1", "node-pnt-7"))
	p := w.trackedPackage(t)
	if p.DestinationKind != "PICKUP_POINT" || p.PickupPointID != "node-pnt-7" {
		t.Fatalf("the label names the point: %+v", p)
	}
	if p.PickupPoint != nil {
		t.Fatalf("before the deposit there are no collection facts: %+v", p.PickupPoint)
	}
	apply(t, w.shipping(),
		scanEvent("s1", "TRK-1", "OUT_FOR_DELIVERY", etaDay+"09:00:00Z"),
		readyEvent("r1", "TRK-1", "node-pnt-7", etaDay+"10:00:00Z", etaDay+"17:00:00Z"))
	p = w.trackedPackage(t)
	if p.State != "READY_FOR_COLLECTION" || p.PickupPoint == nil {
		t.Fatalf("the deposit makes the package collectable: %+v", p)
	}
	if p.PickupPoint.ReadyForCollectionAt.IsZero() || p.PickupPoint.HoldUntil.IsZero() {
		t.Fatalf("readyForCollectionAt and holdUntil must be shown: %+v", p.PickupPoint)
	}
	if !p.PickupPoint.CollectedAt.IsZero() {
		t.Fatalf("nothing was collected yet: %+v", p.PickupPoint)
	}
	apply(t, w.shipping(), collectedEvent("c1", "TRK-1", "node-pnt-7", etaDay+"16:30:00Z", "4821"))
	p = w.trackedPackage(t)
	if p.State != "COLLECTED" || p.PickupPoint.CollectedAt.IsZero() || p.PickupPoint.ProofCode != "4821" {
		t.Fatalf("collection is what the shopper sees next: %+v", p.PickupPoint)
	}
}

func TestAShipToPackageAnswersShipToAndNoPoint(t *testing.T) {
	w := newWorld(t)
	apply(t, w.sourcing(), locked("l1", 1), shipped("e1", 1, "TRK-1"))
	apply(t, w.shipping(),
		event("lab", kafkaconsumer.TypeLabelIssued, map[string]any{"trackingNumber": "TRK-1", "assignmentId": "as-1", "carrier": "c-1"}),
		scanEvent("s1", "TRK-1", "OUT_FOR_DELIVERY", etaDay+"09:00:00Z"))
	p := w.trackedPackage(t)
	if p.DestinationKind != "SHIP_TO" || p.PickupPointID != "" || p.PickupPoint != nil {
		t.Fatalf("a ship-to package carries no point: %+v", p)
	}
}

func TestCollectionEventsAreIdempotentOnTheEventID(t *testing.T) {
	w := newWorld(t)
	apply(t, w.sourcing(), locked("l1", 1), shipped("e1", 1, "TRK-1"))
	apply(t, w.shipping(), readyEvent("same", "TRK-1", "node-pnt-7", etaDay+"10:00:00Z", etaDay+"17:00:00Z"))
	apply(t, w.shipping(), readyEvent("same", "TRK-1", "node-pnt-9", etaDay+"11:00:00Z", etaDay+"18:00:00Z"))
	p := w.trackedPackage(t)
	if p.PickupPointID != "node-pnt-7" || p.PickupPoint == nil || !p.PickupPoint.HoldUntil.Equal(mustTime(t, etaDay+"17:00:00Z")) {
		t.Fatalf("a replayed event id must have no effect: %+v", p)
	}
	if !w.claimed(t, "same") {
		t.Fatal("the id must be claimed")
	}
}

func TestACollectionEventWithoutItsInstantIsRejected(t *testing.T) {
	w := newWorld(t)
	if err := handle(t, w.shipping(), event("bad", kafkaconsumer.TypePackageReadyForCollection, map[string]any{
		"trackingNumber": "TRK-1", "pickupPointId": "node-pnt-7", "readyForCollectionAt": etaDay + "10:00:00Z",
	})); err == nil {
		t.Fatal("a payload without holdUntil must surface to the loop so it is dead-lettered")
	}
	if err := handle(t, w.shipping(), event("bad2", kafkaconsumer.TypePackageCollected, map[string]any{
		"trackingNumber": "TRK-1", "pickupPointId": "node-pnt-7",
	})); err == nil {
		t.Fatal("a payload without collectedAt must surface to the loop so it is dead-lettered")
	}
	if w.claimed(t, "bad") || w.claimed(t, "bad2") {
		t.Fatal("a bad payload must not be claimed")
	}
}
