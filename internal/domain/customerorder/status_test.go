package customerorder

import "testing"

func TestDeriveStatus(t *testing.T) {
	P, L, S, D, R, C := GroupPromised, GroupLocked, GroupShipped, GroupDelivered, GroupCancellationRequested, GroupCancelled
	cases := []struct {
		name   string
		groups []GroupStatus
		want   Status
	}{
		{"all promised", []GroupStatus{P, P}, StatusPlaced},
		{"cancellation requested counts as unlocked", []GroupStatus{R}, StatusPlaced},
		{"one locked", []GroupStatus{L, P}, StatusInFulfillment},
		{"all locked", []GroupStatus{L, L}, StatusInFulfillment},
		{"one shipped of two", []GroupStatus{S, P}, StatusPartiallyShipped},
		{"shipped and locked", []GroupStatus{S, L}, StatusPartiallyShipped},
		{"all shipped", []GroupStatus{S, S}, StatusShipped},
		{"one delivered of two shipped", []GroupStatus{D, S}, StatusPartiallyDelivered},
		{"delivered and promised", []GroupStatus{D, P}, StatusPartiallyDelivered},
		{"all delivered", []GroupStatus{D, D}, StatusDelivered},
		{"all cancelled", []GroupStatus{C, C}, StatusCancelled},
		{"cancelled ones do not count: shipped", []GroupStatus{C, S}, StatusShipped},
		{"cancelled ones do not count: delivered", []GroupStatus{C, D}, StatusDelivered},
		{"cancelled ones do not count: promised", []GroupStatus{C, P}, StatusPlaced},
		{"cancelled ones do not count: locked", []GroupStatus{L, C}, StatusInFulfillment},
		{"requested beside shipped", []GroupStatus{R, S}, StatusPartiallyShipped},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gs := make([]GroupPromise, len(c.groups))
			for i, s := range c.groups {
				gs[i] = GroupPromise{GroupNo: i + 1, Status: s}
			}
			if got := derive(gs); got != c.want {
				t.Fatalf("derive = %s, want %s", got, c.want)
			}
		})
	}
}

func TestStatusAndEnumValidity(t *testing.T) {
	for _, s := range []Status{StatusPlaced, StatusInFulfillment, StatusPartiallyShipped, StatusShipped, StatusPartiallyDelivered, StatusDelivered, StatusCancelled} {
		if !s.Valid() {
			t.Errorf("%s must be valid", s)
		}
	}
	if Status("LOST").Valid() || Status("").Valid() {
		t.Error("unknown order status accepted")
	}
	for _, s := range []GroupStatus{GroupPromised, GroupLocked, GroupShipped, GroupDelivered, GroupCancellationRequested, GroupCancelled} {
		if !s.Valid() {
			t.Errorf("%s must be valid", s)
		}
	}
	if GroupStatus("LOST").Valid() {
		t.Error("unknown group status accepted")
	}
	for _, tr := range []Tier{TierSameDay, TierNextDay, TierTwoDay, TierStandard} {
		if !tr.Valid() {
			t.Errorf("%s must be valid", tr)
		}
	}
	if Tier("WEEKLY").Valid() {
		t.Error("unknown tier accepted")
	}
}
