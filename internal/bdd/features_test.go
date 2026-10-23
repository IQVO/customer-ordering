package bdd

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/memory"
	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/promising"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/application/views"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

var t0 = time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)

// TestFeatures runs the Gherkin scenarios of ../../features against the use
// cases wired to the in-memory doubles.
func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: initializeScenario,
		Options:             &godog.Options{Format: "pretty", Paths: []string{"../../features"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

// world is the state of one scenario.
type world struct {
	store   *memory.Store
	gw      *memory.PromiseGateway
	clock   *memory.Clock
	deps    usecases.Deps
	orderID string
	seq     int

	lastErr     error
	lastView    views.Order
	shopperView views.Shopper
	promiseOf   map[int]string
}

func newWorld() *world {
	s := memory.NewStore()
	w := &world{store: s, gw: memory.NewPromiseGateway(), clock: memory.NewClock(t0), promiseOf: map[int]string{}}
	w.deps = usecases.Deps{
		Orders: s.Orders(), Shoppers: s.Shoppers(), Tracking: s.Tracking(), Inbox: s.Inbox(), Outbox: s.Outbox(), UoW: s.UnitOfWork(),
		Promises: w.gw, Clock: w.clock, IDs: &memory.SequentialIDs{},
	}
	return w
}

func (w *world) consumer() usecases.Consumer { return usecases.Consumer{Deps: w.deps} }

func (w *world) meta(typ string) usecases.Meta {
	w.seq++
	return usecases.Meta{EventID: fmt.Sprintf("ev-%d", w.seq), Type: typ, At: t0.Add(time.Duration(30+w.seq) * time.Minute)}
}

func initializeScenario(sc *godog.ScenarioContext) {
	w := newWorld()
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = *newWorld()
		return ctx, nil
	})
	registerSetup(sc, w)
	registerCheckout(sc, w)
	registerCancellation(sc, w)
	registerFulfilment(sc, w)
	registerAssertions(sc, w)
}

func registerSetup(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^a registered shopper$`, func() error { return w.registerShopper("ana@example.com") })
	sc.Step(`^a shopper registered with "([^"]*)"$`, func(email string) error { return w.registerShopper(email) })
	sc.Step(`^delivery-promise holds offer "([^"]*)" for group 1 and offer "([^"]*)" for group 2$`, func(o1, o2 string) error {
		w.gw.Terms[o1], w.gw.Terms[o2] = terms("p-1", "fc-aus-1"), terms("p-2", "fc-aus-2")
		w.gw.Offers = []promising.Offer{{OfferID: "fresh-1", Tier: "NEXT_DAY", DeliverByEarliest: t0.Add(48 * time.Hour), DeliverByLatest: t0.Add(60 * time.Hour), ExpiresAt: t0.Add(time.Hour)}}
		return nil
	})
	sc.Step(`^the offer "([^"]*)" has expired$`, func(offer string) error { delete(w.gw.Terms, offer); return nil })
	sc.Step(`^delivery-promise answers group 2 with destination "([^"]*)"$`, func(prefix string) error {
		t := w.gw.Terms["offer-2"]
		t.DestinationPostalPrefix = prefix
		w.gw.Terms["offer-2"] = t
		return nil
	})
	sc.Step(`^delivery-promise cannot cancel the promise of group (\d+)$`, func(group int) error {
		w.gw.CancelErr[fmt.Sprintf("p-%d", group)] = ports.ErrPromiseUnavailable
		return nil
	})
	sc.Step(`^delivery-promise cannot confirm the promise of group 2$`, func() error {
		w.gw.ConfirmErr["p-2"] = ports.ErrPromiseOfferNotQuotable
		return nil
	})
}

func terms(promise, node string) promising.Terms {
	return promising.Terms{
		PromiseID: promise, Tier: "NEXT_DAY", WitnessNodeID: node, ShipBy: t0.Add(10 * time.Hour),
		DeliverByEarliest: t0.Add(20 * time.Hour), DeliverByLatest: t0.Add(30 * time.Hour), DestinationPostalPrefix: "787",
	}
}

func (w *world) registerShopper(email string) error {
	s, err := shopper.Register(shopper.NewShopper{ID: w.deps.IDs.NewShopperID(), Email: email}, t0)
	if err != nil {
		return err
	}
	return w.store.Shoppers().Save(context.Background(), s)
}

func address(postal string) shipto.ShipTo {
	return shipto.ShipTo{Name: "Ana Example", Line1: "1 Sample Street", City: "Austin", Region: "TX", PostalCode: postal, CountryCode: "US"}
}

func (w *world) cart(postal string, groups ...usecases.CheckoutGroup) usecases.PlaceOrderInput {
	w.orderID = "order-1"
	return usecases.PlaceOrderInput{
		OrderID: customerorder.ID(w.orderID), ShopperID: "shopper-1", ShipTo: address(postal),
		Lines:  []customerorder.OrderLine{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2}, {LineNo: 2, CatalogItemID: "sku-200", Quantity: 1}, {LineNo: 3, CatalogItemID: "sku-300", Quantity: 5}},
		Groups: groups,
	}
}

func twoGroups() []usecases.CheckoutGroup {
	return []usecases.CheckoutGroup{{GroupNo: 1, OfferID: "offer-1", LineNos: []int{1, 2}}, {GroupNo: 2, OfferID: "offer-2", LineNos: []int{3}}}
}

func (w *world) checkout(in usecases.PlaceOrderInput) {
	w.lastView, w.lastErr = usecases.PlaceOrder{Deps: w.deps}.Execute(context.Background(), in)
}

