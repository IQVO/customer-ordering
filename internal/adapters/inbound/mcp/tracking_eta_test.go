package mcp_test

import (
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
)

// The MCP tracking output mirrors the REST package: the day-of window is
// present while the package is out for delivery and absent otherwise (ADR 0014).
func TestGetOrderTrackingCarriesTheEtaWindowWhileOutForDelivery(t *testing.T) {
	h := newHarness(t)
	h.seedOrder(t, "ord-1")
	h.seedTracking(t, "ord-1")
	tr := h.store.Tracking()
	must(t, tr.ApplyScan(t.Context(), readmodel.Scan{TrackingNumber: "TRK-100", NodeID: "ds-1", State: "OUT_FOR_DELIVERY", At: at(900)}))
	must(t, tr.ApplyEta(t.Context(), readmodel.Eta{TrackingNumber: "TRK-100", Start: at(3600), End: at(10800), AssignedAt: at(900)}))
	pkg := h.firstPackage(t, "ord-1")
	if pkg["etaStart"] != "2026-10-24T11:00:00Z" || pkg["etaEnd"] != "2026-10-24T13:00:00Z" {
		t.Fatalf("package = %v", pkg)
	}
	must(t, tr.ApplyScan(t.Context(), readmodel.Scan{TrackingNumber: "TRK-100", NodeID: "ds-1", State: "AT_STATION", At: at(20000)}))
	pkg = h.firstPackage(t, "ord-1")
	if _, ok := pkg["etaStart"]; ok {
		t.Fatalf("package = %v", pkg)
	}
}

func (h *harness) firstPackage(t *testing.T, orderID string) map[string]any {
	t.Helper()
	got := h.ok(t, "get_order_tracking", map[string]any{"orderId": orderID})
	return got["groups"].([]any)[0].(map[string]any)["packages"].([]any)[0].(map[string]any)
}
