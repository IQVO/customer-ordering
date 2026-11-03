package repocontract

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

func testInbox(t *testing.T, e Env) {
	first, err := e.Inbox.Claim(bg(), "ev-1", "type.A")
	must(t, err)
	again, err := e.Inbox.Claim(bg(), "ev-1", "type.A")
	must(t, err)
	other, err := e.Inbox.Claim(bg(), "ev-2", "type.A")
	must(t, err)
	if !first || again || !other {
		t.Fatalf("claims = %v %v %v, want true false true", first, again, other)
	}
}

var errBoom = errors.New("boom")

func testUnitOfWork(t *testing.T, e Env) {
	t.Run("a unit that fails rolls back every table", func(t *testing.T) {
		err := e.UoW.Do(bg(), func(ctx context.Context) error {
			must(t, e.Orders.Save(ctx, newOrder(t, "order-rb")))
			must(t, e.Shoppers.Save(ctx, newShopper(t, "shopper-rb", "rb@example.com")))
			must(t, e.Tracking.LinkAssignment(ctx, readmodel.AssignmentLink{AssignmentID: "as-rb", OrderID: "order-rb", GroupNo: 1}))
			must(t, e.Tracking.AttachTrackingNumbers(ctx, "order-rb", 1, []string{"TRK-RB"}))
			first, err := e.Inbox.Claim(ctx, "ev-rb", "type.A")
			must(t, err)
			if !first {
				t.Fatal("the claim inside the unit must be the first")
			}
			must(t, e.Outbox.Append(ctx, mustEvents(t)...))
			return errBoom
		})
		wantErr(t, err, errBoom)
		_, err = e.Orders.Get(bg(), "order-rb")
		wantErr(t, err, ports.ErrNotFound)
		_, err = e.Shoppers.Get(bg(), "shopper-rb")
		wantErr(t, err, ports.ErrNotFound)
		_, err = e.Tracking.ResolveAssignment(bg(), "as-rb")
		wantErr(t, err, ports.ErrNotFound)
		if list, _ := e.Tracking.Packages(bg(), "order-rb"); len(list) != 0 {
			t.Fatalf("rolled back packages are visible: %+v", list)
		}
		if first, _ := e.Inbox.Claim(bg(), "ev-rb", "type.A"); !first {
			t.Fatal("the rolled back claim must be free again")
		}
		if n := e.OutboxCount(t); n != 0 {
			t.Fatalf("outbox holds %d events after a rollback", n)
		}
	})
	t.Run("a unit that succeeds commits everything", func(t *testing.T) {
		must(t, e.UoW.Do(bg(), func(ctx context.Context) error {
			must(t, e.Orders.Save(ctx, newOrder(t, "order-ok")))
			must(t, e.Outbox.Append(ctx, mustEvents(t)...))
			return nil
		}))
		if _, err := e.Orders.Get(bg(), "order-ok"); err != nil {
			t.Fatalf("committed order missing: %v", err)
		}
		if n := e.OutboxCount(t); n != 1 {
			t.Fatalf("outbox holds %d events, want 1", n)
		}
	})
	t.Run("a nested unit joins the outer one", func(t *testing.T) {
		err := e.UoW.Do(bg(), func(ctx context.Context) error {
			must(t, e.UoW.Do(ctx, func(ctx context.Context) error { return e.Orders.Save(ctx, newOrder(t, "order-nest")) }))
			return errBoom
		})
		wantErr(t, err, errBoom)
		_, err = e.Orders.Get(bg(), "order-nest")
		wantErr(t, err, ports.ErrNotFound)
	})
	t.Run("a failing nested unit undoes only its own writes", func(t *testing.T) {
		must(t, e.UoW.Do(bg(), func(ctx context.Context) error {
			must(t, e.Orders.Save(ctx, newOrder(t, "order-outer")))
			err := e.UoW.Do(ctx, func(ctx context.Context) error {
				must(t, e.Orders.Save(ctx, newOrder(t, "order-inner")))
				return errBoom
			})
			wantErr(t, err, errBoom)
			return nil
		}))
		if _, err := e.Orders.Get(bg(), "order-outer"); err != nil {
			t.Fatalf("the outer write must commit: %v", err)
		}
		_, err := e.Orders.Get(bg(), "order-inner")
		wantErr(t, err, ports.ErrNotFound)
	})
}

func mustEvents(t *testing.T) []shared.Event {
	t.Helper()
	_, evs, err := customerorder.Place(customerorder.NewOrder{
		ID: "order-ev", ShopperID: "shopper-1", ShipTo: address(),
		Lines:  []customerorder.OrderLine{{LineNo: 1, CatalogItemID: "sku-1", Quantity: 1}},
		Groups: []customerorder.GroupInput{groupInput(1, "p-ev", 1)},
	}, t0)
	must(t, err)
	return evs
}
