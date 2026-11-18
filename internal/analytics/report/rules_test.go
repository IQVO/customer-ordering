package report_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/analytics/report"
)

func TestVocabulary(t *testing.T) {
	if report.ConsolidatedTier() != "CONSOLIDATED" {
		t.Fatalf("ConsolidatedTier = %q", report.ConsolidatedTier())
	}
	want := []string{"DELAY_EXPECTED", "ETA_WINDOW_ASSIGNED", "DELIVERY_POINT_ADVISORY", "GROUP_SHIPPED", "GROUP_DELIVERED", "GROUP_LOST", "GROUP_RETURNED"}
	if !reflect.DeepEqual(report.NoticeFamilies(), want) {
		t.Fatalf("NoticeFamilies = %v", report.NoticeFamilies())
	}
	if report.UnrecognisedFamily() != "UNRECOGNISED" || report.MaxNoticeKinds() != 5 || report.SplitMinGroups() != 2 {
		t.Fatalf("bounds = %q %d %d", report.UnrecognisedFamily(), report.MaxNoticeKinds(), report.SplitMinGroups())
	}
	// A caller cannot corrupt the vocabulary through the returned slice.
	report.NoticeFamilies()[0] = "X"
	if report.NoticeFamilies()[0] != "DELAY_EXPECTED" {
		t.Fatal("NoticeFamilies is shared state")
	}
}

func TestIsSplitFromTwoGroups(t *testing.T) {
	for groups, want := range map[int]bool{0: false, 1: false, 2: true, 3: true} {
		if got := report.IsSplit(groups); got != want {
			t.Errorf("IsSplit(%d) = %v, want %v", groups, got, want)
		}
	}
}

func TestNormalizeFamily(t *testing.T) {
	for _, f := range report.NoticeFamilies() {
		if got := report.NormalizeFamily(f); got != f {
			t.Errorf("NormalizeFamily(%q) = %q", f, got)
		}
	}
	for _, f := range []string{"", "group_lost", "Ana Souza", "SOMETHING_NEW"} {
		if got := report.NormalizeFamily(f); got != "UNRECOGNISED" {
			t.Errorf("NormalizeFamily(%q) = %q, want UNRECOGNISED", f, got)
		}
	}
}

var at = time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)

func placed() report.Event {
	return report.Event{Kind: report.KindOrderPlaced, EventID: "e1", At: at, OrderID: "o1", OccurredAt: at, GroupCount: 2, ConsolidatedGroups: 1}
}

func cancelled() report.Event {
	return report.Event{Kind: report.KindOrderCancelled, EventID: "e2", At: at, OrderID: "o1", OccurredAt: at}
}

func notice() report.Event {
	return report.Event{Kind: report.KindNoticeDecided, EventID: "e3", At: at, OrderID: "o1", OccurredAt: at, Family: "GROUP_LOST", NoticeKey: "k"}
}

func TestValidateAcceptsEveryKind(t *testing.T) {
	for _, e := range []report.Event{placed(), cancelled(), notice()} {
		if err := e.Validate(); err != nil {
			t.Errorf("%s: %v", e.Kind, err)
		}
	}
	// The consolidated count may be zero or every group.
	e := placed()
	e.ConsolidatedGroups = 0
	if err := e.Validate(); err != nil {
		t.Errorf("no consolidated group: %v", err)
	}
	e.ConsolidatedGroups = e.GroupCount
	if err := e.Validate(); err != nil {
		t.Errorf("every group consolidated: %v", err)
	}
	// A single-group order is the smallest valid one.
	e.GroupCount, e.ConsolidatedGroups = 1, 1
	if err := e.Validate(); err != nil {
		t.Errorf("one group: %v", err)
	}
}

func TestValidateNamesTheMissingField(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*report.Event)
		base   func() report.Event
		want   string
	}{
		{"id", func(e *report.Event) { e.EventID = "" }, placed, "id is required"},
		{"time", func(e *report.Event) { e.At = time.Time{} }, placed, "time is required"},
		{"order", func(e *report.Event) { e.OrderID = "" }, cancelled, "orderId is required"},
		{"instant", func(e *report.Event) { e.OccurredAt = time.Time{} }, notice, "the business instant is required"},
		{"no groups", func(e *report.Event) { e.GroupCount, e.ConsolidatedGroups = 0, 0 }, placed, "groups is required"},
		{"negative consolidated", func(e *report.Event) { e.ConsolidatedGroups = -1 }, placed, "consistent consolidated group count"},
		{"too many consolidated", func(e *report.Event) { e.ConsolidatedGroups = e.GroupCount + 1 }, placed, "consistent consolidated group count"},
		{"family", func(e *report.Event) { e.Family = "" }, notice, "family is required"},
		{"notice key", func(e *report.Event) { e.NoticeKey = "" }, notice, "noticeKey is required"},
		{"kind", func(e *report.Event) { e.Kind = "Other" }, placed, "a known kind is required"},
	}
	for _, c := range cases {
		e := c.base()
		c.mutate(&e)
		err := e.Validate()
		if !errors.Is(err, report.ErrInvalidEvent) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want ErrInvalidEvent containing %q", c.name, err, c.want)
		}
	}
}
