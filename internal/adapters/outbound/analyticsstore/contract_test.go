package analyticsstore_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/analytics/report"
)

// store is what both twins implement: the writer and the reader port.
type store interface {
	report.Projection
	report.Reader
}

// runContract is THE contract of the analytical store (ADR 0018). It runs
// against MemoryStore (memory_test.go) and against real Postgres
// (postgres_integration_test.go); newStore returns an EMPTY store.
func runContract(t *testing.T, newStore func(t *testing.T) store) {
	t.Run("an empty store answers empty, non-nil aggregates", func(t *testing.T) { contractEmpty(t, newStore(t)) })
	t.Run("a replayed id is a no-op", func(t *testing.T) { contractReplay(t, newStore(t)) })
	t.Run("placed orders per day", func(t *testing.T) { contractPlaced(t, newStore(t)) })
	t.Run("cancelled orders count distinct orders per day", func(t *testing.T) { contractCancelled(t, newStore(t)) })
	t.Run("notices per day and family", func(t *testing.T) { contractNotices(t, newStore(t)) })
	t.Run("the freshness basis is the newest CloudEvents time", func(t *testing.T) { contractLastEvent(t, newStore(t)) })
	t.Run("an invalid fact is rejected and leaves nothing", func(t *testing.T) { contractRejected(t, newStore(t)) })
}

var (
	ctx       = context.Background()
	nov3      = time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	nov4      = nov3.Add(24 * time.Hour)
	fullRange = report.Range{From: nov3, To: nov3.Add(48 * time.Hour)}
)

// seeder applies facts; every event gets a fresh id and a CloudEvents time one
// minute after its business instant.
type seeder struct {
	t   *testing.T
	s   store
	seq int
}

func (d *seeder) apply(e report.Event) {
	d.t.Helper()
	d.seq++
	e.EventID = fmt.Sprintf("evt-%d", d.seq)
	e.At = e.OccurredAt.Add(time.Minute)
	if applied, err := d.s.Apply(ctx, e); err != nil || !applied {
		d.t.Fatalf("apply %s %s: applied=%v err=%v", e.Kind, e.EventID, applied, err)
	}
}

func (d *seeder) placed(order string, at time.Time, groups, consolidated int, pickup bool) {
	d.t.Helper()
	d.apply(report.Event{Kind: report.KindOrderPlaced, OrderID: order, OccurredAt: at, GroupCount: groups, ConsolidatedGroups: consolidated, PickupPoint: pickup})
}

func (d *seeder) cancelled(order string, at time.Time) {
	d.t.Helper()
	d.apply(report.Event{Kind: report.KindOrderCancelled, OrderID: order, OccurredAt: at})
}

func (d *seeder) notice(order, key, family string, at time.Time) {
	d.t.Helper()
	d.apply(report.Event{Kind: report.KindNoticeDecided, OrderID: order, OccurredAt: at, Family: family, NoticeKey: key})
}

func contractEmpty(t *testing.T, s store) {
	placed, err := s.PlacedOrders(ctx, fullRange)
	if err != nil || placed == nil || len(placed) != 0 {
		t.Fatalf("placed = %#v, %v; want empty non-nil", placed, err)
	}
	cancelled, err := s.CancelledOrders(ctx, fullRange)
	if err != nil || cancelled == nil || len(cancelled) != 0 {
		t.Fatalf("cancelled = %#v, %v; want empty non-nil", cancelled, err)
	}
	notices, err := s.NoticesDecided(ctx, fullRange)
	if err != nil || notices == nil || len(notices) != 0 {
		t.Fatalf("notices = %#v, %v; want empty non-nil", notices, err)
	}
	if last, err := s.LastEventAt(ctx); err != nil || last != nil {
		t.Fatalf("last = %v, %v; want nil", last, err)
	}
}

func contractReplay(t *testing.T, s store) {
	e := report.Event{Kind: report.KindOrderPlaced, EventID: "evt-1", OrderID: "o1", At: nov3.Add(time.Hour), OccurredAt: nov3, GroupCount: 1}
	if applied, err := s.Apply(ctx, e); err != nil || !applied {
		t.Fatalf("first apply: %v %v", applied, err)
	}
	if applied, err := s.Apply(ctx, e); err != nil || applied {
		t.Fatalf("replay: applied=%v err=%v; want a no-op", applied, err)
	}
	placed, err := s.PlacedOrders(ctx, fullRange)
	if err != nil || len(placed) != 1 || placed[0].Orders != 1 {
		t.Fatalf("placed after a replay = %+v, %v; want one order", placed, err)
	}
}

