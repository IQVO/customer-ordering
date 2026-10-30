package kafka_test

import (
	"context"
	"testing"

	kafkaconsumer "github.com/claudioed/customer-ordering/internal/adapters/inbound/kafka"
)

func handle(t *testing.T, c *kafkaconsumer.Consumer, value []byte) error {
	t.Helper()
	return c.HandleMessage(context.Background(), value)
}

func TestSourcingEventsDriveTheOrderStatus(t *testing.T) {
	w := newWorld(t)
	c := w.sourcing()
	steps := []struct {
		name  string
		value []byte
		want  string
	}{
		{"lock group 1", locked("e1", 1), "IN_FULFILLMENT"},
		{"lock group 2", locked("e2", 2), "IN_FULFILLMENT"},
		{"ship group 1", event("e3", kafkaconsumer.TypeFulfillmentShipped, map[string]any{"assignmentId": "as-1", "orderId": orderID, "groupNo": 1, "trackingNumbers": []string{"TRK-1"}}), "PARTIALLY_SHIPPED"},
		{"ship group 2", event("e4", kafkaconsumer.TypeFulfillmentShipped, map[string]any{"assignmentId": "as-2", "orderId": orderID, "groupNo": 2, "trackingNumbers": []string{"TRK-2"}}), "SHIPPED"},
	}
	for _, s := range steps {
		if err := handle(t, c, s.value); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if got := w.status(t); got != s.want {
			t.Fatalf("%s: status %s, want %s", s.name, got, s.want)
		}
	}
}

func TestTheSameCloudEventIDIsAppliedOnce(t *testing.T) {
	w := newWorld(t)
	c := w.sourcing()
	for range 3 {
		if err := handle(t, c, locked("same", 1)); err != nil {
			t.Fatal(err)
		}
	}
	o, err := w.store.Orders().Get(context.Background(), orderID)
	if err != nil || o.LoadedVersion() != 2 { // placed (1) + one lock (2); the redeliveries changed nothing
		t.Fatalf("version %d err %v", o.LoadedVersion(), err)
	}
	if !w.claimed(t, "same") {
		t.Fatal("the id must be claimed after the first delivery")
	}
}

func TestCancellationOutcomesAreApplied(t *testing.T) {
	w := newWorld(t)
	c := w.sourcing()
	if err := handle(t, c, event("e1", kafkaconsumer.TypeFulfillmentCancelRefuse, map[string]any{"orderId": orderID, "groupNo": 1, "reason": "VENDOR_ACKNOWLEDGED"})); err != nil {
		t.Fatal(err)
	}
	if err := handle(t, c, event("e2", kafkaconsumer.TypeFulfillmentUnsourceable, map[string]any{"orderId": orderID, "groupNo": 2, "reason": "NO_STOCK"})); err != nil {
		t.Fatal(err)
	}
	if err := handle(t, c, event("e3", kafkaconsumer.TypeFulfillmentCancelled, map[string]any{"orderId": orderID, "groupNo": 2})); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"e1", "e2", "e3"} {
		if !w.claimed(t, id) {
			t.Fatalf("%s must be recorded even when the effect is skipped (A7)", id)
		}
	}
}

