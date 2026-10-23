package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/postgres/pgtx"
	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
)

// pgUniqueViolation is the SQLSTATE of a unique constraint violation.
const pgUniqueViolation = "23505"

func isUniqueViolation(err error) (constraint string, ok bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		return pgErr.ConstraintName, true
	}
	return "", false
}

// shipToJSON is the stored form of the ship-to value object (synthetic PII).
type shipToJSON struct {
	Name        string `json:"name"`
	Line1       string `json:"line1"`
	Line2       string `json:"line2,omitempty"`
	City        string `json:"city"`
	Region      string `json:"region"`
	PostalCode  string `json:"postalCode"`
	CountryCode string `json:"countryCode"`
	Phone       string `json:"phone,omitempty"`
}

func toShipToJSON(s shipto.ShipTo) shipToJSON { return shipToJSON(s) }

func (j shipToJSON) value() shipto.ShipTo { return shipto.ShipTo(j) }

// nullTime stores the zero time as NULL.
func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// timeOrZero reads a nullable timestamp as UTC (zero when NULL).
func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

// OrderRepository is the Postgres ports.OrderRepository.
type OrderRepository struct{ pool *pgxpool.Pool }

var _ ports.OrderRepository = (*OrderRepository)(nil)

// NewOrderRepository constructs an OrderRepository over pool.
func NewOrderRepository(pool *pgxpool.Pool) *OrderRepository { return &OrderRepository{pool: pool} }

