package kafka_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/kafka"
	"github.com/claudioed/customer-ordering/internal/application/outbox"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
)

var update = flag.Bool("update", false, "rewrite the golden files")

var t0 = time.Date(2026, 10, 23, 10, 15, 30, 123456000, time.UTC)

func placedOrder(t *testing.T) (*customerorder.CustomerOrder, []shared.Event) {
	t.Helper()
	group := func(no int, promise string, tier customerorder.Tier, lines ...int) customerorder.GroupInput {
		return customerorder.GroupInput{
			GroupNo: no, PromiseID: promise, Tier: tier, WitnessNodeID: "fc-aus-1", ShipBy: t0.Add(12 * time.Hour),
			DeliverByEarliest: t0.Add(24 * time.Hour), DeliverByLatest: t0.Add(36 * time.Hour), LineNos: lines, DestinationPostalPrefix: "787",
		}
	}
	o, evs, err := customerorder.Place(customerorder.NewOrder{
		ID: "7a1e6c52-0d2b-4f0e-9d6a-5c6f1b8e2a10", ShopperID: "3b0c1f0e-6f0a-4a39-8a55-2b7f0d8c9e11",
		ShipTo: shipto.ShipTo{Name: "Ana Example", Line1: "1 Sample Street", City: "Austin", Region: "TX", PostalCode: "78701", CountryCode: "US", Phone: "+1 555 0100"},
		Lines:  []customerorder.OrderLine{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2}, {LineNo: 2, CatalogItemID: "sku-200", Quantity: 1}, {LineNo: 3, CatalogItemID: "sku-300", Quantity: 5}},
		Groups: []customerorder.GroupInput{group(1, "p-1001", customerorder.TierNextDay, 1, 2), group(2, "p-1002", customerorder.TierStandard, 3)},
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	return o, evs
}

func encodeOne(t *testing.T, ev shared.Event) outbox.Message {
	t.Helper()
	enc := &kafka.Encoder{NewID: func() string { return "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c40" }}
	msgs, err := enc.Encode(ev)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("Encode: %v (%d messages)", err, len(msgs))
	}
	return msgs[0]
}

