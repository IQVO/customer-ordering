package usecases_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/notice"
)

// noticesOf returns the ShopperNoticeDecided events now in the outbox.
func noticesOf(t *testing.T, f *fx) []notice.ShopperNoticeDecided {
	t.Helper()
	var out []notice.ShopperNoticeDecided
	for _, ev := range f.store.Outbox().Events() {
		if n, ok := ev.(notice.ShopperNoticeDecided); ok {
			out = append(out, n)
		}
	}
	return out
}

// familyNotices filters the decided notices of one family.
func familyNotices(t *testing.T, f *fx, family notice.Family) []notice.ShopperNoticeDecided {
	t.Helper()
	var out []notice.ShopperNoticeDecided
	for _, n := range noticesOf(t, f) {
		if n.Family == family {
			out = append(out, n)
		}
	}
	return out
}

// publishPolicy stores version 1 with the notifier for every family.
func publishPolicy(t *testing.T, f *fx, notifier notice.Notifier) {
	t.Helper()
	rows := notice.DefaultRows()
	for i := range rows {
		rows[i].Notifier = notifier
	}
	uc := usecases.NotificationPolicies{Deps: f.deps}
	if _, err := uc.Publish(context.Background(), rows); err != nil {
		t.Fatal(err)
	}
}

func lockGroup1(t *testing.T, f *fx, id string) {
	t.Helper()
	must(t, f.consumer().ApplyFulfillmentLocked(bg(), f.meta("sourcing.Locked"), usecases.FulfillmentLocked{AssignmentID: "as-1", OrderID: id, GroupNo: 1, NodeID: "fc-aus-1"}))
}

func shipGroup1(t *testing.T, f *fx, id string) {
	t.Helper()
	must(t, f.consumer().ApplyFulfillmentShipped(bg(), f.meta("sourcing.Shipped"), usecases.FulfillmentShipped{AssignmentID: "as-1", OrderID: id, GroupNo: 1, TrackingNumbers: []string{"TRK-1"}, ShippedAt: at(20)}))
}

// TestShippingAndTheTerminalFactsDecideNotices proves the GROUP_* families: one
// fact per transition, emitted regardless of the notifier (ADR 0017).
func TestShippingAndTheTerminalFactsDecideNotices(t *testing.T) {
	f := newFx(t)
	id := f.place()
	lockGroup1(t, f, id)
	if got := noticesOf(t, f); len(got) != 0 {
		t.Fatalf("a lock decides nothing: %+v", got)
	}
	shipGroup1(t, f, id)
	got := noticesOf(t, f)
	if len(got) != 1 || got[0].Family != notice.FamilyGroupShipped || got[0].Notifier != notice.NotifierNobody || got[0].PolicyVersion != 0 {
		t.Fatalf("shipped = %+v", got)
	}
	if got[0].Fact.Status != "SHIPPED" || got[0].OrderID != id || got[0].GroupNo != 1 || len(got[0].NoticeKey) != 64 {
		t.Fatalf("fact = %+v", got[0])
	}
	// A repeated shipment is not a new fact.
	shipGroup1(t, f, id)
	if again := noticesOf(t, f); len(again) != 1 {
		t.Fatalf("a repeat decides nothing: %+v", again)
	}
	// The delivery settles the group: the DELIVERED fact follows the SHIPPED one.
	must(t, f.consumer().ApplyPackageDelivered(bg(), f.meta("shipping.Delivered"), usecases.PackageDelivered{TrackingNumber: "TRK-1", AssignmentID: "as-1", OrderID: id, DeliveredAt: at(30)}))
	got = noticesOf(t, f)
	if len(got) != 2 || got[1].Family != notice.FamilyGroupDelivered || got[1].Fact.Status != "DELIVERED" {
		t.Fatalf("delivered = %+v", got)
	}
}

