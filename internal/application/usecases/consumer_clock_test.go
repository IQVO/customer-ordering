package usecases_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/usecases"
)

// A consumed CloudEvent whose `time` the producer omitted falls back to the
// service clock: the lock is stamped with the clock reading, not the zero time
// (the Meta and the event payload both carry no time here).
func TestAFactWithoutATimeUsesTheClock(t *testing.T) {
	f := newFx(t)
	id := f.place()
	when := at(700)
	f.clock.Set(when)
	m := f.meta("FulfillmentLocked") // At stays zero below
	m.At = time.Time{}
	must(f.t, f.consumer().ApplyFulfillmentLocked(context.Background(), m, usecases.FulfillmentLocked{
		AssignmentID: "as-9", OrderID: id, GroupNo: 1, NodeID: "fc-aus-1",
	}))
	for _, g := range f.order(id).Groups() {
		if g.GroupNo == 1 && !g.LockedAt.Equal(when) {
			t.Fatalf("group 1 LockedAt = %v, want the clock reading %v", g.LockedAt, when)
		}
	}
}

// A promise revision whose producer omitted ShipBy keeps the group's own
// ship-by deadline; only the delivery window and revision move.
func TestARevisionWithoutShipByKeepsTheGroupsOwnShipBy(t *testing.T) {
	f := newFx(t)
	id := f.place()
	var before int64
	for _, g := range f.order(id).Groups() {
		if g.GroupNo == 1 {
			before = g.ShipBy.Unix()
		}
	}
	must(f.t, f.consumer().ApplyPromiseRevised(context.Background(), f.meta("PromiseRevised"), usecases.PromiseRevised{
		PromiseID: "p-1-1", OrderID: id, RevisionNo: 2,
		DeliverByEarliest: at(1300), DeliverByLatest: at(1700),
	}))
	for _, g := range f.order(id).Groups() {
		if g.GroupNo == 1 {
			if got := g.ShipBy.Unix(); got != before {
				t.Fatalf("group 1 ShipBy = %d, want the group's own %d (the producer omitted it)", got, before)
			}
			if !g.DeliverByLatest.Equal(at(1700)) || g.RevisionNo != 2 {
				t.Fatalf("group 1 = revision %d latest %v, want revision 2 and %v", g.RevisionNo, g.DeliverByLatest, at(1700))
			}
		}
	}
}
