package usecases

import (
	"context"

	"github.com/claudioed/customer-ordering/internal/application/views"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
)

// GetOrder is the query GetOrder (GET /orders/{orderId}).
type GetOrder struct{ Deps }

// Execute returns the order without its ship-to, or ports.ErrNotFound.
func (u GetOrder) Execute(ctx context.Context, id customerorder.ID) (views.Order, error) {
	o, err := u.Orders.Get(ctx, id)
	if err != nil {
		return views.Order{}, err
	}
	return views.OrderFrom(o), nil
}

// GetOrderTracking is the query GetTracking (GET /orders/{orderId}/tracking),
// served from OrderTrackingView.
type GetOrderTracking struct{ Deps }

// Execute returns the tracking view, or ports.ErrNotFound.
func (u GetOrderTracking) Execute(ctx context.Context, id customerorder.ID) (views.Tracking, error) {
	o, err := u.Orders.Get(ctx, id)
	if err != nil {
		return views.Tracking{}, err
	}
	signals, err := u.Tracking.GroupSignals(ctx, string(id))
	if err != nil {
		return views.Tracking{}, err
	}
	packages, err := u.Tracking.Packages(ctx, string(id))
	if err != nil {
		return views.Tracking{}, err
	}
	return views.TrackingFrom(o, signals, packages), nil
}

// GetShipTo is the query GetShipTo (GET /orders/{orderId}/ship-to), the only
// exit of the address (restricted, label printing).
type GetShipTo struct{ Deps }

// Execute returns the ship-to address, or ports.ErrNotFound.
func (u GetShipTo) Execute(ctx context.Context, id customerorder.ID) (views.ShipTo, error) {
	o, err := u.Orders.Get(ctx, id)
	if err != nil {
		return views.ShipTo{}, err
	}
	return views.ShipToFrom(o.ShipTo()), nil
}
