package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
)

// LabelIssued is the consumed shipping-and-tracking event of the same name.
type LabelIssued struct {
	TrackingNumber string
	AssignmentID   string
	Carrier        string
}

// ApplyLabelIssued creates or completes the package row.
func (c Consumer) ApplyLabelIssued(ctx context.Context, m Meta, in LabelIssued) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		return c.Tracking.ApplyLabel(ctx, readmodel.Label{
			TrackingNumber: in.TrackingNumber, AssignmentID: in.AssignmentID, Carrier: in.Carrier, At: c.when(m),
		})
	})
}

// LabelVoided is the consumed shipping-and-tracking event of the same name.
type LabelVoided struct{ TrackingNumber string }

// ApplyLabelVoided marks the package VOIDED (G18).
func (c Consumer) ApplyLabelVoided(ctx context.Context, m Meta, in LabelVoided) error {
	return c.handle(ctx, m, func(ctx context.Context) error { return c.Tracking.ApplyVoid(ctx, in.TrackingNumber) })
}

// PackageScanned is the consumed shipping-and-tracking event of the same name.
type PackageScanned struct {
	TrackingNumber string
	NodeID         string
	State          string
	At             time.Time
}

// ApplyPackageScanned records the scan unless a newer one is stored.
func (c Consumer) ApplyPackageScanned(ctx context.Context, m Meta, in PackageScanned) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		return c.Tracking.ApplyScan(ctx, readmodel.Scan{
			TrackingNumber: in.TrackingNumber, NodeID: in.NodeID, State: in.State, At: orElse(in.At, c.when(m)),
		})
	})
}

// PackageExceptionRaised is the consumed shipping-and-tracking event of the same name.
type PackageExceptionRaised struct {
	TrackingNumber string
	AssignmentID   string
	OrderID        string
	Reason         string
	At             time.Time
}

// ApplyPackageException records the exception reason.
func (c Consumer) ApplyPackageException(ctx context.Context, m Meta, in PackageExceptionRaised) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		return c.Tracking.ApplyException(ctx, readmodel.Exception{
			TrackingNumber: in.TrackingNumber, AssignmentID: in.AssignmentID, OrderID: in.OrderID,
			Reason: reasonOrDefault(in.Reason), At: orElse(in.At, c.when(m)),
		})
	})
}

// PackageDelivered is the consumed shipping-and-tracking event of the same name.
type PackageDelivered struct {
	TrackingNumber string
	AssignmentID   string
	OrderID        string
	DeliveredAt    time.Time
}

// ApplyPackageDelivered marks the package delivered and runs
// MarkGroupDelivered when the group is known. A delivery whose group is not
// known yet stays in the tracking rows and is replayed when the group's
// shipment arrives (A21).
func (c Consumer) ApplyPackageDelivered(ctx context.Context, m Meta, in PackageDelivered) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		at := orElse(in.DeliveredAt, c.when(m))
		if err := c.Tracking.ApplyDelivery(ctx, readmodel.Delivery{
			TrackingNumber: in.TrackingNumber, AssignmentID: in.AssignmentID, OrderID: in.OrderID, At: at,
		}); err != nil {
			return err
		}
		orderID, groupNo, err := c.locate(ctx, in)
		if err != nil || orderID == "" {
			return err
		}
		return c.mutate(ctx, orderID, quiet(func(o *customerorder.CustomerOrder) error {
			n := groupNo
			if n == 0 {
				var ok bool
				if n, ok = groupOf(o, in); !ok {
					return nil
				}
			}
			return o.MarkGroupDelivered(n, in.TrackingNumber, at)
		}))
	})
}

// locate resolves the order and, when the assignment link is known, the group.
func (c Consumer) locate(ctx context.Context, in PackageDelivered) (string, int, error) {
	orderID := in.OrderID
	link, err := c.Tracking.ResolveAssignment(ctx, in.AssignmentID)
	switch {
	case err == nil:
		if orderID == "" {
			orderID = link.OrderID
		}
		return orderID, link.GroupNo, nil
	case errors.Is(err, ports.ErrNotFound):
		return orderID, 0, nil
	}
	return "", 0, err
}

func groupOf(o *customerorder.CustomerOrder, in PackageDelivered) (int, bool) {
	if n, ok := o.GroupNoByTrackingNumber(in.TrackingNumber); ok {
		return n, true
	}
	return o.GroupNoByAssignment(in.AssignmentID)
}
