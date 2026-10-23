package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/memory"
	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/promising"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/application/views"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

func errorsIs(err, target error) bool { return errors.Is(err, target) }

var bg = context.Background

func TestRegisterShopper(t *testing.T) {
	f := newFx(t)
	uc := usecases.RegisterShopper{Deps: f.deps}
	v, err := uc.Execute(bg(), usecases.RegisterShopperInput{Email: " Bea@Example.com ", SavedAddresses: []shipto.ShipTo{address()}})
	must(t, err)
	if v.ShopperID != "shopper-1" && v.ShopperID != "shopper-2" {
		t.Fatalf("shopper id = %q", v.ShopperID)
	}
	if v.Email != "bea@example.com" || v.Status != "ACTIVE" || !v.RegisteredAt.Equal(t0) {
		t.Fatalf("view = %+v", v)
	}
	got, err := f.store.Shoppers().Get(bg(), shopper.ID(v.ShopperID))
	must(t, err)
	if len(got.SavedAddresses()) != 1 {
		t.Fatal("saved address lost")
	}
}

func TestRegisterShopperRefusals(t *testing.T) {
	f := newFx(t)
	uc := usecases.RegisterShopper{Deps: f.deps}
	_, err := uc.Execute(bg(), usecases.RegisterShopperInput{Email: "ANA@example.com"})
	if !errors.Is(err, shopper.ErrShopperExists) {
		t.Fatalf("want ErrShopperExists, got %v", err)
	}
	before := f.store.Counts()
	if _, err := uc.Execute(bg(), usecases.RegisterShopperInput{Email: "nope"}); !isValidation(err) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
	if f.store.Counts() != before {
		t.Fatal("a refused registration must store nothing")
	}
}

func TestPlaceOrderMakesThenConfirmsThenStores(t *testing.T) {
	f := newFx(t)
	v, err := usecases.PlaceOrder{Deps: f.deps}.Execute(bg(), checkout("order-1"))
	must(t, err)
	assertOrder(t, v, "PLACED")
	assertPlacedView(t, v)
	assertMakeCalls(t, f.gw.Made())
	assertConfirmCalls(t, f.gw.Confirmed())
	events := f.store.Outbox().Events()
	if len(events) != 1 || events[0].EventName() != "CustomerOrderPlaced" || events[0].SubjectID() != "order-1" {
		t.Fatalf("events = %+v", events)
	}
	if c := f.store.Counts(); c.Orders != 1 || c.OutboxEvents != 1 {
		t.Fatalf("counts = %+v", c)
	}
}

func assertPlacedView(t *testing.T, v views.Order) {
	t.Helper()
	if v.OrderID != "order-1" || v.ShopperID != "shopper-1" || v.DestinationPostalPrefix != "787" || len(v.Lines) != 3 || len(v.Groups) != 2 {
		t.Fatalf("view = %+v", v)
	}
	g := v.Groups[0]
	if g.PromiseID != "p-1" || g.Tier != "NEXT_DAY" || g.Status != "PROMISED" || len(g.LineNos) != 2 {
		t.Fatalf("group = %+v", g)
	}
}

func assertMakeCalls(t *testing.T, made []promising.MakeRequest) {
	t.Helper()
	if len(made) != 2 {
		t.Fatalf("made = %+v", made)
	}
	first, second := made[0], made[1]
	if first.OrderID != "order-1" || first.GroupNo != 1 || first.OfferID != "offer-1" || len(first.Lines) != 2 {
		t.Fatalf("first MakePromise = %+v", first)
	}
	if first.Lines[0].CatalogItemID != "sku-100" || first.Lines[0].Quantity != 2 || second.GroupNo != 2 || len(second.Lines) != 1 {
		t.Fatalf("lines of the MakePromise calls = %+v %+v", first.Lines, second)
	}
}

func assertConfirmCalls(t *testing.T, confirmed []memory.ConfirmCall) {
	t.Helper()
	if len(confirmed) != 2 {
		t.Fatalf("confirmed = %+v", confirmed)
	}
	if confirmed[0] != (memory.ConfirmCall{PromiseID: "p-1", OrderID: "order-1", GroupNo: 1}) ||
		confirmed[1] != (memory.ConfirmCall{PromiseID: "p-2", OrderID: "order-1", GroupNo: 2}) {
		t.Fatalf("confirmed = %+v", confirmed)
	}
}

