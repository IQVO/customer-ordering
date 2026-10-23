package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// TrackingRepository is the Postgres ports.TrackingRepository. Its semantics
// are the ones of the in-memory double; the repository contract suite runs
// against both.
type TrackingRepository struct{ pool *pgxpool.Pool }

var _ ports.TrackingRepository = (*TrackingRepository)(nil)

// NewTrackingRepository constructs a TrackingRepository over pool.
func NewTrackingRepository(pool *pgxpool.Pool) *TrackingRepository {
	return &TrackingRepository{pool: pool}
}

// linkRow fills the order and group of a package row from its assignment link
// when the row does not know them yet.
const linkRow = `
	UPDATE tracking_packages p SET order_id = l.order_id, group_no = l.group_no
	FROM assignment_links l
	WHERE p.tracking_number = $1 AND l.assignment_id = p.assignment_id AND (p.order_id IS NULL OR p.group_no IS NULL)`

func (r *TrackingRepository) exec(ctx context.Context, what string, fn func(q querier) error) error {
	if err := inTx(ctx, r.pool, fn); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// LinkAssignment implements ports.TrackingRepository.
func (r *TrackingRepository) LinkAssignment(ctx context.Context, l readmodel.AssignmentLink) error {
	return r.exec(ctx, "link assignment "+l.AssignmentID, func(q querier) error {
		if _, err := q.Exec(ctx, `
			INSERT INTO assignment_links (assignment_id, order_id, group_no, promise_id) VALUES ($1, $2, $3, $4)
			ON CONFLICT (assignment_id) DO UPDATE SET order_id = EXCLUDED.order_id, group_no = EXCLUDED.group_no, promise_id = EXCLUDED.promise_id`,
			l.AssignmentID, l.OrderID, l.GroupNo, l.PromiseID); err != nil {
			return err
		}
		_, err := q.Exec(ctx, `UPDATE tracking_packages SET order_id = $2, group_no = $3 WHERE assignment_id = $1`, l.AssignmentID, l.OrderID, l.GroupNo)
		return err
	})
}

// ResolveAssignment implements ports.TrackingRepository.
func (r *TrackingRepository) ResolveAssignment(ctx context.Context, assignmentID string) (readmodel.AssignmentLink, error) {
	l := readmodel.AssignmentLink{AssignmentID: assignmentID}
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT order_id, group_no, promise_id FROM assignment_links WHERE assignment_id = $1`, assignmentID).
		Scan(&l.OrderID, &l.GroupNo, &l.PromiseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return readmodel.AssignmentLink{}, fmt.Errorf("assignment %q: %w", assignmentID, ports.ErrNotFound)
	}
	if err != nil {
		return readmodel.AssignmentLink{}, fmt.Errorf("resolve assignment %q: %w", assignmentID, err)
	}
	return l, nil
}

// AttachTrackingNumbers implements ports.TrackingRepository.
func (r *TrackingRepository) AttachTrackingNumbers(ctx context.Context, orderID string, groupNo int, tns []string) error {
	return r.exec(ctx, "attach tracking numbers", func(q querier) error {
		for _, tn := range tns {
			if _, err := q.Exec(ctx, `
				INSERT INTO tracking_packages (tracking_number, order_id, group_no) VALUES ($1, $2, $3)
				ON CONFLICT (tracking_number) DO UPDATE SET order_id = EXCLUDED.order_id, group_no = EXCLUDED.group_no`, tn, orderID, groupNo); err != nil {
				return err
			}
		}
		return nil
	})
}

// ApplyLabel implements ports.TrackingRepository.
func (r *TrackingRepository) ApplyLabel(ctx context.Context, l readmodel.Label) error {
	return r.exec(ctx, "apply label "+l.TrackingNumber, func(q querier) error {
		if _, err := q.Exec(ctx, `
			INSERT INTO tracking_packages (tracking_number, assignment_id, carrier, state) VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), 'LABELLED')
			ON CONFLICT (tracking_number) DO UPDATE SET
				assignment_id = coalesce(NULLIF($2, ''), tracking_packages.assignment_id),
				carrier = coalesce(NULLIF($3, ''), tracking_packages.carrier),
				state = coalesce(tracking_packages.state, 'LABELLED')`, l.TrackingNumber, l.AssignmentID, l.Carrier); err != nil {
			return err
		}
		_, err := q.Exec(ctx, linkRow, l.TrackingNumber)
		return err
	})
}

// ApplyScan implements ports.TrackingRepository.
func (r *TrackingRepository) ApplyScan(ctx context.Context, s readmodel.Scan) error {
	at := shared.UTC(s.At)
	return r.exec(ctx, "apply scan "+s.TrackingNumber, func(q querier) error {
		if _, err := q.Exec(ctx, `
			INSERT INTO tracking_packages (tracking_number, last_scan_node_id, last_scan_at, state) VALUES ($1, NULLIF($2, ''), $3, NULLIF($4, ''))
			ON CONFLICT (tracking_number) DO UPDATE SET
				last_scan_node_id = CASE WHEN tracking_packages.last_scan_at IS NULL OR $3 >= tracking_packages.last_scan_at
					THEN NULLIF($2, '') ELSE tracking_packages.last_scan_node_id END,
				state = CASE WHEN tracking_packages.outcome IS NULL AND (tracking_packages.last_scan_at IS NULL OR $3 >= tracking_packages.last_scan_at)
					THEN coalesce(NULLIF($4, ''), tracking_packages.state) ELSE tracking_packages.state END,
				last_scan_at = greatest(tracking_packages.last_scan_at, $3)`, s.TrackingNumber, s.NodeID, at, s.State); err != nil {
			return err
		}
		_, err := q.Exec(ctx, linkRow, s.TrackingNumber)
		return err
	})
}

// ApplyDelivery implements ports.TrackingRepository.
func (r *TrackingRepository) ApplyDelivery(ctx context.Context, d readmodel.Delivery) error {
	at := shared.UTC(d.At)
	return r.exec(ctx, "apply delivery "+d.TrackingNumber, func(q querier) error {
		if _, err := q.Exec(ctx, `
			INSERT INTO tracking_packages (tracking_number, assignment_id, order_id, delivered_at, last_scan_at, state)
			VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), $4, $4, 'DELIVERED')
			ON CONFLICT (tracking_number) DO UPDATE SET
				assignment_id = coalesce(tracking_packages.assignment_id, NULLIF($2, '')),
				order_id = coalesce(tracking_packages.order_id, NULLIF($3, '')),
				delivered_at = greatest(tracking_packages.delivered_at, $4),
				last_scan_at = greatest(tracking_packages.last_scan_at, $4),
				state = 'DELIVERED'`, d.TrackingNumber, d.AssignmentID, d.OrderID, at); err != nil {
			return err
		}
		_, err := q.Exec(ctx, linkRow, d.TrackingNumber)
		return err
	})
}

// ApplyOutcome implements ports.TrackingRepository.
func (r *TrackingRepository) ApplyOutcome(ctx context.Context, o readmodel.Outcome) error {
	at := shared.UTC(o.At)
	return r.exec(ctx, "apply outcome "+o.TrackingNumber, func(q querier) error {
		_, err := q.Exec(ctx, `
			INSERT INTO tracking_packages (tracking_number, order_id, state, outcome, outcome_at, last_scan_at)
			VALUES ($1, NULLIF($2, ''), $3, $3, $4, $4)
			ON CONFLICT (tracking_number) DO UPDATE SET
				order_id = coalesce(tracking_packages.order_id, NULLIF($2, '')),
				state = CASE WHEN tracking_packages.state = 'DELIVERED' OR tracking_packages.outcome IS NOT NULL THEN tracking_packages.state ELSE $3 END,
				outcome = coalesce(tracking_packages.outcome, $3),
				outcome_at = coalesce(tracking_packages.outcome_at, $4),
				last_scan_at = greatest(tracking_packages.last_scan_at, $4)`, o.TrackingNumber, o.OrderID, o.Kind, at)
		return err
	})
}

// OutcomePackages implements ports.TrackingRepository.
func (r *TrackingRepository) OutcomePackages(ctx context.Context, orderID string, groupNo int) ([]readmodel.Outcome, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
		SELECT tracking_number, outcome, outcome_at FROM tracking_packages
		WHERE order_id = $1 AND group_no = $2 AND outcome IS NOT NULL ORDER BY outcome_at, tracking_number`, orderID, groupNo)
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