// Get implements ports.OrderRepository. Inside a unit of work the order row is
// locked (FOR UPDATE) so concurrent writers of one order serialise; outside, a
// read-only REPEATABLE READ transaction gives a consistent snapshot of the
// order, its lines and its groups.
func (r *OrderRepository) Get(ctx context.Context, id customerorder.ID) (*customerorder.CustomerOrder, error) {
	if tx, ok := pgtx.From(ctx); ok {
		return loadOrder(ctx, tx, id, " FOR UPDATE")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin read of order %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	return loadOrder(ctx, tx, id, "")
}

func loadOrder(ctx context.Context, q querier, id customerorder.ID, lock string) (*customerorder.CustomerOrder, error) {
	var (
		snap      customerorder.Snapshot
		shipJSON  []byte
		cancelled *time.Time
		status    string
	)
	err := q.QueryRow(ctx, `
		SELECT order_id, shopper_id, ship_to, destination_postal_prefix, status, placed_at, cancelled_at, version
		FROM customer_orders WHERE order_id = $1`+lock, string(id)).
		Scan(&snap.ID, &snap.ShopperID, &shipJSON, &snap.DestinationPostalPrefix, &status, &snap.PlacedAt, &cancelled, &snap.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("order %s: %w", id, ports.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read order %s: %w", id, err)
	}
	var sj shipToJSON
	if err := json.Unmarshal(shipJSON, &sj); err != nil {
		return nil, fmt.Errorf("decode ship-to of order %s: %w", id, err)
	}
	snap.ShipTo, snap.Status, snap.CancelledAt = sj.value(), customerorder.Status(status), timeOrZero(cancelled)
	if snap.Lines, err = loadLines(ctx, q, id); err != nil {
		return nil, err
	}
	if snap.Groups, err = loadGroups(ctx, q, id); err != nil {
		return nil, err
	}
	return customerorder.Restore(snap)
}

func loadLines(ctx context.Context, q querier, id customerorder.ID) ([]customerorder.OrderLine, error) {
	rows, err := q.Query(ctx, `SELECT line_no, catalog_item_id, quantity FROM order_lines WHERE order_id = $1 ORDER BY line_no`, string(id))
	if err != nil {
		return nil, fmt.Errorf("read lines of order %s: %w", id, err)
	}
	defer rows.Close()
	var out []customerorder.OrderLine
	for rows.Next() {
		var l customerorder.OrderLine
		if err := rows.Scan(&l.LineNo, &l.CatalogItemID, &l.Quantity); err != nil {
			return nil, fmt.Errorf("scan line of order %s: %w", id, err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

const groupColumns = `group_no, promise_id, tier, witness_node_id, ship_by, deliver_by_earliest, deliver_by_latest, line_nos, status, revision_no,
	coalesce(assignment_id, ''), coalesce(node_id, ''), locked_at, shipped_at, tracking_numbers, delivered_tracking_numbers, delivered_at,
	coalesce(cancel_reason, ''), cancel_requested_at, cancelled_at, coalesce(cancel_refused_reason, '')`

func loadGroups(ctx context.Context, q querier, id customerorder.ID) ([]customerorder.GroupPromise, error) {
	rows, err := q.Query(ctx, `SELECT `+groupColumns+` FROM order_groups WHERE order_id = $1 ORDER BY group_no`, string(id))
	if err != nil {
		return nil, fmt.Errorf("read groups of order %s: %w", id, err)
	}
	defer rows.Close()
	var out []customerorder.GroupPromise
	for rows.Next() {
		var (
			g                                              customerorder.GroupPromise
			tier, status                                   string
			locked, shipped, delivered, requested, cancels *time.Time
		)
		if err := rows.Scan(&g.GroupNo, &g.PromiseID, &tier, &g.WitnessNodeID, &g.ShipBy, &g.DeliverByEarliest, &g.DeliverByLatest, &g.LineNos,
			&status, &g.RevisionNo, &g.AssignmentID, &g.NodeID, &locked, &shipped, &g.TrackingNumbers, &g.DeliveredTrackingNumbers, &delivered,
			&g.CancelReason, &requested, &cancels, &g.CancelRefusedReason); err != nil {
			return nil, fmt.Errorf("scan group of order %s: %w", id, err)
		}
		g.Tier, g.Status = customerorder.Tier(tier), customerorder.GroupStatus(status)
		g.LockedAt, g.ShippedAt, g.DeliveredAt = timeOrZero(locked), timeOrZero(shipped), timeOrZero(delivered)
		g.CancelRequestedAt, g.CancelledAt = timeOrZero(requested), timeOrZero(cancels)
		out = append(out, g)
	}
	return out, rows.Err()
}

// Save implements ports.OrderRepository: the order is saved under the rule
// "stored version == o.LoadedVersion()" and a create expects the id to be new.
func (r *OrderRepository) Save(ctx context.Context, o *customerorder.CustomerOrder) error {
	snap := o.Snapshot()
	err := inTx(ctx, r.pool, func(q querier) error {
		if o.LoadedVersion() == 0 {
			if err := insertOrder(ctx, q, snap); err != nil {
				return err
			}
		} else if err := updateOrder(ctx, q, snap, o.LoadedVersion()); err != nil {
			return err
		}
		for _, g := range snap.Groups {
			if err := upsertGroup(ctx, q, string(snap.ID), g); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	o.MarkPersisted()
	return nil
}

func insertOrder(ctx context.Context, q querier, s customerorder.Snapshot) error {
	ship, err := json.Marshal(toShipToJSON(s.ShipTo))
	if err != nil {
		return fmt.Errorf("encode ship-to: %w", err)
	}
	_, err = q.Exec(ctx, `
		INSERT INTO customer_orders (order_id, shopper_id, ship_to, destination_postal_prefix, status, placed_at, cancelled_at, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		string(s.ID), s.ShopperID, string(ship), s.DestinationPostalPrefix, string(s.Status), s.PlacedAt.UTC(), nullTime(s.CancelledAt), s.Version)
	if _, dup := isUniqueViolation(err); dup {
		return fmt.Errorf("order %s already stored: %w", s.ID, ports.ErrConcurrentUpdate)
	}
	if err != nil {
		return fmt.Errorf("insert order %s: %w", s.ID, err)
	}
	for _, l := range s.Lines {
		if _, err := q.Exec(ctx, `INSERT INTO order_lines (order_id, line_no, catalog_item_id, quantity) VALUES ($1, $2, $3, $4)`,
			string(s.ID), l.LineNo, l.CatalogItemID, l.Quantity); err != nil {
			return fmt.Errorf("insert line %d of order %s: %w", l.LineNo, s.ID, err)
		}
	}
	return nil
}

func updateOrder(ctx context.Context, q querier, s customerorder.Snapshot, loaded int64) error {
	tag, err := q.Exec(ctx, `UPDATE customer_orders SET status = $2, cancelled_at = $3, version = $4 WHERE order_id = $1 AND version = $5`,
		string(s.ID), string(s.Status), nullTime(s.CancelledAt), s.Version, loaded)
	if err != nil {
		return fmt.Errorf("update order %s: %w", s.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("order %s loaded at version %d: %w", s.ID, loaded, ports.ErrConcurrentUpdate)
	}
	return nil
}

func upsertGroup(ctx context.Context, q querier, orderID string, g customerorder.GroupPromise) error {
	_, err := q.Exec(ctx, `
		INSERT INTO order_groups (order_id, group_no, promise_id, tier, witness_node_id, ship_by, deliver_by_earliest, deliver_by_latest, line_nos,
			status, revision_no, assignment_id, node_id, locked_at, shipped_at, tracking_numbers, delivered_tracking_numbers, delivered_at,
			cancel_reason, cancel_requested_at, cancelled_at, cancel_refused_reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NULLIF($12, ''), NULLIF($13, ''), $14, $15, $16, $17, $18, NULLIF($19, ''), $20, $21, NULLIF($22, ''))
		ON CONFLICT (order_id, group_no) DO UPDATE SET
			promise_id = EXCLUDED.promise_id, witness_node_id = EXCLUDED.witness_node_id, ship_by = EXCLUDED.ship_by,
			deliver_by_earliest = EXCLUDED.deliver_by_earliest, deliver_by_latest = EXCLUDED.deliver_by_latest, status = EXCLUDED.status,
			revision_no = EXCLUDED.revision_no, assignment_id = EXCLUDED.assignment_id, node_id = EXCLUDED.node_id,
			locked_at = EXCLUDED.locked_at, shipped_at = EXCLUDED.shipped_at, tracking_numbers = EXCLUDED.tracking_numbers,
			delivered_tracking_numbers = EXCLUDED.delivered_tracking_numbers, delivered_at = EXCLUDED.delivered_at,
			cancel_reason = EXCLUDED.cancel_reason, cancel_requested_at = EXCLUDED.cancel_requested_at,
			cancelled_at = EXCLUDED.cancelled_at, cancel_refused_reason = EXCLUDED.cancel_refused_reason`,
		orderID, g.GroupNo, g.PromiseID, string(g.Tier), g.WitnessNodeID, g.ShipBy.UTC(), g.DeliverByEarliest.UTC(), g.DeliverByLatest.UTC(), g.LineNos,
		string(g.Status), g.RevisionNo, g.AssignmentID, g.NodeID, nullTime(g.LockedAt), nullTime(g.ShippedAt), g.TrackingNumbers, g.DeliveredTrackingNumbers,
		nullTime(g.DeliveredAt), g.CancelReason, nullTime(g.CancelRequestedAt), nullTime(g.CancelledAt), g.CancelRefusedReason)
	if err != nil {
		return fmt.Errorf("save group %d of order %s: %w", g.GroupNo, orderID, err)
	}
	return nil
}
