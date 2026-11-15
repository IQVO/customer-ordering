package usecases

import (
	"context"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/domain/notice"
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
// is stored, and decides the DELIVERY_POINT_ADVISORY notice when the stored
// reason changed (a repeat of the same reason is not a new fact, ADR 0017). It
// touches the read model only: no order, group, promise or ETA window is read
// or written (ADR 0015, decision 6).
func (c Consumer) ApplyDeliveryPointFlag(ctx context.Context, m Meta, in PackageDeliveryPointFlagged) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		targets, err := c.noticeTargetsOfTracking(ctx, in.TrackingNumber, in.AssignmentID, in.OrderID)
		if err != nil {
			return err
		}
		var before string
		if len(targets) > 0 {
			before, err = c.flagOf(ctx, targets, in.TrackingNumber)
			if err != nil {
				return err
			}
		}
		if err := c.Tracking.ApplyPointFlag(ctx, readmodel.PointFlag{
			TrackingNumber: in.TrackingNumber, AssignmentID: in.AssignmentID, OrderID: in.OrderID,
			Reason: in.Reason, FlaggedAt: orElse(in.FlaggedAt, c.when(m)),
		}); err != nil {
			return err
		}
		if len(targets) == 0 {
			return nil // the fact cannot be attributed to a group yet (ADR 0017)
		}
		after, err := c.flagOf(ctx, targets, in.TrackingNumber)
		if err != nil {
			return err
		}
		if after == before {
			return nil // a replay of the same advisory, or an older one: not a new fact
		}
		return c.publishNotices(ctx, c.when(m), notice.FamilyDeliveryPointAdvisory, notice.PointFlagFact(after), targets)
	})
}
