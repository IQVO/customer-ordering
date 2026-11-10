package kafka_test

import (
	"encoding/json"
	"strings"
	"testing"
)

const goldenKey = "9f2c6d0b1a7e4c3d8b5a0e6f1d2c3b4a5968778695a4b3c2d1e0f9a8b7c6d5e4"

func TestCustomerOrderPlacedWithKeyGolden(t *testing.T) {
	_, evs := placedOrderKeyed(t, goldenKey)
	msg := encodeOne(t, evs[0])
	assertGolden(t, "CustomerOrderPlaced.with-key", msg.Value)
	assertEnvelope(t, msg, "CustomerOrderPlaced")
}

func TestCustomerOrderPlacedOmitsAnUnderivedKey(t *testing.T) {
	_, evs := placedOrder(t)
	if raw := string(encodeOne(t, evs[0]).Value); strings.Contains(raw, "consolidationKey") {
		t.Fatalf("an order without a key must not carry the field: %s", raw)
	}
}

// TestPlacedEventStaysFreeOfAnyAddressField pins that adding the key did not
// open a door for the ship-to: the data object holds exactly the documented
// top-level fields, and the key is a bare 64-hex string.
func TestPlacedEventStaysFreeOfAnyAddressField(t *testing.T) {
	_, evs := placedOrderKeyed(t, goldenKey)
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(encodeOne(t, evs[0]).Value, &env); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"orderId": true, "shopperId": true, "destinationPostalPrefix": true, "placedAt": true, "groups": true, "consolidationKey": true}
	for field := range env.Data {
		if !want[field] {
			t.Errorf("CustomerOrderPlaced.data carries an undocumented field %q", field)
		}
	}
	if len(env.Data) != len(want) {
		t.Errorf("CustomerOrderPlaced.data has %d fields, want %d", len(env.Data), len(want))
	}
	if string(env.Data["consolidationKey"]) != `"`+goldenKey+`"` {
		t.Errorf("consolidationKey = %s", env.Data["consolidationKey"])
	}
}
