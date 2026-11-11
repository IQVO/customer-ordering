package customerorder

import (
	"errors"
	"testing"

	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

func yes() *bool { v := true; return &v }
func no() *bool  { v := false; return &v }

func consolidatedOrder(day string, eligible *bool) NewOrder {
	in := oneGroup()
	g := in.Groups[0]
	g.Tier, g.TimeZone, g.ChosenDeliveryDay, g.ConsolidationEligible = TierConsolidated, "America/Chicago", day, eligible
	in.Groups = []GroupInput{g}
	return in
}

func TestTierAcceptsTheFiveValues(t *testing.T) {
	for _, tier := range []Tier{TierSameDay, TierNextDay, TierTwoDay, TierStandard, TierConsolidated} {
		if !tier.Valid() {
			t.Errorf("%s must be valid", tier)
		}
	}
	for _, tier := range []Tier{"", "consolidated", "WEEKLY", "CONSOLIDATED "} {
		if tier.Valid() {
			t.Errorf("%q must not be valid", tier)
		}
	}
}

func TestPlaceStoresTheChoiceOnAConsolidatedGroup(t *testing.T) {
	o, events, err := Place(consolidatedOrder("2026-10-27", yes()), t0)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	g := o.Groups()[0]
	if g.Tier != TierConsolidated || g.ChosenDeliveryDay != "2026-10-27" || g.ConsolidationEligible == nil || !*g.ConsolidationEligible {
		t.Fatalf("group = %+v", g)
	}
	pg := events[0].(CustomerOrderPlaced).Groups[0]
	if pg.Tier != TierConsolidated || pg.ChosenDeliveryDay != "2026-10-27" || pg.ConsolidationEligible == nil || !*pg.ConsolidationEligible {
		t.Fatalf("placed group = %+v", pg)
	}
}

func TestPlaceKeepsAnUnknownChoiceUnknown(t *testing.T) {
	o, events, err := Place(consolidatedOrder("", nil), t0)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if g := o.Groups()[0]; g.ChosenDeliveryDay != "" || g.ConsolidationEligible != nil {
		t.Fatalf("unknown must stay unknown: %+v", g)
	}
	if pg := events[0].(CustomerOrderPlaced).Groups[0]; pg.ConsolidationEligible != nil {
		t.Fatalf("unknown eligibility must not become false: %+v", pg)
	}
}

func TestPlaceKeepsAFalseEligibilityAsFalse(t *testing.T) {
	o, _, err := Place(consolidatedOrder("2026-10-26", no()), t0)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if g := o.Groups()[0]; g.ConsolidationEligible == nil || *g.ConsolidationEligible {
		t.Fatalf("a stated false must stay false: %+v", g)
	}
}

func TestPlaceRefusesAChoiceOnAnotherTier(t *testing.T) {
	for _, tc := range []struct {
		name     string
		day      string
		eligible *bool
	}{{"a day", "2026-10-27", nil}, {"an eligibility", "", yes()}, {"a false eligibility", "", no()}} {
		in := oneGroup()
		in.Groups[0].ChosenDeliveryDay, in.Groups[0].ConsolidationEligible = tc.day, tc.eligible
		if _, _, err := Place(in, t0); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s on a NEXT_DAY group = %v, want ErrValidation", tc.name, err)
		}
	}
}

func TestPlaceRefusesAMalformedDay(t *testing.T) {
	for _, day := range []string{"2026-1-5", "27/10/2026", "2026-02-30", "2026-10-27T00:00:00Z", " 2026-10-27", "tomorrow"} {
		if _, _, err := Place(consolidatedOrder(day, nil), t0); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("day %q = %v, want ErrValidation", day, err)
		}
	}
}

func TestPlaceDoesNotAliasTheCallersEligibility(t *testing.T) {
	v := true
	in := consolidatedOrder("2026-10-27", &v)
	o, _, err := Place(in, t0)
	if err != nil {
		t.Fatal(err)
	}
	v = false
	if g := o.Groups()[0]; g.ConsolidationEligible == nil || !*g.ConsolidationEligible {
		t.Fatalf("the order aliases the caller's pointer: %+v", g.ConsolidationEligible)
	}
	*o.Groups()[0].ConsolidationEligible = false
	if g := o.Groups()[0]; !*g.ConsolidationEligible {
		t.Fatal("Groups() must return a copy")
	}
}

func TestRestoreKeepsAndChecksTheChoice(t *testing.T) {
	snap := mustPlace(t, consolidatedOrder("2026-10-27", yes())).Snapshot()
	got, err := Restore(snap)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if g := got.Groups()[0]; g.ChosenDeliveryDay != "2026-10-27" || g.ConsolidationEligible == nil || !*g.ConsolidationEligible {
		t.Fatalf("group = %+v", g)
	}
	bad := mustPlace(t, consolidatedOrder("2026-10-27", yes())).Snapshot()
	bad.Groups[0].ChosenDeliveryDay = "soon"
	if _, err := Restore(bad); !errors.Is(err, shared.ErrInvalidSnapshot) {
		t.Fatalf("a malformed day = %v, want ErrInvalidSnapshot", err)
	}
	other := mustPlace(t, oneGroup()).Snapshot()
	other.Groups[0].ConsolidationEligible = yes()
	if _, err := Restore(other); !errors.Is(err, shared.ErrInvalidSnapshot) {
		t.Fatalf("a choice on a NEXT_DAY group = %v, want ErrInvalidSnapshot", err)
	}
}
