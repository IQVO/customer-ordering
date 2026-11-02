package usecases

import (
	"context"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/pagination"
	"github.com/claudioed/customer-ordering/internal/application/views"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

// Bounds of the order history page (REST guideline 3.2). Plain literals.
const (
	// DefaultOrderListLimit is the page size when the caller passes none.
	DefaultOrderListLimit = 100
	// MaxOrderListLimit is the largest accepted page size.
	MaxOrderListLimit = 500
)

// ListOrdersInput is the query ListOrdersByShopper (GET
// /orders?shopperId=&cursor=&limit=). After is the keyset the previous page
// ended at (nil for the first page); Limit 0 means "absent" and defaults to
// DefaultOrderListLimit.
type ListOrdersInput struct {
	ShopperID string
	After     *pagination.Keyset
	Limit     int
}

// ListOrdersByShopper is the order history query (NG2): newest first, keyset
// on (placedAt DESC, orderId ASC), never carrying the address.
type ListOrdersByShopper struct{ Deps }

// Execute returns one page. An unknown shopper is ports.ErrNotFound; an
// out-of-range limit is shared.ErrValidation.
func (u ListOrdersByShopper) Execute(ctx context.Context, in ListOrdersInput) (views.OrderList, error) {
	limit := in.Limit
	if limit == 0 {
		limit = DefaultOrderListLimit
	}
	if limit < 0 || limit > MaxOrderListLimit {
		return views.OrderList{}, shared.Invalid("limit must be between 1 and %d", MaxOrderListLimit)
	}
	if _, err := u.Shoppers.Get(ctx, shopper.ID(in.ShopperID)); err != nil {
		return views.OrderList{}, err
	}
	var afterAt time.Time
	var afterID string
	if in.After != nil {
		afterAt, afterID = in.After.PlacedAt, in.After.ID
	}
	// One extra row decides whether a next page exists.
	orders, err := u.Orders.ListByShopper(ctx, in.ShopperID, afterAt, afterID, limit+1)
	if err != nil {
		return views.OrderList{}, err
	}
	var next *pagination.Keyset
	if len(orders) > limit {
		orders = orders[:limit]
		last := orders[limit-1]
		next = &pagination.Keyset{PlacedAt: last.PlacedAt(), ID: string(last.ID())}
	}
	items := make([]views.Order, 0, len(orders))
	for _, o := range orders {
		items = append(items, views.OrderFrom(o))
	}
	return views.OrderList{Items: items, Next: next}, nil
}
