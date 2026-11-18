package analyticsstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/claudioed/customer-ordering/internal/analytics/report"
)

// MemoryStore is the in-memory twin of Projection and Reader, for unit tests
// of the consumer and the reports. contract_test.go runs the SAME suite
// against it and against Postgres, so its behaviour cannot drift from the real
// store. The shipped binaries never construct it.
type MemoryStore struct {
	mu            sync.Mutex
	processed     map[string]time.Time
	placed        map[string]memPlaced
	cancellations []memCancellation
	notices       map[string]memNotice
	// Fail, when set, is returned by the next Apply (and cleared), to drive
	// the consumer's transient and rejection paths.
	Fail error
}

type memPlaced struct {
	at               time.Time
	groups, consolid int
	pickup           bool
}

type memCancellation struct {
	order string
	at    time.Time
}

type memNotice struct {
	family string
	at     time.Time
}

// NewMemoryStore returns an empty twin.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{processed: map[string]time.Time{}, placed: map[string]memPlaced{}, notices: map[string]memNotice{}}
}

var (
	_ report.Projection = (*MemoryStore)(nil)
	_ report.Reader     = (*MemoryStore)(nil)
)

// Apply implements report.Projection with the Postgres store's semantics: an
// event the schema's CHECKs would refuse is rejected before anything is
// recorded, a replay is a no-op, an order is placed once and a notice counts
// once per notice key.
func (m *MemoryStore) Apply(_ context.Context, e report.Event) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		err := m.Fail
		m.Fail = nil
		return false, err
	}
	if err := e.Validate(); err != nil {
		return false, fmt.Errorf("%w: %w", report.ErrRejected, err)
	}
	if _, seen := m.processed[e.EventID]; seen {
		return false, nil
	}
	m.processed[e.EventID] = e.At.UTC()
	at := e.OccurredAt.UTC()
	switch e.Kind {
	case report.KindOrderPlaced:
		if _, placed := m.placed[e.OrderID]; !placed {
			m.placed[e.OrderID] = memPlaced{at: at, groups: e.GroupCount, consolid: e.ConsolidatedGroups, pickup: e.PickupPoint}
		}
	case report.KindOrderCancelled:
		m.cancellations = append(m.cancellations, memCancellation{order: e.OrderID, at: at})
	default: // Validate admitted only the notice kind here
		if _, known := m.notices[e.NoticeKey]; !known {
			m.notices[e.NoticeKey] = memNotice{family: report.NormalizeFamily(e.Family), at: at}
		}
	}
	return true, nil
}

func utcDay(t time.Time) time.Time { return t.UTC().Truncate(24 * time.Hour) }

func inRange(r report.Range, at time.Time) bool { return at.Compare(r.From) >= 0 && at.Before(r.To) }

// PlacedOrders implements report.Reader.
func (m *MemoryStore) PlacedOrders(_ context.Context, r report.Range) ([]report.PlacedDay, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	days := map[time.Time]*report.PlacedDay{}
	for _, p := range m.placed {
		if !inRange(r, p.at) {
			continue
		}
		d := days[utcDay(p.at)]
		if d == nil {
			d = &report.PlacedDay{Day: utcDay(p.at)}
			days[d.Day] = d
		}
		d.Orders++
		if p.consolid > 0 {
			d.Consolidated++
		}
		if p.pickup {
			d.Pickup++
		}
		if report.IsSplit(p.groups) {
			d.Split++
		}
	}
	out := make([]report.PlacedDay, 0, len(days))
	for _, d := range days {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
	return out, nil
}

// CancelledOrders implements report.Reader: DISTINCT orders per day.
func (m *MemoryStore) CancelledOrders(_ context.Context, r report.Range) ([]report.CancelledDay, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	orders := map[time.Time]map[string]bool{}
	for _, c := range m.cancellations {
		if !inRange(r, c.at) {
			continue
		}
		day := utcDay(c.at)
		if orders[day] == nil {
			orders[day] = map[string]bool{}
		}
		orders[day][c.order] = true
	}
	out := make([]report.CancelledDay, 0, len(orders))
	for day, set := range orders {
		out = append(out, report.CancelledDay{Day: day, Orders: len(set)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
	return out, nil
}

// NoticesDecided implements report.Reader.
func (m *MemoryStore) NoticesDecided(_ context.Context, r report.Range) ([]report.NoticeDay, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	type key struct {
		day    time.Time
		family string
	}
	counts := map[key]int{}
	for _, n := range m.notices {
		if inRange(r, n.at) {
			counts[key{utcDay(n.at), n.family}]++
		}
	}
	out := make([]report.NoticeDay, 0, len(counts))
	for k, n := range counts {
		out = append(out, report.NoticeDay{Day: k.day, Family: k.family, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Day.Equal(out[j].Day) {
			return out[i].Day.Before(out[j].Day)
		}
		return out[i].Family < out[j].Family
	})
	return out, nil
}

// LastEventAt implements report.Reader.
func (m *MemoryStore) LastEventAt(context.Context) (*time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var last *time.Time
	for _, at := range m.processed {
		if last == nil || at.After(*last) {
			a := at
			last = &a
		}
	}
	return last, nil
}

// Ping is the twin's readiness check: always ready.
func (m *MemoryStore) Ping(context.Context) error { return nil }
