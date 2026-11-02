package usecases

import (
	"context"

	"github.com/claudioed/customer-ordering/internal/application/views"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
)

// CancelOrderInput is the command CancelOrder.
type CancelOrderInput struct {
	OrderID customerorder.ID
	Reason  string
}

// CancelOrder requests the cancellation of every unlocked group
// (POST /orders/{orderId}/cancellations, ADR 0002 A3).
type CancelOrder struct{ Deps }

// Execute returns the order after the request. A repeat changes and publishes
// nothing.
func (u CancelOrder) Execute(ctx context.Context, in CancelOrderInput) (views.Order, error) {
	return transact(ctx, u.UoW, commandAttempts, func(ctx context.Context) (views.Order, error) {
		o, err := u.Orders.Get(ctx, in.OrderID)
		if err != nil {
			return views.Order{}, err
		}
		events, err := o.CancelOrder(in.Reason, u.now())
		if err != nil {
			return views.Order{}, err
		}
		if len(events) > 0 {
			if err := u.Orders.Save(ctx, o); err != nil {
				return views.Order{}, err
			}
			if err := u.publish(ctx, events); err != nil {
				return views.Order{}, err
			}
		}
		return views.OrderFrom(o), nil
	})
}
