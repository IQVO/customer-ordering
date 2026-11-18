package kafka_test

import (
	"strings"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/kafka"
	"github.com/claudioed/customer-ordering/internal/domain/notice"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// decided builds one ShopperNoticeDecided occurrence with a deterministic key.
// The header carries the decision instant exactly as the notice use case sets
// it (usecases/notice.go): a golden over an event without one would pin the
// zero CloudEvents time. TestEveryPublishedEventCarriesANonZeroTime builds the
// event through the real use case instead of by hand.
func decided(family notice.Family, fact notice.Fact, notifier notice.Notifier) notice.ShopperNoticeDecided {
	at := t0.Add(2 * time.Hour)
	return notice.ShopperNoticeDecided{
		Header: shared.Header{At: at}, OrderID: "7a1e6c52-0d2b-4f0e-9d6a-5c6f1b8e2a10", GroupNo: 2, Family: family,
		Notifier: notifier, PolicyVersion: 1, NoticeKey: notice.NoticeKey("7a1e6c52-0d2b-4f0e-9d6a-5c6f1b8e2a10", 2, family, fact),
		DecidedAt: at, Fact: fact,
	}
}

// TestShopperNoticeDecidedGoldens pins one golden per family (ADR 0017): the
// envelope, the stamp of the deciding policy and the fact of the family.
func TestShopperNoticeDecidedGoldens(t *testing.T) {
	cases := []struct {
		name     string
		family   notice.Family
		fact     notice.Fact
		notifier notice.Notifier
	}{
		{"ShopperNoticeDecided.delay-expected", notice.FamilyDelayExpected, notice.DelayFact(t0.Add(48 * time.Hour)), notice.NotifierMarketplace},
		{"ShopperNoticeDecided.eta-window", notice.FamilyEtaWindowAssigned, notice.EtaFact(t0.Add(30*time.Hour), t0.Add(31*time.Hour)), notice.NotifierNobody},
		{"ShopperNoticeDecided.point-flag", notice.FamilyDeliveryPointAdvisory, notice.PointFlagFact("NOT_HOME"), notice.NotifierSeller},
		{"ShopperNoticeDecided.group-shipped", notice.FamilyGroupShipped, notice.StatusFact("SHIPPED"), notice.NotifierNobody},
		{"ShopperNoticeDecided.group-delivered", notice.FamilyGroupDelivered, notice.StatusFact("DELIVERED"), notice.NotifierMarketplace},
		{"ShopperNoticeDecided.group-lost", notice.FamilyGroupLost, notice.StatusFact("LOST"), notice.NotifierNobody},
		{"ShopperNoticeDecided.group-returned", notice.FamilyGroupReturned, notice.StatusFact("RETURNED"), notice.NotifierMarketplace},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := encodeOne(t, decided(c.family, c.fact, c.notifier))
			if msg.Topic != kafka.Topic || string(msg.Key) != "7a1e6c52-0d2b-4f0e-9d6a-5c6f1b8e2a10" {
				t.Fatalf("message = %s %s", msg.Topic, msg.Key)
			}
			if msg.EventType != "com.retailnetwork.commerce.customer-ordering.order.ShopperNoticeDecided" ||
				msg.DataSchema != "urn:retailnetwork:customer-ordering:events:ShopperNoticeDecided:v1" {
				t.Fatalf("envelope = %s %s", msg.EventType, msg.DataSchema)
			}
			if !strings.Contains(string(msg.Value), `"time":"2026-10-23T12:15:30.123456Z"`) || strings.Contains(string(msg.Value), "0001-01-01") {
				t.Fatalf("the golden must pin the decision time, got %s", msg.Value)
			}
			assertGolden(t, c.name, msg.Value)
		})
	}
}
