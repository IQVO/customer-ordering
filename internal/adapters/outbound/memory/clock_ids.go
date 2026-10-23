package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/promising"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

// Clock is a settable ports.Clock.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a clock reading t.
func NewClock(t time.Time) *Clock { return &Clock{now: t} }

// Now returns the current reading.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set moves the clock to t.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// Advance moves the clock forward by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// SequentialIDs is a ports.IDGenerator minting shopper-1, shopper-2, ... and
// deriving order ids as "order-<key>" (the key is made of valid id characters
// in tests).
type SequentialIDs struct {
	mu       sync.Mutex
	shoppers int
}

// NewShopperID returns the next shopper id.
func (g *SequentialIDs) NewShopperID() shopper.ID {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.shoppers++
	return shopper.ID(fmt.Sprintf("shopper-%d", g.shoppers))
}

// OrderIDFor returns "order-" plus the key.
func (g *SequentialIDs) OrderIDFor(key string) customerorder.ID {
	return customerorder.ID("order-" + key)
}

// ConfirmCall records one ConfirmPromise call.
type ConfirmCall struct {
	PromiseID string
	OrderID   string
	GroupNo   int
}

// QuoteCall records one QuoteOffers call.
type QuoteCall struct {
	Prefix string
	Lines  []promising.Line
}

// PromiseGateway is a scripted ports.PromiseGateway. An offer id with no
// scripted terms is not quotable. All fields are read under the mutex by the
// methods; set them before the use case runs.
type PromiseGateway struct {
	mu sync.Mutex
	// Terms is what MakePromise returns per offer id.
	Terms map[string]promising.Terms
	// MakeErr makes MakePromise fail per offer id.
	MakeErr map[string]error
	// ConfirmErr makes ConfirmPromise fail per promise id.
	ConfirmErr map[string]error
	// Offers is what QuoteOffers returns; QuoteErr makes it fail.
	Offers   []promising.Offer
	QuoteErr error

	made      []promising.MakeRequest
	confirmed []ConfirmCall
	quoted    []QuoteCall
}

var _ ports.PromiseGateway = (*PromiseGateway)(nil)

// NewPromiseGateway returns a gateway with nothing scripted.
func NewPromiseGateway() *PromiseGateway {
	return &PromiseGateway{Terms: map[string]promising.Terms{}, MakeErr: map[string]error{}, ConfirmErr: map[string]error{}}
}

// QuoteOffers implements ports.PromiseGateway.
func (g *PromiseGateway) QuoteOffers(_ context.Context, prefix string, lines []promising.Line) ([]promising.Offer, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.quoted = append(g.quoted, QuoteCall{Prefix: prefix, Lines: append([]promising.Line(nil), lines...)})
	if g.QuoteErr != nil {
		return nil, g.QuoteErr
	}
	return append([]promising.Offer(nil), g.Offers...), nil
}

// MakePromise implements ports.PromiseGateway.
func (g *PromiseGateway) MakePromise(_ context.Context, req promising.MakeRequest) (promising.Terms, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.made = append(g.made, req)
	if err := g.MakeErr[req.OfferID]; err != nil {
		return promising.Terms{}, err
	}
	t, ok := g.Terms[req.OfferID]
	if !ok {
		return promising.Terms{}, fmt.Errorf("offer %s: %w", req.OfferID, ports.ErrPromiseOfferNotQuotable)
	}
	return t, nil
}

// ConfirmPromise implements ports.PromiseGateway.
func (g *PromiseGateway) ConfirmPromise(_ context.Context, promiseID, orderID string, groupNo int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.confirmed = append(g.confirmed, ConfirmCall{PromiseID: promiseID, OrderID: orderID, GroupNo: groupNo})
	return g.ConfirmErr[promiseID]
}

// Made returns the MakePromise calls in order.
func (g *PromiseGateway) Made() []promising.MakeRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]promising.MakeRequest(nil), g.made...)
}

// Confirmed returns the ConfirmPromise calls in order.
func (g *PromiseGateway) Confirmed() []ConfirmCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]ConfirmCall(nil), g.confirmed...)
}

// Quoted returns the QuoteOffers calls in order.
func (g *PromiseGateway) Quoted() []QuoteCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]QuoteCall(nil), g.quoted...)
}