func TestPlaceOrderReplayReturnsTheStoredOrder(t *testing.T) {
	f := newFx(t)
	uc := usecases.PlaceOrder{Deps: f.deps}
	first, err := uc.Execute(bg(), checkout("order-1"))
	must(t, err)
	second, err := uc.Execute(bg(), checkout("order-1"))
	must(t, err)
	if first.OrderID != second.OrderID || len(f.gw.Made()) != 2 || len(f.gw.Confirmed()) != 2 || f.store.Counts().OutboxEvents != 1 {
		t.Fatalf("a replay must not call delivery-promise again or publish twice: made %d confirmed %d", len(f.gw.Made()), len(f.gw.Confirmed()))
	}
	other := checkout("order-1")
	other.ShopperID = "shopper-2"
	if _, err := uc.Execute(bg(), other); !errors.Is(err, ports.ErrConcurrentUpdate) {
		t.Fatalf("an order id used by another shopper is a conflict: %v", err)
	}
}

func TestPlaceOrderRequotesWhenAnOfferIsNotQuotable(t *testing.T) {
	f := newFx(t)
	delete(f.gw.Terms, "offer-2") // offer-2 expired
	_, err := usecases.PlaceOrder{Deps: f.deps}.Execute(bg(), checkout("order-1"))
	var rq *usecases.RequoteRequired
	if !errors.As(err, &rq) || !errors.Is(err, ports.ErrPromiseOfferNotQuotable) {
		t.Fatalf("want RequoteRequired, got %v", err)
	}
	assertRequoted(t, rq)
	quoted := f.gw.Quoted()
	if len(quoted) != 2 || quoted[0].Prefix != "787" || len(quoted[0].Lines) != 2 || len(quoted[1].Lines) != 1 {
		t.Fatalf("quotes = %+v", quoted)
	}
	if len(f.gw.Confirmed()) != 0 {
		t.Fatal("nothing is confirmed when a group could not be promised")
	}
	if c := f.store.Counts(); c.Orders != 0 || c.OutboxEvents != 0 {
		t.Fatalf("no order may be placed: %+v", c)
	}
}

func assertRequoted(t *testing.T, rq *usecases.RequoteRequired) {
	t.Helper()
	if len(rq.Offers) != 2 || rq.Offers[0].GroupNo != 1 || rq.Offers[1].GroupNo != 2 || rq.Offers[0].OfferID != "new-1" || rq.Offers[0].Tier != "NEXT_DAY" {
		t.Fatalf("offers = %+v", rq.Offers)
	}
	if rq.Error() == "" {
		t.Fatal("RequoteRequired needs a message")
	}
}

func TestPlaceOrderRequotesWhenAConfirmFails(t *testing.T) {
	f := newFx(t)
	f.gw.ConfirmErr["p-2"] = ports.ErrPromiseOfferNotQuotable
	_, err := usecases.PlaceOrder{Deps: f.deps}.Execute(bg(), checkout("order-1"))
	var rq *usecases.RequoteRequired
	if !errors.As(err, &rq) {
		t.Fatalf("want RequoteRequired, got %v", err)
	}
	if f.store.Counts().Orders != 0 {
		t.Fatal("no order may be placed after a failed confirm")
	}
	if len(f.gw.Confirmed()) != 2 {
		t.Fatalf("both confirms were attempted in order, got %+v", f.gw.Confirmed())
	}
}

func TestPlaceOrderPassesDeliveryPromiseFailuresThrough(t *testing.T) {
	cases := map[string]struct {
		arrange func(f *fx)
		want    error
	}{
		"capacity":            {func(f *fx) { f.gw.MakeErr["offer-1"] = ports.ErrPromiseCapacityExhausted }, ports.ErrPromiseCapacityExhausted},
		"book closed":         {func(f *fx) { f.gw.MakeErr["offer-2"] = ports.ErrPromiseBookClosed }, ports.ErrPromiseBookClosed},
		"spans nodes":         {func(f *fx) { f.gw.MakeErr["offer-1"] = ports.ErrPromiseGroupSpansNodes }, ports.ErrPromiseGroupSpansNodes},
		"unavailable":         {func(f *fx) { f.gw.MakeErr["offer-1"] = ports.ErrPromiseUnavailable }, ports.ErrPromiseUnavailable},
		"confirm unavailable": {func(f *fx) { f.gw.ConfirmErr["p-1"] = ports.ErrPromiseUnavailable }, ports.ErrPromiseUnavailable},
		"quote unavailable": {func(f *fx) {
			delete(f.gw.Terms, "offer-1")
			f.gw.QuoteErr = ports.ErrPromiseUnavailable
		}, ports.ErrPromiseUnavailable},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFx(t)
			c.arrange(f)
			_, err := usecases.PlaceOrder{Deps: f.deps}.Execute(bg(), checkout("order-1"))
			if !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
			if f.store.Counts().Orders != 0 {
				t.Fatal("no order may be placed")
			}
		})
	}
}

