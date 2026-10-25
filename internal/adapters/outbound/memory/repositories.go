package memory

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/idempotency"
	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

var (
	_ ports.OrderRepository    = (*OrderRepository)(nil)
	_ ports.ShopperRepository  = (*ShopperRepository)(nil)
	_ ports.Inbox              = (*Inbox)(nil)
	_ ports.EventOutbox        = (*Outbox)(nil)
	_ ports.UnitOfWork         = (*UnitOfWork)(nil)
	_ ports.IdempotencyStore   = (*IdempotencyStore)(nil)
	_ ports.TrackingRepository = (*TrackingRepository)(nil)
)

// OrderRepository is the in-memory ports.OrderRepository.
type OrderRepository struct{ s *Store }

// Get implements ports.OrderRepository.
func (r *OrderRepository) Get(ctx context.Context, id customerorder.ID) (*customerorder.CustomerOrder, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.orders[id]
	if !ok {
		return nil, fmt.Errorf("order %s: %w", id, ports.ErrNotFound)
	}
	return customerorder.Restore(snap)
}

// Save implements ports.OrderRepository.
func (r *OrderRepository) Save(ctx context.Context, o *customerorder.CustomerOrder) error {
	defer r.s.enter(ctx)()
	stored, exists := r.s.orders[o.ID()]
	if o.LoadedVersion() == 0 && exists || o.LoadedVersion() > 0 && (!exists || stored.Version != o.LoadedVersion()) {
		return fmt.Errorf("order %s loaded at version %d: %w", o.ID(), o.LoadedVersion(), ports.ErrConcurrentUpdate)
	}
	r.s.orders[o.ID()] = o.Snapshot()
	o.MarkPersisted()
	return nil
}

// ListByShopper implements ports.OrderRepository: newest first (placedAt DESC,
// orderId ASC), strictly after the cursor position when it is set.
func (r *OrderRepository) ListByShopper(ctx context.Context, shopperID string, afterPlacedAt time.Time, afterOrderID string, limit int) ([]*customerorder.CustomerOrder, error) {
	defer r.s.enter(ctx)()
	var page []customerorder.Snapshot
	for _, snap := range r.s.orders {
		if snap.ShopperID != shopperID {
			continue
		}
		if !afterPlacedAt.IsZero() && !afterCursor(snap, afterPlacedAt, afterOrderID) {
			continue
		}
		page = append(page, snap)
	}
	sort.Slice(page, func(i, j int) bool {
		if page[i].PlacedAt.Equal(page[j].PlacedAt) {
			return page[i].ID < page[j].ID
		}
		return page[i].PlacedAt.After(page[j].PlacedAt)
	})
	if len(page) > limit {
		page = page[:limit]
	}
	out := make([]*customerorder.CustomerOrder, 0, len(page))
	for _, snap := range page {
		o, err := customerorder.Restore(snap)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

// afterCursor reports whether the snapshot sorts strictly after the cursor in
// the (placedAt DESC, orderId ASC) order.
func afterCursor(snap customerorder.Snapshot, at time.Time, id string) bool {
	if snap.PlacedAt.Equal(at) {
		return string(snap.ID) > id
	}
	return snap.PlacedAt.Before(at)
}

// ShopperRepository is the in-memory ports.ShopperRepository.
type ShopperRepository struct{ s *Store }

// Get implements ports.ShopperRepository.
func (r *ShopperRepository) Get(ctx context.Context, id shopper.ID) (*shopper.Shopper, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.shoppers[id]
	if !ok {
		return nil, fmt.Errorf("shopper %s: %w", id, ports.ErrNotFound)
	}
	return shopper.Restore(snap)
}

// FindByEmail implements ports.ShopperRepository: the e-mail arrives already
// normalised and hits the same index Save maintains.
func (r *ShopperRepository) FindByEmail(ctx context.Context, email string) (*shopper.Shopper, error) {
	defer r.s.enter(ctx)()
	id, ok := r.s.emails[email]
	if !ok {
		return nil, fmt.Errorf("shopper with email %q: %w", email, ports.ErrNotFound)
	}
	return shopper.Restore(r.s.shoppers[id])
}

// Save implements ports.ShopperRepository.
func (r *ShopperRepository) Save(ctx context.Context, sh *shopper.Shopper) error {
	defer r.s.enter(ctx)()
	stored, exists := r.s.shoppers[sh.ID()]
	if sh.Version() == 1 && exists || sh.Version() > 1 && (!exists || stored.Version != sh.Version()-1) {
		return fmt.Errorf("shopper %s at version %d: %w", sh.ID(), sh.Version(), ports.ErrConcurrentUpdate)
	}
	if owner, taken := r.s.emails[sh.Email()]; taken && owner != sh.ID() {
		return fmt.Errorf("%w: %s", shopper.ErrShopperExists, sh.Email())
	}
	r.s.shoppers[sh.ID()] = sh.Snapshot()
	r.s.emails[sh.Email()] = sh.ID()
	return nil
}

// Inbox is the in-memory ports.Inbox.
type Inbox struct{ s *Store }

// Claim implements ports.Inbox.
func (i *Inbox) Claim(ctx context.Context, eventID, eventType string) (bool, error) {
	defer i.s.enter(ctx)()
	if _, seen := i.s.inbox[eventID]; seen {
		return false, nil
	}
	i.s.inbox[eventID] = eventType
	return true, nil
}

// UnitOfWork is the in-memory ports.UnitOfWork: serialised, atomic, re-entrant.
type UnitOfWork struct{ s *Store }

// Do runs fn holding the store. When fn returns an error or panics the state
// taken before fn is restored; the error is returned unchanged. A Do inside a
// Do joins it.
func (u *UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if ctx.Value(txKey{u.s}) != nil {
		return fn(ctx)
	}
	u.s.mu.Lock()
	defer u.s.mu.Unlock()
	before := u.s.capture()
	committed := false
	defer func() {
		if !committed {
			u.s.restore(before)
		}
	}()
	err = fn(context.WithValue(ctx, txKey{u.s}, struct{}{}))
	committed = err == nil
	return err
}

// Outbox is the in-memory ports.EventOutbox.
type Outbox struct{ s *Store }

// Append stores the events in order.
func (o *Outbox) Append(ctx context.Context, events ...shared.Event) error {
	defer o.s.enter(ctx)()
	o.s.outbox = append(o.s.outbox, events...)
	return nil
}

// Events returns the appended events in order (a copy of the slice).
func (o *Outbox) Events() []shared.Event {
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
	return append([]shared.Event(nil), o.s.outbox...)
}

// IdempotencyStore is the in-memory ports.IdempotencyStore. Records older than
// idempotency.TTL are treated as absent.
type IdempotencyStore struct{ s *Store }

// Get implements ports.IdempotencyStore.
func (i *IdempotencyStore) Get(ctx context.Context, route, key string) (idempotency.Record, error) {
	defer i.s.enter(ctx)()
	rec, ok := i.s.idempotency[idemKey{route, key}]
	if !ok || i.s.now().Sub(rec.CreatedAt) > idempotency.TTL {
		return idempotency.Record{}, ports.ErrNotFound
	}
	return rec, nil
}

// Put implements ports.IdempotencyStore.
func (i *IdempotencyStore) Put(ctx context.Context, route, key string, rec idempotency.Record) (bool, error) {
	defer i.s.enter(ctx)()
	k := idemKey{route, key}
	if old, ok := i.s.idempotency[k]; ok && i.s.now().Sub(old.CreatedAt) <= idempotency.TTL {
		return false, nil
	}
	i.s.idempotency[k] = rec
	return true, nil
}
