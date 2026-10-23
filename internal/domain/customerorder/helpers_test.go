package customerorder

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/domain/shared"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
)

var (
	zeroTime time.Time
	brt      = time.FixedZone("BRT", -3*3600)
)

var t0 = time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)

// at returns t0 plus m minutes.
func at(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }

func goodShipTo() shipto.ShipTo {
	return shipto.ShipTo{Name: "Ana Example", Line1: "1 Sample Street", City: "Austin", Region: "TX", PostalCode: "78701", CountryCode: "US"}
}

func groupInput(no int, promise string, lineNos ...int) GroupInput {
	return GroupInput{
		GroupNo: no, PromiseID: promise, Tier: TierNextDay, WitnessNodeID: "fc-aus-1", ShipBy: at(600),
		DeliverByEarliest: at(1200), DeliverByLatest: at(1500), LineNos: lineNos, DestinationPostalPrefix: "787",
	}
}

// twoGroups is an order of three lines in two groups.
func twoGroups() NewOrder {
	return NewOrder{
		ID: "order-1", ShopperID: "shopper-1", ShipTo: goodShipTo(),
		Lines:  []OrderLine{{1, "sku-100", 2}, {2, "sku-200", 1}, {3, "sku-300", 5}},
		Groups: []GroupInput{groupInput(1, "p-1", 1, 2), groupInput(2, "p-2", 3)},
	}
}

func oneGroup() NewOrder {
	return NewOrder{
		ID: "order-1", ShopperID: "shopper-1", ShipTo: goodShipTo(),
		Lines: []OrderLine{{1, "sku-100", 2}}, Groups: []GroupInput{groupInput(1, "p-1", 1)},
	}
}

func mustPlace(t *testing.T, in NewOrder) *CustomerOrder {
	t.Helper()
	o, _, err := Place(in, t0)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	return o
}

// orderWith builds a placed order whose groups are forced into the given
// states, with the facts each state requires, and the derived status.
func orderWith(t *testing.T, states ...GroupStatus) *CustomerOrder {
	t.Helper()
	in := NewOrder{ID: "order-1", ShopperID: "shopper-1", ShipTo: goodShipTo()}
	for i := range states {
		no := i + 1
		in.Lines = append(in.Lines, OrderLine{no, "sku-1", 1})
		in.Groups = append(in.Groups, groupInput(no, "p-"+string(rune('0'+no)), no))
	}
	o := mustPlace(t, in)
	for i, s := range states {
		forceState(&o.groups[i], s)
	}
	o.status = derive(o.groups)
	if o.status == StatusCancelled {
		o.cancelledAt = at(50)
	}
	return o
}

func forceState(g *GroupPromise, s GroupStatus) {
	g.Status = s
	switch s {
	case GroupLocked:
		g.AssignmentID, g.NodeID, g.LockedAt = "as-"+string(rune('0'+g.GroupNo)), "fc-aus-1", at(10)
	case GroupShipped:
		g.AssignmentID, g.NodeID, g.LockedAt, g.ShippedAt = "as-"+string(rune('0'+g.GroupNo)), "fc-aus-1", at(10), at(20)
		g.TrackingNumbers = []string{"TRK-" + string(rune('0'+g.GroupNo))}
	case GroupDelivered:
		g.AssignmentID, g.NodeID, g.LockedAt, g.ShippedAt, g.DeliveredAt = "as-"+string(rune('0'+g.GroupNo)), "fc-aus-1", at(10), at(20), at(30)
		g.TrackingNumbers = []string{"TRK-" + string(rune('0'+g.GroupNo))}
		g.DeliveredTrackingNumbers = append([]string{}, g.TrackingNumbers...)
	case GroupCancellationRequested:
		g.CancelReason, g.CancelRequestedAt = "changed my mind", at(5)
	case GroupCancelled:
		g.CancelReason, g.CancelRequestedAt, g.CancelledAt = "changed my mind", at(5), at(6)
	}
}

func assertErr(t *testing.T, got, want error) {
	t.Helper()
	switch {
	case want == nil && got != nil:
		t.Fatalf("unexpected error: %v", got)
	case want != nil && !errors.Is(got, want):
		t.Fatalf("want %v, got %v", want, got)
	}
}

func assertOrderStatus(t *testing.T, o *CustomerOrder, want Status) {
	t.Helper()
	if o.Status() != want {
		t.Fatalf("order status = %s, want %s", o.Status(), want)
	}
}

func assertGroupStatus(t *testing.T, o *CustomerOrder, groupNo int, want GroupStatus) {
	t.Helper()
	g := o.Groups()[o.indexByGroup(groupNo)]
	if g.Status != want {
		t.Fatalf("group %d status = %s, want %s", groupNo, g.Status, want)
	}
}

func assertVersion(t *testing.T, o *CustomerOrder, want int64) {
	t.Helper()
	if o.Version() != want {
		t.Fatalf("version = %d, want %d", o.Version(), want)
	}
}

func mustNil(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func isValidation(err error) bool { return errors.Is(err, shared.ErrValidation) }

func errorsIs(err, target error) bool { return errors.Is(err, target) }
