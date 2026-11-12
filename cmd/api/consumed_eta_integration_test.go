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

// File order matters (see consumed_outcomes_integration_test.go): this file
// sorts before e2e_integration_test.go, which stops the shared broker.
//
// ADR 0014 over the real wiring: PackageEtaWindowAssigned travels on the
// shipping-and-tracking topic of a real Kafka, is stored on the package row in a
// real Postgres, and shows on REST only while the package is out for delivery.

type etaPackage struct {
	State    string `json:"state"`
	EtaStart string `json:"etaStart"`
	EtaEnd   string `json:"etaEnd"`
}

// firstPackageOf reads the first package of group 1; ok is false while the
// group lists none (the shipment is not applied yet).
func firstPackageOf(body string) (p etaPackage, ok bool) {
	var view struct {
		Groups []struct {
			Packages []etaPackage `json:"packages"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(body), &view); err != nil || len(view.Groups) == 0 || len(view.Groups[0].Packages) != 1 {
		return etaPackage{}, false
	}
	return view.Groups[0].Packages[0], true
}

func TestEtaWindowShowsOnTheTrackingViewOverKafka(t *testing.T) {
	dsn := pgtest.URL(t)
	brokers := kafkatest.Brokers(t)
	for _, topic := range []string{topicEvents, topicSourcing, topicShipping, topicPromise} {
		kafkatest.CreateTopic(t, topic)
	}
	dp := fakeDeliveryPromise(t)
	svc := boot(t, map[string]string{
		"DATABASE_URL": dsn, "KAFKA_BROKERS": strings.Join(brokers, ","), "DELIVERY_PROMISE_URL": dp.URL,
		"DELIVERY_PROMISE_CONSUMER_GROUP": "co-eta-promise", "ORDER_SOURCING_CONSUMER_GROUP": "co-eta-sourcing", "SHIPPING_CONSUMER_GROUP": "co-eta-shipping",
		"OUTBOX_RELAY_INTERVAL": "100ms", "SHUTDOWN_DRAIN_DELAY": "0", "LOG_LEVEL": "warn",
	})
	shopper := svc.call(t, "POST", "/shoppers", "shopper-key", `{"email":"ana@example.com"}`)
	placed := svc.call(t, "POST", "/checkout", "checkout-eta-1", fmt.Sprintf(checkout, shopper.field(t, "shopperId").(string)))
	if placed.status != 201 {
		t.Fatalf("checkout = %d %s", placed.status, placed.body)
	}
	orderID := placed.field(t, "orderId").(string)
	tracking := func() etaPackage {
		p, _ := firstPackageOf(svc.call(t, "GET", "/orders/"+orderID+"/tracking", "", "").body)
		return p
	}
	waitFor := func(what string, cond func() bool) { eventually(t, 60*time.Second, what, cond) }

	// The window arrives BEFORE the shipment and the scan: it must be kept, and hidden.
	window := event("eta-1", journey+"PackageEtaWindowAssigned", "TRK-eta-1", map[string]any{
		"trackingNumber": "TRK-eta-1", "assignmentId": "as-eta-1", "orderId": orderID,
		"etaStart": "2026-10-26T13:00:00Z", "etaEnd": "2026-10-26T15:00:00Z", "assignedAt": "2026-10-26T09:00:00Z", "futureField": true,
	})
	kafkatest.Produce(t, topicShipping, window, window) // the same CloudEvent twice: the inbox applies it once
	waitFor("the window in the inbox", func() bool { return pgtest.EventProcessed(t, dsn, "eta-1") })
	kafkatest.Produce(t, topicSourcing, event("ship-eta-1", "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentShipped", orderID,
		map[string]any{"assignmentId": "as-eta-1", "orderId": orderID, "groupNo": 1, "trackingNumbers": []string{"TRK-eta-1"}}))
	waitFor("the package listed", func() bool {
		_, ok := firstPackageOf(svc.call(t, "GET", "/orders/"+orderID+"/tracking", "", "").body)
		return ok
	})
	if p := tracking(); p.EtaStart != "" || p.EtaEnd != "" {
		t.Fatalf("the window must stay hidden before the package is out for delivery: %+v", p)
	}

	kafkatest.Produce(t, topicShipping, event("scan-eta-1", journey+"PackageScanned", "TRK-eta-1",
		map[string]any{"trackingNumber": "TRK-eta-1", "nodeId": "ds-1", "state": "OUT_FOR_DELIVERY", "at": "2026-10-26T09:00:00Z"}))
	waitFor("the window shown", func() bool { return tracking().EtaStart == "2026-10-26T13:00:00Z" })
	if p := tracking(); p.State != "OUT_FOR_DELIVERY" || p.EtaEnd != "2026-10-26T15:00:00Z" {
		t.Fatalf("package = %+v", p)
	}

	// A failed attempt sends the package back to the station: the window is hidden again.
	kafkatest.Produce(t, topicShipping, event("scan-eta-2", journey+"PackageScanned", "TRK-eta-1",
		map[string]any{"trackingNumber": "TRK-eta-1", "nodeId": "ds-1", "state": "AT_STATION", "at": "2026-10-26T16:00:00Z"}))
	waitFor("the window hidden", func() bool { return tracking().State == "AT_STATION" })
	if p := tracking(); p.EtaStart != "" || p.EtaEnd != "" {
		t.Fatalf("a failed attempt must hide the window: %+v", p)
	}
}
