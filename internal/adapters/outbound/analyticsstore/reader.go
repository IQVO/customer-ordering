package analyticsstore

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/customer-ordering/internal/analytics/report"
)

// Reader is the Postgres READER (report.Reader). Every query filters the
// business instant with `>= from AND < to`, and days are UTC calendar days.
// SQL only counts; the split threshold comes from internal/analytics/report
// and every derived figure (shares, the bounded kind breakdown) is computed
// there.
type Reader struct {
	pool *pgxpool.Pool
}

// NewReader constructs a Reader over the (read-only) reports pool.
func NewReader(pool *pgxpool.Pool) *Reader { return &Reader{pool: pool} }

var _ report.Reader = (*Reader)(nil)

// placedSQL: per UTC day of placedAt, the orders, those with a CONSOLIDATED
// group, those to a pickup point and those split over >= $3 groups.
//
//	$1 from, $2 to, $3 the split threshold
const placedSQL = `
	SELECT (placed_at AT TIME ZONE 'UTC')::date AS day,
	       count(*),
	       count(*) FILTER (WHERE consolidated_groups > 0),
	       count(*) FILTER (WHERE pickup_point),
	       count(*) FILTER (WHERE group_count >= $3)
	FROM placed_orders
	WHERE placed_at >= $1 AND placed_at < $2
	GROUP BY day
	ORDER BY day`

// PlacedOrders implements report.Reader.
func (r *Reader) PlacedOrders(ctx context.Context, rg report.Range) ([]report.PlacedDay, error) {
	return collect(ctx, r.pool, placedSQL, func(rows pgx.Rows) (report.PlacedDay, error) {
		var d report.PlacedDay
		err := rows.Scan(&d.Day, &d.Orders, &d.Consolidated, &d.Pickup, &d.Split)
		d.Day = d.Day.UTC()
		return d, err
	}, rg.From, rg.To, report.SplitMinGroups())
}

// cancelledSQL: per UTC day of cancelledAt, the DISTINCT orders with a
// cancellation request.
//
//	$1 from, $2 to
const cancelledSQL = `
	SELECT (cancelled_at AT TIME ZONE 'UTC')::date AS day, count(DISTINCT order_id)
	FROM order_cancellations
	WHERE cancelled_at >= $1 AND cancelled_at < $2
	GROUP BY day
	ORDER BY day`

// CancelledOrders implements report.Reader.
func (r *Reader) CancelledOrders(ctx context.Context, rg report.Range) ([]report.CancelledDay, error) {
	return collect(ctx, r.pool, cancelledSQL, func(rows pgx.Rows) (report.CancelledDay, error) {
		var d report.CancelledDay
		err := rows.Scan(&d.Day, &d.Orders)
		d.Day = d.Day.UTC()
		return d, err
	}, rg.From, rg.To)
}

// noticesSQL: per UTC day of decidedAt and per family, the notices decided.
//
//	$1 from, $2 to
const noticesSQL = `
	SELECT (decided_at AT TIME ZONE 'UTC')::date AS day, family, count(*)
	FROM shopper_notices
	WHERE decided_at >= $1 AND decided_at < $2
	GROUP BY day, family
	ORDER BY day, family`

// NoticesDecided implements report.Reader.
func (r *Reader) NoticesDecided(ctx context.Context, rg report.Range) ([]report.NoticeDay, error) {
	return collect(ctx, r.pool, noticesSQL, func(rows pgx.Rows) (report.NoticeDay, error) {
		var d report.NoticeDay
		err := rows.Scan(&d.Day, &d.Family, &d.Count)
		d.Day = d.Day.UTC()
		return d, err
	}, rg.From, rg.To)
}

// LastEventAt implements report.Reader: the newest CloudEvents time among the
// applied events, nil while none was applied.
func (r *Reader) LastEventAt(ctx context.Context) (*time.Time, error) {
	var at *time.Time
	if err := r.pool.QueryRow(ctx, `SELECT max(occurred_at) FROM analytics_processed_events`).Scan(&at); err != nil {
		return nil, fmt.Errorf("analyticsstore: last event: %w", err)
	}
	if at != nil {
		utc := at.UTC()
		at = &utc
	}
	return at, nil
}

// Ping checks the analytical database answers (the reports' /readyz).
func (r *Reader) Ping(ctx context.Context) error { return r.pool.Ping(ctx) }

// collect runs query with args and scans every row; the result is a non-nil
// slice even when empty.
func collect[T any](ctx context.Context, pool *pgxpool.Pool, query string, scan func(pgx.Rows) (T, error), args ...any) ([]T, error) {
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("analyticsstore: query: %w", err)
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("analyticsstore: scan: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("analyticsstore: rows: %w", err)
	}
	return out, nil
}
