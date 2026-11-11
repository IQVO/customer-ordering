package a2a

import (
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
)

// The A2A tracking answer mirrors the REST package: the day-of window is
// present while the package is out for delivery and absent otherwise (ADR 0014).
func TestWireGetOrderTrackingCarriesTheEtaWindowWhileOutForDelivery(t *testing.T) {
	store := seededStore(t)
	tr := store.Tracking()
	must(t, tr.ApplyScan(t.Context(), readmodel.Scan{TrackingNumber: "TRK-100", NodeID: "ds-1", State: "OUT_FOR_DELIVERY", At: at(900)}))
	must(t, tr.ApplyEta(t.Context(), readmodel.Eta{TrackingNumber: "TRK-100", Start: at(3600), End: at(10800), AssignedAt: at(900)}))
	w := newWire(t, depsOver(store))

	a := w.skill(t, skillGetOrderTracking, map[string]any{"orderId": "ord-1"})
	if a.env.Data == nil || a.env.Data.Tracking == nil {
		t.Fatalf("envelope = %s", a.raw)
	}
	pkg := a.env.Data.Tracking.Groups[0].Packages[0]
	if pkg.EtaStart != "2026-10-24T11:00:00Z" || pkg.EtaEnd != "2026-10-24T13:00:00Z" {
		t.Fatalf("package = %+v", pkg)
	}

	must(t, tr.ApplyScan(t.Context(), readmodel.Scan{TrackingNumber: "TRK-100", NodeID: "ds-1", State: "AT_STATION", At: at(20000)}))
	a = w.skill(t, skillGetOrderTracking, map[string]any{"orderId": "ord-1"})
	if pkg := a.env.Data.Tracking.Groups[0].Packages[0]; pkg.EtaStart != "" || pkg.EtaEnd != "" {
		t.Fatalf("a failed attempt must hide the window: %+v", pkg)
	}
}
