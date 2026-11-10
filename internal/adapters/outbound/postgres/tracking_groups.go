package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
)

// A tracking number belongs to a SET of groups (ADR 0012): the package facts
// stay one row per tracking number in tracking_packages, the groups that named
// the package live in tracking_package_groups, and rows are only ever added.
// tracking_packages.order_id/group_no keep "the first group that named the
// package" (and the order an event named): the fallback for a package whose
// group is not known yet, and what a rollback of migration 0007 leaves behind.

// linkGroupRow adds the group of the package's assignment link, when it has one.
const linkGroupRow = `
	INSERT INTO tracking_package_groups (tracking_number, order_id, group_no)
	SELECT p.tracking_number, l.order_id, l.group_no FROM tracking_packages p
	JOIN assignment_links l ON l.assignment_id = p.assignment_id
	WHERE p.tracking_number = $1
	ON CONFLICT DO NOTHING`

// linkRows fills a package row from its assignment link: the first-group
// columns while it does not know them yet, and the group set.
func linkRows(ctx context.Context, q querier, trackingNumber string) error {
	if _, err := q.Exec(ctx, linkRow, trackingNumber); err != nil {
		return err
	}
	_, err := q.Exec(ctx, linkGroupRow, trackingNumber)
	return err
}

// AttachTrackingNumbers implements ports.TrackingRepository: the group joins
// the set of every tracking number; the package row is created when missing and
// only learns its first group while it has none.
func (r *TrackingRepository) AttachTrackingNumbers(ctx context.Context, orderID string, groupNo int, tns []string) error {
	return r.exec(ctx, "attach tracking numbers", func(q querier) error {
		for _, tn := range tns {
			if _, err := q.Exec(ctx, `
				INSERT INTO tracking_packages (tracking_number, order_id, group_no) VALUES ($1, $2, $3)
				ON CONFLICT (tracking_number) DO UPDATE SET
					order_id = CASE WHEN tracking_packages.group_no IS NULL THEN EXCLUDED.order_id ELSE tracking_packages.order_id END,
					group_no = coalesce(tracking_packages.group_no, EXCLUDED.group_no)`, tn, orderID, groupNo); err != nil {
				return err
			}
			if _, err := q.Exec(ctx, `
				INSERT INTO tracking_package_groups (tracking_number, order_id, group_no) VALUES ($1, $2, $3)
				ON CONFLICT DO NOTHING`, tn, orderID, groupNo); err != nil {
				return err
			}
		}
		return nil
	})
}

// GroupsOfTracking implements ports.TrackingRepository.
func (r *TrackingRepository) GroupsOfTracking(ctx context.Context, trackingNumber string) ([]readmodel.GroupRef, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
		SELECT order_id, group_no FROM tracking_package_groups WHERE tracking_number = $1 ORDER BY order_id, group_no`, trackingNumber)
	if err != nil {
		return nil, fmt.Errorf("read groups of tracking number: %w", err)
	}
	defer rows.Close()
	out := []readmodel.GroupRef{}
	for rows.Next() {
		var g readmodel.GroupRef
		if err := rows.Scan(&g.OrderID, &g.GroupNo); err != nil {
			return nil, fmt.Errorf("scan group of tracking number: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// OutcomePackages implements ports.TrackingRepository.
func (r *TrackingRepository) OutcomePackages(ctx context.Context, orderID string, groupNo int) ([]readmodel.Outcome, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
		SELECT p.tracking_number, p.outcome, p.outcome_at FROM tracking_package_groups g
		JOIN tracking_packages p ON p.tracking_number = g.tracking_number
		WHERE g.order_id = $1 AND g.group_no = $2 AND p.outcome IS NOT NULL ORDER BY p.outcome_at, p.tracking_number`, orderID, groupNo)
	if err != nil {
		return nil, fmt.Errorf("read package outcomes: %w", err)
	}
	defer rows.Close()
	out := []readmodel.Outcome{}
	for rows.Next() {
		o := readmodel.Outcome{OrderID: orderID}
		var at time.Time
		if err := rows.Scan(&o.TrackingNumber, &o.Kind, &at); err != nil {
			return nil, fmt.Errorf("scan package outcome: %w", err)
		}
		o.At = at.UTC()
		out = append(out, o)
	}
	return out, rows.Err()
}

