package memory

import (
	"context"
	"slices"
	"sort"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
)

// The group set of a tracking number (ADR 0012), the twin of the Postgres table
// tracking_package_groups: rows are only ever added. The package row keeps
// "the first group that named the package" in OrderID/GroupNo.

func (s *Store) groupRefCount() int {
	n := 0
	for _, refs := range s.pkgGroups {
		n += len(refs)
	}
	return n
}

// addGroupRef adds ref to the set of tn. A stored slice is replaced, never
// edited in place, so capture() can share it.
func (s *Store) addGroupRef(tn string, ref readmodel.GroupRef) {
	refs := s.pkgGroups[tn]
	if slices.Contains(refs, ref) {
		return
	}
	s.pkgGroups[tn] = append(slices.Clone(refs), ref)
}

func (s *Store) inGroup(tn, orderID string, groupNo int) bool {
	return slices.Contains(s.pkgGroups[tn], readmodel.GroupRef{OrderID: orderID, GroupNo: groupNo})
}

// linkGroup adds the group of the package's assignment link, when it has one
// (the twin of linkGroupRow).
func (r *TrackingRepository) linkGroup(tn string) {
	p := r.s.packages[tn]
	if l, ok := r.s.links[p.AssignmentID]; ok && p.AssignmentID != "" {
		r.s.addGroupRef(tn, readmodel.GroupRef{OrderID: l.OrderID, GroupNo: l.GroupNo})
	}
}

// AttachTrackingNumbers implements ports.TrackingRepository: the group joins
// the set of every tracking number; the row is created when missing and only
// learns its first group while it has none.
func (r *TrackingRepository) AttachTrackingNumbers(ctx context.Context, orderID string, groupNo int, tns []string) error {
	defer r.s.enter(ctx)()
	for _, tn := range tns {
		p := r.row(tn)
		if p.GroupNo == 0 {
			p.OrderID, p.GroupNo = orderID, groupNo
		}
		r.s.packages[tn] = p
		r.s.addGroupRef(tn, readmodel.GroupRef{OrderID: orderID, GroupNo: groupNo})
	}
	return nil
}

// GroupsOfTracking implements ports.TrackingRepository.
func (r *TrackingRepository) GroupsOfTracking(ctx context.Context, trackingNumber string) ([]readmodel.GroupRef, error) {
	defer r.s.enter(ctx)()
	out := slices.Clone(r.s.pkgGroups[trackingNumber])
	if out == nil {
		out = []readmodel.GroupRef{}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OrderID != out[j].OrderID {
			return out[i].OrderID < out[j].OrderID
		}
		return out[i].GroupNo < out[j].GroupNo
	})
	return out, nil
}

// packagesOf returns the package rows of the group, through the group sets.
func (r *TrackingRepository) packagesOf(orderID string, groupNo int) []readmodel.Package {
	var out []readmodel.Package
	for tn, p := range r.s.packages {
		if r.s.inGroup(tn, orderID, groupNo) {
			out = append(out, p)
		}
	}
	return out
}

// OutcomePackages implements ports.TrackingRepository.
func (r *TrackingRepository) OutcomePackages(ctx context.Context, orderID string, groupNo int) ([]readmodel.Outcome, error) {
	defer r.s.enter(ctx)()
	out := []readmodel.Outcome{}
	for _, p := range r.packagesOf(orderID, groupNo) {
		if p.Outcome != "" {
			out = append(out, readmodel.Outcome{TrackingNumber: p.TrackingNumber, OrderID: orderID, Kind: p.Outcome, At: p.OutcomeAt})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		return out[i].TrackingNumber < out[j].TrackingNumber
	})
	return out, nil
}

// DeliveredPackages implements ports.TrackingRepository.
func (r *TrackingRepository) DeliveredPackages(ctx context.Context, orderID string, groupNo int) ([]readmodel.Delivery, error) {
	defer r.s.enter(ctx)()
	out := []readmodel.Delivery{}
	for _, p := range r.packagesOf(orderID, groupNo) {
		if !p.DeliveredAt.IsZero() {
			out = append(out, readmodel.Delivery{TrackingNumber: p.TrackingNumber, AssignmentID: p.AssignmentID, OrderID: orderID, At: p.DeliveredAt})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TrackingNumber < out[j].TrackingNumber })
	return out, nil
}

// Packages implements ports.TrackingRepository: one row per group of the
// package's set that belongs to the order, then the packages that only name the
// order (no group of it known yet) under their stored group, 0 when unknown.
func (r *TrackingRepository) Packages(ctx context.Context, orderID string) ([]readmodel.Package, error) {
	defer r.s.enter(ctx)()
	out := []readmodel.Package{}
	for tn, p := range r.s.packages {
		listed := false
		for _, ref := range r.s.pkgGroups[tn] {
			if ref.OrderID == orderID {
				q := p
				q.OrderID, q.GroupNo = orderID, ref.GroupNo
				out = append(out, q)
				listed = true
			}
		}
		if !listed && p.OrderID == orderID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].GroupNo != out[j].GroupNo {
			return out[i].GroupNo < out[j].GroupNo
		}
		return out[i].TrackingNumber < out[j].TrackingNumber
	})
	return out, nil
}
