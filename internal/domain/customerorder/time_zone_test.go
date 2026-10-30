package customerorder

import (
	"testing"

	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// The delivery window's time zone (ADR 0009): optional, carried from the
// promise into the group and CustomerOrderPlaced, moved by a revision that
// names one, validated in shape only.

func zonedOrder() NewOrder {
	in := twoGroups()
	in.Groups[0].TimeZone = "America/Chicago"
	return in
}

func TestPlaceCarriesTheWindowTimeZoneToTheGroupAndTheEvent(t *testing.T) {
	o, evs, err := Place(zonedOrder(), t0)
	mustNil(t, err)
	if g := o.Groups(); g[0].TimeZone != "America/Chicago" || g[1].TimeZone != "" {
		t.Fatalf("group zones = %q %q, want America/Chicago and empty (unknown)", g[0].TimeZone, g[1].TimeZone)
	}
	placed, ok := evs[0].(CustomerOrderPlaced)
	if !ok {
		t.Fatalf("event = %T", evs[0])
	}
	if placed.Groups[0].TimeZone != "America/Chicago" || placed.Groups[1].TimeZone != "" {
		t.Fatalf("event zones = %q %q", placed.Groups[0].TimeZone, placed.Groups[1].TimeZone)
	}
}

func TestPlaceRejectsAMalformedTimeZone(t *testing.T) {
	for _, zone := range []string{" America/Chicago", "America Chicago", "-UTC", "a" + string(make([]byte, shared.MaxTimeZone))} {
		in := twoGroups()
		in.Groups[1].TimeZone = zone
		if _, _, err := Place(in, t0); !isValidation(err) {
			t.Errorf("timeZone %q: want ErrValidation, got %v", zone, err)
		}
	}
}

func TestUpdateGroupPromiseMovesTheTimeZoneOnlyWhenNamed(t *testing.T) {
	o := mustPlace(t, zonedOrder())
	mustNil(t, o.UpdateGroupPromise("p-1", 1, at(2000), at(2300), at(1900), "", ""))
	if z := o.Groups()[0].TimeZone; z != "America/Chicago" {
		t.Fatalf("a revision without a zone must keep the group's own, got %q", z)
	}
	mustNil(t, o.UpdateGroupPromise("p-2", 1, at(2000), at(2300), at(1900), "", "America/Denver"))
	if z := o.Groups()[1].TimeZone; z != "America/Denver" {
		t.Fatalf("a revision naming a zone must store it, got %q", z)
	}
	v := o.Version()
	mustNil(t, o.UpdateGroupPromise("p-2", 1, at(2000), at(2300), at(1900), "", "America/Chicago"))
	if z := o.Groups()[1].TimeZone; z != "America/Denver" {
		t.Fatalf("a stale revision must not move the zone, got %q", z)
	}
	assertVersion(t, o, v)
	if err := o.UpdateGroupPromise("p-1", 2, at(2000), at(2300), at(1900), "", "not a zone"); !isValidation(err) {
		t.Fatalf("a malformed zone: want ErrValidation, got %v", err)
	}
	if g := o.Groups()[0]; g.RevisionNo != 1 || g.TimeZone != "America/Chicago" {
		t.Fatalf("a rejected revision changed the group: %+v", g)
	}
}

func TestRestoreKeepsTheTimeZoneAndRejectsAMalformedOne(t *testing.T) {
	o := mustPlace(t, zonedOrder())
	back, err := Restore(o.Snapshot())
	mustNil(t, err)
	if z := back.Groups()[0].TimeZone; z != "America/Chicago" {
		t.Fatalf("restored zone = %q", z)
	}
	snap := o.Snapshot()
	snap.Groups[1].TimeZone = "bad zone"
	if _, err := Restore(snap); !errorsIs(err, shared.ErrInvalidSnapshot) {
		t.Fatalf("want ErrInvalidSnapshot, got %v", err)
	}
}
