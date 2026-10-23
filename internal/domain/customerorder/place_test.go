package customerorder

import (
	"strings"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

func TestPlaceBuildsAPlacedOrderAndRaisesTheEvent(t *testing.T) {
	brt := time.FixedZone("BRT", -3*3600)
	o, evs, err := Place(twoGroups(), t0.In(brt))
	mustNil(t, err)
	assertPlacedOrder(t, o)
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	assertPlacedEvent(t, evs[0].(CustomerOrderPlaced))
}

func assertPlacedOrder(t *testing.T, o *CustomerOrder) {
	t.Helper()
	if o.ID() != "order-1" || o.ShopperID() != "shopper-1" || o.DestinationPostalPrefix() != "787" || o.Version() != 1 {
		t.Fatalf("unexpected header: %+v", o.Snapshot())
	}
	assertOrderStatus(t, o, StatusPlaced)
	if o.PlacedAt().Location() != time.UTC || !o.PlacedAt().Equal(t0) || !o.CancelledAt().IsZero() {
		t.Fatalf("placedAt/cancelledAt wrong: %v %v", o.PlacedAt(), o.CancelledAt())
	}
	if o.ShipTo() != goodShipTo() || len(o.Lines()) != 3 || len(o.Groups()) != 2 {
		t.Fatalf("ship-to, lines or groups lost")
	}
	assertPlacedGroupPromise(t, o.Groups()[0])
	assertPlacedGroupDefaults(t, o.Groups()[0])
}

func assertPlacedGroupPromise(t *testing.T, g GroupPromise) {
	t.Helper()
	if g.PromiseID != "p-1" || g.Tier != TierNextDay || g.WitnessNodeID != "fc-aus-1" {
		t.Fatalf("promise identity wrong: %+v", g)
	}
	if !g.ShipBy.Equal(at(600)) || !g.DeliverByEarliest.Equal(at(1200)) || !g.DeliverByLatest.Equal(at(1500)) || len(g.LineNos) != 2 {
		t.Fatalf("promise window or lines wrong: %+v", g)
	}
}

func assertPlacedGroupDefaults(t *testing.T, g GroupPromise) {
	t.Helper()
	if g.Status != GroupPromised || g.RevisionNo != 0 || g.TrackingNumbers == nil || g.DeliveredTrackingNumbers == nil {
		t.Fatalf("group defaults wrong: %+v", g)
	}
}

func assertPlacedEvent(t *testing.T, ev CustomerOrderPlaced) {
	t.Helper()
	if ev.EventName() != "CustomerOrderPlaced" || ev.Entity() != "customerorder" || ev.SubjectID() != "order-1" ||
		ev.AggregateVersion() != 1 || !ev.OccurredAt().Equal(t0) {
		t.Fatalf("event header wrong: %+v", ev)
	}
	if ev.ShopperID != "shopper-1" || ev.DestinationPostalPrefix != "787" || !ev.PlacedAt.Equal(t0) || len(ev.Groups) != 2 {
		t.Fatalf("event body wrong: %+v", ev)
	}
	assertPlacedEventGroup1(t, ev.Groups[0])
	assertPlacedEventGroup2(t, ev.Groups[1])
}

func assertPlacedEventGroup1(t *testing.T, g PlacedGroup) {
	t.Helper()
	if g.GroupNo != 1 || g.PromiseID != "p-1" || g.Tier != TierNextDay || g.WitnessNodeID != "fc-aus-1" {
		t.Fatalf("group 1 of the event wrong: %+v", g)
	}
	if !g.ShipBy.Equal(at(600)) || !g.DeliverByEarliest.Equal(at(1200)) || !g.DeliverByLatest.Equal(at(1500)) {
		t.Fatalf("group 1 window wrong: %+v", g)
	}
	if len(g.Lines) != 2 || g.Lines[0] != (OrderLine{1, "sku-100", 2}) || g.Lines[1] != (OrderLine{2, "sku-200", 1}) {
		t.Fatalf("group 1 lines wrong: %+v", g.Lines)
	}
}

func assertPlacedEventGroup2(t *testing.T, g PlacedGroup) {
	t.Helper()
	if g.GroupNo != 2 || len(g.Lines) != 1 || g.Lines[0] != (OrderLine{3, "sku-300", 5}) {
		t.Fatalf("group 2 wrong: %+v", g)
	}
}

func TestPlaceCopiesItsInput(t *testing.T) {
	in := twoGroups()
	o := mustPlace(t, in)
	in.Lines[0].Quantity = 99
	in.Groups[0].LineNos[0] = 3
	if o.Lines()[0].Quantity != 2 || o.Groups()[0].LineNos[0] != 1 {
		t.Fatal("the aggregate must own its lines and group line numbers")
	}
	o.Lines()[0].Quantity = 77
	o.Groups()[0].LineNos[0] = 3
	if o.Lines()[0].Quantity != 2 || o.Groups()[0].LineNos[0] != 1 {
		t.Fatal("accessors must return copies")
	}
}

func TestPlaceRejects(t *testing.T) {
	long := strings.Repeat("x", 65)
	cases := []struct {
		name   string
		mutate func(*NewOrder)
		want   error
	}{
		{"bad order id", func(n *NewOrder) { n.ID = "-1" }, shared.ErrValidation},
		{"bad shopper id", func(n *NewOrder) { n.ShopperID = "" }, shared.ErrValidation},
		{"bad ship-to", func(n *NewOrder) { n.ShipTo.City = "" }, shared.ErrValidation},
		{"no lines", func(n *NewOrder) { n.Lines = nil }, shared.ErrValidation},
		{"line number zero", func(n *NewOrder) { n.Lines[0].LineNo = 0 }, shared.ErrValidation},
		{"line number above max", func(n *NewOrder) { n.Lines[0].LineNo = MaxNumber + 1 }, shared.ErrValidation},
		{"duplicate line number", func(n *NewOrder) { n.Lines[1].LineNo = 1 }, shared.ErrValidation},
		{"bad catalog item", func(n *NewOrder) { n.Lines[0].CatalogItemID = long }, shared.ErrValidation},
		{"quantity zero", func(n *NewOrder) { n.Lines[0].Quantity = 0 }, shared.ErrValidation},
		{"quantity above max", func(n *NewOrder) { n.Lines[0].Quantity = MaxNumber + 1 }, shared.ErrValidation},
		{"no groups", func(n *NewOrder) { n.Groups = nil }, ErrLineWithoutPromise},
		{"group without promise", func(n *NewOrder) { n.Groups[0].PromiseID = "" }, ErrLineWithoutPromise},
		{"line in no group", func(n *NewOrder) { n.Groups[0].LineNos = []int{1} }, ErrLineWithoutPromise},
		{"line in two groups", func(n *NewOrder) { n.Groups[1].LineNos = []int{3, 1} }, ErrLineWithoutPromise},
		{"group names unknown line", func(n *NewOrder) { n.Groups[1].LineNos = []int{9} }, shared.ErrValidation},
		{"group without lines", func(n *NewOrder) { n.Groups[1].LineNos = nil }, shared.ErrValidation},
		{"group number zero", func(n *NewOrder) { n.Groups[0].GroupNo = 0 }, shared.ErrValidation},
		{"group number above max", func(n *NewOrder) { n.Groups[0].GroupNo = MaxNumber + 1 }, shared.ErrValidation},
		{"duplicate group number", func(n *NewOrder) { n.Groups[1].GroupNo = 1 }, shared.ErrValidation},
		{"bad promise id", func(n *NewOrder) { n.Groups[0].PromiseID = long }, shared.ErrValidation},
		{"bad witness node", func(n *NewOrder) { n.Groups[0].WitnessNodeID = "" }, shared.ErrValidation},
		{"bad tier", func(n *NewOrder) { n.Groups[0].Tier = "WEEKLY" }, shared.ErrValidation},
		{"no shipBy", func(n *NewOrder) { n.Groups[0].ShipBy = time.Time{} }, shared.ErrValidation},
		{"no earliest", func(n *NewOrder) { n.Groups[0].DeliverByEarliest = time.Time{} }, shared.ErrValidation},
		{"no latest", func(n *NewOrder) { n.Groups[0].DeliverByLatest = time.Time{} }, shared.ErrValidation},
		{"latest before earliest", func(n *NewOrder) { n.Groups[0].DeliverByLatest = at(1199) }, shared.ErrValidation},
		{"promise for another destination", func(n *NewOrder) { n.Groups[1].DestinationPostalPrefix = "788" }, ErrPromiseDestinationMismatch},
		{"promise without destination", func(n *NewOrder) { n.Groups[0].DestinationPostalPrefix = "" }, ErrPromiseDestinationMismatch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := twoGroups()
			c.mutate(&in)
			if _, _, err := Place(in, t0); err == nil {
				t.Fatal("want an error")
			} else {
				assertErr(t, err, c.want)
			}
		})
	}
}

