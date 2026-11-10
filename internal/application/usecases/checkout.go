package usecases

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/claudioed/customer-ordering/internal/application/compensation"
	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/promising"
	"github.com/claudioed/customer-ordering/internal/application/views"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

// CheckoutGroup is one shipment group and the offer the shopper saw for it.
type CheckoutGroup struct {
	GroupNo int
	OfferID string
	LineNos []int
}

// PlaceOrderInput is the command PlaceOrder. OrderID is derived from the
// Idempotency-Key by the caller (ADR 0002, A13). Exactly one of ShipTo /
// ShipToAddressID is required (NG1): a saved address is resolved against the
// shopper and COPIED into the order at placement (A25).
type PlaceOrderInput struct {
	OrderID         customerorder.ID
	ShopperID       string
	ShipTo          shipto.ShipTo
	ShipToAddressID string
	Lines           []customerorder.OrderLine
	Groups          []CheckoutGroup
}

// RequoteRequiredError is returned when an offer expired, was consumed or could not
// be confirmed: nothing was placed and Offers are the fresh offers to show
// (policy CheckoutPromising). It unwraps to ports.ErrPromiseOfferNotQuotable.
type RequoteRequiredError struct{ Offers []views.RequotedOffer }

func (e *RequoteRequiredError) Error() string {
	return fmt.Sprintf("%v: %d fresh offers quoted", ports.ErrPromiseOfferNotQuotable, len(e.Offers))
}

// Unwrap returns ports.ErrPromiseOfferNotQuotable.
func (e *RequoteRequiredError) Unwrap() error { return ports.ErrPromiseOfferNotQuotable }

// PlaceOrder turns a cart into an order (POST /checkout): it makes and
// confirms a promise for every group at delivery-promise (G7, E7) and only then
// stores the order and CustomerOrderPlaced in one transaction.
type PlaceOrder struct{ Deps }

// Execute places the order. A replay of the same OrderID returns the stored
// order. See RequoteRequiredError and the ports.ErrPromise... sentinels for the
// delivery-promise failures.
func (u PlaceOrder) Execute(ctx context.Context, in PlaceOrderInput) (views.Order, error) {
	v, replayed, err := u.execute(ctx, in)
	if !replayed {
		u.recordOutcome(ctx, err)
	}
	return v, err
}

// execute is the checkout proper; replayed reports that the stored order of an
// earlier identical checkout was returned, which is not a new attempt.
func (u PlaceOrder) execute(ctx context.Context, in PlaceOrderInput) (_ views.Order, replayed bool, _ error) {
	if err := checkAddressChoice(in); err != nil {
		return views.Order{}, false, err
	}
	if v, found, err := u.replay(ctx, in); found || err != nil {
		return v, found, err
	}
	if err := u.resolveAddress(ctx, &in); err != nil {
		return views.Order{}, false, err
	}
	if err := u.preflight(in); err != nil {
		return views.Order{}, false, err
	}
	if err := u.requireShopper(ctx, in.ShopperID); err != nil {
		return views.Order{}, false, err
	}
	groups, err := u.promiseGroups(ctx, in)
	if errors.Is(err, ports.ErrPromiseOfferNotQuotable) {
		return views.Order{}, false, u.requote(ctx, in)
	}
	if err != nil {
		return views.Order{}, false, err
	}
	placed, err := transact(ctx, u.UoW, commandAttempts, func(ctx context.Context) (views.Order, error) {
		o, events, err := customerorder.Place(u.newOrder(in, groups), u.now())
		if err != nil {
			return views.Order{}, err
		}
		if err := u.Orders.Save(ctx, o); err != nil {
			return views.Order{}, err
		}
		if err := u.publish(ctx, events); err != nil {
			return views.Order{}, err
		}
		return views.OrderFrom(o), nil
	})
	return placed, false, err
}

// newOrder builds the Place input. The consolidation key is derived from the
// shopper and the ship-to AFTER resolveAddress copied a saved address into in
// (ADR 0012); without a keyer it stays empty.
func (u PlaceOrder) newOrder(in PlaceOrderInput, groups []customerorder.GroupInput) customerorder.NewOrder {
	key := ""
	if u.ConsolidationKeys != nil {
		key = u.ConsolidationKeys.KeyFor(in.ShopperID, in.ShipTo.NormalizedAddress())
	}
	return customerorder.NewOrder{ID: in.OrderID, ShopperID: in.ShopperID, ShipTo: in.ShipTo, Lines: in.Lines, Groups: groups, ConsolidationKey: key}
}