// TestAPolicyVersionStampsTheFacts proves the stamp: the same flow under a
// MARKETPLACE policy carries that notifier and version 1, and the group
// aggregate is byte-identical to the default-policy flow (the notice path
// touches no order field, ADR 0017).
func TestAPolicyVersionStampsTheFacts(t *testing.T) {
	def, woke := newFx(t), newFx(t)
	publishPolicy(t, woke, notice.NotifierMarketplace)
	defID, wokeID := def.place(), woke.place()
	for _, step := range []struct {
		f  *fx
		id string
	}{{def, defID}, {woke, wokeID}} {
		lockGroup1(t, step.f, step.id)
		shipGroup1(t, step.f, step.id)
	}
	a, b := noticesOf(t, def), noticesOf(t, woke)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("facts = %d and %d", len(a), len(b))
	}
	if b[0].Notifier != notice.NotifierMarketplace || b[0].PolicyVersion != 1 || b[0].NoticeKey != a[0].NoticeKey {
		t.Fatalf("stamps = %+v vs %+v", a[0], b[0])
	}
	if a[0].Notifier != notice.NotifierNobody || a[0].PolicyVersion != 0 {
		t.Fatalf("default = %+v", a[0])
	}
	// The aggregates are byte-identical: the policy changes only the stamp.
	left, _ := json.Marshal(def.order(defID).Snapshot())
	right, _ := json.Marshal(woke.order(wokeID).Snapshot())
	if string(left) != string(right) {
		t.Fatalf("the notice path must not touch the aggregate:\n%s\n%s", left, right)
	}
}

// TestTheDelayFamilyFiresOnANewRevisionOnly proves DELAY_EXPECTED: a newer
// promise window decides one fact whose fact is the revised deliverByLatest; a
// replay of the same revision decides nothing (ADR 0017).
func TestTheDelayFamilyFiresOnANewRevisionOnly(t *testing.T) {
	f := newFx(t)
	id := f.place()
	c := f.consumer()
	promise := f.order(id).Groups()[0].PromiseID
	in := usecases.PromiseRevised{PromiseID: promise, OrderID: id, RevisionNo: 2, DeliverByEarliest: at(1300), DeliverByLatest: at(1600), ShipBy: at(700), NodeID: "fc-aus-1"}
	must(t, c.ApplyPromiseRevised(bg(), f.meta("promise.Revised"), in))
	got := noticesOf(t, f)
	if len(got) != 1 || got[0].Family != notice.FamilyDelayExpected || !got[0].Fact.DeliverByLatest.Equal(at(1600)) {
		t.Fatalf("revised = %+v", got)
	}
	// The group carries exactly the revision: no promise field drifted.
	g := f.order(id).Groups()[0]
	if g.RevisionNo != 2 || !g.DeliverByLatest.Equal(at(1600)) || !g.DeliverByEarliest.Equal(at(1300)) || !g.ShipBy.Equal(at(700)) {
		t.Fatalf("group = %+v", g)
	}
	must(t, c.ApplyPromiseRevised(bg(), f.meta("promise.Revised2"), in)) // same revisionNo: no new fact
	if again := noticesOf(t, f); len(again) != 1 {
		t.Fatalf("a replayed revision decides nothing: %+v", again)
	}
}

// TestTheEtaFamilyDedupesOnTheStoredWindow proves ETA_WINDOW_ASSIGNED: the
// first window decides, the same window again does not, a re-assigned window
// does (ADR 0017).
func TestTheEtaFamilyDedupesOnTheStoredWindow(t *testing.T) {
	f := newFx(t)
	id := f.place()
	lockGroup1(t, f, id)
	shipGroup1(t, f, id)
	c := f.consumer()
	eta := func(start, end int, at time.Time) usecases.PackageEtaWindowAssigned {
		return usecases.PackageEtaWindowAssigned{TrackingNumber: "TRK-1", AssignmentID: "as-1", OrderID: id, EtaStart: atMinute(start), EtaEnd: atMinute(end), AssignedAt: at}
	}
	must(t, c.ApplyPackageEta(bg(), f.meta("shipping.Eta"), eta(2000, 2060, at(40))))
	got := familyNotices(t, f, notice.FamilyEtaWindowAssigned)
	if len(got) != 1 || !got[0].Fact.EtaStart.Equal(atMinute(2000)) || !got[0].Fact.EtaEnd.Equal(atMinute(2060)) {
		t.Fatalf("eta = %+v", got)
	}
	// The same window again (a different CloudEvent id): not a new fact.
	must(t, c.ApplyPackageEta(bg(), f.meta("shipping.Eta2"), eta(2000, 2060, at(41))))
	if again := familyNotices(t, f, notice.FamilyEtaWindowAssigned); len(again) != 1 {
		t.Fatalf("the same window decides nothing new: %+v", again)
	}
	// A re-assigned window is a new fact with a new noticeKey.
	must(t, c.ApplyPackageEta(bg(), f.meta("shipping.Eta3"), eta(2100, 2160, at(42))))
	all := familyNotices(t, f, notice.FamilyEtaWindowAssigned)
	if len(all) != 2 || all[0].NoticeKey == all[1].NoticeKey {
		t.Fatalf("a re-assigned window is a new fact: %+v", all)
	}
}

