package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
)

const groupColumns = `group_no, promise_id, tier, witness_node_id, ship_by, deliver_by_earliest, deliver_by_latest, line_nos, status, revision_no,
	coalesce(assignment_id, ''), coalesce(node_id, ''), locked_at, shipped_at, tracking_numbers, delivered_tracking_numbers, delivered_at,
	coalesce(cancel_reason, ''), cancel_requested_at, cancelled_at, coalesce(cancel_refused_reason, ''),
	coalesce(outcome_tracking_number, ''), lost_at, returned_at, coalesce(time_zone, ''),
	coalesce(to_char(chosen_delivery_day, 'YYYY-MM-DD'), ''), consolidation_eligible`

func loadGroups(ctx context.Context, q querier, id customerorder.ID) ([]customerorder.GroupPromise, error) {
	rows, err := q.Query(ctx, `SELECT `+groupColumns+` FROM order_groups WHERE order_id = $1 ORDER BY group_no`, string(id))
	if err != nil {
		return nil, fmt.Errorf("read groups of order %s: %w", id, err)
	}
	defer rows.Close()
	var out []customerorder.GroupPromise
	for rows.Next() {
		var (
			g                                                              customerorder.GroupPromise
			tier, status                                                   string
			locked, shipped, delivered, requested, cancels, lost, returned *time.Time
		)
		if err := rows.Scan(&g.GroupNo, &g.PromiseID, &tier, &g.WitnessNodeID, &g.ShipBy, &g.DeliverByEarliest, &g.DeliverByLatest, &g.LineNos,
			&status, &g.RevisionNo, &g.AssignmentID, &g.NodeID, &locked, &shipped, &g.TrackingNumbers, &g.DeliveredTrackingNumbers, &delivered,
			&g.CancelReason, &requested, &cancels, &g.CancelRefusedReason, &g.OutcomeTrackingNumber, &lost, &returned, &g.TimeZone,
			&g.ChosenDeliveryDay, &g.ConsolidationEligible); err != nil {
			return nil, fmt.Errorf("scan group of order %s: %w", id, err)
		}
		g.Tier, g.Status = customerorder.Tier(tier), customerorder.GroupStatus(status)
		g.LockedAt, g.ShippedAt, g.DeliveredAt = timeOrZero(locked), timeOrZero(shipped), timeOrZero(delivered)
		g.CancelRequestedAt, g.CancelledAt = timeOrZero(requested), timeOrZero(cancels)
		g.LostAt, g.ReturnedAt = timeOrZero(lost), timeOrZero(returned)
		out = append(out, g)
	}
	return out, rows.Err()
}

func upsertGroup(ctx context.Context, q querier, orderID string, g customerorder.GroupPromise) error {
	_, err := q.Exec(ctx, `
		INSERT INTO order_groups (order_id, group_no, promise_id, tier, witness_node_id, ship_by, deliver_by_earliest, deliver_by_latest, line_nos,
			status, revision_no, assignment_id, node_id, locked_at, shipped_at, tracking_numbers, delivered_tracking_numbers, delivered_at,
			cancel_reason, cancel_requested_at, cancelled_at, cancel_refused_reason, outcome_tracking_number, lost_at, returned_at, time_zone,
			chosen_delivery_day, consolidation_eligible)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NULLIF($12, ''), NULLIF($13, ''), $14, $15, $16, $17, $18, NULLIF($19, ''), $20, $21, NULLIF($22, ''), NULLIF($23, ''), $24, $25, NULLIF($26, ''), NULLIF($27, '')::date, $28)
		ON CONFLICT (order_id, group_no) DO UPDATE SET
			promise_id = EXCLUDED.promise_id, witness_node_id = EXCLUDED.witness_node_id, ship_by = EXCLUDED.ship_by,
			deliver_by_earliest = EXCLUDED.deliver_by_earliest, deliver_by_latest = EXCLUDED.deliver_by_latest, status = EXCLUDED.status,
			revision_no = EXCLUDED.revision_no, assignment_id = EXCLUDED.assignment_id, node_id = EXCLUDED.node_id,
			locked_at = EXCLUDED.locked_at, shipped_at = EXCLUDED.shipped_at, tracking_numbers = EXCLUDED.tracking_numbers,
			delivered_tracking_numbers = EXCLUDED.delivered_tracking_numbers, delivered_at = EXCLUDED.delivered_at,
			cancel_reason = EXCLUDED.cancel_reason, cancel_requested_at = EXCLUDED.cancel_requested_at,
			cancelled_at = EXCLUDED.cancelled_at, cancel_refused_reason = EXCLUDED.cancel_refused_reason,
			outcome_tracking_number = EXCLUDED.outcome_tracking_number, lost_at = EXCLUDED.lost_at, returned_at = EXCLUDED.returned_at,
			time_zone = EXCLUDED.time_zone, chosen_delivery_day = EXCLUDED.chosen_delivery_day,
			consolidation_eligible = EXCLUDED.consolidation_eligible`,
		orderID, g.GroupNo, g.PromiseID, string(g.Tier), g.WitnessNodeID, g.ShipBy.UTC(), g.DeliverByEarliest.UTC(), g.DeliverByLatest.UTC(), g.LineNos,
		string(g.Status), g.RevisionNo, g.AssignmentID, g.NodeID, nullTime(g.LockedAt), nullTime(g.ShippedAt), g.TrackingNumbers, g.DeliveredTrackingNumbers,
		nullTime(g.DeliveredAt), g.CancelReason, nullTime(g.CancelRequestedAt), nullTime(g.CancelledAt), g.CancelRefusedReason,
		g.OutcomeTrackingNumber, nullTime(g.LostAt), nullTime(g.ReturnedAt), g.TimeZone, g.ChosenDeliveryDay, g.ConsolidationEligible)
	if err != nil {
		return fmt.Errorf("save group %d of order %s: %w", g.GroupNo, orderID, err)
	}
	return nil
}
