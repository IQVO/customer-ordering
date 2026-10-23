package repocontract

import (
	"reflect"
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

func testOrders(t *testing.T, e Env) {
	t.Run("a miss is ErrNotFound", func(t *testing.T) {
		_, err := e.Orders.Get(bg(), "order-nope")
		wantErr(t, err, ports.ErrNotFound)
	})
	t.Run("a saved order reads back identically", func(t *testing.T) {
		o := newOrder(t, "order-rt")
		must(t, e.Orders.Save(bg(), o))
		got, err := e.Orders.Get(bg(), "order-rt")
		must(t, err)
		if !reflect.DeepEqual(got.Snapshot(), o.Snapshot()) {
			t.Fatalf("round trip changed the order:\n got %+v\nwant %+v", got.Snapshot(), o.Snapshot())
		}
	})
	t.Run("every group state and fact round-trips", func(t *testing.T) {
		o := newOrder(t, "order-facts")
		must(t, e.Orders.Save(bg(), o))
		must(t, o.MarkInFulfillment(1, "as-1", "fc-aus-1", at(10)))
		must(t, o.MarkGroupShipped(1, []string{"TRK-1", "TRK-2"}, at(20)))
		must(t, o.MarkGroupDelivered(1, "TRK-1", at(30)))
		must(t, o.UpdateGroupPromise("p-order-facts-2", 2, at(2000), at(2300), at(1900), "fc-aus-2"))
		_, err := o.CancelOrder("changed my mind", at(40))
		must(t, err)
		must(t, e.Orders.Save(bg(), o))
		got, err := e.Orders.Get(bg(), "order-facts")
		must(t, err)
		if !reflect.DeepEqual(got.Snapshot(), o.Snapshot()) {
			t.Fatalf("round trip changed the order:\n got %+v\nwant %+v", got.Snapshot(), o.Snapshot())
		}
	})
	t.Run("a lost and a returned group round-trip with their facts", func(t *testing.T) { ordersLostAndReturnedRoundTrip(t, e) })
	t.Run("a cancelled order keeps cancelledAt", func(t *testing.T) {
		o := newOrder(t, "order-cancelled")
		must(t, e.Orders.Save(bg(), o))
		_, err := o.CancelOrder("x", at(5))
		must(t, err)
		must(t, o.ConfirmGroupCancelled(1, at(6)))
		must(t, o.ConfirmGroupCancelled(2, at(7)))
		must(t, e.Orders.Save(bg(), o))
		got, err := e.Orders.Get(bg(), "order-cancelled")
		must(t, err)
		if got.Status() != customerorder.StatusCancelled || !got.CancelledAt().Equal(at(7)) {
			t.Fatalf("status/cancelledAt = %s %v", got.Status(), got.CancelledAt())
		}
	})
	t.Run("optimistic concurrency", func(t *testing.T) {
		o := newOrder(t, "order-cc")
		must(t, e.Orders.Save(bg(), o))
		dup := newOrder(t, "order-cc")
		wantErr(t, e.Orders.Save(bg(), dup), ports.ErrConcurrentUpdate) // create of a stored id

		first, err := e.Orders.Get(bg(), "order-cc")
		must(t, err)
		second, err := e.Orders.Get(bg(), "order-cc")
		must(t, err)
		must(t, first.MarkInFulfillment(1, "as-1", "fc", at(10)))
		must(t, second.MarkInFulfillment(2, "as-2", "fc", at(11)))
		must(t, e.Orders.Save(bg(), first))
		wantErr(t, e.Orders.Save(bg(), second), ports.ErrConcurrentUpdate) // stale

		snap := newOrder(t, "order-ghost").Snapshot()
		snap.Version = 2
		ghost, err := customerorder.Restore(snap) // loaded at version 2, never stored
		must(t, err)
		must(t, ghost.MarkInFulfillment(1, "as-9", "fc", at(10)))
		wantErr(t, e.Orders.Save(bg(), ghost), ports.ErrConcurrentUpdate) // update of a missing row

		got, err := e.Orders.Get(bg(), "order-cc")
		must(t, err)
		if got.Version() != 2 {
			t.Fatalf("stored version = %d, want 2 after one accepted update", got.Version())
		}
		third, err := e.Orders.Get(bg(), "order-cc")
		must(t, err)
		must(t, third.MarkInFulfillment(2, "as-3", "fc", at(12)))
		must(t, third.MarkGroupShipped(2, []string{"TRK-3"}, at(13)))
		must(t, e.Orders.Save(bg(), third)) // two commands, one save
		must(t, third.MarkGroupDelivered(2, "TRK-3", at(14)))
		must(t, e.Orders.Save(bg(), third)) // a saved order may be saved again
		final, err := e.Orders.Get(bg(), "order-cc")
		must(t, err)
		if final.Version() != 5 {
			t.Fatalf("stored version = %d, want 5", final.Version())
		}
	})
}

func testShoppers(t *testing.T, e Env) {
	t.Run("a miss is ErrNotFound", func(t *testing.T) {
		_, err := e.Shoppers.Get(bg(), "shopper-nope")
		wantErr(t, err, ports.ErrNotFound)
	})
	t.Run("a saved shopper reads back identically", func(t *testing.T) {
		s := newShopper(t, "shopper-1", "ana@example.com")
		must(t, e.Shoppers.Save(bg(), s))
		got, err := e.Shoppers.Get(bg(), "shopper-1")
		must(t, err)
		if !reflect.DeepEqual(got.Snapshot(), s.Snapshot()) {
			t.Fatalf("round trip changed the shopper:\n got %+v\nwant %+v", got.Snapshot(), s.Snapshot())
		}
	})
	t.Run("the e-mail is unique, case-insensitively", func(t *testing.T) {
		must(t, e.Shoppers.Save(bg(), newShopper(t, "shopper-a", "dup@example.com")))
		wantErr(t, e.Shoppers.Save(bg(), newShopper(t, "shopper-b", "DUP@example.com")), shopper.ErrShopperExists)
		_, err := e.Shoppers.Get(bg(), "shopper-b")
		wantErr(t, err, ports.ErrNotFound)
	})
	t.Run("creating a stored id again is a concurrent update", func(t *testing.T) {
		must(t, e.Shoppers.Save(bg(), newShopper(t, "shopper-c", "c@example.com")))
		wantErr(t, e.Shoppers.Save(bg(), newShopper(t, "shopper-c", "c2@example.com")), ports.ErrConcurrentUpdate)
	})
}

func ordersLostAndReturnedRoundTrip(t *testing.T, e Env) {
	o := newOrder(t, "order-outcomes")
	mustSave := func() { must(t, e.Orders.Save(bg(), o)) }
	mustSave()
	must(t, o.MarkGroupShipped(1, []string{"TRK-1", "TRK-2"}, at(20)))
	must(t, o.MarkGroupShipped(2, []string{"TRK-3"}, at(21)))
	must(t, o.MarkGroupLost(1, "TRK-2", at(30)))
	must(t, o.MarkGroupReturned(2, "TRK-3", at(31)))
	mustSave()
	got, err := e.Orders.Get(bg(), "order-outcomes")
	must(t, err)
	if !reflect.DeepEqual(got.Snapshot(), o.Snapshot()) || got.Status() != customerorder.StatusLost {
		t.Fatalf("round trip changed the order (status %s):\n got %+v\nwant %+v", got.Status(), got.Snapshot(), o.Snapshot())
	}
	if g := got.Groups(); g[0].LostAt.IsZero() || g[0].OutcomeTrackingNumber != "TRK-2" || g[1].ReturnedAt.IsZero() {
		t.Fatalf("outcome facts lost: %+v", g)
	}
}
