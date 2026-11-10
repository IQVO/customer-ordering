package customerorder

import (
	"errors"
	"fmt"
	"time"
)

// cloneBool copies an optional boolean so a stored group never aliases the
// caller's value.
func cloneBool(b *bool) *bool {
	if b == nil {
		return nil
	}
	v := *b
	return &v
}

// checkChoice enforces ADR 0013: the chosen delivery day and the eligibility
// answer belong to a CONSOLIDATED group only, and the day is a real calendar
// date written YYYY-MM-DD (or empty: unknown).
func checkChoice(tier Tier, day string, eligible *bool) error {
	if tier != TierConsolidated && (day != "" || eligible != nil) {
		return errors.New("chosenDeliveryDay and consolidationEligible belong to a CONSOLIDATED group only")
	}
	if day == "" {
		return nil
	}
	if t, err := time.Parse(deliveryDayLayout, day); err != nil || t.Format(deliveryDayLayout) != day {
		return fmt.Errorf("chosenDeliveryDay %q is not a calendar date YYYY-MM-DD", day)
	}
	return nil
}

// deliveryDayLayout is the layout of ChosenDeliveryDay.
const deliveryDayLayout = "2006-01-02"
