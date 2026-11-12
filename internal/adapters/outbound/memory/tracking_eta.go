package memory

import (
	"context"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// ApplyEta implements ports.TrackingRepository: the twin of the Postgres
// upsert. The window is stored only when the event is not older than the stored
// one (latest wins, an equal instant rewrites the same values).
func (r *TrackingRepository) ApplyEta(ctx context.Context, e readmodel.Eta) error {
	defer r.s.enter(ctx)()
	p := r.row(e.TrackingNumber)
	assigned := shared.UTC(e.AssignedAt)
	if p.EtaAssignedAt.IsZero() || !assigned.Before(p.EtaAssignedAt) {
		p.EtaStart, p.EtaEnd, p.EtaAssignedAt = shared.UTC(e.Start), shared.UTC(e.End), assigned
	}
	r.s.packages[e.TrackingNumber] = r.identify(p, e.AssignmentID, e.OrderID)
	r.linkGroup(e.TrackingNumber)
	return nil
}
