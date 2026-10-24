package usecases_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/memory"
	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/promising"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/application/views"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

var t0 = time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)

func at(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }

type fx struct {
	t     *testing.T
	store *memory.Store
	gw    *memory.PromiseGateway
	clock *memory.Clock
	deps  usecases.Deps
	seq   int
}

func newFx(t *testing.T) *fx {
	t.Helper()
	f := &fx{t: t, store: memory.NewStore(memory.NewClock(t0)), gw: memory.NewPromiseGateway(), clock: memory.NewClock(t0)}
	s := f.store
	f.deps = usecases.Deps{
		Orders: s.Orders(), Shoppers: s.Shoppers(), Tracking: s.Tracking(), Inbox: s.Inbox(), Outbox: s.Outbox(), UoW: s.UnitOfWork(),
		Promises: f.gw, Clock: f.clock, IDs: &memory.SequentialIDs{},
	}
	sh, err := shopper.Register(shopper.NewShopper{ID: "shopper-1", Email: "ana@example.com"}, t0)
	must(t, err)
	must(t, s.Shoppers().Save(context.Background(), sh))
	f.gw.Terms["offer-1"] = terms("p-1", "fc-aus-1")
	f.gw.Terms["offer-2"] = terms("p-2", "fc-aus-2")
	f.gw.Offers = []promising.Offer{{OfferID: "new-1", Tier: "NEXT_DAY", DeliverByEarliest: at(2000), DeliverByLatest: at(2300), ExpiresAt: at(30)}}
	return f
}

func terms(promise, node string) promising.Terms {
	return promising.Terms{
		PromiseID: promise, Tier: "NEXT_DAY", WitnessNodeID: node, ShipBy: at(600), DeliverByEarliest: at(1200), DeliverByLatest: at(1500),
		DestinationPostalPrefix: "787",
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func address() shipto.ShipTo {
	return shipto.ShipTo{Name: "Ana Example", Line1: "1 Sample Street", City: "Austin", Region: "TX", PostalCode: "78701", CountryCode: "US"}
}

func checkout(orderID string) usecases.PlaceOrderInput {
	return usecases.PlaceOrderInput{
		OrderID: customerorder.ID(orderID), ShopperID: "shopper-1", ShipTo: address(),
		Lines: []customerorder.OrderLine{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2}, {LineNo: 2, CatalogItemID: "sku-200", Quantity: 1}, {LineNo: 3, CatalogItemID: "sku-300", Quantity: 5}},
		Groups: []usecases.CheckoutGroup{
			{GroupNo: 1, OfferID: "offer-1", LineNos: []int{1, 2}},
			{GroupNo: 2, OfferID: "offer-2", LineNos: []int{3}},
		},
	}
}

// place checks an order out and returns its id.
func (f *fx) place() string {
	f.t.Helper()
	f.seq++
	id := fmt.Sprintf("order-%d", f.seq)
	f.gw.Terms["offer-1"] = terms(fmt.Sprintf("p-%d-1", f.seq), "fc-aus-1")
	f.gw.Terms["offer-2"] = terms(fmt.Sprintf("p-%d-2", f.seq), "fc-aus-2")
	_, err := usecases.PlaceOrder{Deps: f.deps}.Execute(context.Background(), checkout(id))
	must(f.t, err)
	return id
}

func (f *fx) order(id string) *customerorder.CustomerOrder {
	f.t.Helper()
	o, err := f.store.Orders().Get(context.Background(), customerorder.ID(id))
	must(f.t, err)
	return o
}

func (f *fx) groupStatus(id string, groupNo int) customerorder.GroupStatus {
	f.t.Helper()
	for _, g := range f.order(id).Groups() {
		if g.GroupNo == groupNo {
			return g.Status
		}
	}
	f.t.Fatalf("order %s has no group %d", id, groupNo)
	return ""
}

func (f *fx) meta(typ string) usecases.Meta {
	f.seq++
	return usecases.Meta{EventID: fmt.Sprintf("ev-%d", f.seq), Type: typ, At: at(100 + f.seq)}
}

func (f *fx) consumer() usecases.Consumer { return usecases.Consumer{Deps: f.deps} }

func assertOrder(t *testing.T, v views.Order, status string) {
	t.Helper()
	if v.Status != status {
		t.Fatalf("status = %s, want %s", v.Status, status)
	}
}

// failingOrders makes the first n Saves report a concurrent update.
type failingOrders struct {
	ports.OrderRepository
	failures int
	saves    int
}

func (f *failingOrders) Save(ctx context.Context, o *customerorder.CustomerOrder) error {
	f.saves++
	if f.failures > 0 {
		f.failures--
		return fmt.Errorf("simulated: %w", ports.ErrConcurrentUpdate)
	}
	return f.OrderRepository.Save(ctx, o)
}

var errValidation = shared.ErrValidation

func isValidation(err error) bool { return errorsIs(err, shared.ErrValidation) }
