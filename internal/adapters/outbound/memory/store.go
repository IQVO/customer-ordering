// Package memory holds in-memory TEST DOUBLES of the application's out ports.
// They are for unit, BDD and contract tests only: cmd/** never imports this
// package (enforced by internal/architecture) because the service runs on real
// Postgres and Kafka.
//
// Every repository round-trips its aggregate through Snapshot and Restore, so
// a loaded aggregate never aliases stored state, a corrupt row can not become a
// live aggregate and the doubles keep the persistence contract of the real
// adapters: a miss is ports.ErrNotFound, a stale or duplicate save is
// ports.ErrConcurrentUpdate, the unique e-mail index raises
// shopper.ErrShopperExists.
//
// One Store backs every double. Its UnitOfWork is atomic: it holds the store
// for the whole unit and restores the state taken at the start when the unit
// fails or panics, outbox, inbox and read models included.
package memory

import (
	"context"
	"sync"

	"github.com/claudioed/customer-ordering/internal/application/idempotency"
	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

type signalKey struct {
	orderID string
	groupNo int
}

type idemKey struct{ route, key string }

// Store is the shared state of the in-memory doubles.
type Store struct {
	mu          sync.Mutex
	orders      map[customerorder.ID]customerorder.Snapshot
	shoppers    map[shopper.ID]shopper.Snapshot
	emails      map[string]shopper.ID
	links       map[string]readmodel.AssignmentLink
	packages    map[string]readmodel.Package
	signals     map[signalKey]readmodel.GroupSignal
	inbox       map[string]string
	outbox      []shared.Event
	idempotency map[idemKey]idempotency.Record
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{
		orders: map[customerorder.ID]customerorder.Snapshot{}, shoppers: map[shopper.ID]shopper.Snapshot{}, emails: map[string]shopper.ID{},
		links: map[string]readmodel.AssignmentLink{}, packages: map[string]readmodel.Package{},
		signals: map[signalKey]readmodel.GroupSignal{}, inbox: map[string]string{}, idempotency: map[idemKey]idempotency.Record{},
	}
}

// state is a copy of everything a unit of work can change. Stored values are
// replaced, never edited in place, so copying the maps is enough.
type state struct {
	orders   map[customerorder.ID]customerorder.Snapshot
	shoppers map[shopper.ID]shopper.Snapshot
	emails   map[string]shopper.ID
	links    map[string]readmodel.AssignmentLink
	packages map[string]readmodel.Package
	signals  map[signalKey]readmodel.GroupSignal
	inbox    map[string]string
	outbox   []shared.Event
}

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (s *Store) capture() state {
	return state{
		orders: cloneMap(s.orders), shoppers: cloneMap(s.shoppers), emails: cloneMap(s.emails), links: cloneMap(s.links),
		packages: cloneMap(s.packages), signals: cloneMap(s.signals), inbox: cloneMap(s.inbox), outbox: append([]shared.Event(nil), s.outbox...),
	}
}

func (s *Store) restore(st state) {
	s.orders, s.shoppers, s.emails, s.links, s.packages, s.signals, s.inbox, s.outbox =
		st.orders, st.shoppers, st.emails, st.links, st.packages, st.signals, st.inbox, st.outbox
}

// txKey marks a ctx that carries the open unit of work of one store.
type txKey struct{ store *Store }

// enter locks the store for one repository call, unless ctx already carries the
// unit of work that holds it. The returned func releases the lock.
func (s *Store) enter(ctx context.Context) func() {
	if ctx.Value(txKey{s}) != nil {
		return func() {}
	}
	s.mu.Lock()
	return s.mu.Unlock
}

// Orders returns the OrderRepository double.
func (s *Store) Orders() *OrderRepository { return &OrderRepository{s} }

// Shoppers returns the ShopperRepository double.
func (s *Store) Shoppers() *ShopperRepository { return &ShopperRepository{s} }

// Tracking returns the TrackingRepository double.
func (s *Store) Tracking() *TrackingRepository { return &TrackingRepository{s} }

// Inbox returns the Inbox double.
func (s *Store) Inbox() *Inbox { return &Inbox{s} }

// Outbox returns the EventOutbox double.
func (s *Store) Outbox() *Outbox { return &Outbox{s} }

// UnitOfWork returns the UnitOfWork double.
func (s *Store) UnitOfWork() *UnitOfWork { return &UnitOfWork{s} }

// Idempotency returns the IdempotencyStore double.
func (s *Store) Idempotency() *IdempotencyStore { return &IdempotencyStore{s} }

// Counts is how many rows each table of the store holds.
type Counts struct {
	Orders, Shoppers, Links, Packages, Signals, Inbox, OutboxEvents int
	// VersionSum is the sum of every aggregate version: any saved change moves it.
	VersionSum int64
}

// Counts returns the size of every table and the sum of the aggregate
// versions, so a test can prove that a failed use case stored and changed
// nothing.
func (s *Store) Counts() Counts {
	s.mu.Lock()
	defer s.mu.Unlock()
	var versions int64
	for _, o := range s.orders {
		versions += o.Version
	}
	for _, sh := range s.shoppers {
		versions += sh.Version
	}
	return Counts{
		Orders: len(s.orders), Shoppers: len(s.shoppers), Links: len(s.links), Packages: len(s.packages),
		Signals: len(s.signals), Inbox: len(s.inbox), OutboxEvents: len(s.outbox), VersionSum: versions,
	}
}
