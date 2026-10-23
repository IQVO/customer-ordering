package customerorder

import (
	"reflect"
	"strings"
	"testing"

	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

func TestSnapshotRestoreRoundTripsEveryReachableState(t *testing.T) {
	orders := map[string]*CustomerOrder{
		"placed":      mustPlace(t, twoGroups()),
		"every state": orderWith(t, GroupPromised, GroupLocked, GroupShipped, GroupDelivered, GroupCancellationRequested, GroupCancelled),
		"cancelled":   orderWith(t, GroupCancelled, GroupCancelled),
		"delivered":   orderWith(t, GroupDelivered),
	}
	flow := mustPlace(t, twoGroups())
	mustNil(t, flow.MarkInFulfillment(1, "as-1", "fc-1", at(10)))
	mustNil(t, flow.MarkGroupShipped(1, []string{"TRK-1"}, at(20)))
	mustNil(t, flow.UpdateGroupPromise("p-2", 1, at(2000), at(2300), at(1900), "fc-2"))
	mustNil(t, flow.MarkGroupDelivered(2, "TRK-early", at(30)))
	_, err := flow.CancelOrder("changed my mind", at(40))
	mustNil(t, err)
	orders["flow"] = flow
	refused := orderWith(t, GroupCancellationRequested)
	mustNil(t, refused.RefuseGroupCancellation(1, "VENDOR_ACKNOWLEDGED", at(60)))
	orders["refused"] = refused

	for name, o := range orders {
		t.Run(name, func(t *testing.T) {
			back, err := Restore(o.Snapshot())
			mustNil(t, err)
			if !reflect.DeepEqual(back.Snapshot(), o.Snapshot()) {
				t.Fatalf("round trip changed the order:\n got %+v\nwant %+v", back.Snapshot(), o.Snapshot())
			}
		})
	}
}

func TestRestoreNormalisesInstantsToUTC(t *testing.T) {
	s := orderWith(t, GroupDelivered).Snapshot()
	s.PlacedAt = t0.In(brt)
	s.Groups[0].ShipBy = s.Groups[0].ShipBy.In(brt)
	back, err := Restore(s)
	mustNil(t, err)
	if back.PlacedAt().Location().String() != "UTC" || back.Groups()[0].ShipBy.Location().String() != "UTC" ||
		back.Groups()[0].DeliveredAt.Location().String() != "UTC" {
		t.Fatal("restored instants must be UTC")
	}
}

func TestRestoreRejectsCorruptSnapshots(t *testing.T) {
	good := func() Snapshot { return orderWith(t, GroupPromised, GroupShipped).Snapshot() }
	long := strings.Repeat("x", MaxReason+1)
	cases := map[string]func(*Snapshot){
		"order id":              func(s *Snapshot) { s.ID = "" },
		"shopper id":            func(s *Snapshot) { s.ShopperID = "-" },
		"ship-to":               func(s *Snapshot) { s.ShipTo.Name = "" },
		"prefix":                func(s *Snapshot) { s.DestinationPostalPrefix = "788" },
		"placedAt":              func(s *Snapshot) { s.PlacedAt = zeroTime },
		"version":               func(s *Snapshot) { s.Version = 0 },
		"no lines":              func(s *Snapshot) { s.Lines = nil },
		"bad status":            func(s *Snapshot) { s.Status = "LOST" },
		"status vs groups":      func(s *Snapshot) { s.Status = StatusPlaced },
		"cancelledAt on live":   func(s *Snapshot) { s.CancelledAt = at(1) },
		"no groups":             func(s *Snapshot) { s.Groups = nil },
		"duplicate group":       func(s *Snapshot) { s.Groups[1].GroupNo = 1 },
		"group number zero":     func(s *Snapshot) { s.Groups[0].GroupNo = 0 },
		"group number max+1":    func(s *Snapshot) { s.Groups[0].GroupNo = MaxNumber + 1 },
		"line in no group":      func(s *Snapshot) { s.Lines = append(s.Lines, OrderLine{3, "sku-1", 1}) },
		"group without lines":   func(s *Snapshot) { s.Groups[0].LineNos = []int{} },
		"line in two groups":    func(s *Snapshot) { s.Groups[0].LineNos = []int{1, 2} },
		"unknown line":          func(s *Snapshot) { s.Groups[0].LineNos = []int{7} },
		"promise id":            func(s *Snapshot) { s.Groups[0].PromiseID = "" },
		"witness":               func(s *Snapshot) { s.Groups[0].WitnessNodeID = "" },
		"tier":                  func(s *Snapshot) { s.Groups[0].Tier = "X" },
		"revision negative":     func(s *Snapshot) { s.Groups[0].RevisionNo = -1 },
		"revision max+1":        func(s *Snapshot) { s.Groups[0].RevisionNo = MaxNumber + 1 },
		"shipBy":                func(s *Snapshot) { s.Groups[0].ShipBy = zeroTime },
		"earliest":              func(s *Snapshot) { s.Groups[0].DeliverByEarliest = zeroTime },
		"window":                func(s *Snapshot) { s.Groups[0].DeliverByLatest = at(1) },
		"group status":          func(s *Snapshot) { s.Groups[0].Status = "X" },
		"repeated tn":           func(s *Snapshot) { s.Groups[1].TrackingNumbers = []string{"A", "A"} },
		"blank tn":              func(s *Snapshot) { s.Groups[1].TrackingNumbers = []string{""} },
		"too many tns":          func(s *Snapshot) { s.Groups[1].TrackingNumbers = manyTNs(MaxTrackingNumbers + 1) },
		"too many delivered":    func(s *Snapshot) { s.Groups[1].DeliveredTrackingNumbers = manyTNs(MaxTrackingNumbers + 1) },
		"deliveredAt without":   func(s *Snapshot) { s.Groups[1].DeliveredAt = at(5) },
		"delivered without at":  func(s *Snapshot) { s.Groups[1].DeliveredTrackingNumbers = []string{"x"} },
		"cancel reason":         func(s *Snapshot) { s.Groups[0].CancelReason = long },
		"refused reason":        func(s *Snapshot) { s.Groups[1].CancelRefusedReason = " " },
		"assignment":            func(s *Snapshot) { s.Groups[1].AssignmentID = "-" },
		"node":                  func(s *Snapshot) { s.Groups[1].NodeID = "-" },
		"promised with lock":    func(s *Snapshot) { s.Groups[0].LockedAt = at(1) },
		"promised with ship":    func(s *Snapshot) { s.Groups[0].ShippedAt = at(1) },
		"promised with request": func(s *Snapshot) { s.Groups[0].CancelRequestedAt = at(1) },
		"promised with cancel":  func(s *Snapshot) { s.Groups[0].CancelledAt = at(1) },
		"promised with refusal": func(s *Snapshot) { s.Groups[0].CancelRefusedReason = "R" },
		"shipped without at":    func(s *Snapshot) { s.Groups[1].ShippedAt = zeroTime },
		"shipped and cancelled": func(s *Snapshot) { s.Groups[1].CancelledAt = at(1) },
		"shipped all delivered": func(s *Snapshot) {
			s.Groups[1].DeliveredTrackingNumbers, s.Groups[1].DeliveredAt = []string{"TRK-2"}, at(1)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := good()
			mutate(&s)
			if _, err := Restore(s); !errorsIs(err, shared.ErrInvalidSnapshot) {
				t.Fatalf("want ErrInvalidSnapshot, got %v", err)
			}
		})
	}
}

