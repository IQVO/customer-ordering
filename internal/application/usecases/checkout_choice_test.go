package usecases_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/promising"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
)

func consolidatedTerms(promise string, tz string, eligible *bool) promising.Terms {
	t := terms(promise, "fc-aus-1")
	t.Tier, t.TimeZone, t.ConsolidationEligible = "CONSOLIDATED", tz, eligible
	// 2026-10-28T02:30Z is still the evening of Oct 27 in Chicago (UTC-5).
	t.DeliverByLatest = t0.AddDate(0, 0, 4).Add(16*time.Hour + 30*time.Minute)
	return t
}

func placeWith(t *testing.T, f *fx, id string, one promising.Terms, other promising.Terms) customerorder.GroupPromise {
	t.Helper()
	f.gw.Terms["offer-1"], f.gw.Terms["offer-2"] = one, other
	if _, err := (usecases.PlaceOrder{Deps: f.deps}).Execute(context.Background(), checkout(id)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return f.order(id).Groups()[0]
}

func TestCheckoutStoresTheChosenDayInTheDestinationZone(t *testing.T) {
	f := newFx(t)
	yes := true
	g := placeWith(t, f, "order-ch", consolidatedTerms("p-c1", "America/Chicago", &yes), terms("p-c2", "fc-aus-2"))
	if g.Tier != customerorder.TierConsolidated {
		t.Fatalf("tier = %s", g.Tier)
	}
	if want := "2026-10-27"; g.ChosenDeliveryDay != want {
		t.Fatalf("chosen day = %q, want %q: the LOCAL date of deliverByLatest (%v) in America/Chicago", g.ChosenDeliveryDay, want, g.DeliverByLatest)
	}
	if g.ConsolidationEligible == nil || !*g.ConsolidationEligible {
		t.Fatalf("eligible = %v", g.ConsolidationEligible)
	}
	// The other group of the same checkout is a classic tier: no choice stored.
	if other := f.order("order-ch").Groups()[1]; other.ChosenDeliveryDay != "" || other.ConsolidationEligible != nil {
		t.Fatalf("a NEXT_DAY group carries a choice: %+v", other)
	}
}

func TestCheckoutTheSameInstantIsAnotherDayInAnotherZone(t *testing.T) {
	f := newFx(t)
	g := placeWith(t, f, "order-tokyo", consolidatedTerms("p-c1", "Asia/Tokyo", nil), terms("p-c2", "fc-aus-2"))
	if g.ChosenDeliveryDay != "2026-10-28" || g.ConsolidationEligible != nil {
		t.Fatalf("group = %+v, want day 2026-10-28 (UTC+9) and unknown eligibility", g)
	}
}

func TestCheckoutKeepsAStatedFalseEligibility(t *testing.T) {
	f := newFx(t)
	no := false
	g := placeWith(t, f, "order-false", consolidatedTerms("p-c1", "America/Chicago", &no), terms("p-c2", "fc-aus-2"))
	if g.ConsolidationEligible == nil || *g.ConsolidationEligible {
		t.Fatalf("a stated false must stay false: %v", g.ConsolidationEligible)
	}
}

func TestCheckoutWithoutAZoneStoresNoDayButStillPlaces(t *testing.T) {
	f := newFx(t)
	yes := true
	for i, tz := range []string{"", "Nowhere/Land"} {
		g := placeWith(t, f, "order-nozone-"+string(rune('a'+i)), consolidatedTerms("p-z"+string(rune('a'+i)), tz, &yes), terms("p-y"+string(rune('a'+i)), "fc-aus-2"))
		if g.ChosenDeliveryDay != "" || g.Tier != customerorder.TierConsolidated || g.ConsolidationEligible == nil {
			t.Fatalf("zone %q: group = %+v (an unresolvable zone leaves the day unknown, the order is still placed)", tz, g)
		}
	}
}

func TestAStrayEligibilityOnAnotherTierNeverFailsTheCheckout(t *testing.T) {
	f := newFx(t)
	yes := true
	stray := terms("p-s1", "fc-aus-1")
	stray.ConsolidationEligible = &yes
	g := placeWith(t, f, "order-stray", stray, terms("p-s2", "fc-aus-2"))
	if g.ConsolidationEligible != nil || g.ChosenDeliveryDay != "" {
		t.Fatalf("a NEXT_DAY group kept a stray choice: %+v", g)
	}
}

func TestACheckoutEventCarriesTheChoice(t *testing.T) {
	f := newFx(t)
	yes := true
	placeWith(t, f, "order-evt", consolidatedTerms("p-c1", "America/Chicago", &yes), terms("p-c2", "fc-aus-2"))
	placed := placedEvent(t, f)
	g := placed.Groups[0]
	if g.Tier != customerorder.TierConsolidated || g.ChosenDeliveryDay != "2026-10-27" || g.ConsolidationEligible == nil || !*g.ConsolidationEligible {
		t.Fatalf("placed group = %+v", g)
	}
	if o := placed.Groups[1]; o.ChosenDeliveryDay != "" || o.ConsolidationEligible != nil {
		t.Fatalf("a classic group carries a choice on the wire: %+v", o)
	}
}
