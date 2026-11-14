package notice

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

var at = time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)

func rows(n Notifier) []Row {
	out := make([]Row, len(Families))
	for i, f := range Families {
		out[i] = Row{Family: f, Notifier: n}
	}
	return out
}

func TestDefaultIsEveryFamilyNobody(t *testing.T) {
	p := Default()
	if p.Version() != 0 || !p.PublishedAt().IsZero() {
		t.Fatalf("default = v%d at %v", p.Version(), p.PublishedAt())
	}
	for _, f := range Families {
		if n := p.Notifier(f); n != NotifierNobody {
			t.Fatalf("family %s = %s", f, n)
		}
	}
}

func TestPublishRejectsBrokenDocuments(t *testing.T) {
	dup := rows(NotifierMarketplace)
	dup = append(dup, Row{Family: FamilyGroupLost, Notifier: NotifierSeller})
	cases := []struct {
		name string
		rows []Row
		want error
	}{
		{"missing a family", rows(NotifierNobody)[1:], shared.ErrValidation},
		{"a family twice", dup, shared.ErrValidation},
		{"unknown family", []Row{{Family: "GROUP_STOLEN", Notifier: NotifierNobody}}, shared.ErrValidation},
		{"unknown notifier", []Row{{Family: FamilyGroupLost, Notifier: "CARRIER"}}, shared.ErrValidation},
	}
	for _, c := range cases {
		if _, err := Publish(0, c.rows, at); !errors.Is(err, c.want) {
			t.Fatalf("%s: err = %v", c.name, err)
		}
	}
}

func TestPublishAndRestoreRoundTrip(t *testing.T) {
	in := rows(NotifierNobody)
	in[0].Notifier = NotifierMarketplace
	in[6].Notifier = NotifierSeller
	p, err := Publish(3, in, at)
	if err != nil {
		t.Fatal(err)
	}
	if p.Version() != 4 {
		t.Fatalf("version = %d", p.Version())
	}
	got, err := Restore(p.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(p.Snapshot())
	have, _ := json.Marshal(got.Snapshot())
	if string(have) != string(want) {
		t.Fatalf("round trip:\n got %s\nwant %s", have, want)
	}
	if got.Notifier(FamilyDelayExpected) != NotifierMarketplace || got.Notifier(FamilyGroupReturned) != NotifierSeller {
		t.Fatalf("rows = %+v", got.Snapshot().Rows)
	}
}

func TestRestoreRefusesCorruptRows(t *testing.T) {
	bad := rows(NotifierSeller)
	bad[2].Family = "NO_SUCH_FAMILY"
	if _, err := Restore(Snapshot{Version: 1, Rows: bad, PublishedAt: at}); err == nil {
		t.Fatal("an unknown family must be corrupt")
	}
	if _, err := Restore(Snapshot{Version: 0, Rows: rows(NotifierNobody), PublishedAt: at}); err == nil {
		t.Fatal("version 0 is only the default")
	}
	if _, err := Restore(Snapshot{Version: 1, Rows: rows(NotifierNobody)}); err == nil {
		t.Fatal("publishedAt is required")
	}
}

func TestNoticeKeyIsStablePerFactAndNewPerReassignment(t *testing.T) {
	key := NoticeKey("o-1", 2, FamilyEtaWindowAssigned, EtaFact(at, at.Add(time.Hour)))
	if len(key) != 64 {
		t.Fatalf("key = %q", key)
	}
	same := NoticeKey("o-1", 2, FamilyEtaWindowAssigned, EtaFact(at, at.Add(time.Hour)))
	if same != key {
		t.Fatalf("a replay must keep the key: %q vs %q", same, key)
	}
	if again := NoticeKey("o-1", 2, FamilyEtaWindowAssigned, EtaFact(at.Add(time.Minute), at.Add(time.Hour))); again == key {
		t.Fatal("a re-assigned window is a new fact")
	}
	if other := NoticeKey("o-1", 3, FamilyEtaWindowAssigned, EtaFact(at, at.Add(time.Hour))); other == key {
		t.Fatal("another group is another occurrence")
	}
	for _, c := range []struct {
		name string
		fact Fact
		want string
	}{
		{"delay", DelayFact(at), "deliverByLatest=2026-11-14T09:00:00Z"},
		{"eta", EtaFact(at, at.Add(time.Hour)), "etaStart=2026-11-14T09:00:00Z;etaEnd=2026-11-14T10:00:00Z"},
		{"point flag", PointFlagFact("NOT_HOME"), "reason=NOT_HOME"},
		{"status", StatusFact("SHIPPED"), "status=SHIPPED"},
	} {
		if got := c.fact.Digest(); got != c.want {
			t.Fatalf("%s digest = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestShopperNoticeDecidedImplementsTheEventContract(t *testing.T) {
	e := ShopperNoticeDecided{
		Header: shared.Header{Version: 7, At: at}, OrderID: "o-1", GroupNo: 2, Family: FamilyGroupLost,
		Notifier: NotifierMarketplace, PolicyVersion: 3, NoticeKey: "k", DecidedAt: at, Fact: StatusFact("LOST"),
	}
	var ev shared.Event = e
	if ev.EventName() != "ShopperNoticeDecided" || ev.Entity() != "order" || ev.SubjectID() != "o-1" {
		t.Fatalf("event = %s %s %s", ev.EventName(), ev.Entity(), ev.SubjectID())
	}
	if ev.AggregateVersion() != 7 || !ev.OccurredAt().Equal(at) {
		t.Fatalf("header = %d %v", ev.AggregateVersion(), ev.OccurredAt())
	}
}