func registerCheckout(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the shopper checks out a cart of 3 lines in 2 groups to postal code "([^"]*)"$`, func(postal string) error {
		w.checkout(w.cart(postal, twoGroups()...))
		return nil
	})
	sc.Step(`^the shopper has placed a cart of 3 lines in 2 groups$`, func() error {
		w.checkout(w.cart("78701", twoGroups()...))
		return w.lastErr
	})
	sc.Step(`^the shopper checks out a cart whose line 3 is in no group$`, func() error {
		w.checkout(w.cart("78701", usecases.CheckoutGroup{GroupNo: 1, OfferID: "offer-1", LineNos: []int{1, 2}}))
		return nil
	})
	sc.Step(`^the same checkout is repeated$`, func() error {
		w.checkout(w.cart("78701", twoGroups()...))
		return w.lastErr
	})
}

func registerCancellation(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the shopper cancels with "([^"]*)"$`, func(reason string) error {
		w.lastView, w.lastErr = usecases.CancelOrder{Deps: w.deps}.Execute(context.Background(), usecases.CancelOrderInput{OrderID: customerorder.ID(w.orderID), Reason: reason})
		return nil
	})
	sc.Step(`^order-sourcing accepts the cancellation of group (\d+)$`, func(n int) error {
		return w.consumer().ApplyFulfillmentCancelled(context.Background(), w.meta("FulfillmentCancelled"), usecases.FulfillmentCancelled{OrderID: w.orderID, GroupNo: n})
	})
	sc.Step(`^order-sourcing refuses the cancellation of group (\d+) with "([^"]*)"$`, func(n int, reason string) error {
		return w.consumer().ApplyFulfillmentCancelRefused(context.Background(), w.meta("FulfillmentCancelRefused"), usecases.FulfillmentCancelRefused{OrderID: w.orderID, GroupNo: n, Reason: reason})
	})
	sc.Step(`^delivery-promise declares the promise of group 2 unkeepable$`, func() error {
		return w.consumer().ApplyPromiseUnkeepable(context.Background(), w.meta("PromiseUnkeepable"), usecases.PromiseUnkeepable{PromiseID: "p-2", OrderID: w.orderID, GroupNo: 2, Reason: "NO_FEASIBLE_PATH"})
	})
}

func (w *world) lock(n int) error {
	return w.consumer().ApplyFulfillmentLocked(context.Background(), w.meta("FulfillmentLocked"), usecases.FulfillmentLocked{
		AssignmentID: fmt.Sprintf("as-%d", n), OrderID: w.orderID, GroupNo: n, NodeID: "fc-aus-1", LockedAt: t0.Add(time.Hour),
	})
}

func registerFulfilment(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^order-sourcing locks group (\d+)$`, w.lock)
	sc.Step(`^order-sourcing locks group (\d+) again$`, w.lock)
	sc.Step(`^order-sourcing ships group (\d+) with packages "([^"]*)"$`, func(n int, tns string) error {
		return w.consumer().ApplyFulfillmentShipped(context.Background(), w.meta("FulfillmentShipped"), usecases.FulfillmentShipped{
			OrderID: w.orderID, GroupNo: n, TrackingNumbers: strings.Split(tns, ","), ShippedAt: t0.Add(2 * time.Hour),
		})
	})
	sc.Step(`^shipping-and-tracking delivers package "([^"]*)"$`, func(tn string) error {
		return w.consumer().ApplyPackageDelivered(context.Background(), w.meta("PackageDelivered"), usecases.PackageDelivered{
			TrackingNumber: tn, OrderID: w.orderID, DeliveredAt: t0.Add(3 * time.Hour),
		})
	})
	sc.Step(`^shipping-and-tracking declares package "([^"]*)" lost$`, func(tn string) error {
		return w.consumer().ApplyPackageLost(context.Background(), w.meta("PackageLost"), usecases.PackageLost{
			TrackingNumber: tn, OrderID: w.orderID, DeclaredAt: t0.Add(4 * time.Hour),
		})
	})
	sc.Step(`^shipping-and-tracking returns package "([^"]*)" to the sender$`, func(tn string) error {
		return w.consumer().ApplyPackageReturned(context.Background(), w.meta("PackageReturned"), usecases.PackageReturned{
			TrackingNumber: tn, OrderID: w.orderID, ReturnedAt: t0.Add(5 * time.Hour),
		})
	})
	sc.Step(`^delivery-promise revises the promise of group 1 one day later$`, func() error {
		return w.consumer().ApplyPromiseRevised(context.Background(), w.meta("PromiseRevised"), usecases.PromiseRevised{
			PromiseID: "p-1", OrderID: w.orderID, RevisionNo: 1, DeliverByEarliest: t0.Add(44 * time.Hour), DeliverByLatest: t0.Add(54 * time.Hour),
		})
	})
	sc.Step(`^delivery-promise reports the promise of group 1 broken by (\d+) minutes$`, func(minutes int) error {
		return w.consumer().ApplyPromiseBroken(context.Background(), w.meta("PromiseBroken"), usecases.PromiseBroken{PromiseID: "p-1", OrderID: w.orderID, LateByMinutes: &minutes})
	})
	sc.Step(`^a shopper registers with "([^"]*)"$`, func(email string) error {
		w.shopperView, w.lastErr = usecases.RegisterShopper{Deps: w.deps}.Execute(context.Background(), usecases.RegisterShopperInput{Email: email})
		return nil
	})
}
