package customerorder

import (
	"strings"
	"testing"

	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

func asCancelled(t *testing.T, evs []shared.Event) CustomerOrderCancelled {
	t.Helper()
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	ev, ok := evs[0].(CustomerOrderCancelled)
	if !ok {
		t.Fatalf("want CustomerOrderCancelled, got %T", evs[0])
	}
	return ev
}

func assertCancelledEvent(t *testing.T, ev CustomerOrderCancelled, version int64, reason string, nos ...int) {
	t.Helper()
	if ev.EventName() != "CustomerOrderCancelled" || ev.Entity() != "customerorder" || ev.SubjectID() != "order-1" ||
		ev.AggregateVersion() != version || !ev.OccurredAt().Equal(at(100)) || !ev.CancelledAt.Equal(at(100)) || ev.Reason != reason {
		t.Fatalf("event wrong: %+v", ev)
	}
	if len(ev.GroupNos) != len(nos) {
		t.Fatalf("groupNos = %v, want %v", ev.GroupNos, nos)
	}
	for i, n := range nos {
		if ev.GroupNos[i] != n {
			t.Fatalf("groupNos = %v, want %v", ev.GroupNos, nos)
		}
	}
}

func TestCancelOrderRequestsOnlyTheUnlockedGroups(t *testing.T) {
	o := orderWith(t, GroupPromised, GroupLocked, GroupPromised, GroupShipped)
	evs, err := o.CancelOrder("changed my mind", at(100))
	mustNil(t, err)
	assertCancelledEvent(t, asCancelled(t, evs), 2, "changed my mind", 1, 3)
	for no, want := range map[int]GroupStatus{1: GroupCancellationRequested, 2: GroupLocked, 3: GroupCancellationRequested, 4: GroupShipped} {
		assertGroupStatus(t, o, no, want)
	}
	g := o.Groups()[0]
	if g.CancelReason != "changed my mind" || !g.CancelRequestedAt.Equal(at(100)) {
		t.Fatalf("request facts missing: %+v", g)
	}
	assertOrderStatus(t, o, StatusPartiallyShipped)
	assertVersion(t, o, 2)
}

func TestCancelOrderRequestKeepsTheOrderStatusUntilConfirmed(t *testing.T) {
	o := mustPlace(t, twoGroups())
	evs, err := o.CancelOrder("changed my mind", at(100))
	mustNil(t, err)
	assertCancelledEvent(t, asCancelled(t, evs), 2, "changed my mind", 1, 2)
	assertOrderStatus(t, o, StatusPlaced)
	mustNil(t, o.ConfirmGroupCancelled(1, at(101)))
	assertOrderStatus(t, o, StatusPlaced)
	assertVersion(t, o, 3)
	mustNil(t, o.ConfirmGroupCancelled(2, at(102)))
	assertOrderStatus(t, o, StatusCancelled)
	if !o.CancelledAt().Equal(at(102)) {
		t.Fatalf("cancelledAt = %v", o.CancelledAt())
	}
	// Once CANCELLED, a repeat request changes nothing.
	evs, err = o.CancelOrder("again", at(103))
	mustNil(t, err)
	if len(evs) != 0 {
		t.Fatal("a repeat raises nothing")
	}
	assertVersion(t, o, 4)
}

func TestCancelOrderOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		states []GroupStatus
		err    error
		events int
		bump   int64
	}{
		{"all promised", []GroupStatus{GroupPromised}, nil, 1, 1},
		{"repeat: requested", []GroupStatus{GroupCancellationRequested}, nil, 0, 0},
		{"repeat: requested beside locked", []GroupStatus{GroupCancellationRequested, GroupLocked}, nil, 0, 0},
		{"repeat: cancelled beside shipped", []GroupStatus{GroupCancelled, GroupShipped}, nil, 0, 0},
		{"repeat: all cancelled", []GroupStatus{GroupCancelled}, nil, 0, 0},
		{"all locked", []GroupStatus{GroupLocked, GroupLocked}, ErrCancelAfterLock, 0, 0},
		{"locked and shipped", []GroupStatus{GroupLocked, GroupShipped}, ErrCancelAfterLock, 0, 0},
		{"partially delivered with an unlocked group", []GroupStatus{GroupDelivered, GroupPromised}, nil, 1, 1},
		{"partially delivered, rest locked", []GroupStatus{GroupDelivered, GroupLocked}, ErrCancelAfterLock, 0, 0},
		{"delivered order", []GroupStatus{GroupDelivered}, ErrStatusRegression, 0, 0},
		{"delivered order beside cancelled", []GroupStatus{GroupDelivered, GroupCancelled}, ErrStatusRegression, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := orderWith(t, c.states...)
			before := o.Version()
			evs, err := o.CancelOrder("reason", at(100))
			assertErr(t, err, c.err)
			if len(evs) != c.events {
				t.Fatalf("events = %d, want %d", len(evs), c.events)
			}
			assertVersion(t, o, before+c.bump)
		})
	}
}