// ApplyException implements ports.TrackingRepository.
func (r *TrackingRepository) ApplyException(ctx context.Context, e readmodel.Exception) error {
	return r.exec(ctx, "apply exception "+e.TrackingNumber, func(q querier) error {
		if _, err := q.Exec(ctx, `
			INSERT INTO tracking_packages (tracking_number, assignment_id, order_id, exception_reason) VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), $4)
			ON CONFLICT (tracking_number) DO UPDATE SET
				assignment_id = coalesce(tracking_packages.assignment_id, NULLIF($2, '')),
				order_id = coalesce(tracking_packages.order_id, NULLIF($3, '')),
				exception_reason = $4`, e.TrackingNumber, e.AssignmentID, e.OrderID, e.Reason); err != nil {
			return err
		}
		_, err := q.Exec(ctx, linkRow, e.TrackingNumber)
		return err
	})
}

// ApplyVoid implements ports.TrackingRepository.
func (r *TrackingRepository) ApplyVoid(ctx context.Context, tn string) error {
	return r.exec(ctx, "apply void "+tn, func(q querier) error {
		_, err := q.Exec(ctx, `
			INSERT INTO tracking_packages (tracking_number, state) VALUES ($1, 'VOIDED')
			ON CONFLICT (tracking_number) DO UPDATE SET
				state = CASE WHEN tracking_packages.state IS NULL OR tracking_packages.state = 'LABELLED' THEN 'VOIDED' ELSE tracking_packages.state END`, tn)
		return err
	})
}

