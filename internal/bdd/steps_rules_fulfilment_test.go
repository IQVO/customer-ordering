package bdd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
)

// orderOf loads any order of the scenario by id.
func (w *world) orderOf(id string) (*customerorder.CustomerOrder, error) {
	return w.store.Orders().Get(context.Background(), customerorder.ID(id))
}

func (w *world) groupOfOrder(id string, n int) (customerorder.GroupPromise, error) {
	o, err := w.orderOf(id)
	if err != nil {
		return customerorder.GroupPromise{}, err
	}
	for _, g := range o.Groups() {
		if g.GroupNo == n {
			return g, nil
		}
	}
	return customerorder.GroupPromise{}, fmt.Errorf("order %s has no group %d", id, n)
}

// factForOrder delivers one consumed fact that names an order the service
// never stored; the consumer must acknowledge it (A7).
func (w *world) factForOrder(kind, order string) error {
	c, ctx, m := w.consumer(), context.Background(), w.meta(kind)
	switch kind {
	case "FulfillmentLocked":
		return c.ApplyFulfillmentLocked(ctx, m, usecases.FulfillmentLocked{AssignmentID: "as-9", OrderID: order, GroupNo: 1, NodeID: "fc-aus-1", LockedAt: at(60)})
	case "FulfillmentShipped":
		return c.ApplyFulfillmentShipped(ctx, m, usecases.FulfillmentShipped{OrderID: order, GroupNo: 1, TrackingNumbers: []string{"TRK-9"}, ShippedAt: at(120)})
	case "FulfillmentCancelled":
		return c.ApplyFulfillmentCancelled(ctx, m, usecases.FulfillmentCancelled{OrderID: order, GroupNo: 1})
	case "FulfillmentCancelRefused":
		return c.ApplyFulfillmentCancelRefused(ctx, m, usecases.FulfillmentCancelRefused{OrderID: order, GroupNo: 1, Reason: "VENDOR_ACKNOWLEDGED"})
	case "PackageDelivered":
		return c.ApplyPackageDelivered(ctx, m, usecases.PackageDelivered{TrackingNumber: "TRK-9", OrderID: order, DeliveredAt: at(180)})
	case "PackageLost":
		return c.ApplyPackageLost(ctx, m, usecases.PackageLost{TrackingNumber: "TRK-9", OrderID: order, DeclaredAt: at(180)})
	case "PackageReturned":
		return c.ApplyPackageReturned(ctx, m, usecases.PackageReturned{TrackingNumber: "TRK-9", OrderID: order, ReturnedAt: at(180)})
	case "PromiseRevised":
		return c.ApplyPromiseRevised(ctx, m, usecases.PromiseRevised{PromiseID: "p-1", OrderID: order, RevisionNo: 1, DeliverByEarliest: at(2000), DeliverByLatest: at(2100)})
	case "PromiseUnkeepable":
		return c.ApplyPromiseUnkeepable(ctx, m, usecases.PromiseUnkeepable{PromiseID: "p-1", OrderID: order, GroupNo: 1, Reason: "NO_FEASIBLE_PATH"})
	case "PromiseAtRisk":
		return c.ApplyPromiseAtRisk(ctx, m, usecases.PromiseAtRisk{PromiseID: "p-1", OrderID: order, Reason: "CARRIER_DELAY"})
	}
	return fmt.Errorf("no scripted fact %q", kind)
}

// revise delivers PromiseRevised for the promise of a group: the window moves
// `hours` later than the one checkout promised (earliest +20h, latest +30h,
// shipBy +10h after t0).
func (w *world) revise(group, revision, hours int, withShipBy bool) error {
	h := time.Duration(hours) * time.Hour
	in := usecases.PromiseRevised{
		PromiseID: fmt.Sprintf("p-%d", group), OrderID: w.orderID, RevisionNo: revision,
		DeliverByEarliest: t0.Add(20*time.Hour + h), DeliverByLatest: t0.Add(30*time.Hour + h),
	}
	if withShipBy {
		in.ShipBy = t0.Add(10*time.Hour + h)
	}
	return w.consumer().ApplyPromiseRevised(context.Background(), w.meta("PromiseRevised"), in)
}