func TestCancelOrderReasonRules(t *testing.T) {
	for name, reason := range map[string]string{"blank": "", "padded": " no", "control": "a\tb", "too long": strings.Repeat("r", MaxReason+1)} {
		o := orderWith(t, GroupPromised)
		if _, err := o.CancelOrder(reason, at(100)); !isValidation(err) {
			t.Errorf("%s: want ErrValidation, got %v", name, err)
		}
		assertVersion(t, o, 1)
	}
	o := orderWith(t, GroupPromised)
	if _, err := o.CancelOrder(strings.Repeat("r", MaxReason), at(100)); err != nil {
		t.Fatalf("a reason of %d characters is fine: %v", MaxReason, err)
	}
}

func TestCancelUnkeepableGroup(t *testing.T) {
	o := orderWith(t, GroupPromised, GroupPromised)
	evs, err := o.CancelUnkeepableGroup(2, "NO_FEASIBLE_PATH", at(100))
	mustNil(t, err)
	assertCancelledEvent(t, asCancelled(t, evs), 2, "PROMISE_UNKEEPABLE: NO_FEASIBLE_PATH", 2)
	assertGroupStatus(t, o, 2, GroupCancelled)
	assertOrderStatus(t, o, StatusPlaced)
	g := o.Groups()[1]
	if !g.CancelledAt.Equal(at(100)) || !g.CancelRequestedAt.Equal(at(100)) || g.CancelReason != "PROMISE_UNKEEPABLE: NO_FEASIBLE_PATH" {
		t.Fatalf("cancel facts wrong: %+v", g)
	}
	evs, err = o.CancelUnkeepableGroup(1, "  ", at(100))
	mustNil(t, err)
	assertCancelledEvent(t, asCancelled(t, evs), 3, "PROMISE_UNKEEPABLE:", 1)
	assertOrderStatus(t, o, StatusCancelled)
	if !o.CancelledAt().Equal(at(100)) {
		t.Fatal("the order is CANCELLED when nothing remains")
	}
}

func TestCancelUnkeepableGroupRefusals(t *testing.T) {
	for _, s := range []GroupStatus{GroupLocked, GroupShipped, GroupDelivered, GroupCancellationRequested, GroupCancelled} {
		o := orderWith(t, s)
		before := o.Version()
		if _, err := o.CancelUnkeepableGroup(1, "x", at(100)); !errorsIs(err, shared.ErrIllegalTransition) {
			t.Errorf("%s: want ErrIllegalTransition, got %v", s, err)
		}
		assertVersion(t, o, before)
	}
	o := orderWith(t, GroupPromised)
	if _, err := o.CancelUnkeepableGroup(9, "x", at(100)); !errorsIs(err, ErrUnknownGroup) {
		t.Fatalf("want ErrUnknownGroup, got %v", err)
	}
}

func TestCancelUnkeepableGroupCleansAndClipsTheReason(t *testing.T) {
	o := orderWith(t, GroupPromised, GroupPromised)
	evs, err := o.CancelUnkeepableGroup(1, "bad\x00\tnews\n"+strings.Repeat("é", 400), at(100))
	mustNil(t, err)
	reason := asCancelled(t, evs).Reason
	if strings.ContainsAny(reason, "\x00\t\n") || len([]rune(reason)) != MaxReason || !strings.HasPrefix(reason, "PROMISE_UNKEEPABLE: badnews") {
		t.Fatalf("reason not cleaned/clipped: %q (%d)", reason, len([]rune(reason)))
	}
	// A clip that lands on a space must not leave it trailing.
	pad := strings.Repeat("a", MaxReason-len(UnkeepableReasonPrefix)-1)
	evs, err = o.CancelUnkeepableGroup(2, pad+" bc", at(100))
	mustNil(t, err)
	if got := asCancelled(t, evs).Reason; got != UnkeepableReasonPrefix+pad {
		t.Fatalf("trailing space kept: %q", got)
	}
}

