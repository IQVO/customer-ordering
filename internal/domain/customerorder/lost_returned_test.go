package customerorder

import "testing"

// A19 / A23: PackageLost and PackageReturned close a SHIPPED group.

func shippedPair(t *testing.T) *CustomerOrder {
	t.Helper()
	o := mustPlace(t, twoGroups())
	mustNil(t, o.MarkGroupShipped(1, []string{"TRK-A", "TRK-B"}, at(20)))
	mustNil(t, o.MarkGroupShipped(2, []string{"TRK-C"}, at(21)))
	return o
}

func TestMarkGroupLostAndReturnedFromShipped(t *testing.T) {
	for _, c := range []struct {
		name string
		run  func(o *CustomerOrder) error
		want GroupStatus
	}{
		{"lost", func(o *CustomerOrder) error { return o.MarkGroupLost(1, "TRK-B", at(50)) }, GroupLost},
		{"returned", func(o *CustomerOrder) error { return o.MarkGroupReturned(1, "TRK-B", at(50)) }, GroupReturned},
	} {
		t.Run(c.name, func(t *testing.T) {
			o := shippedPair(t)
			before := o.Version()
			mustNil(t, c.run(o))
			assertGroupStatus(t, o, 1, c.want)
			assertVersion(t, o, before+1)
			g := o.Groups()[0]
			if g.OutcomeTrackingNumber != "TRK-B" {
				t.Fatalf("outcome package = %q", g.OutcomeTrackingNumber)
			}
			if c.want == GroupLost && (!g.LostAt.Equal(at(50)) || !g.ReturnedAt.IsZero()) || c.want == GroupReturned && (!g.ReturnedAt.Equal(at(50)) || !g.LostAt.IsZero()) {
				t.Fatalf("outcome instants wrong: %+v", g)
			}
			if back, err := Restore(o.Snapshot()); err != nil || back.Status() != o.Status() {
				t.Fatalf("a LOST/RETURNED order must restore: %v", err)
			}
			// Terminal and idempotent: the same outcome again changes nothing.
			mustNil(t, c.run(o))
			assertVersion(t, o, before+1)
		})
	}
}

func TestMarkGroupLostAndReturnedByGroupState(t *testing.T) {
	for _, from := range allGroupStates {
		for _, kind := range []GroupStatus{GroupLost, GroupReturned} {
			t.Run(string(from)+"->"+string(kind), func(t *testing.T) {
				o := orderWith(t, from)
				before := o.Version()
				var err error
				if kind == GroupLost {
					err = o.MarkGroupLost(1, "TRK-1", at(50))
				} else {
					err = o.MarkGroupReturned(1, "TRK-1", at(50))
				}
				switch from {
				case GroupShipped:
					mustNil(t, err)
					assertGroupStatus(t, o, 1, kind)
				case kind:
					mustNil(t, err)
					assertVersion(t, o, before)
				default: // not shipped, DELIVERED, the other outcome or CANCELLED
					assertErr(t, err, sharedIllegal)
					assertGroupStatus(t, o, 1, from)
					assertVersion(t, o, before)
				}
			})
		}
	}
}

func TestMarkGroupLostRejects(t *testing.T) {
	o := shippedPair(t)
	v := o.Version()
	for name, err := range map[string]error{
		"blank package":     o.MarkGroupLost(1, "", at(1)),
		"padded package":    o.MarkGroupReturned(1, " TRK-A", at(1)),
		"zero instant":      o.MarkGroupLost(1, "TRK-A", zeroTime),
		"zero return":       o.MarkGroupReturned(1, "TRK-A", zeroTime),
		"too long a number": o.MarkGroupLost(1, string(make([]byte, MaxTrackingNumberLen+1)), at(1)),
	} {
		if !isValidation(err) {
			t.Errorf("%s: want ErrValidation, got %v", name, err)
		}
	}
	assertErr(t, o.MarkGroupLost(9, "TRK-A", at(1)), ErrUnknownGroup)
	assertErr(t, o.MarkGroupLost(1, "TRK-C", at(1)), ErrUnknownGroup) // announced by group 2, not group 1
	assertErr(t, o.MarkGroupReturned(1, "TRK-nope", at(1)), ErrUnknownGroup)
	assertVersion(t, o, v)
}

