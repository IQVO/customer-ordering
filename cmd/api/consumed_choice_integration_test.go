//go:build integration

package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/testing/kafkatest"
	"github.com/claudioed/customer-ordering/internal/testing/pgtest"
)

// File order matters: e2e_integration_test.go stops the shared Kafka broker in
// its last subtest, so every test that needs Kafka sorts before it by file name.
//
// ADR 0012 and 0013 over the real wiring: a checkout whose group 1 is a
// CONSOLIDATED option stores the tier and the chosen day in Postgres, shows them
// over REST, and publishes them on CustomerOrderPlaced together with the
// consolidation key derived from CONSOLIDATION_KEY_SECRET.

const consolidatedCheckout = `{"shopperId":%q,
 "shipTo":{"name":"Ana Example","line1":"1 Sample Street","city":"Austin","region":"TX","postalCode":"78701","countryCode":"US"},
 "lines":[{"lineNo":1,"catalogItemId":"sku-100","quantity":2},{"lineNo":2,"catalogItemId":"sku-200","quantity":1}],
 "groups":[{"groupNo":1,"offerId":"offer-c1","lineNos":[1]},{"groupNo":2,"offerId":"offer-2","lineNos":[2]}]}`

func TestAConsolidatedChoiceIsStoredShownAndPublished(t *testing.T) {
	dsn := pgtest.URL(t)
	brokers := kafkatest.Brokers(t)
	for _, topic := range []string{topicEvents, topicSourcing, topicShipping, topicPromise} {
		kafkatest.CreateTopic(t, topic)
	}
	dp := fakeDeliveryPromise(t)
	svc := boot(t, map[string]string{
		"DATABASE_URL": dsn, "KAFKA_BROKERS": strings.Join(brokers, ","), "DELIVERY_PROMISE_URL": dp.URL,
		"DELIVERY_PROMISE_CONSUMER_GROUP": "co-ch-promise", "ORDER_SOURCING_CONSUMER_GROUP": "co-ch-sourcing", "SHIPPING_CONSUMER_GROUP": "co-ch-shipping",
		"CONSOLIDATION_KEY_SECRET": "integration-test-secret", "OUTBOX_RELAY_INTERVAL": "100ms", "SHUTDOWN_DRAIN_DELAY": "0", "LOG_LEVEL": "warn",
	})
	shopper := svc.call(t, "POST", "/shoppers", "shopper-key-ch", `{"email":"ana.choice@example.com"}`)
	placed := svc.call(t, "POST", "/checkout", "checkout-ch-1", fmt.Sprintf(consolidatedCheckout, shopper.field(t, "shopperId").(string)))
	if placed.status != 201 {
		t.Fatalf("checkout = %d %s", placed.status, placed.body)
	}
	orderID := placed.field(t, "orderId").(string)

	type group struct {
		Tier                  string `json:"tier"`
		TimeZone              string `json:"timeZone"`
		ChosenDeliveryDay     string `json:"chosenDeliveryDay"`
		ConsolidationEligible *bool  `json:"consolidationEligible"`
	}
	read := func(body string) []group {
		var order struct {
			Groups []group `json:"groups"`
		}
		if err := json.Unmarshal([]byte(body), &order); err != nil || len(order.Groups) != 2 {
			t.Fatalf("order = %s (%v)", body, err)
		}
		return order.Groups
	}
	check := func(what string, g []group) {
		if g[0].Tier != "CONSOLIDATED" || g[0].TimeZone != "America/Chicago" || g[0].ChosenDeliveryDay != "2026-10-28" ||
			g[0].ConsolidationEligible == nil || !*g[0].ConsolidationEligible {
			t.Fatalf("%s: group 1 = %+v, want CONSOLIDATED, 2026-10-28 (the Chicago date of 2026-10-29T02:00:00Z), eligible", what, g[0])
		}
		if g[1].Tier != "NEXT_DAY" || g[1].ChosenDeliveryDay != "" || g[1].ConsolidationEligible != nil {
			t.Fatalf("%s: group 2 = %+v, a NEXT_DAY group carries no choice", what, g[1])
		}
	}
	check("checkout", read(placed.body))
	check("GET /orders (read back from Postgres)", read(svc.call(t, "GET", "/orders/"+orderID, "", "").body))

	msgs := kafkatest.ReadKeyed(t, topicEvents, orderID, 1, 60*time.Second)
	var env struct {
		Data struct {
			ConsolidationKey string  `json:"consolidationKey"`
			Groups           []group `json:"groups"`
		} `json:"data"`
	}
	if err := json.Unmarshal(msgs[0].Value, &env); err != nil {
		t.Fatalf("event = %s (%v)", msgs[0].Value, err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(env.Data.ConsolidationKey) {
		t.Fatalf("consolidationKey = %q, want 64 lower-case hex characters (a secret is configured)", env.Data.ConsolidationKey)
	}
	check("CustomerOrderPlaced", env.Data.Groups)
	if strings.Contains(string(msgs[0].Value), "Sample Street") {
		t.Fatalf("the event leaks the address: %s", msgs[0].Value)
	}
}
