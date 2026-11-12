package usecases

import (
	"context"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
)

// PackageDeliveryPointFlagged is the consumed shipping-and-tracking event of the
// same name (ADR 0015): the delivery point of the package failed twice.
type PackageDeliveryPointFlagged struct {
	TrackingNumber string
	AssignmentID   string
	OrderID        string
	Reason         string
	FlaggedAt      time.Time
}

// ApplyDeliveryPointFlag records the flag on the package row unless a newer one
// is stored. It touches the read model only: no order, group, promise or ETA
// window is read or written (ADR 0015, decision 6).
func (c Consumer) ApplyDeliveryPointFlag(ctx context.Context, m Meta, in PackageDeliveryPointFlagged) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		return c.Tracking.ApplyPointFlag(ctx, readmodel.PointFlag{
			TrackingNumber: in.TrackingNumber, AssignmentID: in.AssignmentID, OrderID: in.OrderID,
			Reason: in.Reason, FlaggedAt: orElse(in.FlaggedAt, c.when(m)),
		})
	})
}
