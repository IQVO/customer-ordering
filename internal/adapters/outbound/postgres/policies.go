package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/domain/notice"
)

// PolicyRepository is the Postgres ports.NotificationPolicyRepository (table
// notification_policies, one jsonb snapshot per version, ADR 0017).
type PolicyRepository struct{ pool *pgxpool.Pool }

// NewPolicyRepository constructs the repository over pool.
func NewPolicyRepository(pool *pgxpool.Pool) *PolicyRepository { return &PolicyRepository{pool: pool} }

var _ ports.NotificationPolicyRepository = (*PolicyRepository)(nil)

// policyRow is one row of the stored snapshot (private: the domain structs
// carry no tags).
type policyRow struct {
	Family   string `json:"family"`
	Notifier string `json:"notifier"`
}

// policyJSON is the stored snapshot.
type policyJSON struct {
	Version     int         `json:"version"`
	Rows        []policyRow `json:"rows"`
	PublishedAt time.Time   `json:"publishedAt"`
}

// Current returns the highest version or ports.ErrNotFound.
func (r *PolicyRepository) Current(ctx context.Context) (*notice.Policy, error) {
	var raw []byte
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT snapshot FROM notification_policies ORDER BY version DESC LIMIT 1`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read notification policy: %w", err)
	}
	var j policyJSON
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("decode notification policy: %w", err)
	}
	rows := make([]notice.Row, len(j.Rows))
	for i, row := range j.Rows {
		rows[i] = notice.Row{Family: notice.Family(row.Family), Notifier: notice.Notifier(row.Notifier)}
	}
	return notice.Restore(notice.Snapshot{Version: j.Version, Rows: rows, PublishedAt: j.PublishedAt.UTC()})
}

// Save stores a new version; an existing version is ports.ErrConcurrentUpdate.
func (r *PolicyRepository) Save(ctx context.Context, p *notice.Policy) error {
	s := p.Snapshot()
	rows := make([]policyRow, len(s.Rows))
	for i, row := range s.Rows {
		rows[i] = policyRow{Family: string(row.Family), Notifier: string(row.Notifier)}
	}
	raw, err := json.Marshal(policyJSON{Version: s.Version, Rows: rows, PublishedAt: s.PublishedAt.UTC()})
	if err != nil {
		return fmt.Errorf("encode notification policy: %w", err)
	}
	// ON CONFLICT DO NOTHING, not a unique violation: a lost race raises no
	// server error, so it leaves the enclosing transaction (the idempotency
	// middleware's) usable and the use case's retry can read the winner.
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `INSERT INTO notification_policies (version, snapshot) VALUES ($1, $2) ON CONFLICT (version) DO NOTHING`, s.Version, raw)
	if err != nil {
		return fmt.Errorf("save notification policy %d: %w", s.Version, err)
	}
	if tag.RowsAffected() == 0 {
		return ports.ErrConcurrentUpdate
	}
	return nil
}
