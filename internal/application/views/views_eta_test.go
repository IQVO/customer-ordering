package views_test

import (
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/application/views"
)

// etaPackage is a package of group 1 with a window assigned at assignedAt and a
// last scan at scanAt in the given state (ADR 0014).
func etaPackage(state string, assignedAt, scanAt time.Time) readmodel.Package {
	return readmodel.Package{
		TrackingNumber: "T1", GroupNo: 1, State: state, LastScanAt: scanAt,
		EtaStart: t0.Add(4 * time.Hour), EtaEnd: t0.Add(6 * time.Hour), EtaAssignedAt: assignedAt,
	}
}

func shownWindow(t *testing.T, p readmodel.Package) (time.Time, time.Time) {
	t.Helper()
	v := views.TrackingFrom(placed(t), nil, []readmodel.Package{p})
	if len(v.Groups[0].Packages) != 1 {
		t.Fatalf("packages = %+v", v.Groups[0].Packages)
	}
	got := v.Groups[0].Packages[0]
	return got.EtaStart, got.EtaEnd
}

// TestTheWindowIsShownOnlyWhileOutForDeliveryAndNotOlderThanTheLastScan is the
// matrix of the single view rule: every journey state, and the assigned instant
// before, equal to and after the last scan.
func TestTheWindowIsShownOnlyWhileOutForDeliveryAndNotOlderThanTheLastScan(t *testing.T) {
	scan := t0.Add(time.Hour)
	states := []string{"LABELLED", "IN_TRANSIT", "AT_STATION", "OUT_FOR_DELIVERY", "DELIVERED", "RETURNED", "LOST", "VOIDED", ""}
	instants := []struct {
		name     string
		assigned time.Time
		fresh    bool
	}{
		{"assigned after the scan", scan.Add(time.Minute), true},
		{"assigned at the scan", scan, true},
		{"assigned before the scan", scan.Add(-time.Minute), false},
		{"never assigned", time.Time{}, false},
	}
	for _, state := range states {
		for _, in := range instants {
			start, end := shownWindow(t, etaPackage(state, in.assigned, scan))
			shown := !start.IsZero() && !end.IsZero()
			if want := state == "OUT_FOR_DELIVERY" && in.fresh; shown != want {
				t.Fatalf("state %q, %s: shown = %v, want %v", state, in.name, shown, want)
			}
			if shown && (!start.Equal(t0.Add(4*time.Hour)) || !end.Equal(t0.Add(6*time.Hour))) {
				t.Fatalf("state %q, %s: window %v..%v is not the stored one", state, in.name, start, end)
			}
		}
	}
}

// The window is shown as received: one that ends after the group's
// deliverByLatest (t0+3h) is neither clipped nor hidden.
func TestAWindowAfterTheLatestPromisedTimeIsShownUnclipped(t *testing.T) {
	start, end := shownWindow(t, etaPackage("OUT_FOR_DELIVERY", t0.Add(time.Hour), t0.Add(time.Hour)))
	if !start.Equal(t0.Add(4*time.Hour)) || !end.Equal(t0.Add(6*time.Hour)) {
		t.Fatalf("window = %v..%v", start, end)
	}
}

// A package shared by two groups is listed once per group; both show the window.
func TestASharedPackageShowsTheWindowInBothGroups(t *testing.T) {
	a, b := etaPackage("OUT_FOR_DELIVERY", t0.Add(time.Hour), t0.Add(time.Hour)), etaPackage("OUT_FOR_DELIVERY", t0.Add(time.Hour), t0.Add(time.Hour))
	b.GroupNo = 2
	v := views.TrackingFrom(placed(t), nil, []readmodel.Package{a, b})
	for i, g := range v.Groups {
		if len(g.Packages) != 1 || g.Packages[0].EtaStart.IsZero() || g.Packages[0].EtaEnd.IsZero() {
			t.Fatalf("group %d = %+v", i+1, g.Packages)
		}
	}
}
