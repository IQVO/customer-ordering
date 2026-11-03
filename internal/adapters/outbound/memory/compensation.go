package memory

import (
	"context"
	"sync"

	"github.com/claudioed/customer-ordering/internal/application/compensation"
	"github.com/claudioed/customer-ordering/internal/application/ports"
)

// Compensations is the in-memory ports.CompensationQueue: it records every
// enqueued cancel so tests can assert what a failed checkout left behind. Err,
// when set, is returned by Enqueue to exercise the queue-failure path.
type Compensations struct {
	mu       sync.Mutex
	enqueued []compensation.Cancel
	// Err makes Enqueue fail when set.
	Err error
}

var _ ports.CompensationQueue = (*Compensations)(nil)

// Enqueue records the cancels; a cancel already recorded -- a replayed
// checkout enqueues it again -- is not duplicated, mirroring the Postgres
// queue's ON CONFLICT DO NOTHING.
func (c *Compensations) Enqueue(_ context.Context, cancels ...compensation.Cancel) error {
	if c.Err != nil {
		return c.Err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, in := range cancels {
		duplicate := false
		for _, have := range c.enqueued {
			if in == have {
				duplicate = true
				break
			}
		}
		if !duplicate {
			c.enqueued = append(c.enqueued, in)
		}
	}
	return nil
}

// Enqueued returns every recorded cancel, in order.
func (c *Compensations) Enqueued() []compensation.Cancel {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]compensation.Cancel(nil), c.enqueued...)
}
