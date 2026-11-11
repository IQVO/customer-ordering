package repocontract

import (
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
)

// Fixture of the day-of window cases (ADR 0014).
const (
	tnEta      = "TRK-ETA1"
	tnEtaShare = "TRK-ETA2"
	orderEta   = "o-eta"
	orderEta2  = "o-eta2"
	assignEta  = "as-eta"
	tnEtaScan  = "TRK-ETA4"
	nodeEta    = "ds-eta"
)

func eta(tn string, startMin, endMin, assignedMin int) readmodel.Eta {
	return readmodel.Eta{TrackingNumber: tn, OrderID: orderEta, Start: at(startMin), End: at(endMin), AssignedAt: at(assignedMin)}
}

func wantWindow(t *testing.T, p readmodel.Package, start, end, assigned time.Time) {
	t.Helper()
	if !p.EtaStart.Equal(start) || !p.EtaEnd.Equal(end) || !p.EtaAssignedAt.Equal(assigned) {
		t.Fatalf("window = %v..%v assigned %v, want %v..%v assigned %v", p.EtaStart, p.EtaEnd, p.EtaAssignedAt, start, end, assigned)
	}
}

func testTrackingEta(t *testing.T, e Env) {
	cases := []struct {
		name string
		run  func(*testing.T, Env)
	}{
		{"a window before anything else creates the package and is listed once the group is known", trackingEtaBeforeTheLabelCreatesTheRow},
		{"the latest window wins, an older one is ignored, an equal instant rewrites", trackingEtaLatestWins},
		{"a window survives scans and a delivery (the view decides what is current)", trackingEtaSurvivesLaterFacts},
		{"a shared package shows the window under every group", trackingEtaSharedPackageShowsItInEveryGroup},
		{"a package without a window has none", trackingEtaAbsentByDefault},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, e) })
	}
}

func trackingEtaBeforeTheLabelCreatesTheRow(t *testing.T, e Env) {
	in := eta(tnEta, 600, 720, 540)
	in.AssignmentID = assignEta
	must(t, e.Tracking.ApplyEta(bg(), in))
	// The row names its order only: listed under group 0 until a group is known.
	p := packageByTN(t, e, orderEta, tnEta)
	if p.GroupNo != 0 || p.AssignmentID != assignEta {
		t.Fatalf("package = %+v", p)
	}
	wantWindow(t, p, at(600), at(720), at(540))
	// The lock arrives later: the same row is linked to its group.
	must(t, e.Tracking.LinkAssignment(bg(), readmodel.AssignmentLink{AssignmentID: assignEta, OrderID: orderEta, GroupNo: 2}))
	p = packageByTN(t, e, orderEta, tnEta)
	if p.GroupNo != 2 {
		t.Fatalf("package = %+v", p)
	}
	wantWindow(t, p, at(600), at(720), at(540))
}

func trackingEtaLatestWins(t *testing.T, e Env) {
	must(t, e.Tracking.AttachTrackingNumbers(bg(), orderEta, 1, []string{"TRK-ETA3"}))
	must(t, e.Tracking.ApplyEta(bg(), eta("TRK-ETA3", 600, 720, 500)))
	must(t, e.Tracking.ApplyEta(bg(), eta("TRK-ETA3", 660, 780, 520)))
	wantWindow(t, packageByTN(t, e, orderEta, "TRK-ETA3"), at(660), at(780), at(520))
	must(t, e.Tracking.ApplyEta(bg(), eta("TRK-ETA3", 300, 420, 510))) // older: ignored
	wantWindow(t, packageByTN(t, e, orderEta, "TRK-ETA3"), at(660), at(780), at(520))
	must(t, e.Tracking.ApplyEta(bg(), eta("TRK-ETA3", 700, 820, 520))) // equal instant: rewritten
	wantWindow(t, packageByTN(t, e, orderEta, "TRK-ETA3"), at(700), at(820), at(520))
}

func trackingEtaSurvivesLaterFacts(t *testing.T, e Env) {
	must(t, e.Tracking.AttachTrackingNumbers(bg(), orderEta, 1, []string{tnEtaScan}))
	must(t, e.Tracking.ApplyEta(bg(), eta(tnEtaScan, 600, 720, 540)))
	must(t, e.Tracking.ApplyScan(bg(), readmodel.Scan{TrackingNumber: tnEtaScan, NodeID: nodeEta, State: scanOutForDelivery, At: at(545)}))
	p := packageByTN(t, e, orderEta, tnEtaScan)
	if p.State != scanOutForDelivery || !p.LastScanAt.Equal(at(545)) {
		t.Fatalf("package = %+v", p)
	}
	wantWindow(t, p, at(600), at(720), at(540))
	must(t, e.Tracking.ApplyDelivery(bg(), readmodel.Delivery{TrackingNumber: tnEtaScan, OrderID: orderEta, At: at(650)}))
	wantWindow(t, packageByTN(t, e, orderEta, tnEtaScan), at(600), at(720), at(540))
}

func trackingEtaSharedPackageShowsItInEveryGroup(t *testing.T, e Env) {
	must(t, e.Tracking.AttachTrackingNumbers(bg(), orderEta, 1, []string{tnEtaShare}))
	must(t, e.Tracking.AttachTrackingNumbers(bg(), orderEta, 2, []string{tnEtaShare}))
	must(t, e.Tracking.AttachTrackingNumbers(bg(), orderEta2, 1, []string{tnEtaShare}))
	must(t, e.Tracking.ApplyEta(bg(), eta(tnEtaShare, 600, 720, 540)))
	for _, orderID := range []string{orderEta, orderEta2} {
		list, err := e.Tracking.Packages(bg(), orderID)
		must(t, err)
		shown := 0
		for _, p := range list {
			if p.TrackingNumber == tnEtaShare {
				wantWindow(t, p, at(600), at(720), at(540))
				shown++
			}
		}
		if want := map[string]int{orderEta: 2, orderEta2: 1}[orderID]; shown != want {
			t.Fatalf("%s lists the shared package %d times, want %d", orderID, shown, want)
		}
	}
}

func trackingEtaAbsentByDefault(t *testing.T, e Env) {
	must(t, e.Tracking.AttachTrackingNumbers(bg(), orderEta, 3, []string{"TRK-ETA5"}))
	must(t, e.Tracking.ApplyScan(bg(), readmodel.Scan{TrackingNumber: "TRK-ETA5", State: scanOutForDelivery, At: at(5)}))
	p := packageByTN(t, e, orderEta, "TRK-ETA5")
	if !p.EtaStart.IsZero() || !p.EtaEnd.IsZero() || !p.EtaAssignedAt.IsZero() {
		t.Fatalf("package = %+v", p)
	}
}
