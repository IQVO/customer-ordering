package usecases

import (
	"context"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
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

// ApplyPackageEta records the window on the package row unless a newer one is
// stored. It touches the read model only: no order, group or promise field is
// read or written (ADR 0014, decision 6).
func (c Consumer) ApplyPackageEta(ctx context.Context, m Meta, in PackageEtaWindowAssigned) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		return c.Tracking.ApplyEta(ctx, readmodel.Eta{
			TrackingNumber: in.TrackingNumber, AssignmentID: in.AssignmentID, OrderID: in.OrderID,
			Start: in.EtaStart, End: in.EtaEnd, AssignedAt: orElse(in.AssignedAt, c.when(m)),
		})
	})
}
