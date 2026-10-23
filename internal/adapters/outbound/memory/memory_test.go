package memory_test

import (
	"testing"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/memory"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/repocontract"
)

func TestMemoryDoublesHonourTheRepositoryContract(t *testing.T) {
	repocontract.Run(t, func(t *testing.T) repocontract.Env {
		s := memory.NewStore()
		return repocontract.Env{
			Orders: s.Orders(), Shoppers: s.Shoppers(), Tracking: s.Tracking(), Inbox: s.Inbox(), Outbox: s.Outbox(),
			UoW: s.UnitOfWork(), Idempotency: s.Idempotency(),
			OutboxCount: func(*testing.T) int { return len(s.Outbox().Events()) },
		}
	})
}
