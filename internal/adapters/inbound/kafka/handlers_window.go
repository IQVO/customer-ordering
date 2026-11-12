package kafka

import (
	"context"
	"fmt"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// packageEtaData restates the v1 `data` of PackageEtaWindowAssigned (ADR 0014);
// unknown fields are ignored.
type packageEtaData struct {
	TrackingNumber string    `json:"trackingNumber"`
	AssignmentID   string    `json:"assignmentId"`
	OrderID        string    `json:"orderId"`
	EtaStart       time.Time `json:"etaStart"`
	EtaEnd         time.Time `json:"etaEnd"`
	AssignedAt     time.Time `json:"assignedAt"`
}

// validEta refuses a payload that can never be applied: a blank tracking
// number, or a window that is not a proper interval once stored at the
// microsecond Postgres keeps.
func validEta(d packageEtaData) error {
	if err := need("PackageEtaWindowAssigned", "trackingNumber", d.TrackingNumber); err != nil {
		return err
	}
	if !shared.UTC(d.EtaStart).Before(shared.UTC(d.EtaEnd)) || d.EtaStart.IsZero() {
		return fmt.Errorf("%w: PackageEtaWindowAssigned needs etaStart before etaEnd", errBadPayload)
	}
	return nil
}

// etaHandler applies PackageEtaWindowAssigned to the tracking read model.
func etaHandler(c usecases.Consumer) HandlerFunc {
	return handler("PackageEtaWindowAssigned", validEta,
		func(ctx context.Context, m usecases.Meta, d packageEtaData) error {
			return c.ApplyPackageEta(ctx, m, usecases.PackageEtaWindowAssigned{
				TrackingNumber: d.TrackingNumber, AssignmentID: d.AssignmentID, OrderID: d.OrderID,
				EtaStart: d.EtaStart, EtaEnd: d.EtaEnd, AssignedAt: d.AssignedAt,
			})
		})
}
