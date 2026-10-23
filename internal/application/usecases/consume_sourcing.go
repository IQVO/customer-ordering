package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// Meta identifies the consumed CloudEvent. At is its `time` (the zero time
// when the producer sent none).
type Meta struct {
	EventID string
	Type    string
	At      time.Time
}

// Consumer applies the events customer-ordering consumes (ADR 0003). Every
// method runs in one unit of work together with the inbox claim, so an event
// is applied at most once; the events listed in ADR 0002 A7 are recorded and
// skipped instead of failing.
type Consumer struct{ Deps }

// skippable reports the errors a consumer records and skips (A7): the order
// is unknown, the event would move a status backwards, is illegal in the
// current state or names an unknown group.
func skippable(err error) bool {
	return errors.Is(err, ports.ErrNotFound) || errors.Is(err, customerorder.ErrStatusRegression) ||
		errors.Is(err, customerorder.ErrUnknownGroup) || errors.Is(err, shared.ErrIllegalTransition)
}

// handle claims the event and runs apply in the same unit of work.
func (c Consumer) handle(ctx context.Context, m Meta, apply func(ctx context.Context) error) error {
	_, err := transact(ctx, c.UoW, consumerAttempts, func(ctx context.Context) (struct{}, error) {
		first, err := c.Inbox.Claim(ctx, m.EventID, m.Type)
		if err != nil || !first {
			return struct{}{}, err
		}
		if err := apply(ctx); err != nil {
			if !skippable(err) {
				return struct{}{}, err
			}
			c.logger().Info("consumed event skipped", "eventId", m.EventID, "type", m.Type, "reason", err.Error())
		}
		return struct{}{}, nil
	})
	return err
}

// when returns m.At, or now when the producer sent no time.
func (c Consumer) when(m Meta) time.Time {
	if m.At.IsZero() {
		return c.now()
	}
	return m.At
}

// mutate loads the order, applies one domain command and persists the result
// with the events it raised. An unchanged aggregate is not saved.
func (c Consumer) mutate(ctx context.Context, orderID string, apply func(o *customerorder.CustomerOrder) ([]shared.Event, error)) error {
	o, err := c.Orders.Get(ctx, customerorder.ID(orderID))
	if err != nil {
		return err
	}
	before := o.Version()
	events, err := apply(o)
	if err != nil {
		return err
	}
	if o.Version() == before {
		return nil
	}
	if err := c.Orders.Save(ctx, o); err != nil {
		return err
	}
	return c.publish(ctx, events)
}

// quiet adapts a command that raises no event.
func quiet(fn func(o *customerorder.CustomerOrder) error) func(o *customerorder.CustomerOrder) ([]shared.Event, error) {
	return func(o *customerorder.CustomerOrder) ([]shared.Event, error) { return nil, fn(o) }
}

// reconcile replays what shipping-and-tracking reported for the group's
// packages before its shipment was known: the deliveries (A21), then the
// losses and returns (A19).
func (c Consumer) reconcile(ctx context.Context, o *customerorder.CustomerOrder, groupNo int) error {
	delivered, err := c.Tracking.DeliveredPackages(ctx, string(o.ID()), groupNo)
	if err != nil {
		return err
	}
	for _, d := range delivered {
		if err := o.MarkGroupDelivered(groupNo, d.TrackingNumber, d.At); err != nil {
			return err
		}
	}
	return c.reconcileOutcomes(ctx, o, groupNo)
}

// FulfillmentLocked is the consumed order-sourcing event of the same name.
type FulfillmentLocked struct {
	AssignmentID string
	OrderID      string
	GroupNo      int
	NodeID       string
	PromiseID    string
	LockedAt     time.Time
}

// ApplyFulfillmentLocked links the assignment and runs MarkInFulfillment.
func (c Consumer) ApplyFulfillmentLocked(ctx context.Context, m Meta, in FulfillmentLocked) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		link := readmodel.AssignmentLink{AssignmentID: in.AssignmentID, OrderID: in.OrderID, GroupNo: in.GroupNo, PromiseID: in.PromiseID}
		if err := c.Tracking.LinkAssignment(ctx, link); err != nil {
			return err
		}
		return c.mutate(ctx, in.OrderID, quiet(func(o *customerorder.CustomerOrder) error {
			if err := o.MarkInFulfillment(in.GroupNo, in.AssignmentID, in.NodeID, orElse(in.LockedAt, c.when(m))); err != nil {
				return err
			}
			return c.reconcile(ctx, o, in.GroupNo)
		}))
	})
}

// FulfillmentShipped is the consumed order-sourcing event of the same name.
type FulfillmentShipped struct {
	AssignmentID    string
	OrderID         string
	GroupNo         int
	TrackingNumbers []string
	ShippedAt       time.Time
}

// ApplyFulfillmentShipped runs MarkGroupShipped and replays early deliveries.
func (c Consumer) ApplyFulfillmentShipped(ctx context.Context, m Meta, in FulfillmentShipped) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		if err := c.Tracking.AttachTrackingNumbers(ctx, in.OrderID, in.GroupNo, in.TrackingNumbers); err != nil {
			return err
		}
		return c.mutate(ctx, in.OrderID, quiet(func(o *customerorder.CustomerOrder) error {
			if err := o.MarkGroupShipped(in.GroupNo, in.TrackingNumbers, orElse(in.ShippedAt, c.when(m))); err != nil {
				return err
			}
			return c.reconcile(ctx, o, in.GroupNo)
		}))
	})
}

// FulfillmentUnsourceable is the consumed order-sourcing event of the same name.
type FulfillmentUnsourceable struct {
	OrderID string
	GroupNo int
	Reason  string
}

// ApplyFulfillmentUnsourceable records the reason in the tracking view.
func (c Consumer) ApplyFulfillmentUnsourceable(ctx context.Context, m Meta, in FulfillmentUnsourceable) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		reason := reasonOrDefault(in.Reason)
		return c.Tracking.PatchGroupSignal(ctx, readmodel.GroupSignalPatch{OrderID: in.OrderID, GroupNo: in.GroupNo, UnsourceableReason: &reason})
	})
}

// FulfillmentCancelled is the consumed order-sourcing event of the same name.
type FulfillmentCancelled struct {
	OrderID string
	GroupNo int
}

// ApplyFulfillmentCancelled runs ConfirmGroupCancelled.
func (c Consumer) ApplyFulfillmentCancelled(ctx context.Context, m Meta, in FulfillmentCancelled) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		return c.mutate(ctx, in.OrderID, quiet(func(o *customerorder.CustomerOrder) error {
			return o.ConfirmGroupCancelled(in.GroupNo, c.when(m))
		}))
	})
}

// FulfillmentCancelRefused is the consumed order-sourcing event of the same name.
type FulfillmentCancelRefused struct {
	OrderID string
	GroupNo int
	Reason  string
}

// ApplyFulfillmentCancelRefused runs RefuseGroupCancellation.
func (c Consumer) ApplyFulfillmentCancelRefused(ctx context.Context, m Meta, in FulfillmentCancelRefused) error {
	return c.handle(ctx, m, func(ctx context.Context) error {
		return c.mutate(ctx, in.OrderID, quiet(func(o *customerorder.CustomerOrder) error {
			return o.RefuseGroupCancellation(in.GroupNo, in.Reason, c.when(m))
		}))
	})
}

func orElse(t, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback
	}
	return t
}

func reasonOrDefault(s string) string {
	if s == "" {
		return "UNSPECIFIED"
	}
	return s
}
