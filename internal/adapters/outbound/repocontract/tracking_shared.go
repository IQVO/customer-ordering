package repocontract

import (
	"reflect"
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
)

// Fixture of the shared-package cases (ADR 0012): order "o-s1" is the carrier's,
// "o-s2" a follower of another order, both attach the same tracking number.
const (
	tnShared      = "TRK-S"
	orderShared1  = "o-s1"
	orderShared2  = "o-s2"
	assignShared1 = "as-s1"
	tnShared3     = "TRK-S3"
	tnShared5     = "TRK-S5"
	tnShared6     = "TRK-S6"
	orderShared7  = "o-s7"
	orderShared5  = "o-s5"
	reasonAddress = "ADDRESS_NOT_FOUND"
)

func groupRefs(t *testing.T, e Env, tn string) []readmodel.GroupRef {
	t.Helper()
	got, err := e.Tracking.GroupsOfTracking(bg(), tn)
	must(t, err)
	if got == nil {
		t.Fatalf("GroupsOfTracking(%s) must be an empty list, never nil", tn)
	}
	return got
}

func wantRefs(t *testing.T, got, want []readmodel.GroupRef) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("groups = %+v, want %+v", got, want)
	}
}

func trackingSharedNumberHasASetOfGroups(t *testing.T, e Env) {
	wantRefs(t, groupRefs(t, e, tnShared), []readmodel.GroupRef{})
	// The follower attaches first: attach order must not matter, the result is sorted.
	must(t, e.Tracking.AttachTrackingNumbers(bg(), orderShared2, 3, []string{tnShared}))
	must(t, e.Tracking.AttachTrackingNumbers(bg(), orderShared1, 1, []string{tnShared}))
	must(t, e.Tracking.AttachTrackingNumbers(bg(), orderShared1, 2, []string{tnShared}))
	want := []readmodel.GroupRef{{OrderID: orderShared1, GroupNo: 1}, {OrderID: orderShared1, GroupNo: 2}, {OrderID: orderShared2, GroupNo: 3}}
	wantRefs(t, groupRefs(t, e, tnShared), want)
	// A replayed attach adds nothing and never re-owns the number.
	must(t, e.Tracking.AttachTrackingNumbers(bg(), orderShared2, 3, []string{tnShared}))
	wantRefs(t, groupRefs(t, e, tnShared), want)
}

func trackingSharedPackageIsListedPerGroup(t *testing.T, e Env) {
	must(t, e.Tracking.AttachTrackingNumbers(bg(), "o-s3", 1, []string{tnShared3, "TRK-S4"}))
	must(t, e.Tracking.AttachTrackingNumbers(bg(), "o-s3", 2, []string{tnShared3}))
	must(t, e.Tracking.AttachTrackingNumbers(bg(), "o-s4", 1, []string{tnShared3}))
	own, err := e.Tracking.Packages(bg(), "o-s3")
	must(t, err)
	if len(own) != 3 || own[0].TrackingNumber != tnShared3 || own[0].GroupNo != 1 || own[1].TrackingNumber != "TRK-S4" || own[1].GroupNo != 1 ||
		own[2].TrackingNumber != tnShared3 || own[2].GroupNo != 2 || own[2].OrderID != "o-s3" {
		t.Fatalf("packages of o-s3 (by group, then number) = %+v", own)
	}
	other, err := e.Tracking.Packages(bg(), "o-s4")
	must(t, err)
	if len(other) != 1 || other[0].TrackingNumber != tnShared3 || other[0].GroupNo != 1 || other[0].OrderID != "o-s4" {
		t.Fatalf("the follower order lists the shared package under its own group: %+v", other)
	}
}

