package memory

import (
	"context"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// ApplyPointFlag implements ports.TrackingRepository: the twin of the Postgres
// upsert. The flag is stored only when the event is not older than the stored
// one (newest wins, an equal instant rewrites).
func (r *TrackingRepository) ApplyPointFlag(ctx context.Context, f readmodel.PointFlag) error {
	defer r.s.enter(ctx)()
	p := r.row(f.TrackingNumber)
	flagged := shared.UTC(f.FlaggedAt)
	if p.PointFlaggedAt.IsZero() || !flagged.Before(p.PointFlaggedAt) {
		p.PointFlagReason, p.PointFlaggedAt = f.Reason, flagged
	}
	r.s.packages[f.TrackingNumber] = r.identify(p, f.AssignmentID, f.OrderID)
	r.linkGroup(f.TrackingNumber)
	return nil
}
