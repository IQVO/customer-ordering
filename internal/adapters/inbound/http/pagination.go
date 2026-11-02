package http

import (
	"fmt"
	"net/http"
	"time"

	"github.com/claudioed/customer-ordering/internal/adapters/inbound/http/page"
	"github.com/claudioed/customer-ordering/internal/application/pagination"
	"github.com/claudioed/customer-ordering/internal/application/views"
)

// pageParams reads the `limit` and `cursor` query parameters of a collection
// GET and rejects any other parameter than the filters the collection declares
// (400 unknown-query-parameter: a consumer still sending a retired paging
// parameter such as `after` must fail loudly). The cursor is the opaque
// page.Position whose sort key is the placedAt instant of the last returned
// order (RFC 3339, nanoseconds) and whose id is its orderId; a cursor that does
// not decode to that is invalid-cursor.
func pageParams(r *http.Request, filters ...string) (limit int, after *pagination.Keyset, err error) {
	if name := page.Unknown(r, filters...); name != "" {
		return 0, nil, fmt.Errorf("%w %q", errUnknownQueryParameter, name)
	}
	if limit, err = page.Limit(r); err != nil {
		return 0, nil, err
	}
	pos, err := page.After(r)
	if err != nil || pos == nil {
		return limit, nil, err
	}
	at, perr := time.Parse(time.RFC3339Nano, pos.Sort)
	if perr != nil {
		return 0, nil, fmt.Errorf("%w: %v", page.ErrInvalidCursor, perr)
	}
	return limit, &pagination.Keyset{PlacedAt: at.UTC(), ID: pos.ID}, nil
}

// orderPageOut renders one use case page as the {items,nextCursor} envelope.
func orderPageOut(l views.OrderList) page.Page[orderDTO] {
	out := page.Page[orderDTO]{Items: make([]orderDTO, 0, len(l.Items))}
	for _, o := range l.Items {
		out.Items = append(out.Items, orderView(o))
	}
	if l.Next != nil {
		out.NextCursor = page.Encode(page.Position{Sort: l.Next.PlacedAt.UTC().Format(time.RFC3339Nano), ID: l.Next.ID})
	}
	return out
}
