package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/customer-ordering/internal/application/ports"
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
	q := queryFor(ctx, r.pool)
	snap, err := readShopper(ctx, q, `SELECT shopper_id, email, status, registered_at, version FROM shoppers WHERE shopper_id = $1`, string(id))
	if err != nil {
		return nil, err
	}
	return hydrateShopper(ctx, q, snap, fmt.Sprintf("shopper %s", id))
}

// FindByEmail implements ports.ShopperRepository: the e-mail arrives already
// normalised (A16) and hits the same lower-cased index as the unique check.
func (r *ShopperRepository) FindByEmail(ctx context.Context, email string) (*shopper.Shopper, error) {
	q := queryFor(ctx, r.pool)
	snap, err := readShopper(ctx, q, `SELECT shopper_id, email, status, registered_at, version FROM shoppers WHERE lower(email) = lower($1)`, email)
	if err != nil {
		return nil, err
	}
	return hydrateShopper(ctx, q, snap, fmt.Sprintf("shopper with email %q", email))
}

// readShopper scans the shoppers row of the query, mapping a miss to
// ports.ErrNotFound.
func readShopper(ctx context.Context, q querier, sql, arg string) (shopper.Snapshot, error) {
	var (
		snap   shopper.Snapshot
		status string
	)
	err := q.QueryRow(ctx, sql, arg).Scan(&snap.ID, &snap.Email, &status, &snap.RegisteredAt, &snap.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return shopper.Snapshot{}, fmt.Errorf("shopper of %s: %w", arg, ports.ErrNotFound)
	}
	if err != nil {
		return shopper.Snapshot{}, fmt.Errorf("read shopper of %s: %w", arg, err)
	}
	snap.Status = shopper.Status(status)
	return snap, nil
}

// hydrateShopper loads the saved addresses of the row and restores the
// aggregate.
func hydrateShopper(ctx context.Context, q querier, snap shopper.Snapshot, what string) (*shopper.Shopper, error) {
	rows, err := q.Query(ctx, `
		SELECT address_id, name, line1, COALESCE(line2, ''), city, region, postal_code, country_code, COALESCE(phone, '')
		FROM shopper_addresses WHERE shopper_id = $1 ORDER BY position`, string(snap.ID))
	if err != nil {
		return nil, fmt.Errorf("read addresses of %s: %w", what, err)
	}
	defer rows.Close()
	for rows.Next() {
		var a shopper.SavedAddress
		if err := rows.Scan(&a.ID, &a.ShipTo.Name, &a.ShipTo.Line1, &a.ShipTo.Line2, &a.ShipTo.City,
			&a.ShipTo.Region, &a.ShipTo.PostalCode, &a.ShipTo.CountryCode, &a.ShipTo.Phone); err != nil {
			return nil, fmt.Errorf("scan address of %s: %w", what, err)
		}
		snap.SavedAddresses = append(snap.SavedAddresses, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read addresses of %s: %w", what, err)
	}
	return shopper.Restore(snap)
}

// Save implements ports.ShopperRepository: a create expects the id to be new,
// an update expects the stored version to be Version()-1, and the saved
// addresses are replaced in the same transaction. The unique e-mail index
// raises shopper.ErrShopperExists.
func (r *ShopperRepository) Save(ctx context.Context, s *shopper.Shopper) error {
	return inTx(ctx, r.pool, func(q querier) error {
		if s.Version() == 1 {
			if err := insertShopper(ctx, q, s); err != nil {
				return err
			}
		} else if err := updateShopper(ctx, q, s); err != nil {
			return err
		}
		return replaceAddresses(ctx, q, s)
	})
}

// updateShopper bumps the row under the optimistic concurrency rule.
func updateShopper(ctx context.Context, q querier, s *shopper.Shopper) error {
	tag, err := q.Exec(ctx, `UPDATE shoppers SET email = $2, version = $3 WHERE shopper_id = $1 AND version = $4`,
		string(s.ID()), s.Email(), s.Version(), s.Version()-1)
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
}

// replaceAddresses rewrites the shopper_addresses rows of the shopper in the
// caller's transaction; the aggregate owns the list, so a plain replace under
// the row's version guard is enough.
func replaceAddresses(ctx context.Context, q querier, s *shopper.Shopper) error {
	if _, err := q.Exec(ctx, `DELETE FROM shopper_addresses WHERE shopper_id = $1`, string(s.ID())); err != nil {
		return fmt.Errorf("clear addresses of shopper %s: %w", s.ID(), err)
	}
	for i, a := range s.SavedAddresses() {
		_, err := q.Exec(ctx, `
			INSERT INTO shopper_addresses (shopper_id, address_id, position, name, line1, line2, city, region, postal_code, country_code, phone)
			VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9, $10, NULLIF($11, ''))`,
			string(s.ID()), string(a.ID), i+1, a.ShipTo.Name, a.ShipTo.Line1, a.ShipTo.Line2,
			a.ShipTo.City, a.ShipTo.Region, a.ShipTo.PostalCode, a.ShipTo.CountryCode, a.ShipTo.Phone)
		if err != nil {
			return fmt.Errorf("save address %s of shopper %s: %w", a.ID, s.ID(), err)
		}
	}
	return nil
}

func insertShopper(ctx context.Context, q querier, s *shopper.Shopper) error {
	_, err := q.Exec(ctx, `
		INSERT INTO shoppers (shopper_id, email, status, registered_at, version) VALUES ($1, $2, $3, $4, $5)`,
		string(s.ID()), s.Email(), string(s.Status()), s.RegisteredAt().UTC(), s.Version())
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