func TestPromiseAndTrackingEventsAreRecorded(t *testing.T) {
	w := newWorld(t)
	promise := &kafkaconsumer.Consumer{Name: "p", Topic: kafkaconsumer.DeliveryPromiseTopic, Handlers: kafkaconsumer.DeliveryPromiseHandlers(w.consumer()), Logger: testLogger(), DLQ: &fakeDLQWriter{}, DeadLetterInvalid: true}
	shipping := &kafkaconsumer.Consumer{Name: "s", Topic: kafkaconsumer.ShippingAndTrackingTopic, Handlers: kafkaconsumer.ShippingAndTrackingHandlers(w.consumer()), Logger: testLogger(), DLQ: &fakeDLQWriter{}, DeadLetterInvalid: true}
	p := func(name string) string { return "com.retailnetwork.commerce.delivery-promise.promise." + name }
	s := func(kind, name string) string {
		return "com.retailnetwork.logistics.shipping-and-tracking." + kind + "." + name
	}
	for _, c := range []struct {
		consumer *kafkaconsumer.Consumer
		id, typ  string
		data     map[string]any
	}{
		{promise, "p1", p("PromiseAtRisk"), map[string]any{"promiseId": "p-1", "orderId": orderID, "reason": "NODE_DELAY"}},
		{promise, "p2", p("PromiseRevised"), map[string]any{"promiseId": "p-1", "orderId": orderID, "revisionNo": 1, "deliverByEarliest": "2026-10-25T10:00:00Z", "deliverByLatest": "2026-10-25T20:00:00Z"}},
		{promise, "p3", p("PromiseKept"), map[string]any{"promiseId": "p-2", "orderId": orderID}},
		{promise, "p4", p("PromiseBroken"), map[string]any{"promiseId": "p-1", "orderId": orderID, "lateByMinutes": 90}},
		{promise, "p5", p("PromiseUnkeepable"), map[string]any{"promiseId": "p-2", "orderId": orderID, "groupNo": 2, "reason": "NO_FEASIBLE_PATH"}},
		{shipping, "s1", s("label", "LabelIssued"), map[string]any{"trackingNumber": "TRK-9", "assignmentId": "as-1", "carrier": "CARRIER-A"}},
		{shipping, "s2", s("journey", "PackageScanned"), map[string]any{"trackingNumber": "TRK-9", "nodeId": "hub-1", "state": "IN_TRANSIT"}},
		{shipping, "s3", s("journey", "PackageExceptionRaised"), map[string]any{"trackingNumber": "TRK-9", "orderId": orderID, "reason": "DAMAGED"}},
		{shipping, "s4", s("journey", "PackageDelivered"), map[string]any{"trackingNumber": "TRK-9", "orderId": orderID}},
		{shipping, "s5", s("label", "LabelVoided"), map[string]any{"trackingNumber": "TRK-9"}},
	} {
		if err := handle(t, c.consumer, event(c.id, c.typ, c.data)); err != nil {
			t.Fatalf("%s %s: %v", c.id, c.typ, err)
		}
		if !w.claimed(t, c.id) {
			t.Fatalf("%s not claimed", c.id)
		}
	}
	if got := w.status(t); got != "PLACED" && got != "CANCELLED" {
		t.Fatalf("status = %s", got)
	}
}

func TestDeterministicProblemsAreSkippedOrDeadLettered(t *testing.T) {
	w := newWorld(t)
	c := w.sourcing()
	before := w.uow.count()
	for name, value := range map[string][]byte{
		"not json":           []byte("{"),
		"flat envelope":      []byte(`{"eventType":"FulfillmentLocked","orderId":"order-1"}`),
		"unknown type":       event("u1", "com.retailnetwork.commerce.order-sourcing.assignment.SomethingElse", map[string]any{}),
		"other service type": event("u2", "com.warehouse.wms.inventory-storage.stock.StockRelocated", map[string]any{}),
	} {
		if err := handle(t, c, value); err != nil {
			t.Fatalf("%s must be skipped, got %v", name, err)
		}
	}
	if w.uow.count() != before {
		t.Fatal("a skipped message must not open a unit of work")
	}
	for name, data := range map[string]map[string]any{
		"missing orderId":   {"assignmentId": "as-1", "groupNo": 1},
		"missing group":     {"assignmentId": "as-1", "orderId": orderID},
		"wrong field type":  {"assignmentId": "as-1", "orderId": orderID, "groupNo": "one"},
		"overlong order id": {"assignmentId": "as-1", "orderId": string(make([]byte, 300)), "groupNo": 1},
	} {
		err := handle(t, c, event("bad-"+name, kafkaconsumer.TypeFulfillmentLocked, data))
		if err == nil {
			t.Fatalf("%s: a poison payload must surface to the loop so it is dead-lettered", name)
		}
	}
}