// DeliveredPackages implements ports.TrackingRepository.
func (r *TrackingRepository) DeliveredPackages(ctx context.Context, orderID string, groupNo int) ([]readmodel.Delivery, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
		SELECT tracking_number, coalesce(assignment_id, ''), delivered_at FROM tracking_packages
		WHERE order_id = $1 AND group_no = $2 AND delivered_at IS NOT NULL ORDER BY tracking_number`, orderID, groupNo)
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

// PatchGroupSignal implements ports.TrackingRepository.
func (r *TrackingRepository) PatchGroupSignal(ctx context.Context, p readmodel.GroupSignalPatch) error {
	return r.exec(ctx, "patch group signal", func(q querier) error {
		_, err := q.Exec(ctx, `
			INSERT INTO group_signals (order_id, group_no, at_risk_reason, unsourceable_reason, promise_verdict, late_by_minutes)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (order_id, group_no) DO UPDATE SET
				at_risk_reason = coalesce($3, group_signals.at_risk_reason),
				unsourceable_reason = coalesce($4, group_signals.unsourceable_reason),
				promise_verdict = coalesce($5, group_signals.promise_verdict),
				late_by_minutes = coalesce($6, group_signals.late_by_minutes)`,
			p.OrderID, p.GroupNo, p.AtRiskReason, p.UnsourceableReason, p.PromiseVerdict, p.LateByMinutes)
		return err
	})
}

// GroupSignals implements ports.TrackingRepository.
func (r *TrackingRepository) GroupSignals(ctx context.Context, orderID string) ([]readmodel.GroupSignal, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
		SELECT group_no, coalesce(at_risk_reason, ''), coalesce(unsourceable_reason, ''), coalesce(promise_verdict, ''), late_by_minutes
		FROM group_signals WHERE order_id = $1 ORDER BY group_no`, orderID)
	if err != nil {
		return nil, fmt.Errorf("read group signals: %w", err)
	}
	defer rows.Close()
	out := []readmodel.GroupSignal{}
	for rows.Next() {
		g := readmodel.GroupSignal{OrderID: orderID}
		if err := rows.Scan(&g.GroupNo, &g.AtRiskReason, &g.UnsourceableReason, &g.PromiseVerdict, &g.LateByMinutes); err != nil {
			return nil, fmt.Errorf("scan group signal: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// Packages implements ports.TrackingRepository.
func (r *TrackingRepository) Packages(ctx context.Context, orderID string) ([]readmodel.Package, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
		SELECT tracking_number, coalesce(assignment_id, ''), coalesce(group_no, 0), coalesce(carrier, ''), coalesce(state, ''),
			coalesce(last_scan_node_id, ''), last_scan_at, delivered_at, coalesce(exception_reason, ''), coalesce(outcome, ''), outcome_at
		FROM tracking_packages WHERE order_id = $1 ORDER BY coalesce(group_no, 0), tracking_number`, orderID)
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