func registerRuleFulfilmentSteps(sc *godog.ScenarioContext, w *world) {
	registerRuleUnknownFactSteps(sc, w)
	registerRuleRevisionSteps(sc, w)
	registerRuleSharedPackageSteps(sc, w)
	sc.Step(`^order-sourcing delivers the lock of group (\d+) as event "([^"]*)" twice$`, func(n int, id string) error {
		for i := 0; i < 2; i++ {
			err := w.consumer().ApplyFulfillmentLocked(context.Background(), usecases.Meta{EventID: id, Type: "FulfillmentLocked", At: at(60)}, usecases.FulfillmentLocked{
				AssignmentID: fmt.Sprintf("as-%d", n), OrderID: w.orderID, GroupNo: n, NodeID: "fc-aus-1", LockedAt: at(60),
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^the inbox recorded (\d+) events?$`, func(n int) error {
		if got := w.store.Counts().Inbox; got != n {
			return fmt.Errorf("the inbox holds %d events, want %d", got, n)
		}
		return nil
	})
	sc.Step(`^the order was saved (\d+) times$`, func(n int) error {
		o, err := w.order()
		if err != nil {
			return err
		}
		if int(o.Version()) != n {
			return fmt.Errorf("order version = %d, want %d", o.Version(), n)
		}
		return nil
	})
}

func registerRuleUnknownFactSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^a "([^"]*)" fact arrives for the unknown order "([^"]*)"$`, w.factForOrder)
	sc.Step(`^order-sourcing locks the unknown group (\d+)$`, func(n int) error {
		return w.consumer().ApplyFulfillmentLocked(context.Background(), w.meta("FulfillmentLocked"), usecases.FulfillmentLocked{
			AssignmentID: "as-9", OrderID: w.orderID, GroupNo: n, NodeID: "fc-aus-1", LockedAt: at(60),
		})
	})
	sc.Step(`^delivery-promise revises the unknown promise "([^"]*)"$`, func(promise string) error {
		return w.consumer().ApplyPromiseRevised(context.Background(), w.meta("PromiseRevised"), usecases.PromiseRevised{
			PromiseID: promise, OrderID: w.orderID, RevisionNo: 1, DeliverByEarliest: at(2000), DeliverByLatest: at(2100),
		})
	})
}

func registerRuleRevisionSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^delivery-promise revises the promise of group (\d+) to revision (\d+) delivering (\d+) hours later$`, func(g, rev, h int) error {
		return w.revise(g, rev, h, true)
	})
	sc.Step(`^delivery-promise revises the promise of group (\d+) to revision (\d+) delivering (\d+) hours later without a ship-by$`, func(g, rev, h int) error {
		return w.revise(g, rev, h, false)
	})
	sc.Step(`^delivery-promise sends revision (\d+) of group (\d+) with an inverted window$`, func(rev, g int) error {
		w.lastErr = w.consumer().ApplyPromiseRevised(context.Background(), w.meta("PromiseRevised"), usecases.PromiseRevised{
			PromiseID: fmt.Sprintf("p-%d", g), OrderID: w.orderID, RevisionNo: rev, ShipBy: at(60),
			DeliverByEarliest: t0.Add(40 * time.Hour), DeliverByLatest: t0.Add(30 * time.Hour),
		})
		return nil
	})
	sc.Step(`^the consumed event is rejected with "([^"]*)"$`, func(want string) error {
		if w.lastErr == nil {
			return errors.New("the consumer acknowledged the event, want it rejected")
		}
		if got := slug(w.lastErr); got != want {
			return fmt.Errorf("rejected with %q, want %q", got, want)
		}
		return nil
	})
	sc.Step(`^group (\d+) is at revision (\d+) and delivers by "([^"]*)"$`, func(n, rev int, latest string) error {
		return w.groupCheck(n, func(g customerorder.GroupPromise) error {
			if g.RevisionNo != rev {
				return fmt.Errorf("group %d is at revision %d, want %d", n, g.RevisionNo, rev)
			}
			return sameInstant(fmt.Sprintf("group %d delivers by", n), g.DeliverByLatest, latest)
		})
	})
	sc.Step(`^group (\d+) is at revision (\d+)$`, func(n, rev int) error {
		return w.groupCheck(n, func(g customerorder.GroupPromise) error {
			if g.RevisionNo != rev {
				return fmt.Errorf("group %d is at revision %d, want %d", n, g.RevisionNo, rev)
			}
			return nil
		})
	})
	sc.Step(`^group (\d+) ships by "([^"]*)"$`, func(n int, shipBy string) error {
		return w.groupCheck(n, func(g customerorder.GroupPromise) error {
			return sameInstant(fmt.Sprintf("group %d ships by", n), g.ShipBy, shipBy)
		})
	})
	sc.Step(`^group (\d+) keeps the time zone "([^"]*)"$`, func(n int, zone string) error {
		return w.groupCheck(n, func(g customerorder.GroupPromise) error {
			if g.TimeZone != zone {
				return fmt.Errorf("group %d time zone = %q, want %q", n, g.TimeZone, zone)
			}
			return nil
		})
	})
}

