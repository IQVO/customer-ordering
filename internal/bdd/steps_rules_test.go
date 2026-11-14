package bdd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/crypto"
	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
)

// registerRules wires the steps of the documentation-derived scenarios
// (features/*_rules.feature). Every step reuses the harness of the other
// step files: the use cases over the in-memory doubles and the real router.
func registerRules(sc *godog.ScenarioContext, w *world) {
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		// The harness leaves the compensation queue unwired; the A14 rules
		// about a cancel that fails need it.
		w.deps.Compensations = w.store.Compensations()
		return ctx, nil
	})
	registerRuleCheckoutSteps(sc, w)
	registerRulePromiseSteps(sc, w)
	registerRuleDestinationSteps(sc, w)
	registerRuleCancelSteps(sc, w)
	registerRuleFulfilmentSteps(sc, w)
	registerRuleTrackingSteps(sc, w)
	registerRulePathSteps(sc, w)
}

// promiseRefusals names the delivery-promise failures a scenario can script.
var promiseRefusals = map[string]error{
	"capacity-exhausted": ports.ErrPromiseCapacityExhausted,
	"book-closed":        ports.ErrPromiseBookClosed,
	"group-spans-nodes":  ports.ErrPromiseGroupSpansNodes,
	"unavailable":        ports.ErrPromiseUnavailable,
	"not-cancellable":    ports.ErrPromiseNotCancellable,
}

// scriptRefusal looks a named delivery-promise failure up and hands it to set.
func scriptRefusal(name string, set func(failure error)) error {
	failure, ok := promiseRefusals[name]
	if !ok {
		return fmt.Errorf("no scripted delivery-promise failure %q", name)
	}
	set(failure)
	return nil
}

func at(minute int) time.Time { return t0.Add(time.Duration(minute) * time.Minute) }

// generatedCart is a cart of `lines` one-unit lines spread over `groups`
// groups, each with its own scripted offer.
func (w *world) generatedCart(lines, groups int) usecases.PlaceOrderInput {
	w.orderID = "order-1"
	in := usecases.PlaceOrderInput{OrderID: "order-1", ShopperID: "shopper-1", ShipTo: address("78701")}
	gs := make([]usecases.CheckoutGroup, groups)
	for g := range gs {
		offer := fmt.Sprintf("gen-%d", g+1)
		gs[g] = usecases.CheckoutGroup{GroupNo: g + 1, OfferID: offer}
		w.gw.Terms[offer] = terms(fmt.Sprintf("gp-%d", g+1), "fc-aus-1")
	}
	for i := 1; i <= lines; i++ {
		in.Lines = append(in.Lines, customerorder.OrderLine{LineNo: i, CatalogItemID: "sku-100", Quantity: 1})
		g := (i - 1) % groups
		gs[g].LineNos = append(gs[g].LineNos, i)
	}
	in.Groups = gs
	return in
}

func registerRuleCheckoutSteps(sc *godog.ScenarioContext, w *world) {
	registerRuleCartSteps(sc, w)
	sc.Step(`^the checkout is refused$`, func() error {
		if w.lastErr == nil {
			return errors.New("the checkout was accepted, want it refused")
		}
		return nil
	})
	sc.Step(`^the checkout ends as "([^"]*)"(?: with destination prefix "([^"]*)")?$`, w.checkoutEndsAs)
	sc.Step(`^every promise request named order "([^"]*)"$`, w.everyPromiseNamed)
	sc.Step(`^the compensation queue holds (\d+) cancels?$`, w.queueHolds)
	sc.Step(`^delivery-promise was asked to quote these destinations:$`, w.quotedDestinations)
}

func registerRuleCartSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the shopper checks out a cart whose line 2 is in both group 1 and group 2$`, func() error {
		w.checkout(w.cart("78701",
			usecases.CheckoutGroup{GroupNo: 1, OfferID: "offer-1", LineNos: []int{1, 2}},
			usecases.CheckoutGroup{GroupNo: 2, OfferID: "offer-2", LineNos: []int{3, 2}}))
		return nil
	})
	sc.Step(`^the shopper checks out a cart whose group 2 names the missing line 9$`, func() error {
		w.checkout(w.cart("78701",
			usecases.CheckoutGroup{GroupNo: 1, OfferID: "offer-1", LineNos: []int{1, 2}},
			usecases.CheckoutGroup{GroupNo: 2, OfferID: "offer-2", LineNos: []int{3, 9}}))
		return nil
	})
	sc.Step(`^a generated cart of (\d+) lines in (\d+) groups is checked out$`, func(lines, groups int) error {
		w.checkout(w.generatedCart(lines, groups))
		return nil
	})
	sc.Step(`^the shopper checks out one line of quantity (\d+)$`, func(q int) error {
		in := w.cart("78701", usecases.CheckoutGroup{GroupNo: 1, OfferID: "offer-1", LineNos: []int{1}})
		in.Lines = []customerorder.OrderLine{{LineNo: 1, CatalogItemID: "sku-100", Quantity: q}}
		w.checkout(in)
		return nil
	})
}

// checkoutEndsAs asserts PLACED (with the event's destination prefix when one
// is given) or the slug of the refusal, and that a refusal stored nothing.
func (w *world) checkoutEndsAs(outcome, prefix string) error {
	if outcome == "PLACED" {
		if w.lastErr != nil {
			return fmt.Errorf("the checkout was refused: %w", w.lastErr)
		}
		if prefix == "" {
			return w.orderIs("PLACED")
		}
		return w.placedOnce("CustomerOrderPlaced", prefix)
	}
	if got := slug(w.lastErr); got != outcome {
		return fmt.Errorf("the checkout ended as %q, want %q", got, outcome)
	}
	return w.noOrderExists()
}

func (w *world) everyPromiseNamed(order string) error {
	made := w.gw.Made()
	if len(made) == 0 {
		return errors.New("delivery-promise was never asked for a promise")
	}
	for _, m := range made {
		if m.OrderID != order {
			return fmt.Errorf("MakePromise named order %q, want %q", m.OrderID, order)
		}
	}
	return nil
}

func (w *world) queueHolds(n int) error {
	if got := len(w.store.Compensations().Enqueued()); got != n {
		return fmt.Errorf("the compensation queue holds %d cancels, want %d", got, n)
	}
	return nil
}

// quotedDestinations compares the QuoteOffers calls with a table of
// prefix | pickupPoint | lines (sku xqty, comma separated), in call order.
func (w *world) quotedDestinations(t *godog.Table) error {
	want := t.Rows[1:]
	got := w.gw.Quoted()
	if len(got) != len(want) {
		return fmt.Errorf("%d quotes, want %d: %+v", len(got), len(want), got)
	}
	for i, row := range want {
		prefix, point, lines := row.Cells[0].Value, row.Cells[1].Value, row.Cells[2].Value
		var have []string
		for _, l := range got[i].Lines {
			have = append(have, fmt.Sprintf("%sx%d", l.CatalogItemID, l.Quantity))
		}
		if got[i].Prefix != prefix || got[i].PickupPointID != point || strings.Join(have, ",") != lines {
			return fmt.Errorf("quote %d = prefix %q point %q lines %s, want %q %q %s", i+1, got[i].Prefix, got[i].PickupPointID, strings.Join(have, ","), prefix, point, lines)
		}
	}
	return nil
}

// registerRulePromiseSteps scripts delivery-promise: every answer a documented
// checkout rule depends on.
func registerRulePromiseSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^delivery-promise promises every group for destination "([^"]*)"$`, func(prefix string) error {
		for offer, t := range w.gw.Terms {
			t.DestinationPostalPrefix = prefix
			w.gw.Terms[offer] = t
		}
		return nil
	})
	sc.Step(`^delivery-promise promises offer "([^"]*)" for destination "([^"]*)"$`, func(offer, prefix string) error {
		t := w.gw.Terms[offer]
		t.DestinationPostalPrefix = prefix
		w.gw.Terms[offer] = t
		return nil
	})
	sc.Step(`^delivery-promise refuses offer "([^"]*)" with "([^"]*)"$`, func(offer, name string) error {
		return scriptRefusal(name, func(failure error) { w.gw.MakeErr[offer] = failure })
	})
	sc.Step(`^delivery-promise refuses to confirm the promise of group (\d+) with "([^"]*)"$`, func(group int, name string) error {
		return scriptRefusal(name, func(failure error) { w.gw.ConfirmErr[fmt.Sprintf("p-%d", group)] = failure })
	})
	sc.Step(`^delivery-promise refuses to cancel the promise of group (\d+) with "([^"]*)"$`, func(group int, name string) error {
		return scriptRefusal(name, func(failure error) { w.gw.CancelErr[fmt.Sprintf("p-%d", group)] = failure })
	})
	sc.Step(`^delivery-promise cannot quote fresh offers$`, func() error {
		w.gw.QuoteErr = ports.ErrPromiseUnavailable
		return nil
	})
	sc.Step(`^delivery-promise recovers$`, func() error {
		w.gw.MakeErr, w.gw.ConfirmErr, w.gw.QuoteErr = map[string]error{}, map[string]error{}, nil
		return nil
	})
	sc.Step(`^delivery-promise promises offer "([^"]*)" as ([A-Z_]+) in zone "([^"]*)" delivering by "([^"]*)"(?: and (eligible|not eligible))?$`,
		func(offer, tier, zone, latest, eligible string) error {
			t := w.gw.Terms[offer]
			end, err := time.Parse(time.RFC3339, latest)
			if err != nil {
				return err
			}
			t.Tier, t.TimeZone, t.DeliverByLatest, t.DeliverByEarliest = tier, zone, end, end.Add(-10*time.Hour)
			if eligible != "" {
				v := eligible == "eligible"
				t.ConsolidationEligible = &v
			}
			w.gw.Terms[offer] = t
			return nil
		})
	sc.Step(`^delivery-promise answers offer "([^"]*)" with the pickup point "([^"]*)"$`, func(offer, point string) error {
		t := w.gw.Terms[offer]
		t.PickupPointID = point
		w.gw.Terms[offer] = t
		return nil
	})
}

