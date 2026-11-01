//go:build integration

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/testing/kafkatest"
	"github.com/claudioed/customer-ordering/internal/testing/pgtest"
)

// File order matters: e2e_integration_test.go stops the shared Kafka broker in
// its last subtest, so every test that needs Kafka sorts before it by file name.
//
// A19 over the real wiring: PackageLost and PackageReturned travel on the
// shipping-and-tracking topic of a real Kafka, are folded into the groups and
// the order in a real Postgres, and are visible through REST only.

const journey = "com.retailnetwork.logistics.shipping-and-tracking.journey."

func TestPackageLostAndReturnedFoldIntoTheOrderOverKafka(t *testing.T) {
	dsn := pgtest.URL(t)
	brokers := kafkatest.Brokers(t)
	for _, topic := range []string{topicEvents, topicSourcing, topicShipping, topicPromise} {
		kafkatest.CreateTopic(t, topic)
	}
	dp := fakeDeliveryPromise(t)
	svc := boot(t, map[string]string{
		"DATABASE_URL": dsn, "KAFKA_BROKERS": strings.Join(brokers, ","), "DELIVERY_PROMISE_URL": dp.URL,
		"DELIVERY_PROMISE_CONSUMER_GROUP": "co-lr-promise", "ORDER_SOURCING_CONSUMER_GROUP": "co-lr-sourcing", "SHIPPING_CONSUMER_GROUP": "co-lr-shipping",
		"OUTBOX_RELAY_INTERVAL": "100ms", "SHUTDOWN_DRAIN_DELAY": "0", "LOG_LEVEL": "warn",
	})
	shopper := svc.call(t, "POST", "/shoppers", "shopper-key", `{"email":"ana@example.com"}`)
	body := fmt.Sprintf(checkout, shopper.field(t, "shopperId").(string))

	placeOrder := func(key string) string {
		placed := svc.call(t, "POST", "/checkout", key, body)
		if placed.status != 201 {
			t.Fatalf("checkout = %d %s", placed.status, placed.body)
		}
		return placed.field(t, "orderId").(string)
	}
	shipGroups := func(orderID, suffix string) {
		ship := func(group int, tn string) kafkatest.Message {
			return event(fmt.Sprintf("ship-%s-%d", suffix, group), "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentShipped", orderID,
				map[string]any{"assignmentId": fmt.Sprintf("as-%s-%d", suffix, group), "orderId": orderID, "groupNo": group, "trackingNumbers": []string{tn}})
		}
		kafkatest.Produce(t, topicSourcing, ship(1, "TRK-"+suffix+"-1"), ship(2, "TRK-"+suffix+"-2"))
	}
	groupStatus := func(orderID string, group int) string {
		groups, _ := svc.call(t, "GET", "/orders/"+orderID, "", "").field(t, "groups").([]any)
		for _, g := range groups {
			if m := g.(map[string]any); int(m["groupNo"].(float64)) == group {
				return m["status"].(string)
			}
		}
		return ""
	}
	orderStatus := func(orderID string) string {
		return svc.call(t, "GET", "/orders/"+orderID, "", "").field(t, "status").(string)
	}
	waitFor := func(what string, cond func() bool) { eventually(t, 60*time.Second, what, cond) }

	t.Run("a lost package then a returned one close the order", func(t *testing.T) {
		orderID := placeOrder("checkout-lr-1")
		shipGroups(orderID, "a")
		waitFor("SHIPPED", func() bool { return orderStatus(orderID) == "SHIPPED" })

		lost := event("lost-a", journey+"PackageLost", orderID, map[string]any{"trackingNumber": "TRK-a-1", "orderId": orderID, "declaredAt": "2026-10-27T13:00:00Z", "futureField": true})
		// the same CloudEvent twice: the inbox applies it once
		kafkatest.Produce(t, topicShipping, lost, lost)
		waitFor("group 1 LOST", func() bool { return groupStatus(orderID, 1) == "LOST" })
		if got := orderStatus(orderID); got != "SHIPPED" {
			t.Fatalf("group 2 is still open, order = %s", got)
		}

		kafkatest.Produce(t, topicShipping, event("returned-a", journey+"PackageReturned", orderID, map[string]any{"trackingNumber": "TRK-a-2", "orderId": orderID, "returnedAt": "2026-10-26T09:00:00Z"}))
		waitFor("order LOST", func() bool { return orderStatus(orderID) == "LOST" })
		if got := groupStatus(orderID, 2); got != "RETURNED" {
			t.Fatalf("group 2 = %s", got)
		}

		tracking := svc.call(t, "GET", "/orders/"+orderID+"/tracking", "", "")
		var view struct {
			Status string `json:"status"`
			Groups []struct {
				GroupNo        int    `json:"groupNo"`
				Status         string `json:"status"`
				LostAt         string `json:"lostAt"`
				ReturnedAt     string `json:"returnedAt"`
				OutcomePackage string `json:"outcomePackage"`
				Packages       []struct {
					TrackingNumber string `json:"trackingNumber"`
					State          string `json:"state"`
				} `json:"packages"`
			} `json:"groups"`
		}
		if err := json.Unmarshal([]byte(tracking.body), &view); err != nil || view.Status != "LOST" || len(view.Groups) != 2 {
			t.Fatalf("tracking = %s (%v)", tracking.body, err)
		}
		g1, g2 := view.Groups[0], view.Groups[1]
		if g1.Status != "LOST" || g1.LostAt != "2026-10-27T13:00:00Z" || g1.OutcomePackage != "TRK-a-1" || len(g1.Packages) != 1 || g1.Packages[0].State != "LOST" ||
			g2.Status != "RETURNED" || g2.ReturnedAt != "2026-10-26T09:00:00Z" || g2.OutcomePackage != "TRK-a-2" || g2.Packages[0].State != "RETURNED" {
			t.Fatalf("tracking groups = %+v", view.Groups)
		}
		// The order is closed: cancellation answers status-regression.
		if refused := svc.call(t, "POST", "/orders/"+orderID+"/cancellations", "cancel-lr-1", `{"reason":"too late"}`); refused.status != 409 || refused.field(t, "type") != "/problems/status-regression" {
			t.Fatalf("cancel = %d %s", refused.status, refused.body)
		}
	})

	t.Run("a loss that arrives before the shipment is replayed on shipping", func(t *testing.T) {
		orderID := placeOrder("checkout-lr-2")
		kafkatest.Produce(t, topicShipping, event("lost-b", journey+"PackageLost", orderID, map[string]any{"trackingNumber": "TRK-b-1", "orderId": orderID, "declaredAt": "2026-10-27T13:00:00Z"}))
		kafkatest.Produce(t, topicShipping, event("delivered-b", journey+"PackageDelivered", orderID, map[string]any{"trackingNumber": "TRK-b-2", "orderId": orderID, "deliveredAt": "2026-10-27T14:00:00Z"}))
		waitFor("both early events in the inbox", func() bool {
			return pgtest.EventProcessed(t, dsn, "lost-b") && pgtest.EventProcessed(t, dsn, "delivered-b")
		})
		if got := groupStatus(orderID, 1); got != "PROMISED" {
			t.Fatalf("the shipment is not known yet, group 1 = %s", got)
		}
		shipGroups(orderID, "b")
		waitFor("group 1 LOST", func() bool { return groupStatus(orderID, 1) == "LOST" })
		waitFor("group 2 DELIVERED", func() bool { return groupStatus(orderID, 2) == "DELIVERED" })
		if got := orderStatus(orderID); got != "PARTIALLY_DELIVERED" {
			t.Fatalf("closed with one loss and one delivery, order = %s", got)
		}
	})

}
