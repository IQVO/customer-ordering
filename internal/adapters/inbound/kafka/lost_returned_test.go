package kafka_test

import (
	"testing"

	kafkaconsumer "github.com/claudioed/customer-ordering/internal/adapters/inbound/kafka"
)

// A19: PackageLost and PackageReturned close the group that announced the package.

func shipped(id string, group int, tns ...string) []byte {
	return event(id, kafkaconsumer.TypeFulfillmentShipped, map[string]any{"assignmentId": "as-" + string(rune('0'+group)), "orderId": orderID, "groupNo": group, "trackingNumbers": tns})
}

func lost(id, tn string, extra map[string]any) []byte {
	data := map[string]any{"trackingNumber": tn, "orderId": orderID, "declaredAt": "2026-10-27T13:00:00Z"}
	for k, v := range extra {
		data[k] = v
	}
	return event(id, kafkaconsumer.TypePackageLost, data)
}

func returned(id, tn string) []byte {
	return event(id, kafkaconsumer.TypePackageReturned, map[string]any{"trackingNumber": tn, "orderId": orderID, "returnedAt": "2026-10-26T09:00:00Z"})
}

func apply(t *testing.T, c *kafkaconsumer.Consumer, values ...[]byte) {
	t.Helper()
	for _, v := range values {
		if err := handle(t, c, v); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPackageLostAndReturnedCloseTheirGroupsAndTheOrder(t *testing.T) {
	w := newWorld(t)
	apply(t, w.sourcing(), shipped("e1", 1, "TRK-1"), shipped("e2", 2, "TRK-2"))
	if got := w.status(t); got != "SHIPPED" {
		t.Fatalf("status = %s", got)
	}
	apply(t, w.shipping(), lost("l1", "TRK-1", map[string]any{"carrierNote": "tolerated unknown field"}))
	if w.groupStatus(t, 1) != "LOST" || w.status(t) != "SHIPPED" {
		t.Fatalf("group 1 %s order %s", w.groupStatus(t, 1), w.status(t))
	}
	apply(t, w.shipping(), returned("r1", "TRK-2"))
	if w.groupStatus(t, 2) != "RETURNED" || w.status(t) != "LOST" {
		t.Fatalf("group 2 %s order %s", w.groupStatus(t, 2), w.status(t))
	}
	for _, id := range []string{"l1", "r1"} {
		if !w.claimed(t, id) {
			t.Fatalf("%s must be recorded in the inbox", id)
		}
	}
}

func TestPackageLostIsIdempotentOnTheEventID(t *testing.T) {
	w := newWorld(t)
	apply(t, w.sourcing(), shipped("e1", 1, "TRK-1"))
	apply(t, w.shipping(), lost("same", "TRK-1", nil), lost("same", "TRK-1", nil), lost("same", "TRK-1", nil))
	o, err := w.store.Orders().Get(t.Context(), orderID)
	if err != nil || o.LoadedVersion() != 3 { // placed (1), shipped (2), lost (3)
		t.Fatalf("version %d err %v", o.LoadedVersion(), err)
	}
	// A second event id for the same fact changes nothing either.
	apply(t, w.shipping(), lost("other", "TRK-1", nil))
	if o, _ = w.store.Orders().Get(t.Context(), orderID); o.LoadedVersion() != 3 || !w.claimed(t, "other") {
		t.Fatalf("a repeat of the same loss must be recorded and skipped, version %d", o.LoadedVersion())
	}
}

func TestALossReportedBeforeTheShipmentIsReplayedWhenTheShipmentArrives(t *testing.T) {
	w := newWorld(t)
	apply(t, w.shipping(), lost("l1", "TRK-1", nil))
	if w.groupStatus(t, 1) != "PROMISED" {
		t.Fatalf("group 1 = %s: nothing is known about the package yet", w.groupStatus(t, 1))
	}
	apply(t, w.sourcing(), shipped("e1", 1, "TRK-1"))
	if w.groupStatus(t, 1) != "LOST" {
		t.Fatalf("group 1 = %s, the recorded loss must settle the group on shipping", w.groupStatus(t, 1))
	}
}

func TestADeliveryBeforeTheShipmentWinsOverALossOfTheSamePackage(t *testing.T) {
	w := newWorld(t)
	apply(t, w.shipping(),
		event("d1", kafkaconsumer.TypePackageDelivered, map[string]any{"trackingNumber": "TRK-1", "orderId": orderID, "deliveredAt": "2026-10-26T09:00:00Z"}),
		lost("l1", "TRK-1", nil))
	apply(t, w.sourcing(), shipped("e1", 1, "TRK-1"))
	if got := w.groupStatus(t, 1); got != "DELIVERED" {
		t.Fatalf("group 1 = %s, the loss is skipped", got)
	}
}

func TestTolerantDecodingOfLostAndReturned(t *testing.T) {
	w := newWorld(t)
	apply(t, w.sourcing(), shipped("e1", 1, "TRK-1"), shipped("e2", 2, "TRK-2"))
	apply(t, w.shipping(),
		// no declaredAt: the event's own time stands in; extra fields are ignored
		event("l1", kafkaconsumer.TypePackageLost, map[string]any{"trackingNumber": "TRK-1", "orderId": orderID, "future": map[string]any{"a": 1}}),
		event("r1", kafkaconsumer.TypePackageReturned, map[string]any{"trackingNumber": "TRK-2", "orderId": orderID, "reason": "REFUSED"}))
	o, err := w.store.Orders().Get(t.Context(), orderID)
	if err != nil {
		t.Fatal(err)
	}
	if g := o.Groups(); g[0].LostAt.IsZero() || !g[0].LostAt.Equal(t0.Add(3600e9)) || g[1].ReturnedAt.IsZero() {
		t.Fatalf("the instants must come from the event time when the payload has none: %+v", g)
	}
}

func TestLostAndReturnedForUnknownOrdersOrPackagesAreSkipped(t *testing.T) {
	w := newWorld(t)
	apply(t, w.sourcing(), shipped("e1", 1, "TRK-1"))
	apply(t, w.shipping(),
		event("l1", kafkaconsumer.TypePackageLost, map[string]any{"trackingNumber": "TRK-1", "orderId": "order-unknown"}),
		lost("l2", "TRK-unknown", nil))
	if w.groupStatus(t, 1) != "SHIPPED" {
		t.Fatalf("group 1 = %s", w.groupStatus(t, 1))
	}
	for _, id := range []string{"l1", "l2"} {
		if !w.claimed(t, id) {
			t.Fatalf("%s must be recorded and skipped (A7)", id)
		}
	}
}

func TestLostAndReturnedNeedTheirKeys(t *testing.T) {
	w := newWorld(t)
	for name, c := range map[string][]byte{
		"lost without tracking number":     event("b1", kafkaconsumer.TypePackageLost, map[string]any{"orderId": orderID}),
		"lost without order":               event("b2", kafkaconsumer.TypePackageLost, map[string]any{"trackingNumber": "TRK-1"}),
		"returned without tracking number": event("b3", kafkaconsumer.TypePackageReturned, map[string]any{"orderId": orderID}),
		"returned without order":           event("b4", kafkaconsumer.TypePackageReturned, map[string]any{"trackingNumber": "TRK-1"}),
		"lost with a bad instant":          event("b5", kafkaconsumer.TypePackageLost, map[string]any{"trackingNumber": "TRK-1", "orderId": orderID, "declaredAt": "yesterday"}),
	} {
		if err := handle(t, w.shipping(), c); err == nil {
			t.Fatalf("%s must surface to the loop so it is dead-lettered", name)
		}
	}
}
