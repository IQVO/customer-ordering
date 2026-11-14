package usecases

import (
	"context"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/domain/notice"
)

// PackageEtaWindowAssigned is the consumed shipping-and-tracking event of the
// same name (ADR 0014): the day-of delivery window of one package.
type PackageEtaWindowAssigned struct {
	TrackingNumber string
	AssignmentID   string
	OrderID        string
	EtaStart       time.Time
	EtaEnd         time.Time
	AssignedAt     time.Time
}

// windowOf reads the package's stored window (found is false when the row or
// its window is not stored yet).
func (c Consumer) windowOf(ctx context.Context, targets []groupTarget, trackingNumber string) (start, end time.Time, found bool, err error) {
	for _, t := range targets {
		pkgs, e := c.Tracking.Packages(ctx, t.orderID)
		if e != nil {
			return time.Time{}, time.Time{}, false, e
		}
		for _, p := range pkgs {
			if p.TrackingNumber == trackingNumber && !p.EtaStart.IsZero() {
				return p.EtaStart, p.EtaEnd, true, nil
			}
		}
	}
	return time.Time{}, time.Time{}, false, nil
}

// flagOf reads the package's stored delivery-point flag reason ("" when none).
func (c Consumer) flagOf(ctx context.Context, targets []groupTarget, trackingNumber string) (string, error) {
	for _, t := range targets {
		pkgs, err := c.Tracking.Packages(ctx, t.orderID)
		if err != nil {
			return "", err
		}
		for _, p := range pkgs {
			if p.TrackingNumber == trackingNumber && p.PointFlagReason != "" {
				return p.PointFlagReason, nil
			}
		}
	}
	return "", nil
}

// ApplyPackageEta records the window on the package row unless a newer one is
// stored, and decides the ETA_WINDOW_ASSIGNED notice when the stored window
// changed (a re-assignment is a new fact, a replay is not, ADR 0017). It
// touches the read model only: no order, group or promise field is read or
// written (ADR 0014, decision 6).
func (c Consumer) ApplyPackageEta(ctx context.Context, m Meta, in PackageEtaWindowAssigned) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		targets, err := c.noticeTargetsOfTracking(ctx, in.TrackingNumber, in.AssignmentID, in.OrderID)
		if err != nil {
			return err
		}
		var beforeStart, beforeEnd time.Time
		var beforeFound bool
		if len(targets) > 0 {
			beforeStart, beforeEnd, beforeFound, err = c.windowOf(ctx, targets, in.TrackingNumber)
			if err != nil {
				return err
			}
		}
		if err := c.Tracking.ApplyEta(ctx, readmodel.Eta{
			TrackingNumber: in.TrackingNumber, AssignmentID: in.AssignmentID, OrderID: in.OrderID,
			Start: in.EtaStart, End: in.EtaEnd, AssignedAt: orElse(in.AssignedAt, c.when(m)),
		}); err != nil {
			return err
		}
		if len(targets) == 0 {
			return nil // the fact cannot be attributed to a group yet (ADR 0017)
		}
		afterStart, afterEnd, afterFound, err := c.windowOf(ctx, targets, in.TrackingNumber)
		if err != nil {
			return err
		}
		if afterFound == beforeFound && afterStart.Equal(beforeStart) && afterEnd.Equal(beforeEnd) {
			return nil // a replay of the same window, or an older one: not a new fact
		}
		return c.publishNotices(ctx, c.when(m), notice.FamilyEtaWindowAssigned, notice.EtaFact(afterStart, afterEnd), targets)
	})
}