// shipToFrom reads a one-row table (header + values) into a ship-to.
func shipToFrom(t *godog.Table) (shipto.ShipTo, error) {
	if len(t.Rows) != 2 {
		return shipto.ShipTo{}, errors.New("the address table needs a header row and one value row")
	}
	m := map[string]string{}
	for i, c := range t.Rows[0].Cells {
		m[c.Value] = t.Rows[1].Cells[i].Value
	}
	return shipto.ShipTo{Name: m["name"], Line1: m["line1"], Line2: m["line2"], City: m["city"], Region: m["region"],
		PostalCode: m["postalCode"], CountryCode: m["countryCode"], Phone: m["phone"]}, nil
}

// placeFor places the two-group cart as a shopper, to an address or (pickup
// non-empty) to a pickup point, under the given order id.
func (w *world) placeFor(shopper, order string, to shipto.ShipTo, pickup string) {
	in := w.cart("78701", twoGroups()...)
	in.OrderID, in.ShopperID, w.orderID = customerorder.ID(order), shopper, order
	if pickup != "" {
		in.ShipTo, in.PickupPointID = shipto.ShipTo{}, pickup
	} else {
		in.ShipTo = to
	}
	w.checkout(in)
}

func (w *world) placedEvent(order string) (customerorder.CustomerOrderPlaced, error) {
	for _, ev := range w.published("CustomerOrderPlaced") {
		if p := ev.(customerorder.CustomerOrderPlaced); string(p.OrderID) == order {
			return p, nil
		}
	}
	return customerorder.CustomerOrderPlaced{}, fmt.Errorf("no CustomerOrderPlaced for order %q", order)
}

// registerRuleDestinationSteps covers the consolidation key (ADR 0012), the
// pickup destination (ADR 0016) and the CONSOLIDATED choice (ADR 0013).
func registerRuleDestinationSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the consolidation key secret is configured$`, func() error {
		k, err := crypto.NewKeyer("a-secret-of-at-least-16-bytes")
		w.deps.ConsolidationKeys = k
		return err
	})
	sc.Step(`^the shopper "([^"]*)" places order "([^"]*)" shipping to:$`, func(shopper, order string, t *godog.Table) error {
		to, err := shipToFrom(t)
		if err != nil {
			return err
		}
		w.placeFor(shopper, order, to, "")
		return nil
	})
	sc.Step(`^the shopper "([^"]*)" places order "([^"]*)" for pickup point "([^"]*)"$`, func(shopper, order, point string) error {
		w.placeFor(shopper, order, shipto.ShipTo{}, point)
		return nil
	})
	sc.Step(`^orders "([^"]*)" and "([^"]*)" carry (the same|different) consolidation keys$`, w.keysRelate)
	sc.Step(`^the placed event of order "([^"]*)" carries a 64-character lower-case hex consolidation key$`, func(order string) error {
		return w.consolidationKeyIs(order, true)
	})
	sc.Step(`^the placed event of order "([^"]*)" carries no consolidation key$`, func(order string) error {
		return w.consolidationKeyIs(order, false)
	})
	sc.Step(`^the placed event of order "([^"]*)" names pickup point "([^"]*)" and destination prefix "([^"]*)"$`, w.placedNames)
	sc.Step(`^the placed event of group (\d+) carries tier "([^"]*)" and chosen day "([^"]*)"$`, w.placedGroupCarries)
}