func contractPlaced(t *testing.T, s store) {
	d := &seeder{t: t, s: s}
	d.placed("o1", nov3.Add(10*time.Hour), 1, 0, false)                                                                     // plain
	d.placed("o2", nov3.Add(11*time.Hour), 2, 1, false)                                                                     // split + consolidated
	d.placed("o3", nov4.Add(-time.Second), 3, 0, true)                                                                      // split + pickup, the last second of nov3
	d.placed("o4", nov4, 1, 1, true)                                                                                        // pickup + consolidated, the first instant of nov4
	d.placed("o5", fullRange.To, 2, 2, true)                                                                                // exactly at To (exclusive): outside
	d.placed("o6", nov3.Add(-time.Second), 2, 0, false)                                                                     // one second before From: outside
	d.apply(report.Event{Kind: report.KindOrderPlaced, OrderID: "o1", OccurredAt: nov3.Add(12 * time.Hour), GroupCount: 5}) // the same order again: counts once
	got, err := s.PlacedOrders(ctx, fullRange)
	if err != nil {
		t.Fatal(err)
	}
	want := []report.PlacedDay{
		{Day: nov3, Orders: 3, Consolidated: 1, Pickup: 1, Split: 2},
		{Day: nov4, Orders: 1, Consolidated: 1, Pickup: 1, Split: 0},
	}
	if len(got) != len(want) {
		t.Fatalf("placed = %+v\nwant %+v", got, want)
	}
	for i := range want {
		if got[i].Day.Location() != time.UTC || !got[i].Day.Equal(want[i].Day) {
			t.Errorf("row %d day = %v, want %v UTC", i, got[i].Day, want[i].Day)
		}
		got[i].Day = want[i].Day
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	one, err := s.PlacedOrders(ctx, report.Range{From: nov4, To: fullRange.To})
	if err != nil || len(one) != 1 || !one[0].Day.Equal(nov4) {
		t.Fatalf("a one-day range = %+v, %v", one, err)
	}
}

func contractCancelled(t *testing.T, s store) {
	d := &seeder{t: t, s: s}
	d.cancelled("o1", nov3.Add(time.Hour))
	d.cancelled("o1", nov3.Add(2*time.Hour)) // a second request for another group of the same order: one order
	d.cancelled("o2", nov3.Add(3*time.Hour))
	d.cancelled("o1", nov4.Add(time.Hour)) // the same order on another day counts on that day
	d.cancelled("o3", fullRange.To)        // outside
	d.cancelled("o4", nov3.Add(-time.Second))
	got, err := s.CancelledOrders(ctx, fullRange)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].Day.Equal(nov3) || got[0].Orders != 2 || !got[1].Day.Equal(nov4) || got[1].Orders != 1 ||
		got[0].Day.Location() != time.UTC {
		t.Fatalf("cancelled = %+v, want nov3:2 nov4:1", got)
	}
}

func contractNotices(t *testing.T, s store) {
	d := &seeder{t: t, s: s}
	d.notice("o1", "k1", "GROUP_LOST", nov3.Add(time.Hour))
	d.notice("o2", "k2", "GROUP_LOST", nov3.Add(2*time.Hour))
	d.notice("o2", "k3", "GROUP_SHIPPED", nov3.Add(3*time.Hour))
	d.notice("o2", "k3", "GROUP_SHIPPED", nov3.Add(4*time.Hour)) // the same notice key under another event id: once
	d.notice("o3", "k4", "A NAME THAT IS NOT A CODE", nov3.Add(5*time.Hour))
	d.notice("o3", "k5", "DELAY_EXPECTED", nov4.Add(time.Hour))
	d.notice("o3", "k6", "DELAY_EXPECTED", fullRange.To) // outside
	got, err := s.NoticesDecided(ctx, fullRange)
	if err != nil {
		t.Fatal(err)
	}
	want := []report.NoticeDay{
		{Day: nov3, Family: "GROUP_LOST", Count: 2},
		{Day: nov3, Family: "GROUP_SHIPPED", Count: 1},
		{Day: nov3, Family: "UNRECOGNISED", Count: 1},
		{Day: nov4, Family: "DELAY_EXPECTED", Count: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("notices = %+v\nwant %+v", got, want)
	}
	for i := range want {
		if got[i].Day.Location() != time.UTC || !got[i].Day.Equal(want[i].Day) {
			t.Errorf("row %d day = %v, want %v UTC", i, got[i].Day, want[i].Day)
		}
		got[i].Day = want[i].Day
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func contractLastEvent(t *testing.T, s store) {
	d := &seeder{t: t, s: s}
	d.placed("o1", nov3.Add(5*time.Hour), 1, 0, false)
	d.cancelled("o1", nov3.Add(2*time.Hour))
	last, err := s.LastEventAt(ctx)
	newest := nov3.Add(5*time.Hour + time.Minute)
	if err != nil || last == nil || !last.Equal(newest) || last.Location() != time.UTC {
		t.Fatalf("last = %v, %v; want %v UTC (the newest, not the last applied)", last, err, newest)
	}
}

func contractRejected(t *testing.T, s store) {
	bad := []report.Event{
		{Kind: report.KindOrderCancelled, EventID: "evt-bad-1", At: nov3, OccurredAt: nov3},                                                  // no order id
		{Kind: report.KindOrderPlaced, EventID: "evt-bad-2", OrderID: "o", At: nov3, OccurredAt: nov3},                                       // no groups
		{Kind: report.KindOrderPlaced, EventID: "evt-bad-3", OrderID: "o", At: nov3, OccurredAt: nov3, GroupCount: 1, ConsolidatedGroups: 2}, // more consolidated than groups
		{Kind: report.KindNoticeDecided, EventID: "evt-bad-4", OrderID: "o", At: nov3, OccurredAt: nov3, Family: "GROUP_LOST"},               // no notice key
		{Kind: "ShopperNoticeRead", EventID: "evt-bad-5", OrderID: "o", At: nov3, OccurredAt: nov3},                                          // unknown kind
	}
	for _, e := range bad {
		if applied, err := s.Apply(ctx, e); applied || !errors.Is(err, report.ErrRejected) {
			t.Errorf("%s: applied=%v err=%v; want ErrRejected", e.EventID, applied, err)
		}
	}
	if last, err := s.LastEventAt(ctx); err != nil || last != nil {
		t.Fatalf("a rejected fact must leave no mark, last = %v, %v", last, err)
	}
}
