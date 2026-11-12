package kafka

import (
	"context"
	"fmt"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/usecases"
)

// packagePointFlagData restates the v1 `data` of PackageDeliveryPointFlagged
// (ADR 0015); unknown fields are ignored.
type packagePointFlagData struct {
	TrackingNumber string    `json:"trackingNumber"`
	AssignmentID   string    `json:"assignmentId"`
	OrderID        string    `json:"orderId"`
	Reason         string    `json:"reason"`
	FlaggedAt      time.Time `json:"flaggedAt"`
}

// validPointFlag refuses a payload that can never be applied: a blank tracking
// number, or a reason outside the two the producer publishes (the storage CHECK
// allows exactly those two). The reason is carried to the storefront verbatim;
// it is a code, never prose.
func validPointFlag(d packagePointFlagData) error {
	if err := need("PackageDeliveryPointFlagged",
		"trackingNumber", d.TrackingNumber,
		"reason", d.Reason); err != nil {
		return err
	}
	if d.Reason != "NOT_HOME" && d.Reason != "ACCESS_PROBLEM" {
		return fmt.Errorf("%w: PackageDeliveryPointFlagged reason must be NOT_HOME or ACCESS_PROBLEM", errBadPayload)
	}
	return nil
}

// pointFlagHandler applies PackageDeliveryPointFlagged to the tracking read
// model: the advisory on the package of the flagged delivery point.
func pointFlagHandler(c usecases.Consumer) HandlerFunc {
	return handler("PackageDeliveryPointFlagged", validPointFlag,
		func(ctx context.Context, m usecases.Meta, d packagePointFlagData) error {
			return c.ApplyDeliveryPointFlag(ctx, m, usecases.PackageDeliveryPointFlagged{
				TrackingNumber: d.TrackingNumber, AssignmentID: d.AssignmentID, OrderID: d.OrderID,
				Reason: d.Reason, FlaggedAt: d.FlaggedAt,
			})
		})
}
