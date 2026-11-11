package http_test

import "testing"

// fieldOf reads one key of the i-th group of a JSON body; present reports
// whether the key exists at all (absent = unknown, ADR 0013).
func fieldOf(t *testing.T, body map[string]any, i int, key string) (v any, present bool) {
	t.Helper()
	list, ok := body["groups"].([]any)
	if !ok || len(list) <= i {
		t.Fatalf("groups[%d] missing in %v", i, body)
	}
	v, present = list[i].(map[string]any)[key]
	return v, present
}

// A CONSOLIDATED group shows its tier, the day the shopper chose and the
// eligibility answer on every order view (ADR 0013); a stated false is shown
// (false is not unknown) and a classic group shows neither key.
func TestOrderViewsExposeTheConsolidatedChoice(t *testing.T) {
	e := newEnv(t)
	chosen := e.gw.Terms["offer-1"]
	chosen.Tier, chosen.TimeZone = "CONSOLIDATED", "America/Chicago"
	no := false
	chosen.ConsolidationEligible = &no
	e.gw.Terms["offer-1"] = chosen
	created := e.checkout("key-consolidated")
	if created.Status != 201 {
		t.Fatalf("checkout = %d %s", created.Status, created.Body)
	}
	id := created.json(t)["orderId"].(string)
	views := map[string]map[string]any{
		"checkout": created.json(t),
		"order":    e.do("GET", "/orders/"+id, "", "").json(t),
		"history":  e.do("GET", "/orders?shopperId=shopper-1", "", "").json(t)["items"].([]any)[0].(map[string]any),
	}
	for name, body := range views {
		if tier, _ := fieldOf(t, body, 0, "tier"); tier != "CONSOLIDATED" {
			t.Errorf("%s: group 1 tier = %v", name, tier)
		}
		if day, present := fieldOf(t, body, 0, "chosenDeliveryDay"); !present || day == "" {
			t.Errorf("%s: group 1 must show the chosen day, got %v", name, day)
		}
		if eligible, present := fieldOf(t, body, 0, "consolidationEligible"); !present || eligible != false {
			t.Errorf("%s: group 1 eligibility = %v (present %v), want a stated false", name, eligible, present)
		}
		for _, key := range []string{"chosenDeliveryDay", "consolidationEligible"} {
			if _, present := fieldOf(t, body, 1, key); present {
				t.Errorf("%s: a classic group must omit %s", name, key)
			}
		}
	}
}

func TestAnUnknownEligibilityIsOmittedNotFalse(t *testing.T) {
	e := newEnv(t)
	chosen := e.gw.Terms["offer-1"]
	chosen.Tier, chosen.TimeZone = "CONSOLIDATED", "America/Chicago"
	e.gw.Terms["offer-1"] = chosen
	created := e.checkout("key-unknown-eligible")
	if created.Status != 201 {
		t.Fatalf("checkout = %d %s", created.Status, created.Body)
	}
	if v, present := fieldOf(t, created.json(t), 0, "consolidationEligible"); present {
		t.Fatalf("an unknown answer must be omitted, got %v", v)
	}
}
