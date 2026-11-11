package usecases

import (
	"time"

	"github.com/claudioed/customer-ordering/internal/application/promising"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
)

// chosenDeliveryDay is the local calendar date (YYYY-MM-DD) of the promise's
// deliverByLatest in its time zone: the day the shopper chose when the option
// was a CONSOLIDATED one (ADR 0013). It is empty for every other tier, for an
// unknown zone and for a zone name the zone database cannot resolve: the day is
// a display and hand-off fact, never a reason to fail a checkout.
func chosenDeliveryDay(t promising.Terms) string {
	if customerorder.Tier(t.Tier) != customerorder.TierConsolidated || t.TimeZone == "" || t.DeliverByLatest.IsZero() {
		return ""
	}
	loc, err := time.LoadLocation(t.TimeZone)
	if err != nil {
		return ""
	}
	return t.DeliverByLatest.In(loc).Format("2006-01-02")
}

// consolidationEligible keeps delivery-promise's answer for a CONSOLIDATED
// promise only: the aggregate refuses it on any other tier, and a stray value
// from a producer must not fail a checkout.
func consolidationEligible(t promising.Terms) *bool {
	if customerorder.Tier(t.Tier) != customerorder.TierConsolidated || t.ConsolidationEligible == nil {
		return nil
	}
	v := *t.ConsolidationEligible
	return &v
}