// checkAddressChoice enforces NG1: exactly one of shipTo / shipToAddressId.
func checkAddressChoice(in PlaceOrderInput) error {
	inline := in.ShipTo != (shipto.ShipTo{})
	byID := in.ShipToAddressID != ""
	if inline == byID {
		return shared.Invalid("exactly one of shipTo and shipToAddressId is required")
	}
	return nil
}

// resolveAddress copies the saved address into the input (NG1, A25). An
// unknown id, or the id of another shopper's address, is shared.ErrValidation;
// so is an unregistered shopper (A9).
func (u PlaceOrder) resolveAddress(ctx context.Context, in *PlaceOrderInput) error {
	if in.ShipToAddressID == "" {
		return nil
	}
	s, err := u.Shoppers.Get(ctx, shopper.ID(in.ShopperID))
	if errors.Is(err, ports.ErrNotFound) {
		return shared.Invalid("shopper %q is not registered", in.ShopperID)
	}
	if err != nil {
		return err
	}
	a, found := s.Address(shopper.AddressID(in.ShipToAddressID))
	if !found {
		return shared.Invalid("shipToAddressId %q is not a saved address of shopper %q", in.ShipToAddressID, in.ShopperID)
	}
	in.ShipTo = a
	return nil
}

// preflight runs every check that needs no promise by placing a throw-away
// order with placeholder promises, so a bad request never reaches
// delivery-promise.
func (u PlaceOrder) preflight(in PlaceOrderInput) error {
	now := u.now()
	groups := make([]customerorder.GroupInput, len(in.Groups))
	for i, g := range in.Groups {
		if !shared.ValidID(g.OfferID) {
			return shared.Invalid("group %d: offerId %q is not a valid id", g.GroupNo, g.OfferID)
		}
		groups[i] = customerorder.GroupInput{
			GroupNo: g.GroupNo, PromiseID: "pending", Tier: customerorder.TierStandard, WitnessNodeID: "pending", ShipBy: now,
			DeliverByEarliest: now, DeliverByLatest: now, LineNos: g.LineNos, DestinationPostalPrefix: in.ShipTo.DestinationPrefix(),
		}
	}
	_, _, err := customerorder.Place(u.newOrder(in, groups), now)
	return err
}

// replay returns the stored order when the same checkout already succeeded.
func (u PlaceOrder) replay(ctx context.Context, in PlaceOrderInput) (views.Order, bool, error) {
	o, err := u.Orders.Get(ctx, in.OrderID)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return views.Order{}, false, nil
	case err != nil:
		return views.Order{}, false, err
	case o.ShopperID() != in.ShopperID:
		return views.Order{}, false, fmt.Errorf("order %s belongs to another shopper: %w", in.OrderID, ports.ErrConcurrentUpdate)
	}
	return views.OrderFrom(o), true, nil
}

func (u PlaceOrder) requireShopper(ctx context.Context, id string) error {
	_, err := u.Shoppers.Get(ctx, shopper.ID(id))
	if errors.Is(err, ports.ErrNotFound) {
		return shared.Invalid("shopper %q is not registered", id)
	}
	return err
}

func groupLines(in PlaceOrderInput, g CheckoutGroup) []promising.Line {
	byNo := make(map[int]customerorder.OrderLine, len(in.Lines))
	for _, l := range in.Lines {
		byNo[l.LineNo] = l
	}
	out := make([]promising.Line, 0, len(g.LineNos))
	for _, no := range g.LineNos {
		out = append(out, promising.Line{CatalogItemID: byNo[no].CatalogItemID, Quantity: byNo[no].Quantity})
	}
	return out
}

// promiseGroups makes a promise for every group, checks the order against the
// promises (destination, coverage) and only then confirms them all (G7), so a
// mismatch never leaves a confirmed promise behind. When a later confirm
// fails, the promises already confirmed are cancelled (A14) and the confirm
// error is returned unchanged.
func (u PlaceOrder) promiseGroups(ctx context.Context, in PlaceOrderInput) ([]customerorder.GroupInput, error) {
	out := make([]customerorder.GroupInput, 0, len(in.Groups))
	for _, g := range in.Groups {
		t, err := u.Promises.MakePromise(ctx, promising.MakeRequest{
			OrderID: string(in.OrderID), GroupNo: g.GroupNo, OfferID: g.OfferID, Lines: groupLines(in, g),
		})
		if err != nil {
			return nil, err
		}
		out = append(out, groupInput(g, t))
	}
	if _, _, err := customerorder.Place(u.newOrder(in, out), u.now()); err != nil {
		return nil, err
	}
	for i, g := range out {
		if err := u.Promises.ConfirmPromise(ctx, g.PromiseID, string(in.OrderID), g.GroupNo); err != nil {
			u.compensate(ctx, in.OrderID, out[:confirmedBefore(i, err)])
			return nil, err
		}
	}
	return out, nil
}

