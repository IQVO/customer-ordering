package repocontract

import (
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
)

// newOrderAt places an order of one group at a chosen instant: the order
// history needs distinct placedAt values and shared ones for the tiebreak.
func newOrderAt(t *testing.T, id, shopperID string, at time.Time) *customerorder.CustomerOrder {
	t.Helper()
	o, _, err := customerorder.Place(customerorder.NewOrder{
		ID: customerorder.ID(id), ShopperID: shopperID, ShipTo: address(),
		Lines:  []customerorder.OrderLine{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2}, {LineNo: 2, CatalogItemID: "sku-200", Quantity: 1}},
		Groups: []customerorder.GroupInput{groupInput(1, "p-"+id+"-1", 1, 2)},
	}, at)
	if err != nil {
		t.Fatalf("Place %s: %v", id, err)
	}
	return o
}

// testListByShopper is the contract of the order history query (ADR 0002 A26):
// newest first, orderId tiebreak, keyset strictly after the cursor, only the
// shopper's own orders, and an empty page (never an error) without orders.
func testListByShopper(t *testing.T, e Env) {
	t.Helper()
	must(t, e.Orders.Save(bg(), newOrderAt(t, "order-old", "shopper-h", at(0))))
	must(t, e.Orders.Save(bg(), newOrderAt(t, "order-mid", "shopper-h", at(10))))
	must(t, e.Orders.Save(bg(), newOrderAt(t, "order-new", "shopper-h", at(20))))
	must(t, e.Orders.Save(bg(), newOrderAt(t, "order-elsewhere", "shopper-2", at(30))))

	page, err := e.Orders.ListByShopper(bg(), "shopper-h", time.Time{}, "", 10)
	must(t, err)
	want := []customerorder.ID{"order-new", "order-mid", "order-old"}
	if len(page) != len(want) {
		t.Fatalf("history of shopper-h = %d orders, want %d", len(page), len(want))
	}
	for i, id := range want {
		if page[i].ID() != id {
			t.Fatalf("history[%d] = %s, want %s (newest first)", i, page[i].ID(), id)
		}
	}

	// The keyset resumes strictly after (placedAt, orderId): one order left.
	mid := page[1]
	rest, err := e.Orders.ListByShopper(bg(), "shopper-h", mid.PlacedAt(), string(mid.ID()), 10)
	must(t, err)
	if len(rest) != 1 || rest[0].ID() != "order-old" {
		t.Fatalf("history after (%s, %s) = %v", mid.PlacedAt(), mid.ID(), ids(rest))
	}

	// A limit shorter than the history returns the newest page.
	limited, err := e.Orders.ListByShopper(bg(), "shopper-h", time.Time{}, "", 2)
	must(t, err)
	if len(limited) != 2 || limited[0].ID() != "order-new" || limited[1].ID() != "order-mid" {
		t.Fatalf("limited history = %v", ids(limited))
	}

	// Two orders at the same instant: the orderId decides, ascending.
	must(t, e.Orders.Save(bg(), newOrderAt(t, "order-tie-b", "shopper-tie", at(40))))
	must(t, e.Orders.Save(bg(), newOrderAt(t, "order-tie-a", "shopper-tie", at(40))))
	tie, err := e.Orders.ListByShopper(bg(), "shopper-tie", time.Time{}, "", 10)
	must(t, err)
	if len(tie) != 2 || tie[0].ID() != "order-tie-a" || tie[1].ID() != "order-tie-b" {
		t.Fatalf("tiebreak = %v", ids(tie))
	}
	afterTie, err := e.Orders.ListByShopper(bg(), "shopper-tie", tie[0].PlacedAt(), string(tie[0].ID()), 10)
	must(t, err)
	if len(afterTie) != 1 || afterTie[0].ID() != "order-tie-b" {
		t.Fatalf("history after the first tie = %v", ids(afterTie))
	}

	walkEveryPage(t, e)

	// A shopper without orders gets an empty page, never an error.
	empty, err := e.Orders.ListByShopper(bg(), "shopper-none", time.Time{}, "", 10)
	must(t, err)
	if len(empty) != 0 {
		t.Fatalf("history of a shopper without orders = %v", ids(empty))
	}
}

// walkEveryPage holds more rows than a page, some sharing the sort key, and
// follows the keyset (limit+1 per call, like the use case): every order shows
// up exactly once in (placedAt DESC, orderId ASC) order.
func walkEveryPage(t *testing.T, e Env) {
	t.Helper()
	rows := []struct {
		id string
		m  int
	}{{"order-w1", 50}, {"order-w2", 50}, {"order-w3", 50}, {"order-w4", 51}, {"order-w5", 52}, {"order-w6", 52}, {"order-w7", 53}}
	for _, r := range rows {
		must(t, e.Orders.Save(bg(), newOrderAt(t, r.id, "shopper-walk", at(r.m))))
	}
	want := []string{"order-w7", "order-w5", "order-w6", "order-w4", "order-w1", "order-w2", "order-w3"}
	const limit = 3
	var got []string
	var afterAt time.Time
	afterID := ""
	for pages := 0; pages <= len(want); pages++ {
		rows, err := e.Orders.ListByShopper(bg(), "shopper-walk", afterAt, afterID, limit+1)
		must(t, err)
		more := len(rows) > limit
		if more {
			rows = rows[:limit]
		}
		for _, o := range rows {
			got = append(got, string(o.ID()))
		}
		if !more {
			break
		}
		last := rows[len(rows)-1]
		afterAt, afterID = last.PlacedAt(), string(last.ID())
	}
	if len(got) != len(want) {
		t.Fatalf("walk = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("walk = %v, want %v (no duplicate, no skipped row)", got, want)
		}
	}
}

func ids(orders []*customerorder.CustomerOrder) string {
	out := ""
	for i, o := range orders {
		if i > 0 {
			out += ", "
		}
		out += string(o.ID())
	}
	if out == "" {
		return "(empty)"
	}
	return out
}