func TestRestoreRejectsCorruptGroupStates(t *testing.T) {
	cases := map[string]struct {
		states []GroupStatus
		mutate func(*GroupPromise)
	}{
		"locked with ship":          {[]GroupStatus{GroupLocked}, func(g *GroupPromise) { g.ShippedAt = at(1) }},
		"locked with cancel":        {[]GroupStatus{GroupLocked}, func(g *GroupPromise) { g.CancelledAt = at(1) }},
		"delivered without ship":    {[]GroupStatus{GroupDelivered}, func(g *GroupPromise) { g.ShippedAt = zeroTime }},
		"delivered with cancel":     {[]GroupStatus{GroupDelivered}, func(g *GroupPromise) { g.CancelledAt = at(1) }},
		"delivered missing package": {[]GroupStatus{GroupDelivered}, func(g *GroupPromise) { g.TrackingNumbers = append(g.TrackingNumbers, "TRK-2") }},
		"delivered no packages": {[]GroupStatus{GroupDelivered}, func(g *GroupPromise) {
			g.TrackingNumbers, g.DeliveredTrackingNumbers, g.DeliveredAt = nil, nil, zeroTime
		}},
		"requested without at":   {[]GroupStatus{GroupCancellationRequested}, func(g *GroupPromise) { g.CancelRequestedAt = zeroTime }},
		"requested with cancel":  {[]GroupStatus{GroupCancellationRequested}, func(g *GroupPromise) { g.CancelledAt = at(1) }},
		"requested with ship":    {[]GroupStatus{GroupCancellationRequested}, func(g *GroupPromise) { g.ShippedAt = at(1) }},
		"cancelled without at":   {[]GroupStatus{GroupCancelled, GroupPromised}, func(g *GroupPromise) { g.CancelledAt = zeroTime }},
		"cancelled with shipped": {[]GroupStatus{GroupCancelled, GroupPromised}, func(g *GroupPromise) { g.ShippedAt = at(1) }},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			o := orderWith(t, c.states...)
			c.mutate(&o.groups[0])
			if _, err := Restore(o.Snapshot()); !errorsIs(err, shared.ErrInvalidSnapshot) {
				t.Fatalf("want ErrInvalidSnapshot, got %v", err)
			}
		})
	}
}

