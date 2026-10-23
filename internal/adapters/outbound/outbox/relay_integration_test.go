//go:build integration

package outbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/kafka"
	relay "github.com/claudioed/customer-ordering/internal/adapters/outbound/outbox"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/postgres"
	"github.com/claudioed/customer-ordering/internal/application/outbox"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
	"github.com/claudioed/customer-ordering/internal/testing/kafkatest"
	"github.com/claudioed/customer-ordering/internal/testing/pgtest"
)

var t0 = time.Date(2026, 10, 23, 10, 15, 30, 0, time.UTC)

func placed(t *testing.T) (*customerorder.CustomerOrder, []customerorder.CustomerOrderPlaced) {
	t.Helper()
	o, evs, err := customerorder.Place(customerorder.NewOrder{
		ID: "order-relay", ShopperID: "shopper-1",
		ShipTo: shipto.ShipTo{Name: "Ana Example", Line1: "1 Sample Street", City: "Austin", Region: "TX", PostalCode: "78701", CountryCode: "US"},
		Lines:  []customerorder.OrderLine{{LineNo: 1, CatalogItemID: "sku-1", Quantity: 2}},
		Groups: []customerorder.GroupInput{{
			GroupNo: 1, PromiseID: "p-1", Tier: customerorder.TierNextDay, WitnessNodeID: "fc-1", ShipBy: t0.Add(time.Hour),
			DeliverByEarliest: t0.Add(2 * time.Hour), DeliverByLatest: t0.Add(3 * time.Hour), LineNos: []int{1}, DestinationPostalPrefix: "787",
		}},
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	return o, []customerorder.CustomerOrderPlaced{evs[0].(customerorder.CustomerOrderPlaced)}
}

func TestOrderAndOutboxCommitTogetherAndTheRelayPublishesTheExactCloudEvent(t *testing.T) {
	pool := pgtest.NewPool(t)
	topic := kafkatest.Topic(t, "retailnetwork.customer-ordering")
	repo := postgres.NewOutboxRepo(pool, &kafka.Encoder{Topic: topic})
	orders, uow := postgres.NewOrderRepository(pool), postgres.NewUnitOfWork(pool)

	o, evs := placed(t)
	err := uow.Do(context.Background(), func(ctx context.Context) error {
		if err := orders.Save(ctx, o); err != nil {
			return err
		}
		return repo.Append(ctx, evs[0])
	})
	if err != nil {
		t.Fatal(err)
	}

	writer := kafka.NewWriter(kafkatest.Brokers(t)[0])
	defer func() { _ = writer.Close() }()
	r := relay.NewRelay(repo, kafka.Sink{W: writer}, nil, relay.WithMaxAttempts(relay.DefaultMaxAttempts))
	n, err := r.RelayOnce(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("RelayOnce: %d %v", n, err)
	}
	pgtest.WaitAllPublished(t, pool)

	msgs := kafkatest.ReadN(t, topic, 1, 30*time.Second)
	if msgs[0].Key != "order-relay" || msgs[0].Headers["content-type"] != cloudevents.MediaType {
		t.Fatalf("message = %+v", msgs[0])
	}
	e, err := cloudevents.Decode(msgs[0].Value)
	if err != nil {
		t.Fatal(err)
	}
	if e.Type() != "com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderPlaced" || e.Subject() != "order-relay" || e.Source() != "/retailnetwork/customer-ordering" {
		t.Fatalf("envelope = %v", e)
	}
	var stored string
	if err := pool.QueryRow(context.Background(), `SELECT event_id FROM outbox_events`).Scan(&stored); err != nil || stored != e.ID() {
		t.Fatalf("the persisted event id %q must be the published one %q (%v)", stored, e.ID(), err)
	}
}

func TestARolledBackUnitLeavesNoOutboxRow(t *testing.T) {
	pool := pgtest.NewPool(t)
	repo := postgres.NewOutboxRepo(pool, kafka.NewEncoder())
	o, evs := placed(t)
	boom := errors.New("boom")
	err := postgres.NewUnitOfWork(pool).Do(context.Background(), func(ctx context.Context) error {
		if err := postgres.NewOrderRepository(pool).Save(ctx, o); err != nil {
			return err
		}
		if err := repo.Append(ctx, evs[0]); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	var orders, rows int
	if err := pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM customer_orders), (SELECT count(*) FROM outbox_events)`).Scan(&orders, &rows); err != nil {
		t.Fatal(err)
	}
	if orders != 0 || rows != 0 {
		t.Fatalf("orders=%d outbox rows=%d after a rollback", orders, rows)
	}
}

func TestARowThatKeepsFailingStopsBlockingLaterRowsAtTheCap(t *testing.T) {
	pool := pgtest.NewPool(t)
	repo := postgres.NewOutboxRepo(pool, kafka.NewEncoder())
	bad := pgtest.InsertOutboxRow(t, pool, "x.Bad", "k-bad")
	good := pgtest.InsertOutboxRow(t, pool, "x.Good", "k-good")
	sink := failingFor{bad: "x.Bad"}
	r := relay.NewRelay(repo, &sink, nil, relay.WithMaxAttempts(2))

	for pass := 1; pass <= 2; pass++ {
		if n, err := r.RelayOnce(context.Background()); n != 0 || err == nil {
			t.Fatalf("pass %d: n=%d err=%v, want the bad row to fail first", pass, n, err)
		}
	}
	if attempts, lastErr, published := pgtest.OutboxRowState(t, pool, bad); attempts != 2 || lastErr == "" || published {
		t.Fatalf("bad row = %d %q %v", attempts, lastErr, published)
	}
	n, err := r.RelayOnce(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("after the cap the good row overtakes: n=%d err=%v", n, err)
	}
	if _, _, published := pgtest.OutboxRowState(t, pool, good); !published {
		t.Fatal("the good row must be published")
	}
	if attempts, _, published := pgtest.OutboxRowState(t, pool, bad); attempts != 2 || published {
		t.Fatalf("the capped row stays in the outbox: %d %v", attempts, published)
	}
}

type failingFor struct{ bad string }

func (f *failingFor) Send(_ context.Context, msgs ...outbox.Message) error {
	for _, m := range msgs {
		if m.EventType == f.bad {
			return errors.New("broker said no")
		}
	}
	return nil
}
