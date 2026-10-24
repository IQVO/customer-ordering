package mcp_test

import (
	"testing"
)

func TestGetOrder(t *testing.T) {
	h := newHarness(t)
	h.seedOrder(t, "ord-1")
	got := h.ok(t, "get_order", map[string]any{"orderId": "ord-1"})
	for k, v := range map[string]any{
		"orderId": "ord-1", "shopperId": "shopper-1", "status": "PLACED",
		"destinationPostalPrefix": "787", "placedAt": "2026-10-24T10:00:00Z",
	} {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if _, ok := got["cancelledAt"]; ok {
		t.Errorf("cancelledAt must be absent on a placed order: %v", got)
	}
	lines, _ := got["lines"].([]any)
	if len(lines) != 2 || lines[0].(map[string]any)["catalogItemId"] != "sku-100" || lines[0].(map[string]any)["quantity"] != 2.0 {
		t.Fatalf("lines = %v, want the two cart lines", got["lines"])
	}
	groups, _ := got["groups"].([]any)
	if len(groups) != 2 {
		t.Fatalf("groups = %v, want 2", got["groups"])
	}
	first := groups[0].(map[string]any)
	for k, v := range map[string]any{
		"groupNo": 1.0, "promiseId": "p-ord-1-1", "tier": "NEXT_DAY",
		"shipBy": "2026-10-24T10:10:00Z", "deliverByEarliest": "2026-10-24T10:20:00Z",
		"deliverByLatest": "2026-10-24T10:25:00Z", "status": "PROMISED",
	} {
		if first[k] != v {
			t.Errorf("group1 %s = %v, want %v", k, first[k], v)
		}
	}
	if nos := first["lineNos"].([]any); len(nos) != 1 || nos[0] != 1.0 {
		t.Errorf("group1 lineNos = %v, want [1]", first["lineNos"])
	}
}

func TestGetOrderErrors(t *testing.T) {
	h := newHarness(t)
	failWith(t, h.session, "get_order", map[string]any{"orderId": "ord-nope"}, "not-found")
	// The REST adapter's id guard: an id that cannot name an order is a miss.
	failWith(t, h.session, "get_order", map[string]any{"orderId": ""}, "not-found")
	failWith(t, h.session, "get_order", map[string]any{"orderId": "ord-0123456789012345678901234567890123456789012345678901234567890-x"}, "not-found")
}

func TestGetOrderTracking(t *testing.T) {
	h := newHarness(t)
	h.seedOrder(t, "ord-1")
	h.seedTracking(t, "ord-1")
	got := h.ok(t, "get_order_tracking", map[string]any{"orderId": "ord-1"})
	if got["orderId"] != "ord-1" || got["status"] != "PLACED" {
		t.Fatalf("tracking = %v", got)
	}
	groups, _ := got["groups"].([]any)
	if len(groups) != 2 {
		t.Fatalf("groups = %v, want 2", got["groups"])
	}
	first := groups[0].(map[string]any)
	for k, v := range map[string]any{
		"groupNo": 1.0, "promiseId": "p-ord-1-1", "status": "PROMISED",
		"deliverByEarliest": "2026-10-24T10:20:00Z", "deliverByLatest": "2026-10-24T10:25:00Z",
		"promiseVerdict": "AT_RISK", "lateByMinutes": 42.0,
	} {
		if first[k] != v {
			t.Errorf("group1 %s = %v, want %v", k, first[k], v)
		}
	}
	pkgs := first["packages"].([]any)
	if len(pkgs) != 1 {
		t.Fatalf("group1 packages = %v, want 1", first["packages"])
	}
	pkg := pkgs[0].(map[string]any)
	if pkg["trackingNumber"] != "TRK-100" || pkg["carrier"] != "CARRIER-1" || pkg["state"] != "IN_TRANSIT" ||
		pkg["lastScanNodeId"] != "fc-aus-1" || pkg["lastScanAt"] != "2026-10-24T10:13:20Z" {
		t.Fatalf("package = %v", pkg)
	}
	// Group 2 has no package and no signal: the arrays stay arrays, the facts stay absent.
	second := groups[1].(map[string]any)
	if len(second["packages"].([]any)) != 0 {
		t.Errorf("group2 packages = %v, want []", second["packages"])
	}
	for _, absent := range []string{"promiseVerdict", "lateByMinutes", "lockedAt", "shippedAt", "lostAt", "returnedAt"} {
		if _, ok := second[absent]; ok {
			t.Errorf("group2 %s must be absent without a signal: %v", absent, second)
		}
	}
}

func TestGetOrderTrackingWithoutSignals(t *testing.T) {
	h := newHarness(t)
	h.seedOrder(t, "ord-1")
	got := h.ok(t, "get_order_tracking", map[string]any{"orderId": "ord-1"})
	groups := got["groups"].([]any)
	if len(groups) != 2 || len(groups[0].(map[string]any)["packages"].([]any)) != 0 {
		t.Fatalf("a freshly placed order must track with empty package lists: %v", got)
	}
}

func TestGetOrderTrackingErrors(t *testing.T) {
	h := newHarness(t)
	failWith(t, h.session, "get_order_tracking", map[string]any{"orderId": "ord-nope"}, "not-found")
	failWith(t, h.session, "get_order_tracking", map[string]any{"orderId": "this-id-is-far-longer-than-the-sixty-four-characters-an-order-id-may-ever-carry"}, "not-found")
}

func TestGetShipTo(t *testing.T) {
	h := newHarness(t)
	h.seedOrder(t, "ord-1")
	got := h.ok(t, "get_ship_to", map[string]any{"orderId": "ord-1"})
	want := map[string]any{
		"name": "Ana Example", "line1": "1 Sample Street", "line2": "Apt 2", "city": "Austin",
		"region": "TX", "postalCode": "78701", "countryCode": "US", "phone": "+1 555 0100",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

func TestGetShipToErrors(t *testing.T) {
	h := newHarness(t)
	failWith(t, h.session, "get_ship_to", map[string]any{"orderId": "ord-nope"}, "not-found")
	failWith(t, h.session, "get_ship_to", map[string]any{"orderId": ""}, "not-found")
}

// The reads must leave no trace: nothing is appended to the outbox the write
// side owns.
func TestReadsEmitNothing(t *testing.T) {
	h := newHarness(t)
	h.seedOrder(t, "ord-1")
	before := len(h.store.Outbox().Events())
	h.ok(t, "get_order", map[string]any{"orderId": "ord-1"})
	h.ok(t, "get_order_tracking", map[string]any{"orderId": "ord-1"})
	h.ok(t, "get_ship_to", map[string]any{"orderId": "ord-1"})
	if after := len(h.store.Outbox().Events()); after != before {
		t.Fatalf("outbox events %d -> %d: a read tool emitted an event", before, after)
	}
}
