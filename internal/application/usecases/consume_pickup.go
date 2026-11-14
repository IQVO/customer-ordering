package usecases

import (
	"context"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
)

// PackageReadyForCollection is the consumed shipping-and-tracking event of the
// same name (ADR 0016): a pickup package reached its point.
type PackageReadyForCollection struct {
	TrackingNumber string
	AssignmentID   string
	OrderID        string
	PickupPointID  string
	ReadyAt        time.Time
	HoldUntil      time.Time
}

// ApplyPackageReadyForCollection records the deposit on the package row. It
// changes no aggregate: the promise was judged at delivery-promise and the
// order keeps moving on the shipment events (ADR 0016).
func (c Consumer) ApplyPackageReadyForCollection(ctx context.Context, m Meta, in PackageReadyForCollection) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		return c.Tracking.ApplyReadyForCollection(ctx, readmodel.ReadyForCollection{
			TrackingNumber: in.TrackingNumber, AssignmentID: in.AssignmentID, OrderID: in.OrderID,
			PickupPointID: in.PickupPointID, ReadyAt: orElse(in.ReadyAt, c.when(m)), HoldUntil: in.HoldUntil,
		})
	})
}

// PackageCollected is the consumed shipping-and-tracking event of the same
// name (ADR 0016): the shopper collected the package.
type PackageCollected struct {
	TrackingNumber string
	AssignmentID   string
	OrderID        string
	PickupPointID  string
	CollectedAt    time.Time
	ProofCode      string
}

// ApplyPackageCollected records the collection on the package row. Like the
// deposit it changes no aggregate (ADR 0016).
func (c Consumer) ApplyPackageCollected(ctx context.Context, m Meta, in PackageCollected) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		return c.Tracking.ApplyCollected(ctx, readmodel.Collected{
			TrackingNumber: in.TrackingNumber, AssignmentID: in.AssignmentID, OrderID: in.OrderID,
			PickupPointID: in.PickupPointID, CollectedAt: orElse(in.CollectedAt, c.when(m)), ProofCode: in.ProofCode,
		})
	})
}
