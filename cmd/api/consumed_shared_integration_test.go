//go:build integration

package main

import (
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
// ADR 0012 over the real wiring: two orders whose group 1 order-sourcing merged
// into ONE package. Each member group raises its own FulfillmentShipped with the
// same tracking number; shipping-and-tracking raises the package events once,
// under the carrier's ids. One event must reach every group.

func TestASharedPackageReachesEveryGroupOverKafka(t *testing.T) {
	dsn := pgtest.URL(t)
	brokers := kafkatest.Brokers(t)
	for _, topic := range []string{topicEvents, topicSourcing, topicShipping, topicPromise} {
		kafkatest.CreateTopic(t, topic)
	}
	dp := fakeDeliveryPromise(t)
	svc := boot(t, map[string]string{
		"DATABASE_URL": dsn, "KAFKA_BROKERS": strings.Join(brokers, ","), "DELIVERY_PROMISE_URL": dp.URL,
		"DELIVERY_PROMISE_CONSUMER_GROUP": "co-sh-promise", "ORDER_SOURCING_CONSUMER_GROUP": "co-sh-sourcing", "SHIPPING_CONSUMER_GROUP": "co-sh-shipping",
		"OUTBOX_RELAY_INTERVAL": "100ms", "SHUTDOWN_DRAIN_DELAY": "0", "LOG_LEVEL": "warn",
	})
	shopper := svc.call(t, "POST", "/shoppers", "shopper-key-sh", `{"email":"ana.shared@example.com"}`)
	body := fmt.Sprintf(checkout, shopper.field(t, "shopperId").(string))
	place := func(key string) string {
		placed := svc.call(t, "POST", "/checkout", key, body)
		if placed.status != 201 {
			t.Fatalf("checkout = %d %s", placed.status, placed.body)
		}
		return placed.field(t, "orderId").(string)
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
	// shareGroup1 ships group 1 of both orders under the one tracking number tn
	// (group 2 of each ships its own package) and waits until both are SHIPPED.
	shareGroup1 := func(carrier, follower, tn string) {
		ship := func(orderID string, group int, number string) kafkatest.Message {
			return event(fmt.Sprintf("ship-%s-%d-%s", orderID, group, number), "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentShipped", orderID,
				map[string]any{"assignmentId": fmt.Sprintf("as-%s-%d", orderID, group), "orderId": orderID, "groupNo": group, "trackingNumbers": []string{number}})
		}
		kafkatest.Produce(t, topicSourcing,
			ship(carrier, 1, tn), ship(follower, 1, tn), ship(carrier, 2, tn+"-c2"), ship(follower, 2, tn+"-f2"))
		eventually(t, 60*time.Second, "all four groups SHIPPED", func() bool {
			return groupStatus(carrier, 1) == "SHIPPED" && groupStatus(follower, 1) == "SHIPPED" &&
				groupStatus(carrier, 2) == "SHIPPED" && groupStatus(follower, 2) == "SHIPPED"
		})
	}

	t.Run("one PackageDelivered delivers both groups", func(t *testing.T) {
		carrier, follower := place("checkout-sh-1"), place("checkout-sh-2")
		shareGroup1(carrier, follower, "TRK-SH-A")
		kafkatest.Produce(t, topicShipping, event("delivered-sh-a", journey+"PackageDelivered", carrier,
			map[string]any{"trackingNumber": "TRK-SH-A", "orderId": carrier, "assignmentId": "as-" + carrier + "-1", "deliveredAt": "2026-10-27T14:00:00Z"}))
		eventually(t, 60*time.Second, "both groups DELIVERED", func() bool {
			return groupStatus(carrier, 1) == "DELIVERED" && groupStatus(follower, 1) == "DELIVERED"
		})
		for _, id := range []string{carrier, follower} {
			if got := groupStatus(id, 2); got != "SHIPPED" {
				t.Fatalf("group 2 of %s = %s, its own package is not delivered", id, got)
			}
		}
		tracking := svc.call(t, "GET", "/orders/"+follower+"/tracking", "", "")
		if !strings.Contains(tracking.body, "TRK-SH-A") {
			t.Fatalf("the follower's tracking lacks the shared package: %s", tracking.body)
		}
	})

	t.Run("one PackageLost closes both groups", func(t *testing.T) {
		carrier, follower := place("checkout-sh-3"), place("checkout-sh-4")
		shareGroup1(carrier, follower, "TRK-SH-B")
		kafkatest.Produce(t, topicShipping, event("lost-sh-b", journey+"PackageLost", carrier,
			map[string]any{"trackingNumber": "TRK-SH-B", "orderId": carrier, "declaredAt": "2026-10-27T13:00:00Z"}))
		eventually(t, 60*time.Second, "both groups LOST", func() bool {
			return groupStatus(carrier, 1) == "LOST" && groupStatus(follower, 1) == "LOST"
		})
	})
}