func TestConfirmGroupCancelledByGroupState(t *testing.T) {
	cases := []struct {
		from GroupStatus
		err  error
		bump int64
	}{
		{GroupPromised, nil, 1}, {GroupCancellationRequested, nil, 1}, {GroupLocked, nil, 1},
		{GroupCancelled, nil, 0}, {GroupShipped, ErrStatusRegression, 0}, {GroupDelivered, ErrStatusRegression, 0},
	}
	for _, c := range cases {
		t.Run(string(c.from), func(t *testing.T) {
			o := orderWith(t, c.from, GroupPromised)
			before := o.Version()
			assertErr(t, o.ConfirmGroupCancelled(1, at(100)), c.err)
			assertVersion(t, o, before+c.bump)
			if c.err == nil {
				assertGroupStatus(t, o, 1, GroupCancelled)
			}
			if c.bump == 1 && !o.Groups()[0].CancelledAt.Equal(at(100)) {
				t.Fatal("cancelledAt not recorded")
			}
		})
	}
	o := orderWith(t, GroupPromised)
	assertErr(t, o.ConfirmGroupCancelled(9, at(1)), ErrUnknownGroup)
	if err := o.ConfirmGroupCancelled(1, zeroTime); !isValidation(err) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}

func TestRefuseGroupCancellation(t *testing.T) {
	o := orderWith(t, GroupCancellationRequested, GroupPromised)
	mustNil(t, o.RefuseGroupCancellation(1, " VENDOR_ACKNOWLEDGED \x00", at(100)))
	assertGroupStatus(t, o, 1, GroupLocked)
	if g := o.Groups()[0]; g.CancelRefusedReason != "VENDOR_ACKNOWLEDGED" {
		t.Fatalf("refusal reason = %q", g.CancelRefusedReason)
	}
	assertOrderStatus(t, o, StatusInFulfillment)
	assertVersion(t, o, 2)
	// The same reason again is a no-op; a new one is recorded.
	mustNil(t, o.RefuseGroupCancellation(1, "VENDOR_ACKNOWLEDGED", at(101)))
	assertVersion(t, o, 2)
	mustNil(t, o.RefuseGroupCancellation(1, "OTHER", at(102)))
	assertVersion(t, o, 3)
	assertGroupStatus(t, o, 1, GroupLocked)
}

func TestRefuseGroupCancellationByGroupState(t *testing.T) {
	cases := []struct {
		from GroupStatus
		want GroupStatus
		err  error
	}{
		{GroupCancellationRequested, GroupLocked, nil}, {GroupLocked, GroupLocked, nil}, {GroupShipped, GroupShipped, nil},
		{GroupDelivered, GroupDelivered, nil}, {GroupPromised, GroupPromised, shared.ErrIllegalTransition},
		{GroupCancelled, GroupCancelled, shared.ErrIllegalTransition},
	}
	for _, c := range cases {
		t.Run(string(c.from), func(t *testing.T) {
			o := orderWith(t, c.from)
			before := o.Version()
			assertErr(t, o.RefuseGroupCancellation(1, "R", at(100)), c.err)
			assertGroupStatus(t, o, 1, c.want)
			if c.err != nil {
				assertVersion(t, o, before)
			} else if o.Groups()[0].CancelRefusedReason != "R" || o.Version() != before+1 {
				t.Fatalf("refusal not recorded: %+v", o.Groups()[0])
			}
		})
	}
	o := orderWith(t, GroupCancellationRequested)
	mustNil(t, o.RefuseGroupCancellation(1, "  ", at(100)))
	if o.Groups()[0].CancelRefusedReason != "REFUSED" {
		t.Fatal("a blank reason becomes REFUSED")
	}
	assertErr(t, o.RefuseGroupCancellation(9, "R", at(1)), ErrUnknownGroup)
	if err := o.RefuseGroupCancellation(1, "R", zeroTime); !isValidation(err) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}

func TestRefusalThenShipmentFollowsTheVendor(t *testing.T) {
	o := mustPlace(t, oneGroup())
	_, err := o.CancelOrder("changed my mind", at(100))
	mustNil(t, err)
	mustNil(t, o.RefuseGroupCancellation(1, "VENDOR_ACKNOWLEDGED", at(101)))
	mustNil(t, o.MarkGroupShipped(1, []string{"TRK-1"}, at(102)))
	mustNil(t, o.MarkGroupDelivered(1, "TRK-1", at(103)))
	assertOrderStatus(t, o, StatusDelivered)
	if _, err := o.CancelOrder("late", at(104)); !errorsIs(err, ErrStatusRegression) {
		t.Fatalf("a delivered order cannot be cancelled: %v", err)
	}
}
