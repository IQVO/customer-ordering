package usecases

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/views"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

// Bounds of the order history page (NG2, ADR 0002 A26). Plain literals.
const (
	// DefaultOrderListLimit is the page size when the caller passes none.
	DefaultOrderListLimit = 20
	// MaxOrderListLimit is the largest accepted page size.
	MaxOrderListLimit = 100
)

// ListOrdersInput is the query ListOrdersByShopper (GET
// /orders?shopperId=&after=&limit=). After is the opaque cursor of the
// previous page; Limit 0 means "absent" and defaults to DefaultOrderListLimit.
type ListOrdersInput struct {
	ShopperID string
	After     string
	Limit     int
}

// ListOrdersByShopper is the order history query (NG2): newest first, keyset
// on (placedAt DESC, orderId ASC), never carrying the address.
type ListOrdersByShopper struct{ Deps }

// Execute returns one page. An unknown shopper is ports.ErrNotFound; a
// malformed cursor or an out-of-range limit is shared.ErrValidation.
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
	if in.After != "" {
		var err error
		if afterAt, afterID, err = decodeOrderCursor(in.After); err != nil {
			return views.OrderList{}, err
		}
	}
	// One extra row decides whether a next page exists.
	orders, err := u.Orders.ListByShopper(ctx, in.ShopperID, afterAt, afterID, limit+1)
	if err != nil {
		return views.OrderList{}, err
	}
	next := ""
	if len(orders) > limit {
		orders = orders[:limit]
		next = encodeOrderCursor(orders[limit-1].PlacedAt(), string(orders[limit-1].ID()))
	}
	items := make([]views.Order, 0, len(orders))
	for _, o := range orders {
		items = append(items, views.OrderFrom(o))
	}
	return views.OrderList{Items: items, NextCursor: next}, nil
}

// encodeOrderCursor renders the keyset position as an opaque token.
func encodeOrderCursor(placedAt time.Time, orderID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(placedAt.UTC().Format(time.RFC3339Nano) + "|" + orderID))
}

// decodeOrderCursor parses a token back; anything malformed is
// shared.ErrValidation (422 validation-failed).
func decodeOrderCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", shared.Invalid("after is not a valid cursor")
	}
	stamp, id, found := strings.Cut(string(raw), "|")
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if !found || err != nil || !shared.ValidID(id) {
		return time.Time{}, "", shared.Invalid("after is not a valid cursor")
	}
	return at.UTC(), id, nil
}
