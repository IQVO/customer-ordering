package repocontract

import (
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/readmodel"
)

func packageByTN(t *testing.T, e Env, orderID, tn string) readmodel.Package {
	t.Helper()
	list, err := e.Tracking.Packages(bg(), orderID)
	must(t, err)
	for _, p := range list {
		if p.TrackingNumber == tn {
			return p
		}
	}
	t.Fatalf("package %s not listed for order %s: %+v", tn, orderID, list)
	return readmodel.Package{}
}

func testTracking(t *testing.T, e Env) {
	cases := []struct {
		name string
		run  func(*testing.T, Env)
	}{
		{"assignment links resolve and are replaced", trackingAssignmentLinksResolveAndAreReplaced},
		{"a label before the link is linked when the lock arrives", trackingALabelBeforeTheLinkIsLinkedWhenTheLockAr},
		{"a label after the link is linked at once", trackingALabelAfterTheLinkIsLinkedAtOnce},
		{"attached tracking numbers create linked rows", trackingAttachedTrackingNumbersCreateLinkedRows},
		{"scans: the newest wins, a scan may precede the label", trackingScansTheNewestWinsAScanMayPrecedeTheLabe},
		{"deliveries are remembered, listed per group and beat older scans", trackingDeliveriesAreRememberedListedPerGroupAnd},
		{"a delivery with only the assignment is linked by it", trackingADeliveryWithOnlyTheAssignmentIsLinkedBy},
		{"exceptions are recorded and linked", trackingExceptionsAreRecordedAndLinked},
		{"a void closes a labelled package only", trackingAVoidClosesALabelledPackageOnly},
		{"group signals accumulate per field and sort by group", trackingGroupSignalsAccumulatePerFieldAndSortByG},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, e) })
	}
}

func trackingAssignmentLinksResolveAndAreReplaced(t *testing.T, e Env) {
	_, err := e.Tracking.ResolveAssignment(bg(), "as-1")
	wantErr(t, err, ports.ErrNotFound)
	must(t, e.Tracking.LinkAssignment(bg(), readmodel.AssignmentLink{AssignmentID: "as-1", OrderID: "o-1", GroupNo: 1, PromiseID: "p-1"}))
	l, err := e.Tracking.ResolveAssignment(bg(), "as-1")
	must(t, err)
	if l != (readmodel.AssignmentLink{AssignmentID: "as-1", OrderID: "o-1", GroupNo: 1, PromiseID: "p-1"}) {
		t.Fatalf("link = %+v", l)
	}
	must(t, e.Tracking.LinkAssignment(bg(), readmodel.AssignmentLink{AssignmentID: "as-1", OrderID: "o-1", GroupNo: 2}))
	l, _ = e.Tracking.ResolveAssignment(bg(), "as-1")
	if l.GroupNo != 2 || l.PromiseID != "" {
		t.Fatalf("link not replaced: %+v", l)
	}
}

func trackingALabelBeforeTheLinkIsLinkedWhenTheLockAr(t *testing.T, e Env) {
	must(t, e.Tracking.ApplyLabel(bg(), readmodel.Label{TrackingNumber: "TRK-A", AssignmentID: "as-a", Carrier: "NETWORK", At: at(1)}))
	list, err := e.Tracking.Packages(bg(), "o-a")
	must(t, err)
	if len(list) != 0 {
		t.Fatalf("an unlinked package must not be listed: %+v", list)
	}
	must(t, e.Tracking.LinkAssignment(bg(), readmodel.AssignmentLink{AssignmentID: "as-a", OrderID: "o-a", GroupNo: 3}))
	p := packageByTN(t, e, "o-a", "TRK-A")
	if p.GroupNo != 3 || p.Carrier != "NETWORK" || p.State != "LABELLED" || p.AssignmentID != "as-a" {
		t.Fatalf("package = %+v", p)
	}
}

func trackingALabelAfterTheLinkIsLinkedAtOnce(t *testing.T, e Env) {
	must(t, e.Tracking.LinkAssignment(bg(), readmodel.AssignmentLink{AssignmentID: "as-b", OrderID: "o-b", GroupNo: 1}))
	must(t, e.Tracking.ApplyLabel(bg(), readmodel.Label{TrackingNumber: "TRK-B", AssignmentID: "as-b", At: at(1)}))
	if p := packageByTN(t, e, "o-b", "TRK-B"); p.GroupNo != 1 || p.Carrier != "" || p.State != "LABELLED" {
		t.Fatalf("package = %+v", p)
	}
}

func trackingAttachedTrackingNumbersCreateLinkedRows(t *testing.T, e Env) {
	must(t, e.Tracking.AttachTrackingNumbers(bg(), "o-c", 2, []string{"TRK-C1", "TRK-C2"}))
	list, err := e.Tracking.Packages(bg(), "o-c")
	must(t, err)
	if len(list) != 2 || list[0].TrackingNumber != "TRK-C1" || list[1].TrackingNumber != "TRK-C2" || list[0].GroupNo != 2 {
		t.Fatalf("packages = %+v", list)
	}
}

func trackingScansTheNewestWinsAScanMayPrecedeTheLabe(t *testing.T, e Env) {
	must(t, e.Tracking.AttachTrackingNumbers(bg(), "o-d", 1, []string{"TRK-D"}))
	must(t, e.Tracking.ApplyScan(bg(), readmodel.Scan{TrackingNumber: "TRK-D", NodeID: "ds-2", State: "OUT_FOR_DELIVERY", At: at(50)}))
	must(t, e.Tracking.ApplyScan(bg(), readmodel.Scan{TrackingNumber: "TRK-D", NodeID: "ds-1", State: "AT_STATION", At: at(40)}))
	p := packageByTN(t, e, "o-d", "TRK-D")
	if p.State != "OUT_FOR_DELIVERY" || p.LastScanNodeID != "ds-2" || !p.LastScanAt.Equal(at(50)) {
		t.Fatalf("an older scan must not win: %+v", p)
	}
	must(t, e.Tracking.ApplyLabel(bg(), readmodel.Label{TrackingNumber: "TRK-D", AssignmentID: "as-d", At: at(60)}))
	if p := packageByTN(t, e, "o-d", "TRK-D"); p.State != "OUT_FOR_DELIVERY" {
		t.Fatalf("a late label must not reset the state: %+v", p)
	}
}

