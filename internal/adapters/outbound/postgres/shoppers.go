package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

// shoppersEmailIndex is the unique index that raises ErrShopperExists.
const shoppersEmailIndex = "shoppers_email_uq"

// ShopperRepository is the Postgres ports.ShopperRepository.
type ShopperRepository struct{ pool *pgxpool.Pool }

var _ ports.ShopperRepository = (*ShopperRepository)(nil)

// NewShopperRepository constructs a ShopperRepository over pool.
func NewShopperRepository(pool *pgxpool.Pool) *ShopperRepository {
	return &ShopperRepository{pool: pool}
}

// Get implements ports.ShopperRepository.
func (r *ShopperRepository) Get(ctx context.Context, id shopper.ID) (*shopper.Shopper, error) {
	var (
		snap    shopper.Snapshot
		status  string
		saved   []byte
		address []shipToJSON
	)
	err := queryFor(ctx, r.pool).QueryRow(ctx, `
		SELECT shopper_id, email, status, saved_addresses, registered_at, version FROM shoppers WHERE shopper_id = $1`, string(id)).
		Scan(&snap.ID, &snap.Email, &status, &saved, &snap.RegisteredAt, &snap.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("shopper %s: %w", id, ports.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read shopper %s: %w", id, err)
	}
	if err := json.Unmarshal(saved, &address); err != nil {
		return nil, fmt.Errorf("decode addresses of shopper %s: %w", id, err)
	}
	snap.Status = shopper.Status(status)
	snap.SavedAddresses = make([]shipto.ShipTo, len(address))
	for i, a := range address {
		snap.SavedAddresses[i] = a.value()
	}
	return shopper.Restore(snap)
}

// Save implements ports.ShopperRepository: a create expects the id to be new, an
// update expects the stored version to be Version()-1. The unique e-mail index
// raises shopper.ErrShopperExists.
func (r *ShopperRepository) Save(ctx context.Context, s *shopper.Shopper) error {
	saved := make([]shipToJSON, 0, len(s.SavedAddresses()))
	for _, a := range s.SavedAddresses() {
		saved = append(saved, toShipToJSON(a))
	}
	raw, err := json.Marshal(saved)
	if err != nil {
		return fmt.Errorf("encode addresses of shopper %s: %w", s.ID(), err)
	}
	return inTx(ctx, r.pool, func(q querier) error {
		if s.Version() == 1 {
			return insertShopper(ctx, q, s, string(raw))
		}
		tag, err := q.Exec(ctx, `UPDATE shoppers SET email = $2, saved_addresses = $3, version = $4 WHERE shopper_id = $1 AND version = $5`,
			string(s.ID()), s.Email(), string(raw), s.Version(), s.Version()-1)
		if _, dup := isUniqueViolation(err); dup {
			return fmt.Errorf("%w: %s", shopper.ErrShopperExists, s.Email())
		}
		if err != nil {
			return fmt.Errorf("update shopper %s: %w", s.ID(), err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("shopper %s at version %d: %w", s.ID(), s.Version(), ports.ErrConcurrentUpdate)
		}
		return nil
	})
}

func insertShopper(ctx context.Context, q querier, s *shopper.Shopper, savedJSON string) error {
	_, err := q.Exec(ctx, `
		INSERT INTO shoppers (shopper_id, email, status, saved_addresses, registered_at, version) VALUES ($1, $2, $3, $4, $5, $6)`,
		string(s.ID()), s.Email(), string(s.Status()), savedJSON, s.RegisteredAt().UTC(), s.Version())
	if constraint, dup := isUniqueViolation(err); dup {
		if constraint == shoppersEmailIndex {
			return fmt.Errorf("%w: %s", shopper.ErrShopperExists, s.Email())
		}
		return fmt.Errorf("shopper %s already stored: %w", s.ID(), ports.ErrConcurrentUpdate)
	}
	if err != nil {
		return fmt.Errorf("insert shopper %s: %w", s.ID(), err)
	}
	return nil
}
