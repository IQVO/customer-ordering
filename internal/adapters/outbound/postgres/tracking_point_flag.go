package postgres

import (
	"context"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// ApplyPointFlag implements ports.TrackingRepository: the delivery-point flag
// of a package (ADR 0015). The row is created when absent, identity is
// coalesced like in ApplyException, and the two flag columns are rewritten only
// when the event is not older than the stored point_flagged_at (newest wins).
func (r *TrackingRepository) ApplyPointFlag(ctx context.Context, f readmodel.PointFlag) error {
	flagged := shared.UTC(f.FlaggedAt)
	return r.exec(ctx, "apply point flag "+f.TrackingNumber, func(q querier) error {
		if _, err := q.Exec(ctx, `
			INSERT INTO tracking_packages (tracking_number, assignment_id, order_id, point_flag_reason, point_flagged_at)
			VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), $4, $5)
			ON CONFLICT (tracking_number) DO UPDATE SET
				assignment_id = coalesce(tracking_packages.assignment_id, NULLIF($2, '')),
				order_id = coalesce(tracking_packages.order_id, NULLIF($3, '')),
				point_flag_reason = CASE WHEN tracking_packages.point_flagged_at IS NULL OR $5 >= tracking_packages.point_flagged_at THEN $4 ELSE tracking_packages.point_flag_reason END,
				point_flagged_at = greatest(tracking_packages.point_flagged_at, $5)`,
			f.TrackingNumber, f.AssignmentID, f.OrderID, f.Reason, flagged); err != nil {
			return err
		}
		return linkRows(ctx, q, f.TrackingNumber)
	})
}
