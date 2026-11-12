package postgres

import (
	"context"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// ApplyEta implements ports.TrackingRepository: the window of a package (ADR
// 0014). The row is created when absent, identity is coalesced like in
// ApplyException, and the three eta columns are rewritten only when the event
// is not older than the stored assigned_at (latest wins).
func (r *TrackingRepository) ApplyEta(ctx context.Context, e readmodel.Eta) error {
	start, end, assigned := shared.UTC(e.Start), shared.UTC(e.End), shared.UTC(e.AssignedAt)
	return r.exec(ctx, "apply eta "+e.TrackingNumber, func(q querier) error {
		if _, err := q.Exec(ctx, `
			INSERT INTO tracking_packages (tracking_number, assignment_id, order_id, eta_start, eta_end, eta_assigned_at)
			VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), $4, $5, $6)
			ON CONFLICT (tracking_number) DO UPDATE SET
				assignment_id = coalesce(tracking_packages.assignment_id, NULLIF($2, '')),
				order_id = coalesce(tracking_packages.order_id, NULLIF($3, '')),
				eta_start = CASE WHEN tracking_packages.eta_assigned_at IS NULL OR $6 >= tracking_packages.eta_assigned_at THEN $4 ELSE tracking_packages.eta_start END,
				eta_end = CASE WHEN tracking_packages.eta_assigned_at IS NULL OR $6 >= tracking_packages.eta_assigned_at THEN $5 ELSE tracking_packages.eta_end END,
				eta_assigned_at = greatest(tracking_packages.eta_assigned_at, $6)`,
			e.TrackingNumber, e.AssignmentID, e.OrderID, start, end, assigned); err != nil {
			return err
		}
		return linkRows(ctx, q, e.TrackingNumber)
	})
}
