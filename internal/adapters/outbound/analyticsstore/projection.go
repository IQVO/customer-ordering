package analyticsstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/customer-ordering/internal/analytics/report"
)

// Projection is the Postgres WRITER (report.Projection). Apply claims the
// event id and projects the fact in ONE transaction: the id is recorded if and
// only if its effect is, so a failure anywhere leaves nothing behind and the
// redelivered event is applied afresh, while a replay of an applied id is a
// no-op.
type Projection struct {
	pool *pgxpool.Pool
}

// NewProjection constructs a Projection over the writer pool.
func NewProjection(pool *pgxpool.Pool) *Projection { return &Projection{pool: pool} }

var _ report.Projection = (*Projection)(nil)

const claimSQL = `
	INSERT INTO analytics_processed_events (event_id, event_type, occurred_at)
	VALUES ($1, $2, $3)
	ON CONFLICT (event_id) DO NOTHING`

const (
	// An order is placed once: a second placement of the same order id (never
	// raised by the aggregate) does not count twice.
	insertPlacedSQL = `
	INSERT INTO placed_orders (order_id, placed_at, group_count, consolidated_groups, pickup_point, event_id)
	VALUES ($1, $2, $3, $4, $5, $6)
	ON CONFLICT (order_id) DO NOTHING`

	insertCancellationSQL = `
	INSERT INTO order_cancellations (event_id, order_id, cancelled_at)
	VALUES ($1, $2, $3)`

	// A notice is keyed by its own dedupe key: the same notice under another
	// CloudEvents id does not count twice.
	insertNoticeSQL = `
	INSERT INTO shopper_notices (notice_key, order_id, family, decided_at, event_id)
	VALUES ($1, $2, $3, $4, $5)
	ON CONFLICT (notice_key) DO NOTHING`
)

// Apply implements report.Projection.
func (p *Projection) Apply(ctx context.Context, e report.Event) (bool, error) {
	if err := e.Validate(); err != nil {
		return false, fmt.Errorf("%w: %w", report.ErrRejected, err)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("analyticsstore: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	tag, err := tx.Exec(ctx, claimSQL, e.EventID, string(e.Kind), e.At)
	if err != nil {
		return false, classify(fmt.Errorf("analyticsstore: claim event %s: %w", e.EventID, err))
	}
	if tag.RowsAffected() == 0 {
		return false, nil // already applied: the deferred rollback ends the empty tx
	}
	if err := project(ctx, tx, e); err != nil {
		return false, classify(fmt.Errorf("analyticsstore: project %s %s: %w", e.Kind, e.EventID, err))
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("analyticsstore: commit: %w", err)
	}
	return true, nil
}

// errUnknownKind is deterministic: no retry can make the store accept it.
var errUnknownKind = errors.New("unknown event kind")

func project(ctx context.Context, tx pgx.Tx, e report.Event) error {
	var err error
	switch e.Kind {
	case report.KindOrderPlaced:
		_, err = tx.Exec(ctx, insertPlacedSQL, e.OrderID, e.OccurredAt, e.GroupCount, e.ConsolidatedGroups, e.PickupPoint, e.EventID)
	case report.KindOrderCancelled:
		_, err = tx.Exec(ctx, insertCancellationSQL, e.EventID, e.OrderID, e.OccurredAt)
	case report.KindNoticeDecided:
		_, err = tx.Exec(ctx, insertNoticeSQL, e.NoticeKey, e.OrderID, report.NormalizeFamily(e.Family), e.OccurredAt, e.EventID)
	default:
		err = fmt.Errorf("%w %q", errUnknownKind, e.Kind)
	}
	return err
}

// classify wraps a Postgres data-exception (SQLSTATE class 22) or
// integrity-violation (class 23) error, and an unknown event kind, in
// report.ErrRejected: the same event can never succeed, so the consumer
// dead-letters it instead of retrying forever. Everything else (connection
// loss, timeouts, locks, failovers, a raised exception) stays transient.
func classify(err error) error {
	var pgErr *pgconn.PgError
	if errors.Is(err, errUnknownKind) || (errors.As(err, &pgErr) && deterministicClass(pgErr.Code)) {
		return fmt.Errorf("%w: %w", report.ErrRejected, err)
	}
	return err
}

// deterministicClass reports whether an SQLSTATE is in class 22 or 23.
func deterministicClass(code string) bool {
	return len(code) == 5 && (code[:2] == "22" || code[:2] == "23")
}
