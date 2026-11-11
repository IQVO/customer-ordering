package deliverypromise_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/promising"
)

func consolidatedBody(eligible string) string {
	field := ""
	if eligible != "" {
		field = `,"consolidationEligible":` + eligible
	}
	return fmt.Sprintf(`{"promiseId":"pr-c","offerId":"of-c","state":"PROVISIONAL","destinationPostalPrefix":"787",
 "terms":{"tier":"CONSOLIDATED","shipBy":"2026-10-23T20:00:00Z","deliverByEarliest":"2026-10-27T14:00:00Z","deliverByLatest":"2026-10-28T02:00:00Z","timeZone":"America/Chicago"%s},
 "witnessPath":{"nodeId":"fc-aus-1"}}`, field)
}

// TestConsolidationEligibleIsReadTolerantly pins ADR 0013: a boolean is kept
// (false included), absent, null and any non-boolean are unknown, and none of
// them fails the checkout.
func TestConsolidationEligibleIsReadTolerantly(t *testing.T) {
	yes, no := true, false
	for _, c := range []struct {
		name, raw string
		want      *bool
	}{
		{"true", "true", &yes}, {"false", "false", &no},
		{"absent", "", nil}, {"null", "null", nil}, {"a string", `"yes"`, nil}, {"a number", "1", nil}, {"an object", `{"a":1}`, nil},
	} {
		g, _ := serve(t, 201, "application/json", consolidatedBody(c.raw))
		terms, err := g.MakePromise(context.Background(), promising.MakeRequest{OrderID: "o-1", GroupNo: 1, OfferID: "of-c"})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if terms.Tier != "CONSOLIDATED" || terms.TimeZone != "America/Chicago" {
			t.Fatalf("%s: terms = %+v", c.name, terms)
		}
		if (terms.ConsolidationEligible == nil) != (c.want == nil) || c.want != nil && *terms.ConsolidationEligible != *c.want {
			t.Errorf("%s: eligible = %v, want %v", c.name, terms.ConsolidationEligible, c.want)
		}
	}
}

func TestAPromiseOfAnotherTierCarriesNoEligibility(t *testing.T) {
	g, _ := serve(t, 201, "application/json", promiseBody)
	terms, err := g.MakePromise(context.Background(), promising.MakeRequest{OrderID: "o-1", GroupNo: 1, OfferID: "of-1"})
	if err != nil || terms.ConsolidationEligible != nil {
		t.Fatalf("a NEXT_DAY promise = %+v, %v: eligibility must be unknown", terms, err)
	}
}
