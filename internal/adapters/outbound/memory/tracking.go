package memory

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// TrackingRepository is the in-memory ports.TrackingRepository.
type TrackingRepository struct{ s *Store }

// row returns the stored package row, or a fresh one for the tracking number.
func (r *TrackingRepository) row(tn string) readmodel.Package {
	if p, ok := r.s.packages[tn]; ok {
		return p
	}
	return readmodel.Package{TrackingNumber: tn}
}

// link fills the order and group of a row from the assignment link.
func (r *TrackingRepository) link(p readmodel.Package) readmodel.Package {
	if l, ok := r.s.links[p.AssignmentID]; ok && p.AssignmentID != "" {
		p.OrderID, p.GroupNo = l.OrderID, l.GroupNo
	}
	return p
}

// identify merges the order and assignment an event names into a row without
// overwriting what the row already knows, then applies the assignment link.
func (r *TrackingRepository) identify(p readmodel.Package, assignmentID, orderID string) readmodel.Package {
	if p.AssignmentID == "" {
		p.AssignmentID = assignmentID
	}
	if p.OrderID == "" {
		p.OrderID = orderID
	}
	if p.OrderID == "" || p.GroupNo == 0 {
		p = r.link(p)
	}
	return p
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// LinkAssignment implements ports.TrackingRepository.
func (r *TrackingRepository) LinkAssignment(ctx context.Context, l readmodel.AssignmentLink) error {
	defer r.s.enter(ctx)()
	r.s.links[l.AssignmentID] = l
	for tn, p := range r.s.packages {
		if p.AssignmentID == l.AssignmentID {
			p.OrderID, p.GroupNo = l.OrderID, l.GroupNo
			r.s.packages[tn] = p
			r.s.addGroupRef(tn, readmodel.GroupRef{OrderID: l.OrderID, GroupNo: l.GroupNo})
		}
	}
	return nil
}

// ResolveAssignment implements ports.TrackingRepository.
func (r *TrackingRepository) ResolveAssignment(ctx context.Context, assignmentID string) (readmodel.AssignmentLink, error) {
	defer r.s.enter(ctx)()
	l, ok := r.s.links[assignmentID]
	if !ok {
		return readmodel.AssignmentLink{}, fmt.Errorf("assignment %q: %w", assignmentID, ports.ErrNotFound)
	}
	return l, nil
}

// ApplyLabel implements ports.TrackingRepository.
func (r *TrackingRepository) ApplyLabel(ctx context.Context, l readmodel.Label) error {
	defer r.s.enter(ctx)()
	p := r.row(l.TrackingNumber)
	if l.AssignmentID != "" {
		p.AssignmentID = l.AssignmentID
	}
	if l.Carrier != "" {
		p.Carrier = l.Carrier
	}
	if p.State == "" {
		p.State = "LABELLED"
	}
	r.s.packages[l.TrackingNumber] = r.identify(p, "", "")
	r.linkGroup(l.TrackingNumber)
	return nil
}

// ApplyScan implements ports.TrackingRepository.
func (r *TrackingRepository) ApplyScan(ctx context.Context, s readmodel.Scan) error {
	defer r.s.enter(ctx)()
	p := r.row(s.TrackingNumber)
	if !s.At.Before(p.LastScanAt) {
		p.LastScanAt, p.LastScanNodeID = shared.UTC(s.At), s.NodeID
		if s.State != "" && p.Outcome == "" {
			p.State = s.State
		}
	}
	r.s.packages[s.TrackingNumber] = r.identify(p, "", "")
	r.linkGroup(s.TrackingNumber)
	return nil
}

// ApplyDelivery implements ports.TrackingRepository.
func (r *TrackingRepository) ApplyDelivery(ctx context.Context, d readmodel.Delivery) error {
	defer r.s.enter(ctx)()
	p := r.row(d.TrackingNumber)
	at := shared.UTC(d.At)
	p.DeliveredAt, p.LastScanAt, p.State = later(p.DeliveredAt, at), later(p.LastScanAt, at), "DELIVERED"
	r.s.packages[d.TrackingNumber] = r.identify(p, d.AssignmentID, d.OrderID)
	r.linkGroup(d.TrackingNumber)
	return nil
}

// ApplyOutcome implements ports.TrackingRepository.
func (r *TrackingRepository) ApplyOutcome(ctx context.Context, o readmodel.Outcome) error {
	defer r.s.enter(ctx)()
	p := r.row(o.TrackingNumber)
	at := shared.UTC(o.At)
	if p.Outcome == "" {
		if p.State != "DELIVERED" {
			p.State = o.Kind
		}
		p.Outcome, p.OutcomeAt = o.Kind, at
	}
	p.LastScanAt = later(p.LastScanAt, at)
	r.s.packages[o.TrackingNumber] = r.identify(p, "", o.OrderID)
	return nil
}

// ApplyException implements ports.TrackingRepository.
func (r *TrackingRepository) ApplyException(ctx context.Context, e readmodel.Exception) error {
	defer r.s.enter(ctx)()
	p := r.row(e.TrackingNumber)
	p.ExceptionReason = e.Reason
	r.s.packages[e.TrackingNumber] = r.identify(p, e.AssignmentID, e.OrderID)
	r.linkGroup(e.TrackingNumber)
	return nil
}

// ApplyVoid implements ports.TrackingRepository.
func (r *TrackingRepository) ApplyVoid(ctx context.Context, tn string) error {
	defer r.s.enter(ctx)()
	p := r.row(tn)
	if p.State == "" || p.State == "LABELLED" {
		p.State = "VOIDED"
	}
	r.s.packages[tn] = p
	return nil
}

// PatchGroupSignal implements ports.TrackingRepository.
func (r *TrackingRepository) PatchGroupSignal(ctx context.Context, p readmodel.GroupSignalPatch) error {
	defer r.s.enter(ctx)()
	k := signalKey{p.OrderID, p.GroupNo}
	g, ok := r.s.signals[k]
	if !ok {
		g = readmodel.GroupSignal{OrderID: p.OrderID, GroupNo: p.GroupNo}
	}
	if p.AtRiskReason != nil {
		g.AtRiskReason = *p.AtRiskReason
	}
	if p.UnsourceableReason != nil {
		g.UnsourceableReason = *p.UnsourceableReason
	}
	if p.PromiseVerdict != nil {
		g.PromiseVerdict = *p.PromiseVerdict
	}
	if p.LateByMinutes != nil {
		v := *p.LateByMinutes
		g.LateByMinutes = &v
	}
	r.s.signals[k] = g
	return nil
}

// GroupSignals implements ports.TrackingRepository.
func (r *TrackingRepository) GroupSignals(ctx context.Context, orderID string) ([]readmodel.GroupSignal, error) {
	defer r.s.enter(ctx)()
	out := []readmodel.GroupSignal{}
	for k, g := range r.s.signals {
		if k.orderID == orderID {
			if g.LateByMinutes != nil {
				v := *g.LateByMinutes
				g.LateByMinutes = &v
			}
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GroupNo < out[j].GroupNo })
	return out, nil
}