// TestThePointFlagFamilyDecidesPerReason proves DELIVERY_POINT_ADVISORY: the
// advisory decides once per reason, a repeat of the same reason does not
// (ADR 0017).
func TestThePointFlagFamilyDecidesPerReason(t *testing.T) {
	f := newFx(t)
	id := f.place()
	lockGroup1(t, f, id)
	shipGroup1(t, f, id)
	c := f.consumer()
	flag := func(reason string, at time.Time) usecases.PackageDeliveryPointFlagged {
		return usecases.PackageDeliveryPointFlagged{TrackingNumber: "TRK-1", AssignmentID: "as-1", OrderID: id, Reason: reason, FlaggedAt: at}
	}
	must(t, c.ApplyDeliveryPointFlag(bg(), f.meta("shipping.Flag"), flag("NOT_HOME", at(50))))
	got := familyNotices(t, f, notice.FamilyDeliveryPointAdvisory)
	if len(got) != 1 || got[0].Fact.Reason != "NOT_HOME" {
		t.Fatalf("flag = %+v", got)
	}
	must(t, c.ApplyDeliveryPointFlag(bg(), f.meta("shipping.Flag2"), flag("NOT_HOME", at(51))))
	if again := familyNotices(t, f, notice.FamilyDeliveryPointAdvisory); len(again) != 1 {
		t.Fatalf("the same advisory decides nothing new: %+v", again)
	}
	must(t, c.ApplyDeliveryPointFlag(bg(), f.meta("shipping.Flag3"), flag("ACCESS_PROBLEM", at(52))))
	if all := familyNotices(t, f, notice.FamilyDeliveryPointAdvisory); len(all) != 2 {
		t.Fatalf("another advisory is a new fact: %+v", all)
	}
}

// TestAnUnattributableFactDecidesNothing proves the omission semantics: an ETA
// window whose tracking number and assignment are both unknown emits no notice
// (ADR 0017), while the read model still records the window.
func TestAnUnattributableFactDecidesNothing(t *testing.T) {
	f := newFx(t)
	id := f.place()
	must(t, f.consumer().ApplyPackageEta(bg(), f.meta("shipping.Eta"), usecases.PackageEtaWindowAssigned{TrackingNumber: "TRK-X", OrderID: id, EtaStart: atMinute(2000), EtaEnd: atMinute(2060), AssignedAt: at(40)}))
	if got := noticesOf(t, f); len(got) != 0 {
		t.Fatalf("unattributable = %+v", got)
	}
	pkgs, err := f.store.Tracking().Packages(bg(), id)
	must(t, err)
	if len(pkgs) == 0 || pkgs[0].EtaStart.IsZero() {
		t.Fatalf("the read model must still record the window: %+v", pkgs)
	}
}

func atMinute(m int) time.Time { return at(m) }

// TestALostGroupDecidesTheShippedAndLostFacts pins the A19 path: a loss that
// arrives while the group is already shipped decides GROUP_LOST only (the
// shipment already decided its own fact).
func TestALostGroupDecidesTheShippedAndLostFacts(t *testing.T) {
	f := newFx(t)
	id := f.place()
	lockGroup1(t, f, id)
	shipGroup1(t, f, id)
	must(t, f.consumer().ApplyPackageLost(bg(), f.meta("shipping.Lost"), usecases.PackageLost{TrackingNumber: "TRK-1", OrderID: id, DeclaredAt: at(60)}))
	got := noticesOf(t, f)
	if len(got) != 2 || got[0].Family != notice.FamilyGroupShipped || got[1].Family != notice.FamilyGroupLost || got[1].Fact.Status != "LOST" {
		t.Fatalf("lost = %+v", got)
	}
	if f.groupStatus(id, 1) != customerorder.GroupLost {
		t.Fatalf("group 1 = %s", f.groupStatus(id, 1))
	}
}
