package postgres

import (
	"context"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// ended states a deposit no longer overwrites (ADR 0016): a package that was
// already collected, delivered, lost or returned keeps its state; the event's
// instants are still recorded as facts.
const pickupEndedStates = "('COLLECTED', 'DELIVERED')"

// ApplyReadyForCollection implements ports.TrackingRepository: the deposit of
// a pickup package at its point (ADR 0016). The row is created when absent,
// identity is coalesced like in ApplyException, and the three columns of the
// deposit (point, readyForCollectionAt, holdUntil) are the event's facts.
func (r *TrackingRepository) ApplyReadyForCollection(ctx context.Context, e readmodel.ReadyForCollection) error {
	ready, hold := shared.UTC(e.ReadyAt), shared.UTC(e.HoldUntil)
	return r.exec(ctx, "apply ready for collection "+e.TrackingNumber, func(q querier) error {
		if _, err := q.Exec(ctx, `
			INSERT INTO tracking_packages (tracking_number, assignment_id, order_id, pickup_point_id, ready_for_collection_at, hold_until, state)
			VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), $4, $5, $6, 'READY_FOR_COLLECTION')
			ON CONFLICT (tracking_number) DO UPDATE SET
				assignment_id = coalesce(tracking_packages.assignment_id, NULLIF($2, '')),
				order_id = coalesce(tracking_packages.order_id, NULLIF($3, '')),
				pickup_point_id = coalesce(tracking_packages.pickup_point_id, $4),
				ready_for_collection_at = $5,
				hold_until = $6,
				state = CASE WHEN tracking_packages.state IN `+pickupEndedStates+` OR tracking_packages.outcome IS NOT NULL
					THEN tracking_packages.state ELSE 'READY_FOR_COLLECTION' END`,
			e.TrackingNumber, e.AssignmentID, e.OrderID, e.PickupPointID, ready, hold); err != nil {
			return err
		}
		return linkRows(ctx, q, e.TrackingNumber)
	})
}

// ApplyCollected implements ports.TrackingRepository: the collection of a
// pickup package (ADR 0016). The row is created when absent (the deposit may
// arrive later), identity is coalesced like in ApplyException, and collectedAt
// moves only forward; the proof code rides the newest collection.
func (r *TrackingRepository) ApplyCollected(ctx context.Context, e readmodel.Collected) error {
	collected := shared.UTC(e.CollectedAt)
	return r.exec(ctx, "apply collected "+e.TrackingNumber, func(q querier) error {
		if _, err := q.Exec(ctx, `
			INSERT INTO tracking_packages (tracking_number, assignment_id, order_id, pickup_point_id, collected_at, proof_code, state)
			VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), $4, $5, NULLIF($6, ''), 'COLLECTED')
			ON CONFLICT (tracking_number) DO UPDATE SET
				assignment_id = coalesce(tracking_packages.assignment_id, NULLIF($2, '')),
				order_id = coalesce(tracking_packages.order_id, NULLIF($3, '')),
				pickup_point_id = coalesce(tracking_packages.pickup_point_id, $4),
				collected_at = greatest(tracking_packages.collected_at, $5),
				proof_code = CASE WHEN tracking_packages.collected_at IS NULL OR $5 >= tracking_packages.collected_at
					THEN NULLIF($6, '') ELSE tracking_packages.proof_code END,
				state = CASE WHEN tracking_packages.outcome IS NOT NULL
					THEN tracking_packages.state ELSE 'COLLECTED' END`,
			e.TrackingNumber, e.AssignmentID, e.OrderID, e.PickupPointID, collected, e.ProofCode); err != nil {
			return err
		}
		return linkRows(ctx, q, e.TrackingNumber)
	})
}