func trackingDeliveriesAreRememberedListedPerGroupAnd(t *testing.T, e Env) {
	must(t, e.Tracking.ApplyDelivery(bg(), readmodel.Delivery{TrackingNumber: "TRK-E", AssignmentID: "as-e", OrderID: "o-e", At: at(80)}))
	list, err := e.Tracking.DeliveredPackages(bg(), "o-e", 1)
	must(t, err)
	if len(list) != 0 {
		t.Fatalf("the group is not known yet: %+v", list)
	}
	must(t, e.Tracking.AttachTrackingNumbers(bg(), "o-e", 1, []string{"TRK-E"}))
	list, err = e.Tracking.DeliveredPackages(bg(), "o-e", 1)
	must(t, err)
	if len(list) != 1 || list[0].TrackingNumber != "TRK-E" || !list[0].At.Equal(at(80)) || list[0].OrderID != "o-e" {
		t.Fatalf("delivered = %+v", list)
	}
	must(t, e.Tracking.ApplyScan(bg(), readmodel.Scan{TrackingNumber: "TRK-E", NodeID: "ds-1", State: "AT_STATION", At: at(70)}))
	p := packageByTN(t, e, "o-e", "TRK-E")
	if p.State != "DELIVERED" || !p.DeliveredAt.Equal(at(80)) {
		t.Fatalf("an older scan must not undo the delivery: %+v", p)
	}
}

func trackingADeliveryWithOnlyTheAssignmentIsLinkedBy(t *testing.T, e Env) {
	must(t, e.Tracking.LinkAssignment(bg(), readmodel.AssignmentLink{AssignmentID: "as-f", OrderID: "o-f", GroupNo: 4}))
	must(t, e.Tracking.ApplyDelivery(bg(), readmodel.Delivery{TrackingNumber: "TRK-F", AssignmentID: "as-f", At: at(5)}))
	if p := packageByTN(t, e, "o-f", "TRK-F"); p.GroupNo != 4 {
		t.Fatalf("package = %+v", p)
	}
}

func trackingExceptionsAreRecordedAndLinked(t *testing.T, e Env) {
	must(t, e.Tracking.ApplyException(bg(), readmodel.Exception{TrackingNumber: "TRK-G", OrderID: "o-g", Reason: "ADDRESS_NOT_FOUND", At: at(9)}))
	if p := packageByTN(t, e, "o-g", "TRK-G"); p.ExceptionReason != "ADDRESS_NOT_FOUND" {
		t.Fatalf("package = %+v", p)
	}
}

func trackingAVoidClosesALabelledPackageOnly(t *testing.T, e Env) {
	must(t, e.Tracking.AttachTrackingNumbers(bg(), "o-h", 1, []string{"TRK-H1", "TRK-H2"}))
	must(t, e.Tracking.ApplyLabel(bg(), readmodel.Label{TrackingNumber: "TRK-H1", At: at(1)}))
	must(t, e.Tracking.ApplyScan(bg(), readmodel.Scan{TrackingNumber: "TRK-H2", State: "AT_STATION", At: at(2)}))
	must(t, e.Tracking.ApplyVoid(bg(), "TRK-H1"))
	must(t, e.Tracking.ApplyVoid(bg(), "TRK-H2"))
	if p := packageByTN(t, e, "o-h", "TRK-H1"); p.State != "VOIDED" {
		t.Fatalf("H1 = %+v", p)
	}
	if p := packageByTN(t, e, "o-h", "TRK-H2"); p.State != "AT_STATION" {
		t.Fatalf("H2 = %+v", p)
	}
}

func trackingGroupSignalsAccumulatePerFieldAndSortByG(t *testing.T, e Env) {
	risk, verdict, late, unsrc := "NODE_DELAYED", "BROKEN", 660, "NO_STOCK"
	must(t, e.Tracking.PatchGroupSignal(bg(), readmodel.GroupSignalPatch{OrderID: "o-i", GroupNo: 2, AtRiskReason: &risk}))
	must(t, e.Tracking.PatchGroupSignal(bg(), readmodel.GroupSignalPatch{OrderID: "o-i", GroupNo: 1, UnsourceableReason: &unsrc}))
	must(t, e.Tracking.PatchGroupSignal(bg(), readmodel.GroupSignalPatch{OrderID: "o-i", GroupNo: 2, PromiseVerdict: &verdict, LateByMinutes: &late}))
	got, err := e.Tracking.GroupSignals(bg(), "o-i")
	must(t, err)
	if len(got) != 2 || got[0].GroupNo != 1 || got[0].UnsourceableReason != "NO_STOCK" || got[0].LateByMinutes != nil {
		t.Fatalf("signals = %+v", got)
	}
	g2 := got[1]
	if g2.AtRiskReason != "NODE_DELAYED" || g2.PromiseVerdict != "BROKEN" || g2.LateByMinutes == nil || *g2.LateByMinutes != 660 {
		t.Fatalf("group 2 = %+v", g2)
	}
	none, err := e.Tracking.GroupSignals(bg(), "o-none")
	must(t, err)
	if none == nil || len(none) != 0 {
		t.Fatalf("no signals must be an empty list, got %#v", none)
	}
}
