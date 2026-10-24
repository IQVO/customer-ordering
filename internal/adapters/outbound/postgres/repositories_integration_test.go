//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/kafka"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/postgres"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/repocontract"
	"github.com/claudioed/customer-ordering/internal/testing/pgtest"
)

func TestPostgresHonoursTheRepositoryContract(t *testing.T) {
	repocontract.Run(t, func(t *testing.T) repocontract.Env {
		pool := pgtest.NewPool(t)
		return repocontract.Env{
			Orders: postgres.NewOrderRepository(pool), Shoppers: postgres.NewShopperRepository(pool), Tracking: postgres.NewTrackingRepository(pool),
			Inbox: postgres.NewInbox(pool), Outbox: postgres.NewOutboxRepo(pool, kafka.NewEncoder()), UoW: postgres.NewUnitOfWork(pool),
			Idempotency: postgres.NewIdempotencyStore(pool),
			// The Postgres store judges TTL with the database's now(); the
			// contract stamps records with this wall clock, which on this
			// machine is the same second the container's clock reads.
			Clock: wallClock{},
			OutboxCount: func(t *testing.T) int {
				var n int
				if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events`).Scan(&n); err != nil {
					t.Fatal(err)
				}
				return n
			},
		}
	})
}

// wallClock is the real time, for the Postgres side of the repository
// contract: created_at and now() live in the same database.
type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now().UTC() }
