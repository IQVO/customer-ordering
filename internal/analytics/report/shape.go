package report

import (
	"cmp"
	"math"
	"slices"
	"time"
)

// KindCount is the notices decided of one family on one day.
type KindCount struct {
	Kind  string
	Count int
}

// OrderingRow is one UTC day of the ordering performance report. Days are
// sparse: a day with no placed order, no cancellation and no notice has no row.
type OrderingRow struct {
	Day                    time.Time
	OrdersPlaced           int
	OrdersCancelled        int
	ConsolidatedTierOrders int
	PickupPointOrders      int
	SplitShipmentOrders    int
	// SplitShipmentShare is SplitShipmentOrders / OrdersPlaced; nil on a day
	// without placed orders (nothing to take a share of).
	SplitShipmentShare *float64
	NoticesDecided     int
	// NoticesByKind lists at most MaxNoticeKinds kinds, most frequent first
	// (ties by kind); NoticesOther counts the notices of the kinds left out.
	NoticesByKind []KindCount
	NoticesOther  int
}

// dayFacts accumulates the facts of one day while Build merges the readers.
type dayFacts struct {
	placed    PlacedDay
	cancelled int
	notices   []KindCount
}

// Build merges the three per-day aggregates into the report rows, ordered by
// day. A nil or empty input yields an empty, non-nil slice.
func Build(placed []PlacedDay, cancelled []CancelledDay, notices []NoticeDay) []OrderingRow {
	days := map[time.Time]*dayFacts{}
	at := func(d time.Time) *dayFacts {
		f, ok := days[d]
		if !ok {
			f = &dayFacts{}
			days[d] = f
		}
		return f
	}
	for _, p := range placed {
		at(p.Day).placed = p
	}
	for _, c := range cancelled {
		at(c.Day).cancelled += c.Orders
	}
	for _, n := range notices {
		f := at(n.Day)
		f.notices = append(f.notices, KindCount{Kind: n.Family, Count: n.Count})
	}
	out := make([]OrderingRow, 0, len(days))
	for day, f := range days {
		out = append(out, f.row(day))
	}
	slices.SortFunc(out, func(a, b OrderingRow) int { return a.Day.Compare(b.Day) })
	return out
}

func (f *dayFacts) row(day time.Time) OrderingRow {
	top, other := BoundKinds(f.notices, MaxNoticeKinds())
	total := other
	for _, k := range top {
		total += k.Count
	}
	return OrderingRow{
		Day:                    day,
		OrdersPlaced:           f.placed.Orders,
		OrdersCancelled:        f.cancelled,
		ConsolidatedTierOrders: f.placed.Consolidated,
		PickupPointOrders:      f.placed.Pickup,
		SplitShipmentOrders:    f.placed.Split,
		SplitShipmentShare:     Share(f.placed.Split, f.placed.Orders),
		NoticesDecided:         total,
		NoticesByKind:          top,
		NoticesOther:           other,
	}
}

// BoundKinds lists at most limit kinds, most frequent first (ties by kind),
// and returns the number of notices of the kinds left out. The input is not
// modified; the result is never nil.
func BoundKinds(counts []KindCount, limit int) (top []KindCount, other int) {
	sorted := slices.Clone(counts)
	slices.SortFunc(sorted, func(a, b KindCount) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Kind, b.Kind))
	})
	cut := min(limit, len(sorted))
	for _, k := range sorted[cut:] {
		other += k.Count
	}
	return append([]KindCount{}, sorted[:cut]...), other
}

// Share is num/den, or nil when den is not positive (nothing to take a share
// of): an empty day reports no share, not a misleading zero.
func Share(num, den int) *float64 {
	if den <= 0 {
		return nil
	}
	s := float64(num) / float64(den)
	return &s
}

// Freshness is the projection's freshness: AsOf is the CloudEvents time of the
// newest event applied and LagSeconds is now - AsOf. Both are nil while the
// projection has applied nothing.
type Freshness struct {
	AsOf       *time.Time
	LagSeconds *float64
}

// ComputeFreshness derives the lag from the newest applied event time. A clock
// that is behind the event (skew) reads as zero lag, never negative.
func ComputeFreshness(asOf *time.Time, now time.Time) Freshness {
	if asOf == nil {
		return Freshness{}
	}
	// math.Max, not `if lag < 0`: both yield 0 at the boundary, which would
	// leave an equivalent `<` -> `<=` mutant.
	lag := math.Max(0, now.Sub(*asOf).Seconds())
	utc := asOf.UTC()
	return Freshness{AsOf: &utc, LagSeconds: &lag}
}
