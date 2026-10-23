package bdd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/kafka"
	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

// slug maps an error to the problem slug the HTTP adapter answers with.
func slug(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, customerorder.ErrLineWithoutPromise):
		return "line-without-promise"
	case errors.Is(err, customerorder.ErrPromiseDestinationMismatch):
		return "promise-destination-mismatch"
	case errors.Is(err, customerorder.ErrCancelAfterLock):
		return "cancel-after-lock"
	case errors.Is(err, customerorder.ErrStatusRegression):
		return "status-regression"
	case errors.Is(err, shopper.ErrShopperExists):
		return "shopper-exists"
	case errors.Is(err, ports.ErrPromiseOfferNotQuotable):
		return "offer-not-quotable"
	case errors.Is(err, shared.ErrValidation):
		return "validation-failed"
	}
	return "unexpected: " + err.Error()
}

func (w *world) order() (*customerorder.CustomerOrder, error) {
	return w.store.Orders().Get(context.Background(), customerorder.ID(w.orderID))
}

func (w *world) group(n int) (customerorder.GroupPromise, error) {
	o, err := w.order()
	if err != nil {
		return customerorder.GroupPromise{}, err
	}
	for _, g := range o.Groups() {
		if g.GroupNo == n {
			return g, nil
		}
	}
	return customerorder.GroupPromise{}, fmt.Errorf("no group %d", n)
}

func (w *world) published(name string) []any {
	var out []any
	for _, ev := range w.store.Outbox().Events() {
		if ev.EventName() == name {
			out = append(out, ev)
		}
	}
	return out
}

func registerAssertions(sc *godog.ScenarioContext, w *world) {
	registerStatusAssertions(sc, w)
	registerRefusalAssertions(sc, w)
	registerEventAssertions(sc, w)
	registerCallAssertions(sc, w)
	registerTrackingAssertions(sc, w)
}

func registerStatusAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the order is "([^"]*)"$`, w.orderIs)
	sc.Step(`^every group is "([^"]*)"$`, w.everyGroupIs)
	sc.Step(`^group (\d+) is "([^"]*)"$`, w.groupIs)
	sc.Step(`^the group (\d+) refusal reason is "([^"]*)"$`, w.refusalReasonIs)
	sc.Step(`^group (\d+) has revision (\d+) and delivers one day later$`, w.groupRevised)
	sc.Step(`^no order exists$`, w.noOrderExists)
	sc.Step(`^the shopper is ACTIVE with e-mail "([^"]*)"$`, w.shopperIsActive)
}

func (w *world) orderIs(want string) error {
	o, err := w.order()
	if err != nil {
		return err
	}
	if string(o.Status()) != want {
		return fmt.Errorf("order is %s, want %s", o.Status(), want)
	}
	return nil
}

func (w *world) everyGroupIs(want string) error {
	o, err := w.order()
	if err != nil {
		return err
	}
	for _, g := range o.Groups() {
		if string(g.Status) != want {
			return fmt.Errorf("group %d is %s, want %s", g.GroupNo, g.Status, want)
		}
	}
	return nil
}

func (w *world) groupIs(n int, want string) error {
	g, err := w.group(n)
	if err != nil {
		return err
	}
	if string(g.Status) != want {
		return fmt.Errorf("group %d is %s, want %s", n, g.Status, want)
	}
	return nil
}

func (w *world) refusalReasonIs(n int, want string) error {
	g, err := w.group(n)
	if err != nil {
		return err
	}
	if g.CancelRefusedReason != want {
		return fmt.Errorf("refusal reason is %q, want %q", g.CancelRefusedReason, want)
	}
	return nil
}

func (w *world) groupRevised(n, rev int) error {
	g, err := w.group(n)
	if err != nil {
		return err
	}
	if g.RevisionNo != rev || !g.DeliverByLatest.After(t0.Add(30*time.Hour)) {
		return fmt.Errorf("group %d = revision %d latest %v", n, g.RevisionNo, g.DeliverByLatest)
	}
	return nil
}

func (w *world) noOrderExists() error {
	if _, err := w.order(); !errors.Is(err, ports.ErrNotFound) {
		return fmt.Errorf("an order exists or the lookup failed: %v", err)
	}
	return nil
}

func (w *world) shopperIsActive(email string) error {
	if w.lastErr != nil {
		return w.lastErr
	}
	if w.shopperView.Status != "ACTIVE" || w.shopperView.Email != email {
		return fmt.Errorf("shopper = %+v", w.shopperView)
	}
	return nil
}

