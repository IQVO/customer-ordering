package repocontract

import (
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
)

// Fixture of the delivery-point advisory cases (ADR 0015).
const (
	tnFlag      = "TRK-FLAG1"
	tnFlagShare = "TRK-FLAG2"
	tnFlagNone  = "TRK-FLAG3"
)

func flag(tn, reason string, atMin int) readmodel.PointFlag {
	return readmodel.PointFlag{TrackingNumber: tn, AssignmentID: assignEta, OrderID: orderEta, Reason: reason, FlaggedAt: at(atMin)}
}

func wantAdvisory(t *testing.T, p readmodel.Package, reason string, atMin int) {
	t.Helper()
	if p.PointFlagReason != reason || !p.PointFlaggedAt.Equal(at(atMin)) {
		t.Fatalf("flag = %s@%v, want %s@%v", p.PointFlagReason, p.PointFlaggedAt, reason, at(atMin))
	}
}

func testTrackingPointFlag(t *testing.T, e Env) {
	cases := []struct {
		name string
		run  func(*testing.T, Env)
	}{
		{"a flag before anything else creates the package row", trackingPointFlagCreatesTheRow},
		{"the newest flag wins and an older one is ignored", trackingPointFlagNewestWins},
		{"a shared package carries the flag under every group", trackingPointFlagSharedPackage},
		{"a package without a flag has none", trackingPointFlagAbsentByDefault},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, e) })
	}
}

func trackingPointFlagCreatesTheRow(t *testing.T, e Env) {
	must(t, e.Tracking.ApplyPointFlag(bg(), flag(tnFlag, "NOT_HOME", 600)))
	p := packageByTN(t, e, orderEta, tnFlag)
	if p.AssignmentID != assignEta {
		t.Fatalf("package = %+v", p)
	}
	wantAdvisory(t, p, "NOT_HOME", 600)
}

func trackingPointFlagNewestWins(t *testing.T, e Env) {
	must(t, e.Tracking.ApplyPointFlag(bg(), flag(tnFlag, "NOT_HOME", 600)))
	must(t, e.Tracking.ApplyPointFlag(bg(), flag(tnFlag, "ACCESS_PROBLEM", 500))) // older: ignored
	p := packageByTN(t, e, orderEta, tnFlag)
	wantAdvisory(t, p, "NOT_HOME", 600)
	must(t, e.Tracking.ApplyPointFlag(bg(), flag(tnFlag, "ACCESS_PROBLEM", 700))) // newer: wins
	p = packageByTN(t, e, orderEta, tnFlag)
	wantAdvisory(t, p, "ACCESS_PROBLEM", 700)
}

func trackingPointFlagSharedPackage(t *testing.T, e Env) {
	must(t, e.Tracking.ApplyPointFlag(bg(), flag(tnFlagShare, "NOT_HOME", 600)))
	must(t, e.Tracking.LinkAssignment(bg(), readmodel.AssignmentLink{AssignmentID: assignEta, OrderID: orderEta, GroupNo: 2}))
	p := packageByTN(t, e, orderEta, tnFlagShare)
	wantAdvisory(t, p, "NOT_HOME", 600)
}

func trackingPointFlagAbsentByDefault(t *testing.T, e Env) {
	must(t, e.Tracking.AttachTrackingNumbers(bg(), orderEta, 3, []string{tnFlagNone}))
	must(t, e.Tracking.ApplyScan(bg(), readmodel.Scan{TrackingNumber: tnFlagNone, State: scanOutForDelivery, At: at(5)}))
	p := packageByTN(t, e, orderEta, tnFlagNone)
	if p.PointFlagReason != "" || !p.PointFlaggedAt.IsZero() {
		t.Fatalf("no flag may exist by default: %+v", p)
	}
}