func TestPayloadValidationPerType(t *testing.T) {
	w := newWorld(t)
	all := map[string]*kafkaconsumer.Consumer{
		"p": {Name: "p", Handlers: kafkaconsumer.DeliveryPromiseHandlers(w.consumer()), Logger: testLogger(), DLQ: &fakeDLQWriter{}, DeadLetterInvalid: true},
		"o": {Name: "o", Handlers: kafkaconsumer.OrderSourcingHandlers(w.consumer()), Logger: testLogger(), DLQ: &fakeDLQWriter{}, DeadLetterInvalid: true},
		"s": {Name: "s", Handlers: kafkaconsumer.ShippingAndTrackingHandlers(w.consumer()), Logger: testLogger(), DLQ: &fakeDLQWriter{}, DeadLetterInvalid: true},
	}
	bad := []struct{ c, typ string }{
		{"p", kafkaconsumer.TypePromiseAtRisk}, {"p", kafkaconsumer.TypePromiseRevised}, {"p", kafkaconsumer.TypePromiseKept},
		{"p", kafkaconsumer.TypePromiseBroken}, {"p", kafkaconsumer.TypePromiseUnkeepable},
		{"o", kafkaconsumer.TypeFulfillmentLocked}, {"o", kafkaconsumer.TypeFulfillmentShipped}, {"o", kafkaconsumer.TypeFulfillmentUnsourceable},
		{"o", kafkaconsumer.TypeFulfillmentCancelled}, {"o", kafkaconsumer.TypeFulfillmentCancelRefuse},
		{"s", kafkaconsumer.TypeLabelIssued}, {"s", kafkaconsumer.TypeLabelVoided}, {"s", kafkaconsumer.TypePackageScanned},
		{"s", kafkaconsumer.TypePackageDelivered}, {"s", kafkaconsumer.TypePackageException},
		{"s", kafkaconsumer.TypePackageLost}, {"s", kafkaconsumer.TypePackageReturned},
	}
	for _, b := range bad {
		if err := handle(t, all[b.c], event("empty-"+b.typ, b.typ, map[string]any{})); err == nil {
			t.Fatalf("%s with an empty payload must be refused", b.typ)
		}
	}
	tooMany := make([]string, 501)
	if err := handle(t, all["o"], event("many", kafkaconsumer.TypeFulfillmentShipped, map[string]any{"assignmentId": "a", "orderId": orderID, "groupNo": 1, "trackingNumbers": tooMany})); err == nil {
		t.Fatal("501 tracking numbers must be refused")
	}
	if err := handle(t, all["p"], event("rev0", kafkaconsumer.TypePromiseRevised, map[string]any{"promiseId": "p-1", "orderId": orderID, "revisionNo": 0, "deliverByEarliest": "2026-10-25T10:00:00Z", "deliverByLatest": "2026-10-25T20:00:00Z"})); err == nil {
		t.Fatal("revisionNo 0 must be refused")
	}
}