func registerRefusalAssertions(sc *godog.ScenarioContext, w *world) {
	refused := func(want string) error {
		if got := slug(w.lastErr); got != want {
			return fmt.Errorf("refused with %q, want %q", got, want)
		}
		return nil
	}
	sc.Step(`^the checkout is refused with "([^"]*)"$`, refused)
	sc.Step(`^the cancellation is refused with "([^"]*)"$`, refused)
	sc.Step(`^the registration is refused with "([^"]*)"$`, refused)
	sc.Step(`^fresh offers are shown for groups 1 and 2$`, func() error {
		var rq *usecases.RequoteRequired
		if !errors.As(w.lastErr, &rq) {
			return fmt.Errorf("want RequoteRequired, got %v", w.lastErr)
		}
		seen := map[int]bool{}
		for _, o := range rq.Offers {
			seen[o.GroupNo] = true
		}
		if !seen[1] || !seen[2] {
			return fmt.Errorf("offers for groups %v", seen)
		}
		return nil
	})
}

func registerEventAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the event "([^"]*)" was published once with destination prefix "([^"]*)"$`, w.placedOnce)
	sc.Step(`^the event "CustomerOrderCancelled" was published once for groups ([0-9,]+)$`, w.cancelledOnce)
	sc.Step(`^no published event contains the address$`, w.noAddressPublished)
}

func (w *world) placedOnce(name, prefix string) error {
	list := w.published(name)
	if len(list) != 1 {
		return fmt.Errorf("%d %s events, want 1", len(list), name)
	}
	if ev, ok := list[0].(customerorder.CustomerOrderPlaced); !ok || ev.DestinationPostalPrefix != prefix {
		return fmt.Errorf("event = %+v", list[0])
	}
	return nil
}

func (w *world) cancelledOnce(groups string) error {
	list := w.published("CustomerOrderCancelled")
	if len(list) != 1 {
		return fmt.Errorf("%d CustomerOrderCancelled events, want 1", len(list))
	}
	ev := list[0].(customerorder.CustomerOrderCancelled)
	got := make([]string, len(ev.GroupNos))
	for i, n := range ev.GroupNos {
		got[i] = fmt.Sprint(n)
	}
	if strings.Join(got, ",") != groups {
		return fmt.Errorf("groupNos = %v, want %s", ev.GroupNos, groups)
	}
	return nil
}

func (w *world) noAddressPublished() error {
	msgs, err := kafka.NewEncoder().Encode(w.store.Outbox().Events()...)
	if err != nil {
		return err
	}
	for _, m := range msgs {
		for _, pii := range []string{"Ana Example", "1 Sample Street", "Austin", "78701"} {
			if strings.Contains(string(m.Value), pii) {
				return fmt.Errorf("%s contains %q", m.EventType, pii)
			}
		}
	}
	return nil
}

func registerCallAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^delivery-promise confirmed (\d+) promises$`, func(n int) error {
		if got := len(w.gw.Confirmed()); got != n {
			return fmt.Errorf("%d confirms, want %d", got, n)
		}
		return nil
	})
	sc.Step(`^delivery-promise cancelled (\d+) promises$`, func(n int) error {
		if got := len(w.gw.Cancelled()); got != n {
			return fmt.Errorf("%d cancels, want %d", got, n)
		}
		return nil
	})
	sc.Step(`^delivery-promise cancelled the promise of group (\d+)$`, func(group int) error {
		for _, c := range w.gw.Cancelled() {
			if c.GroupNo == group && c.PromiseID == fmt.Sprintf("p-%d", group) {
				return nil
			}
		}
		return fmt.Errorf("group %d was not cancelled: %+v", group, w.gw.Cancelled())
	})
	sc.Step(`^delivery-promise made (\d+) promises$`, func(n int) error {
		if got := len(w.gw.Made()); got != n {
			return fmt.Errorf("%d MakePromise calls, want %d", got, n)
		}
		return nil
	})
	sc.Step(`^delivery-promise was not called$`, func() error {
		if len(w.gw.Made())+len(w.gw.Confirmed())+len(w.gw.Quoted()) != 0 {
			return errors.New("delivery-promise was called")
		}
		return nil
	})
}

func registerTrackingAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the tracking of group (\d+) shows the verdict "([^"]*)" late by (\d+) minutes$`, func(n int, verdict string, minutes int) error {
		v, err := usecases.GetOrderTracking{Deps: w.deps}.Execute(context.Background(), customerorder.ID(w.orderID))
		if err != nil {
			return err
		}
		for _, g := range v.Groups {
			if g.GroupNo == n {
				if g.PromiseVerdict != verdict || g.LateByMinutes == nil || *g.LateByMinutes != minutes {
					return fmt.Errorf("group %d tracking = %+v", n, g)
				}
				return nil
			}
		}
		return fmt.Errorf("no group %d in the tracking", n)
	})
}