func TestRestoreAcceptsTheLimitsAndAnUnlockedCancelledOrder(t *testing.T) {
	s := orderWith(t, GroupShipped).Snapshot()
	s.Groups[0].TrackingNumbers = manyTNs(MaxTrackingNumbers)
	s.Groups[0].DeliveredTrackingNumbers = manyTNs(MaxTrackingNumbers - 1)
	s.Groups[0].DeliveredAt = at(30)
	s.Groups[0].CancelReason = strings.Repeat("r", MaxReason)
	s.Groups[0].RevisionNo = MaxNumber
	if _, err := Restore(s); err != nil {
		t.Fatalf("limits are inclusive: %v", err)
	}
	one := orderWith(t, GroupPromised).Snapshot()
	one.Groups[0].GroupNo = MaxNumber
	one.Lines[0].LineNo = 1
	if _, err := Restore(one); err != nil {
		t.Fatalf("group number %d is valid: %v", MaxNumber, err)
	}
	c := orderWith(t, GroupCancelled).Snapshot()
	if _, err := Restore(c); err != nil {
		t.Fatalf("a CANCELLED order restores: %v", err)
	}
	c.CancelledAt = zeroTime
	if _, err := Restore(c); !errorsIs(err, shared.ErrInvalidSnapshot) {
		t.Fatalf("a CANCELLED order needs cancelledAt: %v", err)
	}
}

func TestRestoreRejectsTooManyGroups(t *testing.T) {
	s := orderWith(t, GroupPromised).Snapshot()
	for i := 2; i <= MaxGroups+1; i++ {
		g := s.Groups[0].clone()
		g.GroupNo = i
		g.LineNos = []int{i}
		g.PromiseID = "p-x"
		s.Groups = append(s.Groups, g)
		s.Lines = append(s.Lines, OrderLine{i, "sku-1", 1})
	}
	if _, err := Restore(s); !errorsIs(err, shared.ErrInvalidSnapshot) {
		t.Fatalf("want ErrInvalidSnapshot for %d groups, got %v", len(s.Groups), err)
	}
	s.Groups, s.Lines = s.Groups[:MaxGroups], s.Lines[:MaxGroups]
	if _, err := Restore(s); err != nil {
		t.Fatalf("%d groups are valid: %v", MaxGroups, err)
	}
}

func manyTNs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "TRK-" + string(rune('A'+i%26)) + string(rune('a'+i/26))
	}
	return out
}

func TestLoadedVersionTracksWhatIsStored(t *testing.T) {
	o := mustPlace(t, twoGroups())
	if o.LoadedVersion() != 0 || o.Version() != 1 {
		t.Fatalf("a new order is at version 1 and was never stored: loaded %d version %d", o.LoadedVersion(), o.Version())
	}
	o.MarkPersisted()
	if o.LoadedVersion() != 1 {
		t.Fatalf("loaded = %d after MarkPersisted", o.LoadedVersion())
	}
	mustNil(t, o.MarkInFulfillment(1, "as-1", "fc-1", at(10)))
	mustNil(t, o.MarkGroupShipped(1, []string{"TRK-1"}, at(20)))
	if o.Version() != 3 || o.LoadedVersion() != 1 {
		t.Fatalf("two commands move the version, not the loaded one: %d %d", o.Version(), o.LoadedVersion())
	}
	back, err := Restore(o.Snapshot())
	mustNil(t, err)
	if back.LoadedVersion() != 3 || back.Version() != 3 {
		t.Fatalf("a restored order was loaded at its version: %d %d", back.LoadedVersion(), back.Version())
	}
}