func TestPlaceAcceptsTheLimits(t *testing.T) {
	in := NewOrder{ID: "order-1", ShopperID: "shopper-1", ShipTo: goodShipTo()}
	for g := 1; g <= MaxGroups; g++ {
		a, b := 2*g-1, 2*g
		in.Lines = append(in.Lines, OrderLine{a, "sku-1", 1}, OrderLine{b, "sku-2", MaxNumber})
		in.Groups = append(in.Groups, groupInput(g, "p-"+strings.Repeat("a", g%9+1), a, b))
	}
	in.Groups[0].GroupNo, in.Groups[0].PromiseID = MaxNumber, "p-first"
	in.Lines[0].LineNo = MaxNumber
	in.Groups[0].LineNos = []int{MaxNumber, 2}
	if len(in.Lines) != MaxLines {
		t.Fatalf("test setup: %d lines", len(in.Lines))
	}
	if _, _, err := Place(in, t0); err != nil {
		t.Fatalf("limits are inclusive: %v", err)
	}
	in.Lines = append(in.Lines, OrderLine{MaxLines + 1, "sku-3", 1})
	in.Groups[1].LineNos = append(in.Groups[1].LineNos, MaxLines+1)
	if _, _, err := Place(in, t0); !isValidation(err) {
		t.Fatalf("101 lines must be refused: %v", err)
	}
	in.Lines = in.Lines[:MaxLines]
	in.Groups[1].LineNos = in.Groups[1].LineNos[:2]
	in.Groups = append(in.Groups, groupInput(MaxGroups+1, "p-extra", 1))
	if _, _, err := Place(in, t0); !isValidation(err) {
		t.Fatalf("51 groups must be refused: %v", err)
	}
}

func TestPlaceAcceptsAnInstantaneousWindowAndALongerPromisePrefix(t *testing.T) {
	in := oneGroup()
	in.Groups[0].DeliverByLatest = in.Groups[0].DeliverByEarliest
	in.Groups[0].DestinationPostalPrefix = "78701"
	if _, _, err := Place(in, t0); err != nil {
		t.Fatalf("earliest == latest and a longer matching prefix are valid: %v", err)
	}
}