func (w *world) keysRelate(a, b, rel string) error {
	ea, err := w.placedEvent(a)
	if err != nil {
		return err
	}
	eb, err := w.placedEvent(b)
	if err != nil {
		return err
	}
	if ea.ConsolidationKey == "" || eb.ConsolidationKey == "" {
		return fmt.Errorf("a key is empty: %q %q", ea.ConsolidationKey, eb.ConsolidationKey)
	}
	if same := ea.ConsolidationKey == eb.ConsolidationKey; same != (rel == "the same") {
		return fmt.Errorf("keys of %s and %s: %s / %s, want %s", a, b, ea.ConsolidationKey, eb.ConsolidationKey, rel)
	}
	return nil
}

// consolidationKeyIs asserts that the placed event of the order carries a
// well-formed key (derived) or none.
func (w *world) consolidationKeyIs(order string, derived bool) error {
	ev, err := w.placedEvent(order)
	if err != nil {
		return err
	}
	if !derived {
		if ev.ConsolidationKey != "" {
			return fmt.Errorf("consolidation key = %q, want none", ev.ConsolidationKey)
		}
		return nil
	}
	if !customerorder.ValidConsolidationKey(ev.ConsolidationKey) || len(ev.ConsolidationKey) != 64 {
		return fmt.Errorf("consolidation key = %q", ev.ConsolidationKey)
	}
	return nil
}

func (w *world) placedNames(order, point, prefix string) error {
	ev, err := w.placedEvent(order)
	if err != nil {
		return err
	}
	if ev.PickupPointID != point || ev.DestinationPostalPrefix != prefix {
		return fmt.Errorf("event = point %q prefix %q, want %q %q", ev.PickupPointID, ev.DestinationPostalPrefix, point, prefix)
	}
	return nil
}

func (w *world) placedGroupCarries(group int, tier, day string) error {
	ev, err := w.placedEvent(w.orderID)
	if err != nil {
		return err
	}
	if day == "none" {
		day = ""
	}
	for _, g := range ev.Groups {
		if g.GroupNo != group {
			continue
		}
		if string(g.Tier) != tier || g.ChosenDeliveryDay != day {
			return fmt.Errorf("event group %d = tier %s day %q, want %s %q", group, g.Tier, g.ChosenDeliveryDay, tier, day)
		}
		return nil
	}
	return fmt.Errorf("no group %d in the placed event", group)
}

func registerRuleCancelSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the cancellation is accepted$`, func() error {
		if w.lastErr != nil {
			return fmt.Errorf("the cancellation was refused: %w", w.lastErr)
		}
		return nil
	})
	sc.Step(`^delivery-promise declares the promise of group (\d+) unkeepable with "([^"]*)"$`, func(n int, reason string) error {
		return w.consumer().ApplyPromiseUnkeepable(context.Background(), w.meta("PromiseUnkeepable"), usecases.PromiseUnkeepable{
			PromiseID: fmt.Sprintf("p-%d", n), OrderID: w.orderID, GroupNo: n, Reason: reason,
		})
	})
	sc.Step(`^the "CustomerOrderCancelled" event number (\d+) carries the reason "([^"]*)" for groups ([0-9,]+)$`, func(n int, reason, groups string) error {
		list := w.published("CustomerOrderCancelled")
		if n < 1 || n > len(list) {
			return fmt.Errorf("%d CustomerOrderCancelled events, no number %d", len(list), n)
		}
		ev := list[n-1].(customerorder.CustomerOrderCancelled)
		got := make([]string, len(ev.GroupNos))
		for i, g := range ev.GroupNos {
			got[i] = fmt.Sprint(g)
		}
		if ev.Reason != reason || strings.Join(got, ",") != groups {
			return fmt.Errorf("event %d = reason %q groups %v, want %q %s", n, ev.Reason, ev.GroupNos, reason, groups)
		}
		return nil
	})
	sc.Step(`^the API cancels order "([^"]*)" with a reason of (\d+) characters$`, func(id string, n int) error {
		return w.call("POST", "/orders/"+id+"/cancellations", fmt.Sprintf("reason-%d", n), fmt.Sprintf(`{"reason":%q}`, strings.Repeat("r", n)))
	})
}
