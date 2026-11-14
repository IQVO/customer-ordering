package usecases

import (
	"context"
	"errors"
	"fmt"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/views"
	"github.com/claudioed/customer-ordering/internal/domain/notice"
)

// currentNoticePolicy returns the highest published notification policy, the
// default (version 0, every family NOBODY) when none was published or no
// repository is wired (ADR 0017).
func currentNoticePolicy(ctx context.Context, repo ports.NotificationPolicyRepository) (*notice.Policy, error) {
	if repo == nil {
		return notice.Default(), nil
	}
	p, err := repo.Current(ctx)
	switch {
	case err == nil:
		return p, nil
	case errors.Is(err, ports.ErrNotFound):
		return notice.Default(), nil
	default:
		return nil, fmt.Errorf("read notification policy: %w", err)
	}
}

// NotificationPolicies are the two use cases of /notification-policy (ADR 0017).
type NotificationPolicies struct{ Deps }

// Current returns the current policy: the published version with the highest
// number, or the defaults with version 0 when nothing was published.
func (uc NotificationPolicies) Current(ctx context.Context) (views.NotificationPolicy, error) {
	p, err := currentNoticePolicy(ctx, uc.Policies)
	if err != nil {
		return views.NotificationPolicy{}, err
	}
	return views.NotificationPolicyFrom(p), nil
}

// Publish validates the whole document and stores version current+1. No event
// is raised: v1 is analytics-free (ADR 0017). A concurrent publish that took
// the same number is retried once on fresh state.
func (uc NotificationPolicies) Publish(ctx context.Context, rows []notice.Row) (views.NotificationPolicy, error) {
	if uc.Policies == nil {
		return views.NotificationPolicy{}, errors.New("notification policy repository is not wired")
	}
	return transact(ctx, uc.UoW, commandAttempts, func(ctx context.Context) (views.NotificationPolicy, error) {
		cur, err := currentNoticePolicy(ctx, uc.Policies)
		if err != nil {
			return views.NotificationPolicy{}, err
		}
		p, err := notice.Publish(cur.Version(), rows, uc.now())
		if err != nil {
			return views.NotificationPolicy{}, err
		}
		if err := uc.Policies.Save(ctx, p); err != nil {
			return views.NotificationPolicy{}, err
		}
		return views.NotificationPolicyFrom(p), nil
	})
}
