package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/customer-ordering/internal/application/compensation"
	"github.com/claudioed/customer-ordering/internal/application/ports"
)

// CompensationRepo is the durable retry queue of promise cancels a failed
// checkout could not perform (A14): Enqueue is the write side
// (ports.CompensationQueue), Drain is the retry worker's read side. A cancel
// is one row, released when the retry succeeded (or was refused -- nothing is
// left to release); a failed retry stops the pass and is retried later.
type CompensationRepo struct{ pool *pgxpool.Pool }

var _ ports.CompensationQueue = (*CompensationRepo)(nil)

// NewCompensationRepo constructs a CompensationRepo over pool.
func NewCompensationRepo(pool *pgxpool.Pool) *CompensationRepo { return &CompensationRepo{pool: pool} }

// Enqueue records the cancels as pending rows. An already-queued cancel -- a
// replayed checkout enqueues it again -- is left as it is, so the retry count
// is not reset and no duplicate row is created.
func (r *CompensationRepo) Enqueue(ctx context.Context, cancels ...compensation.Cancel) error {
	if len(cancels) == 0 {
		return nil
	}
	return inTx(ctx, r.pool, func(q querier) error {
		for _, c := range cancels {
			if _, err := q.Exec(ctx, `
				INSERT INTO compensation_cancels (order_id, group_no, promise_id)
				VALUES ($1, $2, $3)
				ON CONFLICT DO NOTHING`,
				c.OrderID, c.GroupNo, c.PromiseID); err != nil {
				return fmt.Errorf("enqueue compensation cancel %s of %s/%d: %w", c.PromiseID, c.OrderID, c.GroupNo, err)
			}
		}
		return nil
	})
}

type pendingCancel struct {
	id     int64
	cancel compensation.Cancel
}

// Drain is the retry worker's single pass: it claims up to limit unreleased
// rows under a row lock (FOR UPDATE SKIP LOCKED, so two overlapping workers
// never claim the same row) and retries them ONE AT A TIME in id order. A
// retry that returns nil or ports.ErrPromiseNotCancellable releases its row
// (a refused cancel has nothing left to release); the first other error stops
// the pass, is recorded on the row and is returned as a
// *compensation.RetryFailureError. It returns how many rows were released.
//
// With maxAttempts > 0 a row whose attempts already reached the cap is not
// claimed: it stays in compensation_cancels with attempts and last_error for
// inspection. 0 claims every unreleased row.
func (r *CompensationRepo) Drain(ctx context.Context, limit, maxAttempts int, retry func(context.Context, compensation.Cancel) error) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin compensation pass: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	batch, err := claimCancels(ctx, tx, limit, maxAttempts)
	if err != nil {
		return 0, err
	}
	released := 0
	for _, row := range batch {
		err := retry(ctx, row.cancel)
		if errors.Is(err, ports.ErrPromiseNotCancellable) {
			err = nil // a refused cancel is a closed promise: nothing to release
		}
		if err != nil {
			return released, recordCancelFailure(ctx, tx, row, err)
		}
		if _, err := tx.Exec(ctx, `UPDATE compensation_cancels SET released_at = now(), attempts = attempts + 1, last_error = NULL WHERE id = $1`, row.id); err != nil {
			return released, fmt.Errorf("mark compensation row %d released: %w", row.id, err)
		}
		released++
	}
	if err := tx.Commit(ctx); err != nil {
		return released, fmt.Errorf("commit compensation pass: %w", err)
	}
	return released, nil
}

// recordCancelFailure stores the retry error on the row, commits the pass
// (keeping the rows already released) and returns the failure with the row's
// new attempt count. The UPDATE/COMMIT use a ctx that survives cancellation
// so a shutdown mid-pass still persists the rows already released.
func recordCancelFailure(ctx context.Context, tx pgx.Tx, row pendingCancel, retryErr error) error {
	dctx := context.WithoutCancel(ctx)
	failure := &compensation.RetryFailureError{Cancel: row.cancel, Err: retryErr}
	if uerr := tx.QueryRow(dctx, `UPDATE compensation_cancels SET attempts = attempts + 1, last_error = $2 WHERE id = $1 RETURNING attempts`,
		row.id, retryErr.Error()).Scan(&failure.Attempts); uerr != nil {
		failure.Err = errors.Join(failure.Err, fmt.Errorf("record compensation failure: %w", uerr))
	}
	if cerr := tx.Commit(dctx); cerr != nil {
		failure.Err = errors.Join(failure.Err, fmt.Errorf("commit compensation pass: %w", cerr))
	}
	return failure
}

func claimCancels(ctx context.Context, tx pgx.Tx, limit, maxAttempts int) ([]pendingCancel, error) {
	var capFilter string
	args := []any{limit}
	if maxAttempts > 0 {
		capFilter = "AND attempts < $2"
		args = append(args, maxAttempts)
	}
	rows, err := tx.Query(ctx, `
		SELECT id, order_id, group_no, promise_id
		FROM compensation_cancels
		WHERE released_at IS NULL `+capFilter+`
		ORDER BY id
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, args...)
	if err != nil {
		return nil, fmt.Errorf("claim compensation rows: %w", err)
	}
	defer rows.Close()
	var batch []pendingCancel
	for rows.Next() {
		var p pendingCancel
		if err := rows.Scan(&p.id, &p.cancel.OrderID, &p.cancel.GroupNo, &p.cancel.PromiseID); err != nil {
			return nil, fmt.Errorf("scan compensation row: %w", err)
		}
		batch = append(batch, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read compensation rows: %w", err)
	}
	return batch, nil
}
