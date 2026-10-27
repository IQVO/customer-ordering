package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// placeAt places an order and advances the clock, so placedAt differs.
func (f *fx) placeAt(t *testing.T, minutes int) string {
	t.Helper()
	f.clock.Set(at(minutes))
	return f.place()
}

func TestListOrdersByShopperNewestFirst(t *testing.T) {
	f := newFx(t)
	ids := []string{f.placeAt(t, 0), f.placeAt(t, 10), f.placeAt(t, 20)}
	page, err := usecases.ListOrdersByShopper{Deps: f.deps}.Execute(context.Background(), usecases.ListOrdersInput{ShopperID: "shopper-1"})
	must(t, err)
	if len(page.Items) != 3 || page.NextCursor != "" {
		t.Fatalf("page = %d items, cursor %q", len(page.Items), page.NextCursor)
	}
	for i, want := range []string{ids[2], ids[1], ids[0]} {
		if page.Items[i].OrderID != want {
			t.Fatalf("item %d = %s, want %s (newest first)", i, page.Items[i].OrderID, want)
		}
	}
}

func TestListOrdersByShopperPagesWithACursor(t *testing.T) {
	f := newFx(t)
	for i := 0; i < 3; i++ {
		f.placeAt(t, i*10)
	}
	first, err := usecases.ListOrdersByShopper{Deps: f.deps}.Execute(context.Background(), usecases.ListOrdersInput{ShopperID: "shopper-1", Limit: 2})
	must(t, err)
	if len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %d items, cursor %q", len(first.Items), first.NextCursor)
	}
	second, err := usecases.ListOrdersByShopper{Deps: f.deps}.Execute(context.Background(), usecases.ListOrdersInput{ShopperID: "shopper-1", Limit: 2, After: first.NextCursor})
	must(t, err)
	if len(second.Items) != 1 || second.NextCursor != "" {
		t.Fatalf("second page = %d items, cursor %q", len(second.Items), second.NextCursor)
	}
	if second.Items[0].OrderID == first.Items[0].OrderID || second.Items[0].OrderID == first.Items[1].OrderID {
		t.Fatal("the second page repeats the first")
	}
	if !second.Items[0].PlacedAt.Before(first.Items[1].PlacedAt) {
		t.Fatal("pages must walk backwards in time")
	}
}

func TestListOrdersByShopperSameInstantTiebreaksOnOrderID(t *testing.T) {
	f := newFx(t)
	// The clock never moves: every order shares placedAt and the orderId decides.
	ids := []string{f.place(), f.place()}
	page, err := usecases.ListOrdersByShopper{Deps: f.deps}.Execute(context.Background(), usecases.ListOrdersInput{ShopperID: "shopper-1"})
	must(t, err)
	if len(page.Items) != 2 || page.Items[0].OrderID != ids[0] || page.Items[1].OrderID != ids[1] {
		t.Fatalf("tiebreak order = %s, %s; want %s, %s", page.Items[0].OrderID, page.Items[1].OrderID, ids[0], ids[1])
	}
}

func TestListOrdersByShopperLimits(t *testing.T) {
	f := newFx(t)
	f.place()
	page, err := usecases.ListOrdersByShopper{Deps: f.deps}.Execute(context.Background(), usecases.ListOrdersInput{ShopperID: "shopper-1", Limit: usecases.MaxOrderListLimit})
	must(t, err)
	if len(page.Items) != 1 {
		t.Fatalf("the max limit is accepted: %v", page.Items)
	}
	for _, limit := range []int{-1, usecases.MaxOrderListLimit + 1} {
		if _, err := (usecases.ListOrdersByShopper{Deps: f.deps}).Execute(context.Background(), usecases.ListOrdersInput{ShopperID: "shopper-1", Limit: limit}); !errors.Is(err, shared.ErrValidation) {
			t.Fatalf("limit %d: want ErrValidation, got %v", limit, err)
		}
	}
}

func TestListOrdersByShopperUnknownShopperAndBadCursor(t *testing.T) {
	f := newFx(t)
	if _, err := (usecases.ListOrdersByShopper{Deps: f.deps}).Execute(context.Background(), usecases.ListOrdersInput{ShopperID: "shopper-nope"}); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("unknown shopper: want ErrNotFound, got %v", err)
	}
	for _, after := range []string{"!!", "bm90LWEtcGFnaWU", "cGFzdGVk"} {
		if _, err := (usecases.ListOrdersByShopper{Deps: f.deps}).Execute(context.Background(), usecases.ListOrdersInput{ShopperID: "shopper-1", After: after}); !errors.Is(err, shared.ErrValidation) {
			t.Fatalf("cursor %q: want ErrValidation, got %v", after, err)
		}
	}
}

func TestListOrdersByShopperEmptyIsAnEmptyPage(t *testing.T) {
	f := newFx(t)
	page, err := usecases.ListOrdersByShopper{Deps: f.deps}.Execute(context.Background(), usecases.ListOrdersInput{ShopperID: "shopper-1"})
	must(t, err)
	if page.Items == nil || len(page.Items) != 0 || page.NextCursor != "" {
		t.Fatalf("page = %+v", page)
	}
}

func TestListOrdersByShopperExcludesOtherShoppers(t *testing.T) {
	f := newFx(t)
	f.place()
	other, err := registerSecond(t, f, "bob@example.com")
	must(t, err)
	in := checkout("order-other")
	in.ShopperID = other
	_, err = usecases.PlaceOrder{Deps: f.deps}.Execute(context.Background(), in)
	must(t, err)
	page, err := usecases.ListOrdersByShopper{Deps: f.deps}.Execute(context.Background(), usecases.ListOrdersInput{ShopperID: "shopper-1"})
	must(t, err)
	if len(page.Items) != 1 || page.Items[0].ShopperID != "shopper-1" {
		t.Fatalf("page = %+v", page.Items)
	}
}
