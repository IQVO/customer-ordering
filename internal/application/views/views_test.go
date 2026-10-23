package views_test

import (
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/promising"
	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/application/views"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

var t0 = time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)

func address() shipto.ShipTo {
	return shipto.ShipTo{Name: "Ana Example", Line1: "1 Sample Street", Line2: "Apt 2", City: "Austin", Region: "TX", PostalCode: "78701", CountryCode: "US", Phone: "+1 555 0100"}
}

func placed(t *testing.T) *customerorder.CustomerOrder {
	t.Helper()
	g := func(no int, promise string, lines ...int) customerorder.GroupInput {
		return customerorder.GroupInput{
			GroupNo: no, PromiseID: promise, Tier: customerorder.TierNextDay, WitnessNodeID: "fc-1", ShipBy: t0.Add(time.Hour),
			DeliverByEarliest: t0.Add(2 * time.Hour), DeliverByLatest: t0.Add(3 * time.Hour), LineNos: lines, DestinationPostalPrefix: "787",
		}
	}
	o, _, err := customerorder.Place(customerorder.NewOrder{
		ID: "order-1", ShopperID: "shopper-1", ShipTo: address(),
		Lines:  []customerorder.OrderLine{{LineNo: 1, CatalogItemID: "sku-1", Quantity: 2}, {LineNo: 2, CatalogItemID: "sku-2", Quantity: 1}},
		Groups: []customerorder.GroupInput{g(1, "p-1", 1), g(2, "p-2", 2)},
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestOrderFromMapsEverythingButTheShipTo(t *testing.T) {
	o := placed(t)
	if _, err := o.CancelOrder("changed my mind", t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	v := views.OrderFrom(o)
	assertOrderHeader(t, v)
	assertOrderGroup(t, v.Groups[0])
}

func assertOrderHeader(t *testing.T, v views.Order) {
	t.Helper()
	if v.OrderID != "order-1" || v.ShopperID != "shopper-1" || v.Status != "PLACED" || v.DestinationPostalPrefix != "787" {
		t.Fatalf("order header = %+v", v)
	}
	if !v.PlacedAt.Equal(t0) || !v.CancelledAt.IsZero() || len(v.Lines) != 2 || v.Lines[0] != (views.OrderLine{LineNo: 1, CatalogItemID: "sku-1", Quantity: 2}) {
		t.Fatalf("order body = %+v", v)
	}
}

func assertOrderGroup(t *testing.T, g views.OrderGroup) {
	t.Helper()
	if g.GroupNo != 1 || g.PromiseID != "p-1" || g.Tier != "NEXT_DAY" || g.Status != "CANCELLATION_REQUESTED" || g.CancelReason != "changed my mind" {
		t.Fatalf("group identity = %+v", g)
	}
	if !g.CancelRequestedAt.Equal(t0.Add(time.Minute)) || !g.ShipBy.Equal(t0.Add(time.Hour)) || !g.DeliverByEarliest.Equal(t0.Add(2*time.Hour)) ||
		!g.DeliverByLatest.Equal(t0.Add(3*time.Hour)) || len(g.LineNos) != 1 || g.RevisionNo != 0 || g.CancelRefusedReason != "" {
		t.Fatalf("group facts = %+v", g)
	}
}

func TestShipToAndShopperViews(t *testing.T) {
	if got := views.ShipToFrom(address()); got.Name != "Ana Example" || got.Line1 != "1 Sample Street" || got.Line2 != "Apt 2" || got.City != "Austin" ||
		got.Region != "TX" || got.PostalCode != "78701" || got.CountryCode != "US" || got.Phone != "+1 555 0100" {
		t.Fatalf("ship-to view = %+v", got)
	}
	s, err := shopper.Register(shopper.NewShopper{ID: "shopper-1", Email: "a@b.com"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if v := views.ShopperFrom(s); v.ShopperID != "shopper-1" || v.Email != "a@b.com" || v.Status != "ACTIVE" || !v.RegisteredAt.Equal(t0) {
		t.Fatalf("shopper view = %+v", v)
	}
}

func TestTrackingFromJoinsSignalsAndPackages(t *testing.T) {
	o := placed(t)
	late := 30
	v := views.TrackingFrom(o,
		[]readmodel.GroupSignal{{OrderID: "order-1", GroupNo: 2, AtRiskReason: "R", UnsourceableReason: "U", PromiseVerdict: "BROKEN", LateByMinutes: &late}},
		[]readmodel.Package{
			{TrackingNumber: "T2", GroupNo: 2, Carrier: "C", State: "AT_STATION", LastScanNodeID: "n", LastScanAt: t0, DeliveredAt: t0, ExceptionReason: "E"},
			{TrackingNumber: "T0", GroupNo: 0},
		})
	if v.OrderID != "order-1" || v.Status != "PLACED" || len(v.Groups) != 2 {
		t.Fatalf("tracking = %+v", v)
	}
	assertUntrackedGroup(t, v.Groups[0])
	assertTrackedGroup(t, v.Groups[1])
}

func assertUntrackedGroup(t *testing.T, g views.TrackingGroup) {
	t.Helper()
	if g.AtRiskReason != "" || g.Packages == nil || len(g.Packages) != 0 || g.Status != "PROMISED" || g.PromiseID != "p-1" {
		t.Fatalf("group 1 = %+v", g)
	}
}

func assertTrackedGroup(t *testing.T, g views.TrackingGroup) {
	t.Helper()
	if g.AtRiskReason != "R" || g.UnsourceableReason != "U" || g.PromiseVerdict != "BROKEN" || g.LateByMinutes == nil || *g.LateByMinutes != 30 || len(g.Packages) != 1 {
		t.Fatalf("group 2 = %+v", g)
	}
	p := g.Packages[0]
	if p.TrackingNumber != "T2" || p.Carrier != "C" || p.State != "AT_STATION" || p.LastScanNodeID != "n" || !p.LastScanAt.Equal(t0) ||
		!p.DeliveredAt.Equal(t0) || p.ExceptionReason != "E" {
		t.Fatalf("package = %+v", p)
	}
}

func TestRequotedFrom(t *testing.T) {
	got := views.RequotedFrom(3, []promising.Offer{{OfferID: "o", Tier: "STANDARD", DeliverByEarliest: t0, DeliverByLatest: t0.Add(time.Hour), ExpiresAt: t0.Add(time.Minute)}})
	if len(got) != 1 || got[0].GroupNo != 3 || got[0].OfferID != "o" || got[0].Tier != "STANDARD" || !got[0].DeliverByLatest.Equal(t0.Add(time.Hour)) || !got[0].ExpiresAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("requoted = %+v", got)
	}
	if none := views.RequotedFrom(1, nil); none == nil || len(none) != 0 {
		t.Fatalf("no offers must be an empty list, got %#v", none)
	}
}