func TestPlaceOrderRefusesBeforeCallingDeliveryPromise(t *testing.T) {
	cases := map[string]struct {
		mutate func(*usecases.PlaceOrderInput)
		want   error
	}{
		"no lines":         {func(in *usecases.PlaceOrderInput) { in.Lines = nil }, errValidation},
		"line uncovered":   {func(in *usecases.PlaceOrderInput) { in.Groups[1].LineNos = nil }, errValidation},
		"line in no group": {func(in *usecases.PlaceOrderInput) { in.Groups = in.Groups[:1] }, customerorder.ErrLineWithoutPromise},
		"bad ship-to":      {func(in *usecases.PlaceOrderInput) { in.ShipTo.PostalCode = "7" }, errValidation},
		"bad offer id":     {func(in *usecases.PlaceOrderInput) { in.Groups[0].OfferID = "" }, errValidation},
		"bad order id":     {func(in *usecases.PlaceOrderInput) { in.OrderID = "" }, errValidation},
		"unknown shopper":  {func(in *usecases.PlaceOrderInput) { in.ShopperID = "shopper-9" }, errValidation},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFx(t)
			in := checkout("order-1")
			c.mutate(&in)
			_, err := usecases.PlaceOrder{Deps: f.deps}.Execute(bg(), in)
			if !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
			if len(f.gw.Made()) != 0 || f.store.Counts().Orders != 0 {
				t.Fatal("delivery-promise must not be called for a bad request")
			}
		})
	}
}

func TestPlaceOrderChecksTheDestinationBeforeConfirming(t *testing.T) {
	f := newFx(t)
	bad := terms("p-2", "fc-aus-2")
	bad.DestinationPostalPrefix = "100"
	f.gw.Terms["offer-2"] = bad
	_, err := usecases.PlaceOrder{Deps: f.deps}.Execute(bg(), checkout("order-1"))
	if !errors.Is(err, customerorder.ErrPromiseDestinationMismatch) {
		t.Fatalf("want ErrPromiseDestinationMismatch, got %v", err)
	}
	if len(f.gw.Confirmed()) != 0 || f.store.Counts().Orders != 0 {
		t.Fatalf("nothing may be confirmed or stored: %+v", f.gw.Confirmed())
	}
}

func TestPlaceOrderRetriesAConcurrentSave(t *testing.T) {
	f := newFx(t)
	failing := &failingOrders{OrderRepository: f.store.Orders(), failures: 1}
	f.deps.Orders = failing
	_, err := usecases.PlaceOrder{Deps: f.deps}.Execute(bg(), checkout("order-1"))
	must(t, err)
	if failing.saves != 2 || f.store.Counts().Orders != 1 || f.store.Counts().OutboxEvents != 1 {
		t.Fatalf("saves = %d counts = %+v", failing.saves, f.store.Counts())
	}
	f2 := newFx(t)
	always := &failingOrders{OrderRepository: f2.store.Orders(), failures: 99}
	f2.deps.Orders = always
	if _, err := (usecases.PlaceOrder{Deps: f2.deps}).Execute(bg(), checkout("order-1")); !errors.Is(err, ports.ErrConcurrentUpdate) {
		t.Fatalf("want ErrConcurrentUpdate, got %v", err)
	}
	if always.saves != 2 || f2.store.Counts().OutboxEvents != 0 {
		t.Fatalf("a command runs twice, then reports the conflict: saves = %d", always.saves)
	}
}

func TestPlaceOrderPropagatesRepositoryFailures(t *testing.T) {
	f := newFx(t)
	f.deps.Orders = brokenOrders{}
	if _, err := (usecases.PlaceOrder{Deps: f.deps}).Execute(bg(), checkout("order-1")); !errors.Is(err, errDisk) {
		t.Fatalf("want the repository error, got %v", err)
	}
	g := newFx(t)
	g.deps.Shoppers = brokenShoppers{}
	if _, err := (usecases.PlaceOrder{Deps: g.deps}).Execute(bg(), checkout("order-1")); !errors.Is(err, errDisk) {
		t.Fatalf("want the repository error, got %v", err)
	}
}

var errDisk = errors.New("disk on fire")

type brokenOrders struct{ ports.OrderRepository }

func (brokenOrders) Get(context.Context, customerorder.ID) (*customerorder.CustomerOrder, error) {
	return nil, errDisk
}

type brokenShoppers struct{ ports.ShopperRepository }

func (brokenShoppers) Get(context.Context, shopper.ID) (*shopper.Shopper, error) { return nil, errDisk }
