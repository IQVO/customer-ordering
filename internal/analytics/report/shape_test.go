package report_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/analytics/report"
)

func day(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC) }

func ptr(f float64) *float64 { return &f }

func TestBuildOfNothingIsAnEmptyNonNilSlice(t *testing.T) {
	for _, rows := range [][]report.OrderingRow{report.Build(nil, nil, nil), report.Build([]report.PlacedDay{}, []report.CancelledDay{}, []report.NoticeDay{})} {
		if rows == nil || len(rows) != 0 {
			t.Fatalf("rows = %#v, want empty non-nil", rows)
		}
	}
}

func TestBuildMergesTheThreeAggregatesPerDayInOrder(t *testing.T) {
	rows := report.Build(
		[]report.PlacedDay{{Day: day(9), Orders: 4, Consolidated: 1, Pickup: 2, Split: 1}, {Day: day(7), Orders: 2, Split: 2}},
		[]report.CancelledDay{{Day: day(9), Orders: 3}, {Day: day(8), Orders: 1}},
		[]report.NoticeDay{{Day: day(8), Family: "GROUP_LOST", Count: 2}, {Day: day(8), Family: "GROUP_SHIPPED", Count: 5}, {Day: day(7), Family: "DELAY_EXPECTED", Count: 1}},
	)
	want := []report.OrderingRow{
		{Day: day(7), OrdersPlaced: 2, SplitShipmentOrders: 2, SplitShipmentShare: ptr(1), NoticesDecided: 1,
			NoticesByKind: []report.KindCount{{Kind: "DELAY_EXPECTED", Count: 1}}},
		// A day with a cancellation and notices but no placed order: no share.
		{Day: day(8), OrdersCancelled: 1, NoticesDecided: 7,
			NoticesByKind: []report.KindCount{{Kind: "GROUP_SHIPPED", Count: 5}, {Kind: "GROUP_LOST", Count: 2}}},
		{Day: day(9), OrdersPlaced: 4, OrdersCancelled: 3, ConsolidatedTierOrders: 1, PickupPointOrders: 2, SplitShipmentOrders: 1,
			SplitShipmentShare: ptr(0.25), NoticesByKind: []report.KindCount{}},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows =\n%+v\nwant\n%+v", rows, want)
	}
}

func TestBuildBoundsTheKindsAndKeepsTheTotalExact(t *testing.T) {
	var notices []report.NoticeDay
	for i, f := range []string{"A", "B", "C", "D", "E", "F", "G"} {
		notices = append(notices, report.NoticeDay{Day: day(1), Family: f, Count: 10 - i}) // 10 9 8 7 6 5 4
	}
	rows := report.Build(nil, nil, notices)
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	r := rows[0]
	if len(r.NoticesByKind) != 5 || r.NoticesByKind[4].Kind != "E" || r.NoticesOther != 9 || r.NoticesDecided != 49 {
		t.Fatalf("row = %+v", r)
	}
}

func TestBoundKinds(t *testing.T) {
	in := []report.KindCount{{"B", 2}, {"A", 2}, {"C", 9}, {"D", 1}}
	cases := []struct {
		limit     int
		wantTop   []report.KindCount
		wantOther int
	}{
		{0, []report.KindCount{}, 14},
		{1, []report.KindCount{{"C", 9}}, 5},
		{3, []report.KindCount{{"C", 9}, {"A", 2}, {"B", 2}}, 1}, // ties by kind
		{4, []report.KindCount{{"C", 9}, {"A", 2}, {"B", 2}, {"D", 1}}, 0},
		{5, []report.KindCount{{"C", 9}, {"A", 2}, {"B", 2}, {"D", 1}}, 0},
	}
	for _, c := range cases {
		top, other := report.BoundKinds(in, c.limit)
		if !reflect.DeepEqual(top, c.wantTop) || other != c.wantOther {
			t.Errorf("BoundKinds(limit %d) = %v, %d; want %v, %d", c.limit, top, other, c.wantTop, c.wantOther)
		}
	}
	if !reflect.DeepEqual(in, []report.KindCount{{"B", 2}, {"A", 2}, {"C", 9}, {"D", 1}}) {
		t.Fatalf("BoundKinds modified its input: %v", in)
	}
	if top, other := report.BoundKinds(nil, 5); top == nil || len(top) != 0 || other != 0 {
		t.Fatalf("empty = %#v, %d", top, other)
	}
}

func TestShare(t *testing.T) {
	for _, c := range []struct{ num, den int }{{0, 0}, {3, 0}, {1, -1}} {
		if s := report.Share(c.num, c.den); s != nil {
			t.Errorf("Share(%d, %d) = %v, want nil", c.num, c.den, *s)
		}
	}
	if s := report.Share(0, 3); s == nil || *s != 0 {
		t.Errorf("Share(0, 3) = %v, want 0", s)
	}
	if s := report.Share(1, 4); s == nil || *s != 0.25 {
		t.Errorf("Share(1, 4) = %v, want 0.25", s)
	}
	if s := report.Share(2, 2); s == nil || *s != 1 {
		t.Errorf("Share(2, 2) = %v, want 1", s)
	}
}

func TestComputeFreshness(t *testing.T) {
	if f := report.ComputeFreshness(nil, now); f.AsOf != nil || f.LagSeconds != nil {
		t.Fatalf("nothing applied must give nil/nil, got %+v", f)
	}
	asOf := time.Date(2026, 11, 8, 18, 29, 0, 0, time.UTC) // now is 18:30 UTC
	f := report.ComputeFreshness(&asOf, now)
	if f.AsOf == nil || !f.AsOf.Equal(asOf) || f.AsOf.Location() != time.UTC || f.LagSeconds == nil || *f.LagSeconds != 60 {
		t.Fatalf("freshness = %+v", f)
	}
	future := now.Add(time.Minute)
	if f := report.ComputeFreshness(&future, now); *f.LagSeconds != 0 {
		t.Fatalf("a clock behind the event must read zero lag, got %v", *f.LagSeconds)
	}
}
