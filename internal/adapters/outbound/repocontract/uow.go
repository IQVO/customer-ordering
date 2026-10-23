package repocontract

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/idempotency"
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

func testIdempotency(t *testing.T, e Env) {
	_, err := e.Idempotency.Get(bg(), "POST /checkout", "k1")
	wantErr(t, err, ports.ErrNotFound)
	rec := idempotency.Record{RequestHash: "h1", Status: 201, ContentType: "application/json", Body: []byte(`{"a":1}`), CreatedAt: time.Now().UTC()}
	stored, err := e.Idempotency.Put(bg(), "POST /checkout", "k1", rec)
	must(t, err)
	if !stored {
		t.Fatal("the first Put must store")
	}
	stored, err = e.Idempotency.Put(bg(), "POST /checkout", "k1", idempotency.Record{RequestHash: "h2", Status: 500, CreatedAt: time.Now().UTC()})
	must(t, err)
	if stored {
		t.Fatal("a second Put must not overwrite")
	}
	got, err := e.Idempotency.Get(bg(), "POST /checkout", "k1")
	must(t, err)
	if got.RequestHash != "h1" || got.Status != 201 || got.ContentType != "application/json" || string(got.Body) != `{"a":1}` {
		t.Fatalf("stored record = %+v", got)
	}
	if _, err := e.Idempotency.Get(bg(), "POST /shoppers", "k1"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("the route is part of the key: %v", err)
	}
	old := idempotency.Record{RequestHash: "old", Status: 200, CreatedAt: time.Now().UTC().Add(-idempotency.TTL - time.Minute)}
	if _, err := e.Idempotency.Put(bg(), "POST /shoppers", "k-old", old); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Idempotency.Get(bg(), "POST /shoppers", "k-old"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("an expired record must read as absent: %v", err)
	}
	replaced, err := e.Idempotency.Put(bg(), "POST /shoppers", "k-old", idempotency.Record{RequestHash: "new", Status: 201, CreatedAt: time.Now().UTC()})
	must(t, err)
	if !replaced {
		t.Fatal("an expired record is replaceable")
	}
}