// confirmedBefore is how many leading groups a failed confirm of group i makes
// the compensation cancel: the groups confirmed before it and, when the answer
// is unknown (timeout, transport error, 5xx), group i itself, because the
// confirm may have been applied (ADR 0002, A14).
func confirmedBefore(i int, err error) int {
	if errors.Is(err, ports.ErrPromiseUnavailable) {
		return i + 1
	}
	return i
}

// compensate cancels the promises a failed checkout already confirmed, so
// their capacity holds are released (ADR 0002, A14). A cancel that fails is
// enqueued on the compensation queue for the background worker to retry -- a
// queue failure is logged -- and the checkout still fails with the confirm
// error. It runs detached from the request's cancellation so a client that
// gave up does not leave the holds behind.
func (u PlaceOrder) compensate(ctx context.Context, orderID customerorder.ID, confirmed []customerorder.GroupInput) {
	ctx = context.WithoutCancel(ctx)
	var failed []compensation.Cancel
	for _, g := range confirmed {
		err := u.Promises.CancelPromise(ctx, g.PromiseID, string(orderID), g.GroupNo)
		if errors.Is(err, ports.ErrPromiseNotCancellable) {
			// delivery-promise cancels CONFIRMED (or AT_RISK) promises only: a
			// PROVISIONAL one (the confirm never applied) lapses by itself, and a
			// KEPT, BROKEN or RELEASED one is closed. Nothing is left to release.
			u.logger().WarnContext(ctx, "compensating cancel refused by delivery-promise; nothing to release",
				"orderId", sanitizeLog(string(orderID)), "groupNo", sanitizeLog(strconv.Itoa(g.GroupNo)), "promiseId", g.PromiseID, "error", err.Error())
			continue
		}
		if err != nil {
			failed = append(failed, compensation.Cancel{OrderID: string(orderID), GroupNo: g.GroupNo, PromiseID: g.PromiseID})
			u.logger().ErrorContext(ctx, "compensating cancel of a confirmed promise failed; the hold stays at delivery-promise",
				"orderId", sanitizeLog(string(orderID)), "groupNo", sanitizeLog(strconv.Itoa(g.GroupNo)), "promiseId", g.PromiseID, "error", err.Error())
			continue
		}
		u.logger().InfoContext(ctx, "cancelled a confirmed promise of a failed checkout",
			"orderId", sanitizeLog(string(orderID)), "groupNo", sanitizeLog(strconv.Itoa(g.GroupNo)), "promiseId", g.PromiseID)
	}
	u.enqueueFailedCancels(ctx, failed)
}

// enqueueFailedCancels records the cancels that errored on the compensation
// queue, so the worker retries them until the holds are released (A14); when
// even that fails the failed cancel is only logged, as before.
func (u PlaceOrder) enqueueFailedCancels(ctx context.Context, failed []compensation.Cancel) {
	if len(failed) == 0 {
		return
	}
	if err := u.compensations().Enqueue(ctx, failed...); err != nil {
		u.logger().ErrorContext(ctx, "enqueueing failed compensating cancels for retry failed; the holds stay at delivery-promise",
			"cancels", len(failed), "error", err.Error())
		return
	}
	u.logger().InfoContext(ctx, "enqueued failed compensating cancels for retry",
		"cancels", len(failed))
}

func groupInput(g CheckoutGroup, t promising.Terms) customerorder.GroupInput {
	return customerorder.GroupInput{
		GroupNo: g.GroupNo, PromiseID: t.PromiseID, Tier: customerorder.Tier(t.Tier), WitnessNodeID: t.WitnessNodeID,
		ShipBy: t.ShipBy, DeliverByEarliest: t.DeliverByEarliest, DeliverByLatest: t.DeliverByLatest, TimeZone: t.TimeZone, LineNos: g.LineNos,
		DestinationPostalPrefix: t.DestinationPostalPrefix, ChosenDeliveryDay: chosenDeliveryDay(t), ConsolidationEligible: consolidationEligible(t),
	}
}

// requote quotes fresh offers for every group and reports them.
func (u PlaceOrder) requote(ctx context.Context, in PlaceOrderInput) error {
	var all []views.RequotedOffer
	for _, g := range in.Groups {
		offers, err := u.Promises.QuoteOffers(ctx, in.ShipTo.DestinationPrefix(), groupLines(in, g))
		if err != nil {
			return err
		}
		all = append(all, views.RequotedFrom(g.GroupNo, offers)...)
	}
	return &RequoteRequiredError{Offers: all}
}
