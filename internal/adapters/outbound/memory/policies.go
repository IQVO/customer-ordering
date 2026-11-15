package memory

import (
	"context"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/domain/notice"
)

// PolicyRepository is the in-memory ports.NotificationPolicyRepository.
type PolicyRepository struct{ s *Store }

var _ ports.NotificationPolicyRepository = (*PolicyRepository)(nil)

// Current returns the highest version or ports.ErrNotFound.
func (r *PolicyRepository) Current(ctx context.Context) (*notice.Policy, error) {
	defer r.s.enter(ctx)()
	best := 0
	for v := range r.s.policies {
		best = max(best, v)
	}
	if best == 0 {
		return nil, ports.ErrNotFound
	}
	return notice.Restore(r.s.policies[best])
}

// Save stores a new version; an existing version is ports.ErrConcurrentUpdate.
func (r *PolicyRepository) Save(ctx context.Context, p *notice.Policy) error {
	defer r.s.enter(ctx)()
	snap := p.Snapshot()
	if _, exists := r.s.policies[snap.Version]; exists {
		return ports.ErrConcurrentUpdate
	}
	r.s.policies[snap.Version] = snap
	return nil
}

// Policies returns the notification-policy double.
func (s *Store) Policies() *PolicyRepository { return &PolicyRepository{s: s} }
