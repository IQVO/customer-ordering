package kafka_test

import (
	"encoding/json"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/customer-ordering/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/customer-ordering/internal/domain/notice"
)

// noticePayload is the data shape a downstream consumer (the future
// customer-returns context, the storefront) reads off the wire.
type noticePayload struct {
	OrderID       string `json:"orderId"`
	GroupNo       int    `json:"groupNo"`
	Family        string `json:"family"`
	Notifier      string `json:"notifier"`
	PolicyVersion int    `json:"policyVersion"`
	NoticeKey     string `json:"noticeKey"`
	DecidedAt     string `json:"decidedAt"`
	Fact          struct {
		DeliverByLatest string `json:"deliverByLatest"`
		EtaStart        string `json:"etaStart"`
		EtaEnd          string `json:"etaEnd"`
		Reason          string `json:"reason"`
		Status          string `json:"status"`
	} `json:"fact"`
}

// fakeCRHarness is the consumer contract of ShopperNoticeDecided: decode the
// CloudEvent, keep the terminal families, dedupe on noticeKey (ADR 0017).
type fakeCRHarness struct {
	seen    map[string]bool
	entries []noticePayload
}

func newFakeCR() *fakeCRHarness { return &fakeCRHarness{seen: map[string]bool{}} }

func (h *fakeCRHarness) accept(t *testing.T, value []byte) {
	t.Helper()
	var e ce.Event
	if err := json.Unmarshal(value, &e); err != nil {
		t.Fatalf("not a CloudEvent: %v", err)
	}
	if err := e.Validate(); err != nil {
		t.Fatalf("invalid CloudEvent: %v", err)
	}
	if got := e.Type(); got != "com.retailnetwork.commerce.customer-ordering.order.ShopperNoticeDecided" {
		t.Fatalf("type = %s", got)
	}
	var p noticePayload
	if err := json.Unmarshal(e.Data(), &p); err != nil {
		t.Fatalf("data: %v", err)
	}
	if h.seen[p.NoticeKey] {
		return // a replay of the same fact: dropped on the key
	}
	h.seen[p.NoticeKey] = true
	h.entries = append(h.entries, p)
}

// TestShopperNoticeDecidedFeedsAFakeCustomerReturnsHarness proves the contract
// the Wave-4 customer-returns context will consume: a valid CloudEvent whose
// data a consumer can read with the envelope's own codec, terminal families
// identified, and the noticeKey deduping a re-encoded replay while a new ETA
// window flows through (ADR 0017).
func TestShopperNoticeDecidedFeedsAFakeCustomerReturnsHarness(t *testing.T) {
	fact := notice.EtaFact(t0.Add(30*time.Hour), t0.Add(31*time.Hour))
	msg := encodeOne(t, decided(notice.FamilyEtaWindowAssigned, fact, notice.NotifierMarketplace))
	cr := newFakeCR()
	cr.accept(t, msg.Value)
	// The same fact decided again (a new outbox id, a new CloudEvent id):
	// same noticeKey, so the harness keeps one entry.
	cr.accept(t, encodeOne(t, decided(notice.FamilyEtaWindowAssigned, fact, notice.NotifierMarketplace)).Value)
	if len(cr.entries) != 1 {
		t.Fatalf("a replay of the same fact must dedupe on noticeKey: %d entries", len(cr.entries))
	}
	// A re-assigned window is a new fact: a new key, a second entry.
	newFact := notice.EtaFact(t0.Add(32*time.Hour), t0.Add(33*time.Hour))
	cr.accept(t, encodeOne(t, decided(notice.FamilyEtaWindowAssigned, newFact, notice.NotifierMarketplace)).Value)
	if len(cr.entries) != 2 {
		t.Fatalf("a re-assigned window is a new fact: %d entries", len(cr.entries))
	}
	// The terminal families carry the group status a group view needs.
	for _, c := range []struct {
		family notice.Family
		status string
	}{
		{notice.FamilyGroupShipped, "SHIPPED"}, {notice.FamilyGroupDelivered, "DELIVERED"},
		{notice.FamilyGroupLost, "LOST"}, {notice.FamilyGroupReturned, "RETURNED"},
	} {
		m := encodeOne(t, decided(c.family, notice.StatusFact(c.status), notice.NotifierNobody))
		cr.accept(t, m.Value)
	}
	last := cr.entries[len(cr.entries)-1]
	if last.Family != "GROUP_RETURNED" || last.Fact.Status != "RETURNED" || last.Notifier != "NOBODY" {
		t.Fatalf("terminal fact = %+v", last)
	}
	if last.PolicyVersion != 1 || last.OrderID == "" || last.GroupNo != 2 || len(last.NoticeKey) != 64 {
		t.Fatalf("stamps = %+v", last)
	}
}

// TestShopperNoticeDecidedEnvelopeUsesTheOnlyEnvelopeBuilder keeps the encoder
// on the shared codec (the architecture rule, restated on the new type).
func TestShopperNoticeDecidedEnvelopeUsesTheOnlyEnvelopeBuilder(t *testing.T) {
	msg := encodeOne(t, decided(notice.FamilyDelayExpected, notice.DelayFact(t0.Add(48*time.Hour)), notice.NotifierSeller))
	var e ce.Event
	if err := json.Unmarshal(msg.Value, &e); err != nil {
		t.Fatal(err)
	}
	if e.Source() != cloudevents.Source || e.Subject() != "7a1e6c52-0d2b-4f0e-9d6a-5c6f1b8e2a10" {
		t.Fatalf("source/subject = %s %s", e.Source(), e.Subject())
	}
}
