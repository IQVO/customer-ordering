package ports

import "errors"

var (
	// ErrNotFound is what every repository returns for a Get or Find that
	// matches nothing (problem type not-found, 404). An adapter wraps its own
	// miss (no rows, a missing map key) into this sentinel.
	ErrNotFound = errors.New("not found")
	// ErrConcurrentUpdate is what Save returns when the stored version is not
	// the one the aggregate was loaded at (problem type concurrent-update,
	// 409): the expectation is `stored version == aggregate.Version()-1`, and
	// a create (Version() 1) expects that the id is not stored yet.
	ErrConcurrentUpdate = errors.New("concurrent update")
)

// Failures of the delivery-promise gateway (ADR 0002, A12).
var (
	// ErrPromiseOfferNotQuotable: the offer expired, was consumed or the hold
	// can not be confirmed; checkout re-quotes (409 offer-not-quotable).
	ErrPromiseOfferNotQuotable = errors.New("offer not quotable")
	// ErrPromiseCapacityExhausted: no capacity left on the witness book (409).
	ErrPromiseCapacityExhausted = errors.New("promise capacity exhausted")
	// ErrPromiseBookClosed: the witness book is closed (409).
	ErrPromiseBookClosed = errors.New("book closed")
	// ErrPromiseGroupSpansNodes: the group's lines are not all shippable from
	// the offer's node (422).
	ErrPromiseGroupSpansNodes = errors.New("group spans nodes")
	// ErrPromiseUnavailable: delivery-promise is unreachable, timed out or
	// answered with a server error (503 delivery-promise-unavailable).
	ErrPromiseUnavailable = errors.New("delivery-promise unavailable")
	// ErrPromiseNotCancellable: delivery-promise refused the compensating
	// cancel (404 not-found, or 409 illegal-transition because the promise is
	// KEPT or BROKEN). Checkout logs it and goes on (ADR 0002, A14).
	ErrPromiseNotCancellable = errors.New("promise not cancellable")
)
