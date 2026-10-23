package customerorder

import (
	"strings"
	"testing"

	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

var allGroupStates = []GroupStatus{GroupPromised, GroupLocked, GroupShipped, GroupDelivered, GroupLost, GroupReturned, GroupCancellationRequested, GroupCancelled}

func TestMarkInFulfillmentByGroupState(t *testing.T) {
	cases := []struct {
		from    GroupStatus
		want    GroupStatus
		err     error
		changes bool
	}{
		{GroupPromised, GroupLocked, nil, true},
		{GroupLocked, GroupLocked, nil, false},
		{GroupShipped, GroupShipped, ErrStatusRegression, false},
		{GroupDelivered, GroupDelivered, ErrStatusRegression, false},
		{GroupLost, GroupLost, ErrStatusRegression, false},
		{GroupReturned, GroupReturned, ErrStatusRegression, false},
		{GroupCancellationRequested, GroupCancellationRequested, nil, true},
		{GroupCancelled, GroupCancelled, shared.ErrIllegalTransition, false},
	}
	for _, c := range cases {
		t.Run(string(c.from), func(t *testing.T) {
			o := orderWith(t, c.from)
			before := o.Version()
			var prev GroupPromise
			if c.from == GroupLocked {
				prev = o.Groups()[0]
			}
			err := o.MarkInFulfillment(1, "as-new", "fc-new", at(40))
			assertErr(t, err, c.err)
			assertGroupStatus(t, o, 1, c.want)
			if c.changes {
				assertVersion(t, o, before+1)
				if g := o.Groups()[0]; g.AssignmentID != "as-new" || g.NodeID != "fc-new" || !g.LockedAt.Equal(at(40)) {
					t.Fatalf("lock facts not recorded: %+v", g)
				}
			} else {
				assertVersion(t, o, before)
				if c.from == GroupLocked && o.Groups()[0].AssignmentID != prev.AssignmentID {
					t.Fatal("a repeated lock must change nothing")
				}
			}
		})
	}
}

func TestMarkInFulfillmentMovesTheOrderStatus(t *testing.T) {
	o := mustPlace(t, twoGroups())
	mustNil(t, o.MarkInFulfillment(1, "as-1", "fc-1", at(10)))
	assertOrderStatus(t, o, StatusInFulfillment)
	assertVersion(t, o, 2)
	mustNil(t, o.MarkInFulfillment(2, "as-2", "fc-1", at(11)))
	assertOrderStatus(t, o, StatusInFulfillment)
	assertVersion(t, o, 3)
	if ng, ok := o.GroupNoByAssignment("as-2"); !ok || ng != 2 {
		t.Fatalf("assignment lookup = %d %v", ng, ok)
	}
	if _, ok := o.GroupNoByAssignment("nope"); ok {
		t.Fatal("unknown assignment found")
	}
}

func TestMarkInFulfillmentRejects(t *testing.T) {
	o := orderWith(t, GroupPromised)
	for name, fn := range map[string]func() error{
		"blank assignment": func() error { return o.MarkInFulfillment(1, "", "fc", at(1)) },
		"blank node":       func() error { return o.MarkInFulfillment(1, "as", "", at(1)) },
	} {
		if err := fn(); !isValidation(err) {
			t.Errorf("%s: want ErrValidation, got %v", name, err)
		}
	}
	if err := o.MarkInFulfillment(1, "as", "fc", zeroTime); !isValidation(err) {
		t.Errorf("zero time: want ErrValidation, got %v", err)
	}
	assertErr(t, o.MarkInFulfillment(9, "as", "fc", at(1)), ErrUnknownGroup)
	assertVersion(t, o, 1)
}

func TestMarkGroupShippedByGroupState(t *testing.T) {
	cases := []struct {
		from    GroupStatus
		want    GroupStatus
		err     error
		changes bool
	}{
		{GroupPromised, GroupShipped, nil, true},
		{GroupLocked, GroupShipped, nil, true},
		{GroupCancellationRequested, GroupShipped, nil, true},
		{GroupShipped, GroupShipped, nil, false},
		{GroupDelivered, GroupDelivered, ErrStatusRegression, false},
		{GroupLost, GroupLost, ErrStatusRegression, false},
		{GroupReturned, GroupReturned, ErrStatusRegression, false},
		{GroupCancelled, GroupCancelled, shared.ErrIllegalTransition, false},
	}
	for _, c := range cases {
		t.Run(string(c.from), func(t *testing.T) {
			o := orderWith(t, c.from)
			before := o.Version()
			prev := o.Groups()[0]
			err := o.MarkGroupShipped(1, []string{"TRK-A", "TRK-B", "TRK-A"}, at(45))
			assertErr(t, err, c.err)
			assertGroupStatus(t, o, 1, c.want)
			g := o.Groups()[0]
			if c.changes {
				assertVersion(t, o, before+1)
				if len(g.TrackingNumbers) != 2 || g.TrackingNumbers[0] != "TRK-A" || g.TrackingNumbers[1] != "TRK-B" || !g.ShippedAt.Equal(at(45)) {
					t.Fatalf("shipment facts wrong (duplicates must collapse): %+v", g)
				}
				return
			}
			assertVersion(t, o, before)
			if !g.ShippedAt.Equal(prev.ShippedAt) || len(g.TrackingNumbers) != len(prev.TrackingNumbers) {
				t.Fatal("a refused or repeated shipment must change nothing")
			}
		})
	}
}

func TestMarkGroupShippedValidatesTrackingNumbers(t *testing.T) {
	o := orderWith(t, GroupLocked)
	many := make([]string, MaxTrackingNumbers+1)
	for i := range many {
		many[i] = "TRK-" + strings.Repeat("x", i%5) + string(rune('A'+i%26)) + string(rune('a'+i/26))
	}
	for name, list := range map[string][]string{
		"blank":      {""},
		"padded":     {" TRK"},
		"too long":   {strings.Repeat("t", MaxTrackingNumberLen+1)},
		"too many":   many,
		"valid then": {"TRK-1", "\t"},
	} {
		if err := o.MarkGroupShipped(1, list, at(5)); !isValidation(err) {
			t.Errorf("%s: want ErrValidation, got %v", name, err)
		}
	}
	if err := o.MarkGroupShipped(1, []string{"TRK"}, zeroTime); !isValidation(err) {
		t.Errorf("zero shippedAt: want ErrValidation, got %v", err)
	}
	assertErr(t, o.MarkGroupShipped(9, []string{"TRK"}, at(5)), ErrUnknownGroup)
	assertVersion(t, o, 1)
	if err := o.MarkGroupShipped(1, many[:MaxTrackingNumbers], at(5)); err != nil {
		t.Fatalf("exactly %d tracking numbers are fine: %v", MaxTrackingNumbers, err)
	}
	if err := orderWith(t, GroupLocked).MarkGroupShipped(1, []string{strings.Repeat("t", MaxTrackingNumberLen)}, at(5)); err != nil {
		t.Fatalf("a tracking number of %d characters is fine: %v", MaxTrackingNumberLen, err)
	}
}

func TestShipmentMovesTheOrderStatusAndMayDeliverAtOnce(t *testing.T) {
	o := mustPlace(t, twoGroups())
	mustNil(t, o.MarkInFulfillment(1, "as-1", "fc-1", at(10)))
	mustNil(t, o.MarkGroupShipped(1, []string{"TRK-1"}, at(20)))
	assertOrderStatus(t, o, StatusPartiallyShipped)
	mustNil(t, o.MarkGroupShipped(2, nil, at(21)))
	assertOrderStatus(t, o, StatusShipped)
	assertGroupStatus(t, o, 2, GroupShipped)

	// A delivery reported before the shipment settles the group on shipping.
	p := mustPlace(t, oneGroup())
	mustNil(t, p.MarkGroupDelivered(1, "TRK-1", at(30)))
	assertGroupStatus(t, p, 1, GroupPromised)
	mustNil(t, p.MarkGroupShipped(1, []string{"TRK-1"}, at(35)))
	assertGroupStatus(t, p, 1, GroupDelivered)
	assertOrderStatus(t, p, StatusDelivered)
	if ng, ok := p.GroupNoByTrackingNumber("TRK-1"); !ok || ng != 1 {
		t.Fatalf("tracking lookup = %d %v", ng, ok)
	}
	if _, ok := p.GroupNoByTrackingNumber("nope"); ok {
		t.Fatal("unknown tracking number found")
	}
}

func TestMarkGroupDeliveredByGroupState(t *testing.T) {
	cases := []struct {
		from GroupStatus
		want GroupStatus
		err  error
	}{
		{GroupPromised, GroupPromised, nil},
		{GroupLocked, GroupLocked, nil},
		{GroupCancellationRequested, GroupCancellationRequested, nil},
		{GroupShipped, GroupDelivered, nil},
		{GroupDelivered, GroupDelivered, nil},
		{GroupLost, GroupLost, shared.ErrIllegalTransition},
		{GroupReturned, GroupReturned, shared.ErrIllegalTransition},
		{GroupCancelled, GroupCancelled, shared.ErrIllegalTransition},
	}
	for _, c := range cases {
		t.Run(string(c.from), func(t *testing.T) {
			o := orderWith(t, c.from)
			before := o.Version()
			err := o.MarkGroupDelivered(1, "TRK-1", at(60))
			assertErr(t, err, c.err)
			assertGroupStatus(t, o, 1, c.want)
			if c.err != nil || c.from == GroupDelivered { // TRK-1 is already delivered in a DELIVERED group
				assertVersion(t, o, before)
				return
			}
			assertVersion(t, o, before+1)
			if !contains(o.Groups()[0].DeliveredTrackingNumbers, "TRK-1") {
				t.Fatal("delivery not recorded")
			}
		})
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestMarkGroupDeliveredNeedsEveryPackage(t *testing.T) {
	o := mustPlace(t, twoGroups())
	mustNil(t, o.MarkGroupShipped(1, []string{"TRK-A", "TRK-B"}, at(20)))
	mustNil(t, o.MarkGroupDelivered(1, "TRK-A", at(60)))
	assertGroupStatus(t, o, 1, GroupShipped)
	assertOrderStatus(t, o, StatusPartiallyShipped)
	v := o.Version()
	mustNil(t, o.MarkGroupDelivered(1, "TRK-A", at(90)))
	assertVersion(t, o, v) // a repeat changes nothing
	mustNil(t, o.MarkGroupDelivered(1, "TRK-B", at(50)))
	assertGroupStatus(t, o, 1, GroupDelivered)
	if g := o.Groups()[0]; !g.DeliveredAt.Equal(at(60)) {
		t.Fatalf("deliveredAt must be the latest delivery, got %v", g.DeliveredAt)
	}
	assertOrderStatus(t, o, StatusPartiallyDelivered)
	mustNil(t, o.MarkGroupShipped(2, []string{"TRK-C"}, at(21)))
	mustNil(t, o.MarkGroupDelivered(2, "TRK-C", at(70)))
	assertOrderStatus(t, o, StatusDelivered)
}

func TestMarkGroupDeliveredRejectsAndBounds(t *testing.T) {
	o := orderWith(t, GroupShipped)
	for name, fn := range map[string]func() error{
		"blank tn":  func() error { return o.MarkGroupDelivered(1, "", at(1)) },
		"long tn":   func() error { return o.MarkGroupDelivered(1, strings.Repeat("t", MaxTrackingNumberLen+1), at(1)) },
		"zero time": func() error { return o.MarkGroupDelivered(1, "TRK", zeroTime) },
	} {
		if err := fn(); !isValidation(err) {
			t.Errorf("%s: want ErrValidation, got %v", name, err)
		}
	}
	assertErr(t, o.MarkGroupDelivered(9, "TRK", at(1)), ErrUnknownGroup)
	assertVersion(t, o, 1)

	full := orderWith(t, GroupPromised)
	for i := 0; i < MaxTrackingNumbers; i++ {
		mustNil(t, full.MarkGroupDelivered(1, "TRK-"+string(rune('A'+i%26))+string(rune('a'+i/26)), at(1+i)))
	}
	v := full.Version()
	mustNil(t, full.MarkGroupDelivered(1, "TRK-over", at(99)))
	assertVersion(t, full, v)
	if got := len(full.Groups()[0].DeliveredTrackingNumbers); got != MaxTrackingNumbers {
		t.Fatalf("delivered numbers = %d, want the cap %d", got, MaxTrackingNumbers)
	}
}

func TestUpdateGroupPromiseByGroupState(t *testing.T) {
	cases := []struct {
		from GroupStatus
		err  error
	}{
		{GroupPromised, nil}, {GroupLocked, nil}, {GroupShipped, nil}, {GroupCancellationRequested, nil},
		{GroupDelivered, shared.ErrIllegalTransition}, {GroupCancelled, shared.ErrIllegalTransition},
		{GroupLost, shared.ErrIllegalTransition}, {GroupReturned, shared.ErrIllegalTransition},
	}
	for _, c := range cases {
		t.Run(string(c.from), func(t *testing.T) {
			o := orderWith(t, c.from)
			err := o.UpdateGroupPromise("p-1", 1, at(2000), at(2300), at(1900), "fc-new")
			assertErr(t, err, c.err)
			g := o.Groups()[0]
			if c.err != nil {
				assertVersion(t, o, 1)
				return
			}
			assertVersion(t, o, 2)
			if g.RevisionNo != 1 || !g.DeliverByEarliest.Equal(at(2000)) || !g.DeliverByLatest.Equal(at(2300)) || !g.ShipBy.Equal(at(1900)) ||
				g.WitnessNodeID != "fc-new" || g.Status != c.from {
				t.Fatalf("revision not applied or state changed: %+v", g)
			}
		})
	}
}

func TestUpdateGroupPromiseRevisionRules(t *testing.T) {
	o := mustPlace(t, oneGroup())
	mustNil(t, o.UpdateGroupPromise("p-1", 2, at(2000), at(2300), at(1900), ""))
	if g := o.Groups()[0]; g.WitnessNodeID != "fc-aus-1" || g.RevisionNo != 2 {
		t.Fatalf("an empty node keeps the witness node: %+v", g)
	}
	v := o.Version()
	for _, rev := range []int{2, 1} {
		mustNil(t, o.UpdateGroupPromise("p-1", rev, at(3000), at(3300), at(2900), "fc-x"))
	}
	assertVersion(t, o, v)
	if g := o.Groups()[0]; !g.DeliverByLatest.Equal(at(2300)) {
		t.Fatal("a stale revision must not apply")
	}
	mustNil(t, o.UpdateGroupPromise("p-1", 3, at(3000), at(3000), at(2900), ""))
	assertVersion(t, o, v+1)
	if n, ok := o.GroupNoByPromise("p-1"); !ok || n != 1 {
		t.Fatalf("promise lookup = %d %v", n, ok)
	}
	if _, ok := o.GroupNoByPromise("nope"); ok {
		t.Fatal("unknown promise found")
	}
	assertErr(t, o.UpdateGroupPromise("nope", 9, at(1), at(2), at(1), ""), ErrUnknownGroup)
	assertErr(t, o.UpdateGroupPromise("nope", 0, zeroTime, zeroTime, zeroTime, ""), ErrUnknownGroup) // unknown wins over invalid
}

func TestUpdateGroupPromiseRejects(t *testing.T) {
	o := mustPlace(t, oneGroup())
	cases := map[string]func() error{
		"revision zero":   func() error { return o.UpdateGroupPromise("p-1", 0, at(1), at(2), at(1), "") },
		"revision max+1":  func() error { return o.UpdateGroupPromise("p-1", MaxNumber+1, at(1), at(2), at(1), "") },
		"no earliest":     func() error { return o.UpdateGroupPromise("p-1", 1, zeroTime, at(2), at(1), "") },
		"no latest":       func() error { return o.UpdateGroupPromise("p-1", 1, at(1), zeroTime, at(1), "") },
		"latest<earliest": func() error { return o.UpdateGroupPromise("p-1", 1, at(2), at(1), at(1), "") },
		"no shipBy":       func() error { return o.UpdateGroupPromise("p-1", 1, at(1), at(2), zeroTime, "") },
		"bad node":        func() error { return o.UpdateGroupPromise("p-1", 1, at(1), at(2), at(1), "-n") },
	}
	for name, fn := range cases {
		if err := fn(); !isValidation(err) {
			t.Errorf("%s: want ErrValidation, got %v", name, err)
		}
	}
	assertVersion(t, o, 1)
	mustNil(t, o.UpdateGroupPromise("p-1", MaxNumber, at(1), at(2), at(1), ""))
}

func TestEveryGroupStateIsCoveredByTheTables(t *testing.T) {
	if len(allGroupStates) != 8 {
		t.Fatal("update the by-state tables when a group state is added")
	}
}
