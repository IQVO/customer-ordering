package http_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
)

// The tracking body carries the day-of window while the package is out for
// delivery and omits both fields otherwise (ADR 0014).
func TestTrackingShowsTheEtaWindowOnlyWhileOutForDelivery(t *testing.T) {
	e := newEnv(t)
	id := e.placed()
	ctx := context.Background()
	tr := e.store.Tracking()
	start, end := t0.Add(5*time.Hour), t0.Add(7*time.Hour)
	for _, step := range []func() error{
		func() error { return tr.AttachTrackingNumbers(ctx, id, 1, []string{"TRK-1"}) },
		func() error {
			return tr.ApplyScan(ctx, readmodel.Scan{TrackingNumber: "TRK-1", NodeID: "ds-1", State: "OUT_FOR_DELIVERY", At: t0.Add(4 * time.Hour)})
		},
		func() error {
			return tr.ApplyEta(ctx, readmodel.Eta{TrackingNumber: "TRK-1", OrderID: id, Start: start, End: end, AssignedAt: t0.Add(4 * time.Hour)})
		},
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	pkg := e.firstPackage(t, id)
	if pkg["etaStart"] != "2026-10-23T15:00:00Z" || pkg["etaEnd"] != "2026-10-23T17:00:00Z" {
		t.Fatalf("package = %v", pkg)
	}
	if err := tr.ApplyScan(ctx, readmodel.Scan{TrackingNumber: "TRK-1", NodeID: "ds-1", State: "AT_STATION", At: t0.Add(8 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	pkg = e.firstPackage(t, id)
	if _, ok := pkg["etaStart"]; ok {
		t.Fatalf("a failed attempt must hide the window: %v", pkg)
	}
	if _, ok := pkg["etaEnd"]; ok {
		t.Fatalf("a failed attempt must hide the window: %v", pkg)
	}
}

func (e *env) firstPackage(t *testing.T, id string) map[string]any {
	t.Helper()
	r := e.do("GET", "/orders/"+id+"/tracking", "", "")
	if r.Status != 200 {
		t.Fatalf("tracking = %d %s", r.Status, r.Body)
	}
	groups := r.json(t)["groups"].([]any)
	pkgs := groups[0].(map[string]any)["packages"].([]any)
	if len(pkgs) != 1 {
		t.Fatalf("packages = %v", pkgs)
	}
	return pkgs[0].(map[string]any)
}