// assertGolden compares the CloudEvents bytes with the golden file EXACTLY.
func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden.json")
	if *update {
		if err := os.WriteFile(path, append(got, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got)+"\n" != string(want) {
		t.Fatalf("%s differs from its golden file:\n got %s\nwant %s", name, got, want)
	}
}

func TestCustomerOrderPlacedGolden(t *testing.T) {
	_, evs := placedOrder(t)
	msg := encodeOne(t, evs[0])
	assertGolden(t, "CustomerOrderPlaced", msg.Value)
	assertEnvelope(t, msg, "CustomerOrderPlaced")
}

func TestCustomerOrderCancelledGolden(t *testing.T) {
	o, _ := placedOrder(t)
	evs, err := o.CancelOrder("changed my mind", t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	msg := encodeOne(t, evs[0])
	assertGolden(t, "CustomerOrderCancelled", msg.Value)
	assertEnvelope(t, msg, "CustomerOrderCancelled")
}

func assertEnvelope(t *testing.T, msg outbox.Message, name string) {
	t.Helper()
	wantType := "com.retailnetwork.commerce.customer-ordering.customerorder." + name
	if msg.EventType != wantType || msg.EventID != "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c40" || msg.Topic != "retailnetwork.customer-ordering.events" {
		t.Fatalf("message = %+v", msg)
	}
	if msg.Subject != "7a1e6c52-0d2b-4f0e-9d6a-5c6f1b8e2a10" || string(msg.Key) != msg.Subject {
		t.Fatalf("subject/key = %q %q", msg.Subject, msg.Key)
	}
	if msg.DataSchema != "urn:retailnetwork:customer-ordering:events:"+name+":v1" {
		t.Fatalf("dataschema = %q", msg.DataSchema)
	}
	if len(msg.Headers) != 1 || msg.Headers[0].Key != "content-type" || msg.Headers[0].Value != cloudevents.MediaType {
		t.Fatalf("headers = %+v", msg.Headers)
	}
	assertDecodes(t, msg, wantType)
}

func assertDecodes(t *testing.T, msg outbox.Message, wantType string) {
	t.Helper()
	e, err := cloudevents.Decode(msg.Value)
	if err != nil {
		t.Fatalf("the value must decode as a CloudEvent: %v", err)
	}
	if e.Type() != wantType || e.Source() != "/retailnetwork/customer-ordering" || e.Subject() != msg.Subject {
		t.Fatalf("envelope = %v", e)
	}
	if e.ID() != msg.EventID || e.DataSchema() != msg.DataSchema {
		t.Fatalf("id/dataschema = %v", e)
	}
}

func TestNoEventCarriesTheShipTo(t *testing.T) {
	o, evs := placedOrder(t)
	cancelled, err := o.CancelOrder("x", t0)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range append(evs, cancelled...) {
		raw := string(encodeOne(t, ev).Value)
		for _, pii := range []string{"Ana Example", "1 Sample Street", "Austin", "78701", "+1 555 0100", "line1", "postalCode"} {
			if strings.Contains(raw, pii) {
				t.Fatalf("%s leaks %q: %s", ev.EventName(), pii, raw)
			}
		}
	}
}

func TestEncodeMintsAnIDPerEventAndKeepsOrder(t *testing.T) {
	o, evs := placedOrder(t)
	cancelled, err := o.CancelOrder("x", t0)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := kafka.NewEncoder().Encode(append(evs, cancelled...)...)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("Encode: %v (%d)", err, len(msgs))
	}
	if msgs[0].EventID == msgs[1].EventID || msgs[0].EventID == "" || !strings.HasSuffix(msgs[0].EventType, ".CustomerOrderPlaced") || !strings.HasSuffix(msgs[1].EventType, ".CustomerOrderCancelled") {
		t.Fatalf("messages = %+v", msgs)
	}
	var data map[string]any
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(msgs[1].Value, &env); err != nil {
		t.Fatal(err)
	}
	data = env.Data
	if nos, ok := data["groupNos"].([]any); !ok || len(nos) != 2 {
		t.Fatalf("groupNos must be an array: %v", data)
	}
}

type otherEvent struct{ shared.Header }

func (otherEvent) EventName() string { return "Other" }
func (otherEvent) Entity() string    { return "other" }
func (otherEvent) SubjectID() string { return "x" }

func TestEncodeRefusesAnUnpublishedEvent(t *testing.T) {
	if _, err := kafka.NewEncoder().Encode(otherEvent{}); err == nil {
		t.Fatal("an event the service does not publish is a programming error")
	}
	if _, err := (&kafka.Encoder{}).Encode(otherEvent{}); err == nil {
		t.Fatal("the zero Encoder still refuses it")
	}
	msgs, err := (&kafka.Encoder{}).Encode()
	if err != nil || len(msgs) != 0 {
		t.Fatalf("no events encode to nothing: %v %v", msgs, err)
	}
}

func TestEncodeUsesTheZeroEncoderWithUUIDs(t *testing.T) {
	_, evs := placedOrder(t)
	msgs, err := (&kafka.Encoder{}).Encode(evs...)
	if err != nil || len(msgs) != 1 || len(msgs[0].EventID) != 36 {
		t.Fatalf("Encode: %v %+v", err, msgs)
	}
}

func TestHeadersConvertToKafkaHeaders(t *testing.T) {
	got := kafka.Headers([]outbox.Header{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}})
	if len(got) != 2 || got[0].Key != "a" || string(got[0].Value) != "1" || got[1].Key != "b" || string(got[1].Value) != "2" {
		t.Fatalf("headers = %+v", got)
	}
}

func TestEncoderTopicOverride(t *testing.T) {
	_, evs := placedOrder(t)
	msgs, err := (&kafka.Encoder{Topic: "custom.topic"}).Encode(evs...)
	if err != nil || msgs[0].Topic != "custom.topic" {
		t.Fatalf("Encode: %v %+v", err, msgs)
	}
}