// groupCheck loads group n of the scenario's order and runs one check on it.
func (w *world) groupCheck(n int, check func(g customerorder.GroupPromise) error) error {
	g, err := w.group(n)
	if err != nil {
		return err
	}
	return check(g)
}

// sameInstant compares a stored instant with an RFC 3339 text.
func sameInstant(what string, got time.Time, want string) error {
	t, err := time.Parse(time.RFC3339, want)
	if err != nil {
		return err
	}
	if !got.Equal(t) {
		return fmt.Errorf("%s %s, want %s", what, got.Format(time.RFC3339), want)
	}
	return nil
}

// registerRuleSharedPackageSteps drives two orders at once: a package shared
// by merged groups (ADR 0012) belongs to groups of different orders.
func registerRuleSharedPackageSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^order-sourcing locks group (\d+) of order "([^"]*)"$`, func(n int, order string) error {
		return w.consumer().ApplyFulfillmentLocked(context.Background(), w.meta("FulfillmentLocked"), usecases.FulfillmentLocked{
			AssignmentID: fmt.Sprintf("as-%s-%d", order, n), OrderID: order, GroupNo: n, NodeID: "fc-aus-1", LockedAt: at(60),
		})
	})
	sc.Step(`^order-sourcing ships group (\d+) of order "([^"]*)" with packages "([^"]*)"$`, func(n int, order, tns string) error {
		return w.consumer().ApplyFulfillmentShipped(context.Background(), w.meta("FulfillmentShipped"), usecases.FulfillmentShipped{
			OrderID: order, GroupNo: n, TrackingNumbers: splitList(tns), ShippedAt: at(120),
		})
	})
	sc.Step(`^shipping-and-tracking delivers package "([^"]*)" for order "([^"]*)"$`, func(tn, order string) error {
		return w.consumer().ApplyPackageDelivered(context.Background(), w.meta("PackageDelivered"), usecases.PackageDelivered{TrackingNumber: tn, OrderID: order, DeliveredAt: at(180)})
	})
	sc.Step(`^shipping-and-tracking declares package "([^"]*)" lost for order "([^"]*)"$`, func(tn, order string) error {
		return w.consumer().ApplyPackageLost(context.Background(), w.meta("PackageLost"), usecases.PackageLost{TrackingNumber: tn, OrderID: order, DeclaredAt: at(180)})
	})
	sc.Step(`^shipping-and-tracking returns package "([^"]*)" to the sender for order "([^"]*)"$`, func(tn, order string) error {
		return w.consumer().ApplyPackageReturned(context.Background(), w.meta("PackageReturned"), usecases.PackageReturned{TrackingNumber: tn, OrderID: order, ReturnedAt: at(180)})
	})
	sc.Step(`^group (\d+) of order "([^"]*)" is "([^"]*)"$`, func(n int, order, want string) error {
		g, err := w.groupOfOrder(order, n)
		if err != nil {
			return err
		}
		if string(g.Status) != want {
			return fmt.Errorf("group %d of %s is %s, want %s", n, order, g.Status, want)
		}
		return nil
	})
}

func splitList(s string) []string { return strings.Split(s, ",") }
