package usecases

import (
	"context"
	"errors"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// rejectionReasons maps a checkout failure to the problem slug the REST adapter
// answers with (same vocabulary as apis/openapi.yaml), in match order.
var rejectionReasons = []struct {
	err    error
	reason string
}{
	{ports.ErrPromiseOfferNotQuotable, "offer-not-quotable"},
	{ports.ErrPromiseCapacityExhausted, "promise-capacity-exhausted"},
	{ports.ErrPromiseBookClosed, "book-closed"},
	{ports.ErrPromiseUnavailable, "delivery-promise-unavailable"},
	{ports.ErrPromiseGroupSpansNodes, "group-spans-nodes"},
	{customerorder.ErrLineWithoutPromise, "line-without-promise"},
	{customerorder.ErrPromiseDestinationMismatch, "promise-destination-mismatch"},
	{ports.ErrConcurrentUpdate, "concurrent-update"},
	{shared.ErrValidation, "validation-failed"},
}

// internalErrorReason is the reason of every failure that is not a refusal the
// caller can act on (a database error, a bug).
const internalErrorReason = "internal-error"

// recordOutcome counts one checkout attempt: placed when err is nil, else
// rejected with the reason of err.
func (u PlaceOrder) recordOutcome(ctx context.Context, err error) {
	if err == nil {
		u.metrics().OrderPlaced(ctx)
		return
	}
	u.metrics().OrderRejected(ctx, rejectionReason(err))
}

func rejectionReason(err error) string {
	for _, r := range rejectionReasons {
		if errors.Is(err, r.err) {
			return r.reason
		}
	}
	return internalErrorReason
}