func TestAPackageDeliveredBeforeItIsLostCannotBeLost(t *testing.T) {
	o := shippedPair(t)
	mustNil(t, o.MarkGroupDelivered(1, "TRK-A", at(30)))
	v := o.Version()
	assertErr(t, o.MarkGroupLost(1, "TRK-A", at(40)), sharedIllegal)
	assertVersion(t, o, v)
	mustNil(t, o.MarkGroupLost(1, "TRK-B", at(41))) // the other package of the group may be lost
	assertGroupStatus(t, o, 1, GroupLost)
	if g := o.Groups()[0]; len(g.DeliveredTrackingNumbers) != 1 || g.OutcomeTrackingNumber != "TRK-B" {
		t.Fatalf("the delivered package stays recorded: %+v", g)
	}
}

func TestALostGroupIsTerminal(t *testing.T) {
	o := shippedPair(t)
	mustNil(t, o.MarkGroupLost(1, "TRK-A", at(50)))
	v := o.Version()
	assertErr(t, o.MarkGroupDelivered(1, "TRK-B", at(60)), sharedIllegal)
	assertErr(t, o.MarkGroupReturned(1, "TRK-B", at(61)), sharedIllegal)
	assertErr(t, o.UpdateGroupPromise("p-1", 1, at(2000), at(2300), at(1900), "", ""), sharedIllegal)
	assertErr(t, o.MarkGroupShipped(1, []string{"TRK-A", "TRK-B"}, at(62)), ErrStatusRegression)
	assertErr(t, o.MarkInFulfillment(1, "as-1", "fc-1", at(63)), ErrStatusRegression)
	assertErr(t, o.ConfirmGroupCancelled(1, at(64)), ErrStatusRegression)
	assertVersion(t, o, v)
}

func TestTheOrderStatusFollowsLostAndReturnedGroups(t *testing.T) {
	o := shippedPair(t)
	assertOrderStatus(t, o, StatusShipped)
	mustNil(t, o.MarkGroupLost(1, "TRK-A", at(50)))
	assertOrderStatus(t, o, StatusShipped) // group 2 is still open
	mustNil(t, o.MarkGroupDelivered(2, "TRK-C", at(60)))
	assertOrderStatus(t, o, StatusPartiallyDelivered) // closed with one delivery and one loss
	assertGroupStatus(t, o, 1, GroupLost)
	assertGroupStatus(t, o, 2, GroupDelivered)

	both := shippedPair(t)
	mustNil(t, both.MarkGroupReturned(2, "TRK-C", at(50)))
	assertOrderStatus(t, both, StatusShipped)
	mustNil(t, both.MarkGroupReturned(1, "TRK-A", at(51)))
	assertOrderStatus(t, both, StatusReturned)

	mixed := shippedPair(t)
	mustNil(t, mixed.MarkGroupReturned(2, "TRK-C", at(50)))
	mustNil(t, mixed.MarkGroupLost(1, "TRK-A", at(51)))
	assertOrderStatus(t, mixed, StatusLost)
}

func TestALostOrderIsClosedForCancellation(t *testing.T) {
	o := mustPlace(t, oneGroup())
	mustNil(t, o.MarkGroupShipped(1, []string{"TRK-1"}, at(20)))
	mustNil(t, o.MarkGroupLost(1, "TRK-1", at(30)))
	assertOrderStatus(t, o, StatusLost)
	if _, err := o.CancelOrder("too late", at(40)); !errorsIs(err, ErrStatusRegression) {
		t.Fatalf("cancel of a LOST order: %v", err)
	}
}
