package customerorder

import (
	"errors"
	"strings"
	"testing"

	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

var goodKey = strings.Repeat("ab12", 16)

func TestValidConsolidationKey(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want bool
	}{
		{"empty means not derived", "", true},
		{"64 lower-case hex", goodKey, true},
		{"all digits", strings.Repeat("0", 64), true},
		{"63 characters", goodKey[:63], false},
		{"65 characters", goodKey + "a", false},
		{"upper-case hex", strings.ToUpper(goodKey), false},
		{"a non-hex letter", goodKey[:63] + "g", false},
		{"a space", goodKey[:63] + " ", false},
		{"a character below the digits", goodKey[:63] + "/", false},
		{"a character between digits and letters", goodKey[:63] + ":", false},
	}
	for _, c := range cases {
		if got := ValidConsolidationKey(c.key); got != c.want {
			t.Errorf("%s: ValidConsolidationKey(%q) = %v, want %v", c.name, c.key, got, c.want)
		}
	}
}

func TestPlaceCarriesTheConsolidationKeyToOrderSnapshotAndEvent(t *testing.T) {
	in := oneGroup()
	in.ConsolidationKey = goodKey
	o, events, err := Place(in, t0)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if o.ConsolidationKey() != goodKey || o.Snapshot().ConsolidationKey != goodKey {
		t.Fatalf("order key = %q, snapshot key = %q, want %q", o.ConsolidationKey(), o.Snapshot().ConsolidationKey, goodKey)
	}
	placed, ok := events[0].(CustomerOrderPlaced)
	if !ok || placed.ConsolidationKey != goodKey {
		t.Fatalf("CustomerOrderPlaced = %+v, want key %q", events[0], goodKey)
	}
}

func TestPlaceWithoutAKeyLeavesItEmpty(t *testing.T) {
	o, events, err := Place(oneGroup(), t0)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if o.ConsolidationKey() != "" || events[0].(CustomerOrderPlaced).ConsolidationKey != "" {
		t.Fatalf("an order without a key carries %q / %q", o.ConsolidationKey(), events[0].(CustomerOrderPlaced).ConsolidationKey)
	}
}

func TestPlaceRejectsAMalformedKey(t *testing.T) {
	in := oneGroup()
	in.ConsolidationKey = "not-a-key"
	if _, _, err := Place(in, t0); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("Place with a malformed key = %v, want ErrValidation", err)
	}
}

func TestRestoreKeepsAndChecksTheKey(t *testing.T) {
	in := oneGroup()
	in.ConsolidationKey = goodKey
	snap := mustPlace(t, in).Snapshot()
	got, err := Restore(snap)
	if err != nil || got.ConsolidationKey() != goodKey {
		t.Fatalf("Restore = %v, key %q, want %q", err, got.ConsolidationKey(), goodKey)
	}
	snap.ConsolidationKey = "XYZ"
	if _, err := Restore(snap); !errors.Is(err, shared.ErrInvalidSnapshot) {
		t.Fatalf("Restore with a malformed key = %v, want ErrInvalidSnapshot", err)
	}
}