// DeliveredPackages implements ports.TrackingRepository.
func (r *TrackingRepository) DeliveredPackages(ctx context.Context, orderID string, groupNo int) ([]readmodel.Delivery, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
		SELECT p.tracking_number, coalesce(p.assignment_id, ''), p.delivered_at FROM tracking_package_groups g
		JOIN tracking_packages p ON p.tracking_number = g.tracking_number
		WHERE g.order_id = $1 AND g.group_no = $2 AND p.delivered_at IS NOT NULL ORDER BY p.tracking_number`, orderID, groupNo)
	if err != nil {
		return nil, fmt.Errorf("read delivered packages: %w", err)
	}
	defer rows.Close()
	out := []readmodel.Delivery{}
	for rows.Next() {
		d := readmodel.Delivery{OrderID: orderID}
		var at time.Time
		if err := rows.Scan(&d.TrackingNumber, &d.AssignmentID, &at); err != nil {
			return nil, fmt.Errorf("scan delivered package: %w", err)
		}
		d.At = at.UTC()
		out = append(out, d)
	}
	return out, rows.Err()
}

// packagesOfOrder lists the packages of one order: one row per group of the
// package's group set that belongs to the order, then, under group 0, the
// packages that only name the order (no group of this order is known yet).
const packagesOfOrder = `
	SELECT tracking_number, assignment_id, group_no, carrier, state, last_scan_node_id, last_scan_at, delivered_at, exception_reason, outcome, outcome_at FROM (
		SELECT p.tracking_number, coalesce(p.assignment_id, '') AS assignment_id, g.group_no, coalesce(p.carrier, '') AS carrier, coalesce(p.state, '') AS state,
			coalesce(p.last_scan_node_id, '') AS last_scan_node_id, p.last_scan_at, p.delivered_at, coalesce(p.exception_reason, '') AS exception_reason,
			coalesce(p.outcome, '') AS outcome, p.outcome_at
		FROM tracking_package_groups g JOIN tracking_packages p ON p.tracking_number = g.tracking_number
		WHERE g.order_id = $1
		UNION ALL
		SELECT p.tracking_number, coalesce(p.assignment_id, ''), coalesce(p.group_no, 0), coalesce(p.carrier, ''), coalesce(p.state, ''),
			coalesce(p.last_scan_node_id, ''), p.last_scan_at, p.delivered_at, coalesce(p.exception_reason, ''), coalesce(p.outcome, ''), p.outcome_at
		FROM tracking_packages p
		WHERE p.order_id = $1 AND NOT EXISTS (SELECT 1 FROM tracking_package_groups g WHERE g.tracking_number = p.tracking_number AND g.order_id = $1)
	) u ORDER BY group_no, tracking_number`

// Packages implements ports.TrackingRepository.
func (r *TrackingRepository) Packages(ctx context.Context, orderID string) ([]readmodel.Package, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, packagesOfOrder, orderID)
	if err != nil {
		return nil, fmt.Errorf("read packages: %w", err)
	}
	defer rows.Close()
	out := []readmodel.Package{}
	for rows.Next() {
		p := readmodel.Package{OrderID: orderID}
		var scan, delivered, outcomeAt *time.Time
		if err := rows.Scan(&p.TrackingNumber, &p.AssignmentID, &p.GroupNo, &p.Carrier, &p.State, &p.LastScanNodeID, &scan, &delivered, &p.ExceptionReason, &p.Outcome, &outcomeAt); err != nil {
			return nil, fmt.Errorf("scan package: %w", err)
		}
		p.LastScanAt, p.DeliveredAt, p.OutcomeAt = timeOrZero(scan), timeOrZero(delivered), timeOrZero(outcomeAt)
		out = append(out, p)
	}
	return out, rows.Err()
}
