package usecases_test

import (
	"context"
	"errors"
	"slices"
	"sort"
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/pagination"
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
	if len(page.Items) != 3 || page.Next != nil {
		t.Fatalf("page = %d items, next %+v", len(page.Items), page.Next)
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
	if len(first.Items) != 2 || first.Next == nil {
		t.Fatalf("first page = %d items, next %+v", len(first.Items), first.Next)
	}
	second, err := usecases.ListOrdersByShopper{Deps: f.deps}.Execute(context.Background(), usecases.ListOrdersInput{ShopperID: "shopper-1", Limit: 2, After: first.Next})
	must(t, err)
	if len(second.Items) != 1 || second.Next != nil {
		t.Fatalf("second page = %d items, next %+v", len(second.Items), second.Next)
	}
	if second.Items[0].OrderID == first.Items[0].OrderID || second.Items[0].OrderID == first.Items[1].OrderID {
		t.Fatal("the second page repeats the first")
	}
	if !second.Items[0].PlacedAt.Before(first.Items[1].PlacedAt) {
		t.Fatal("pages must walk backwards in time")
	}
}

// TestListOrdersByShopperWalksEveryPage places more orders than one page holds,
// several of them at the same instant, and walks the keyset: every order shows
// up exactly once, in (placedAt DESC, orderId ASC) order.
func TestListOrdersByShopperWalksEveryPage(t *testing.T) {
	f := newFx(t)
	type placed struct {
		id string
		m  int
	}
	var all []placed
	for _, m := range []int{0, 0, 10, 10, 10, 20, 30} {
		all = append(all, placed{f.placeAt(t, m), m})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].m != all[j].m {
			return all[i].m > all[j].m
		}
		return all[i].id < all[j].id
	})
	var want, got []string
	for _, p := range all {
		want = append(want, p.id)
	}
	var after *pagination.Keyset
	for pages := 0; ; pages++ {
		if pages > len(want) {
			t.Fatal("the walk does not end")
		}
		page, err := usecases.ListOrdersByShopper{Deps: f.deps}.Execute(context.Background(), usecases.ListOrdersInput{ShopperID: "shopper-1", Limit: 3, After: after})
		must(t, err)
		for _, o := range page.Items {
			got = append(got, o.OrderID)
		}
		if page.Next == nil {
			break
		}
		if len(page.Items) != 3 {
			t.Fatalf("a page with a next cursor holds %d items, want 3", len(page.Items))
		}
		after = page.Next
	}
	if !slices.Equal(got, want) {
		t.Fatalf("walk = %v\nwant   %v (no duplicate, no skipped row)", got, want)
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

func TestListOrdersByShopperUnknownShopper(t *testing.T) {
	f := newFx(t)
	if _, err := (usecases.ListOrdersByShopper{Deps: f.deps}).Execute(context.Background(), usecases.ListOrdersInput{ShopperID: "shopper-nope"}); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("unknown shopper: want ErrNotFound, got %v", err)
	}
}

func TestListOrdersByShopperEmptyIsAnEmptyPage(t *testing.T) {
	f := newFx(t)
	page, err := usecases.ListOrdersByShopper{Deps: f.deps}.Execute(context.Background(), usecases.ListOrdersInput{ShopperID: "shopper-1"})
	must(t, err)
	if page.Items == nil || len(page.Items) != 0 || page.Next != nil {
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