func TestConsumedTypeStringsAreTheProducersContracts(t *testing.T) {
	for typ, want := range map[string]string{
		kafkaconsumer.TypePromiseAtRisk:           "com.retailnetwork.commerce.delivery-promise.promise.PromiseAtRisk",
		kafkaconsumer.TypePromiseRevised:          "com.retailnetwork.commerce.delivery-promise.promise.PromiseRevised",
		kafkaconsumer.TypePromiseKept:             "com.retailnetwork.commerce.delivery-promise.promise.PromiseKept",
		kafkaconsumer.TypePromiseBroken:           "com.retailnetwork.commerce.delivery-promise.promise.PromiseBroken",
		kafkaconsumer.TypePromiseUnkeepable:       "com.retailnetwork.commerce.delivery-promise.promise.PromiseUnkeepable",
		kafkaconsumer.TypeFulfillmentLocked:       "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentLocked",
		kafkaconsumer.TypeFulfillmentShipped:      "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentShipped",
		kafkaconsumer.TypeFulfillmentUnsourceable: "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentUnsourceable",
		kafkaconsumer.TypeFulfillmentCancelled:    "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentCancelled",
		kafkaconsumer.TypeFulfillmentCancelRefuse: "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentCancelRefused",
		kafkaconsumer.TypeLabelIssued:             "com.retailnetwork.logistics.shipping-and-tracking.label.LabelIssued",
		kafkaconsumer.TypeLabelVoided:             "com.retailnetwork.logistics.shipping-and-tracking.label.LabelVoided",
		kafkaconsumer.TypePackageScanned:          "com.retailnetwork.logistics.shipping-and-tracking.journey.PackageScanned",
		kafkaconsumer.TypePackageDelivered:        "com.retailnetwork.logistics.shipping-and-tracking.journey.PackageDelivered",
		kafkaconsumer.TypePackageException:        "com.retailnetwork.logistics.shipping-and-tracking.journey.PackageExceptionRaised",
		kafkaconsumer.TypePackageLost:             "com.retailnetwork.logistics.shipping-and-tracking.journey.PackageLost",
		kafkaconsumer.TypePackageReturned:         "com.retailnetwork.logistics.shipping-and-tracking.journey.PackageReturned",
	} {
		if typ != want {
			t.Fatalf("type %q, want %q", typ, want)
		}
	}
}

func TestConstructorsBuildConsumersWithoutDialling(t *testing.T) {
	w := newWorld(t)
	for _, c := range []*kafkaconsumer.Consumer{
		kafkaconsumer.NewDeliveryPromiseConsumer([]string{"localhost:1"}, "g1", w.consumer(), testLogger()),
		kafkaconsumer.NewOrderSourcingConsumer([]string{"localhost:1"}, "g2", w.consumer(), testLogger()),
		kafkaconsumer.NewShippingAndTrackingConsumer([]string{"localhost:1"}, "g3", w.consumer(), testLogger()),
	} {
		if c.Reader == nil || c.DLQ == nil || !c.DeadLetterInvalid || c.Topic == "" {
			t.Fatalf("consumer = %+v", c)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// PromiseRevised carries the window's optional timeZone (delivery-promise ADR
// 0011): a named zone is stored on the group; an absent or malformed one keeps
// the group's own and the revision still applies (ADR 0009).
func TestPromiseRevisedCarriesTheWindowTimeZone(t *testing.T) {
	w := newWorld(t)
	c := &kafkaconsumer.Consumer{Name: "p", Topic: kafkaconsumer.DeliveryPromiseTopic, Handlers: kafkaconsumer.DeliveryPromiseHandlers(w.consumer()), Logger: testLogger(), DLQ: &fakeDLQWriter{}, DeadLetterInvalid: true}
	revised := func(id string, rev int, zone any) []byte {
		data := map[string]any{"promiseId": "p-1", "orderId": orderID, "revisionNo": rev, "deliverByEarliest": "2026-10-31T14:00:00Z", "deliverByLatest": "2026-11-01T02:00:00Z"}
		if zone != nil {
			data["timeZone"] = zone
		}
		return event(id, kafkaconsumer.TypePromiseRevised, data)
	}
	group := func() (zone string, rev int) {
		o, err := w.store.Orders().Get(context.Background(), orderID)
		if err != nil {
			t.Fatal(err)
		}
		return o.Groups()[0].TimeZone, o.Groups()[0].RevisionNo
	}
	for _, s := range []struct {
		id       string
		rev      int
		zone     any
		wantZone string
	}{
		{"tz1", 1, "America/Chicago", "America/Chicago"},
		{"tz2", 2, "not a zone", "America/Chicago"},
		{"tz3", 3, nil, "America/Chicago"},
		{"tz4", 4, "America/Denver", "America/Denver"},
	} {
		if err := handle(t, c, revised(s.id, s.rev, s.zone)); err != nil {
			t.Fatalf("%s: %v", s.id, err)
		}
		if zone, rev := group(); zone != s.wantZone || rev != s.rev {
			t.Fatalf("%s: group zone %q revision %d, want %q %d", s.id, zone, rev, s.wantZone, s.rev)
		}
	}
}
