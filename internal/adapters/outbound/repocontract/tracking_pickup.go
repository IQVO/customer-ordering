package repocontract

import (
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
)

// The collection contract of ADR 0016: the pickup label names the point, the
// deposit stores the hold, the collection stores the proof, and a ship-to
// package keeps none of it.
const (
	tnPickup     = "TRK-PICKUP1"
	tnShipToNone = "TRK-PICKUP2"
	pointPickup  = "node-pnt-7"
	holdMinutes  = 7 * 24 * 60

	// The tracking states of a pickup package (ADR 0016).
	stateLabelled  = "LABELLED"
	stateReady     = "READY_FOR_COLLECTION"
	stateCollected = "COLLECTED"
)

func deposit(tn string, readyMin, holdMin int) readmodel.ReadyForCollection {
	return readmodel.ReadyForCollection{
		TrackingNumber: tn, AssignmentID: assignEta, OrderID: orderEta, PickupPointID: pointPickup,
		ReadyAt: at(readyMin), HoldUntil: at(holdMin),
	}
}

func collection(tn string, atMin int, proof string) readmodel.Collected {
	return readmodel.Collected{
		TrackingNumber: tn, AssignmentID: assignEta, OrderID: orderEta, PickupPointID: pointPickup,
		CollectedAt: at(atMin), ProofCode: proof,
	}
}

func testTrackingPickup(t *testing.T, e Env) {
	cases := []struct {
		name string
		run  func(*testing.T, Env)
	}{
		{"a pickup label names the point", trackingPickupLabelNamesThePoint},
		{"the deposit stores the state and the hold", trackingPickupDeposit},
		{"the collection stores the state and the proof", trackingPickupCollection},
		{"a collection before the deposit still lands", trackingPickupCollectionFirst},
		{"a ship-to package has no point at all", trackingPickupAbsentByDefault},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, e) })
	}
}

func trackingPickupLabelNamesThePoint(t *testing.T, e Env) {
	must(t, e.Tracking.LinkAssignment(bg(), readmodel.AssignmentLink{AssignmentID: assignEta, OrderID: orderEta, GroupNo: 2}))
	must(t, e.Tracking.ApplyLabel(bg(), readmodel.Label{TrackingNumber: tnPickup, AssignmentID: assignEta, Carrier: "c-1", PickupPointID: pointPickup}))
	p := packageByTN(t, e, orderEta, tnPickup)
	if p.PickupPointID != pointPickup || p.State != stateLabelled || !p.ReadyForCollectionAt.IsZero() {
		t.Fatalf("package = %+v", p)
	}
}

func trackingPickupDeposit(t *testing.T, e Env) {
	must(t, e.Tracking.ApplyReadyForCollection(bg(), deposit(tnPickup, 600, 600+holdMinutes)))
	p := packageByTN(t, e, orderEta, tnPickup)
	if p.State != stateReady || p.PickupPointID != pointPickup ||
		!p.ReadyForCollectionAt.Equal(at(600)) || !p.HoldUntil.Equal(at(600+holdMinutes)) || !p.CollectedAt.IsZero() {
		t.Fatalf("package = %+v", p)
	}
}

func trackingPickupCollection(t *testing.T, e Env) {
	must(t, e.Tracking.ApplyCollected(bg(), collection(tnPickup, 800, "4821")))
	p := packageByTN(t, e, orderEta, tnPickup)
	if p.State != stateCollected || !p.CollectedAt.Equal(at(800)) || p.ProofCode != "4821" || !p.ReadyForCollectionAt.Equal(at(600)) {
		t.Fatalf("package = %+v", p)
	}
}

func trackingPickupCollectionFirst(t *testing.T, e Env) {
	must(t, e.Tracking.ApplyCollected(bg(), collection(tnPickup, 800, "")))
	p := packageByTN(t, e, orderEta, tnPickup)
	if p.State != stateCollected || p.PickupPointID != pointPickup || !p.CollectedAt.Equal(at(800)) || p.ProofCode != "" {
		t.Fatalf("package = %+v", p)
	}
	must(t, e.Tracking.ApplyReadyForCollection(bg(), deposit(tnPickup, 600, 600+holdMinutes)))
	p = packageByTN(t, e, orderEta, tnPickup)
	if p.State != stateCollected || !p.ReadyForCollectionAt.Equal(at(600)) {
		t.Fatalf("a deposit after the collection is a fact, not a regression: %+v", p)
	}
}

func trackingPickupAbsentByDefault(t *testing.T, e Env) {
	must(t, e.Tracking.AttachTrackingNumbers(bg(), orderEta, 3, []string{tnShipToNone}))
	must(t, e.Tracking.ApplyScan(bg(), readmodel.Scan{TrackingNumber: tnShipToNone, State: scanOutForDelivery, At: at(5)}))
	p := packageByTN(t, e, orderEta, tnShipToNone)
	if p.PickupPointID != "" || !p.ReadyForCollectionAt.IsZero() || !p.HoldUntil.IsZero() || !p.CollectedAt.IsZero() || p.ProofCode != "" {
		t.Fatalf("no pickup fact may exist by default: %+v", p)
	}
}
