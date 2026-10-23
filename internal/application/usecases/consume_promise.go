package usecases

import (
	"context"
	"fmt"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// groupOfPromise resolves the group that holds the promise.
func groupOfPromise(o *customerorder.CustomerOrder, promiseID string) (int, error) {
	n, ok := o.GroupNoByPromise(promiseID)
	if !ok {
		return 0, fmt.Errorf("%w: order %s has no group with promise %q", customerorder.ErrUnknownGroup, o.ID(), promiseID)
	}
	return n, nil
}

// signal loads the order, resolves the group of the promise and patches the
// group's signal row.
func (c Consumer) signal(ctx context.Context, orderID, promiseID string, fill func(p *readmodel.GroupSignalPatch)) error {
	o, err := c.Orders.Get(ctx, customerorder.ID(orderID))
	if err != nil {
		return err
	}
	n, err := groupOfPromise(o, promiseID)
	if err != nil {
		return err
	}
	p := readmodel.GroupSignalPatch{OrderID: orderID, GroupNo: n}
	fill(&p)
	return c.Tracking.PatchGroupSignal(ctx, p)
}

// PromiseAtRisk is the consumed delivery-promise event of the same name.
type PromiseAtRisk struct {
	PromiseID string
	OrderID   string
	Reason    string
}

// ApplyPromiseAtRisk records the reason in the tracking view.
func (c Consumer) ApplyPromiseAtRisk(ctx context.Context, m Meta, in PromiseAtRisk) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		reason := reasonOrDefault(in.Reason)
		return c.signal(ctx, in.OrderID, in.PromiseID, func(p *readmodel.GroupSignalPatch) { p.AtRiskReason = &reason })
	})
}

// PromiseRevised is the consumed delivery-promise event of the same name.
type PromiseRevised struct {
	PromiseID         string
	OrderID           string
	RevisionNo        int
	DeliverByEarliest time.Time
	DeliverByLatest   time.Time
	ShipBy            time.Time
	NodeID            string
}

// ApplyPromiseRevised runs UpdateGroupPromise.
func (c Consumer) ApplyPromiseRevised(ctx context.Context, m Meta, in PromiseRevised) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		return c.mutate(ctx, in.OrderID, quiet(func(o *customerorder.CustomerOrder) error {
			shipBy := in.ShipBy
			if shipBy.IsZero() { // the producer omitted it: keep the group's own
				if n, ok := o.GroupNoByPromise(in.PromiseID); ok {
					shipBy = groupShipBy(o, n)
				}
			}
			return o.UpdateGroupPromise(in.PromiseID, in.RevisionNo, in.DeliverByEarliest, in.DeliverByLatest, shipBy, in.NodeID)
		}))
	})
}

func groupShipBy(o *customerorder.CustomerOrder, groupNo int) time.Time {
	for _, g := range o.Groups() {
		if g.GroupNo == groupNo {
			return g.ShipBy
		}
	}
	return time.Time{}
}

// PromiseKept is the consumed delivery-promise event of the same name.
type PromiseKept struct {
	PromiseID string
	OrderID   string
}

// ApplyPromiseKept records the verdict in the tracking view.
func (c Consumer) ApplyPromiseKept(ctx context.Context, m Meta, in PromiseKept) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		verdict := "KEPT"
		return c.signal(ctx, in.OrderID, in.PromiseID, func(p *readmodel.GroupSignalPatch) { p.PromiseVerdict = &verdict })
	})
}

// PromiseBroken is the consumed delivery-promise event of the same name.
type PromiseBroken struct {
	PromiseID     string
	OrderID       string
	LateByMinutes *int
}

// ApplyPromiseBroken records the verdict and the delay in the tracking view.
func (c Consumer) ApplyPromiseBroken(ctx context.Context, m Meta, in PromiseBroken) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		verdict := "BROKEN"
		return c.signal(ctx, in.OrderID, in.PromiseID, func(p *readmodel.GroupSignalPatch) {
			p.PromiseVerdict, p.LateByMinutes = &verdict, in.LateByMinutes
		})
	})
}

// PromiseUnkeepable is the consumed delivery-promise event of the same name.
type PromiseUnkeepable struct {
	PromiseID string
	OrderID   string
	GroupNo   int
	Reason    string
}

// ApplyPromiseUnkeepable applies the policy CancelOnUnfulfillable (G10): the
// group is cancelled and CustomerOrderCancelled is published.
func (c Consumer) ApplyPromiseUnkeepable(ctx context.Context, m Meta, in PromiseUnkeepable) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		return c.mutate(ctx, in.OrderID, func(o *customerorder.CustomerOrder) ([]shared.Event, error) {
			return o.CancelUnkeepableGroup(in.GroupNo, in.Reason, c.now())
		})
	})
}