func trackingSharedDeliveryAndOutcomeReachEveryGroup(t *testing.T, e Env) {
	// The events carry the carrier's order; the follower attaches only later.
	must(t, e.Tracking.ApplyDelivery(bg(), readmodel.Delivery{TrackingNumber: tnShared5, AssignmentID: "as-s5", OrderID: orderShared5, At: at(80)}))
	must(t, e.Tracking.ApplyOutcome(bg(), readmodel.Outcome{TrackingNumber: tnShared6, OrderID: orderShared5, Kind: readmodel.OutcomeLost, At: at(90)}))
	must(t, e.Tracking.AttachTrackingNumbers(bg(), orderShared5, 1, []string{tnShared5, tnShared6}))
	if d, err := e.Tracking.DeliveredPackages(bg(), "o-s6", 2); err != nil || len(d) != 0 {
		t.Fatalf("a group that did not name the package has no delivery: %+v %v", d, err)
	}
	must(t, e.Tracking.AttachTrackingNumbers(bg(), "o-s6", 2, []string{tnShared5, tnShared6}))
	for _, ref := range []readmodel.GroupRef{{OrderID: orderShared5, GroupNo: 1}, {OrderID: "o-s6", GroupNo: 2}} {
		d, err := e.Tracking.DeliveredPackages(bg(), ref.OrderID, ref.GroupNo)
		must(t, err)
		if len(d) != 1 || d[0].TrackingNumber != tnShared5 || !d[0].At.Equal(at(80)) || d[0].OrderID != ref.OrderID {
			t.Fatalf("delivered for %+v = %+v", ref, d)
		}
		o, err := e.Tracking.OutcomePackages(bg(), ref.OrderID, ref.GroupNo)
		must(t, err)
		if len(o) != 1 || o[0].TrackingNumber != tnShared6 || o[0].Kind != readmodel.OutcomeLost || !o[0].At.Equal(at(90)) || o[0].OrderID != ref.OrderID {
			t.Fatalf("outcomes for %+v = %+v", ref, o)
		}
	}
}

func trackingAssignmentLinkJoinsTheGroupSet(t *testing.T, e Env) {
	// A label that names the carrier's assignment joins the carrier's group at
	// once (or when the lock links it) ...
	must(t, e.Tracking.ApplyLabel(bg(), readmodel.Label{TrackingNumber: "TRK-S7", AssignmentID: assignShared1, At: at(1)}))
	wantRefs(t, groupRefs(t, e, "TRK-S7"), []readmodel.GroupRef{})
	must(t, e.Tracking.LinkAssignment(bg(), readmodel.AssignmentLink{AssignmentID: assignShared1, OrderID: orderShared7, GroupNo: 1}))
	wantRefs(t, groupRefs(t, e, "TRK-S7"), []readmodel.GroupRef{{OrderID: orderShared7, GroupNo: 1}})
	// ... and a follower attaching later does not take it away.
	must(t, e.Tracking.AttachTrackingNumbers(bg(), "o-s8", 4, []string{"TRK-S7"}))
	wantRefs(t, groupRefs(t, e, "TRK-S7"), []readmodel.GroupRef{{OrderID: orderShared7, GroupNo: 1}, {OrderID: "o-s8", GroupNo: 4}})
	if p := packageByTN(t, e, orderShared7, "TRK-S7"); p.GroupNo != 1 || p.AssignmentID != assignShared1 {
		t.Fatalf("the carrier keeps its package: %+v", p)
	}
	// A package that only names an order (no group known) is listed for that
	// order under group 0 and never for another order.
	must(t, e.Tracking.ApplyException(bg(), readmodel.Exception{TrackingNumber: "TRK-S9", OrderID: "o-s9", Reason: reasonAddress, At: at(2)}))
	if p := packageByTN(t, e, "o-s9", "TRK-S9"); p.GroupNo != 0 {
		t.Fatalf("a package of unknown group lists under group 0: %+v", p)
	}
	if other, err := e.Tracking.Packages(bg(), orderShared7); err != nil || len(other) != 1 {
		t.Fatalf("o-s7 lists only its own package: %+v %v", other, err)
	}
	wantRefs(t, groupRefs(t, e, "TRK-S9"), []readmodel.GroupRef{})
}
