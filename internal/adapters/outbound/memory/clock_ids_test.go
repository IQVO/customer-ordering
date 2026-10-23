package memory_test

import (
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/memory"
)

// The settable clock is what every time-sensitive double is driven with; Set
// moves it absolutely, Advance relatively, and Now keeps returning the last
// reading between moves.
func TestClockSetAndAdvance(t *testing.T) {
	t0 := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	c := memory.NewClock(t0)
	if !c.Now().Equal(t0) {
		t.Fatalf("Now() = %v, want the initial reading %v", c.Now(), t0)
	}
	later := t0.Add(2 * time.Hour)
	c.Set(later)
	if !c.Now().Equal(later) {
		t.Fatalf("Now() after Set = %v, want %v", c.Now(), later)
	}
	c.Advance(90 * time.Minute)
	if !c.Now().Equal(later.Add(90 * time.Minute)) {
		t.Fatalf("Now() after Advance = %v, want %v", c.Now(), later.Add(90*time.Minute))
	}
	// A negative duration moves the clock back: it is a reading, not a wall.
	c.Advance(-time.Hour)
	if !c.Now().Equal(later.Add(30 * time.Minute)) {
		t.Fatalf("Now() after a negative Advance = %v, want %v", c.Now(), later.Add(30*time.Minute))
	}
}

// OrderIDFor derives the order id of a checkout from the Idempotency-Key the
// shopper sent ("order-<key>"), which is how replayed checkouts address the
// same aggregate.
func TestSequentialIDsOrderIDForKey(t *testing.T) {
	g := &memory.SequentialIDs{}
	if got, want := string(g.OrderIDFor("abc-123")), "order-abc-123"; got != want {
		t.Fatalf("OrderIDFor = %q, want %q", got, want)
	}
	// deriving an order id mints no shopper id and consumes no sequence
	if got, want := string(g.NewShopperID()), "shopper-1"; got != want {
		t.Fatalf("NewShopperID = %q, want %q after order derivations", got, want)
	}
}
