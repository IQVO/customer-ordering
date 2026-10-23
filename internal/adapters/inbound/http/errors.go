package http

import (
	"errors"
	"net/http"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
	"github.com/claudioed/customer-ordering/internal/domain/shopper"
)

// problemBase is the namespace of this service's RFC 9457 `type` URIs.
const problemBase = "/problems/"

// errMalformedRequest marks a body or parameter the adapter itself rejects
// (bad JSON, unknown field, wrong type).
var errMalformedRequest = errors.New("request is not valid for this operation")

// problem is the fixed (status, slug, title) of one error category; the detail
// comes from the error at write time.
type problem struct {
	status int
	slug   string
	title  string
}

var internalProblem = problem{http.StatusInternalServerError, "internal-error", "Internal server error"}

// Problems the adapter writes itself, before any use case runs.
var (
	idempotencyKeyRequired = problem{http.StatusBadRequest, "idempotency-key-required", "Idempotency-Key header is required"}
	idempotencyKeyConflict = problem{http.StatusConflict, "idempotency-key-conflict", "Idempotency-Key was already used with a different request"}
)

// problemCatalogue maps every typed error to its problem, in match order. The
// slugs are the ones of apis/openapi.yaml and .claude/rules/rest-api.md.
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{errMalformedRequest, problem{http.StatusBadRequest, "malformed-request", "Malformed request"}},
	{ports.ErrNotFound, problem{http.StatusNotFound, "order-not-found", "Order not found"}},
	{ports.ErrPromiseOfferNotQuotable, problem{http.StatusConflict, "offer-not-quotable", "The offer can no longer be used; choose a new one"}},
	{ports.ErrPromiseCapacityExhausted, problem{http.StatusConflict, "promise-capacity-exhausted", "No promise capacity is left"}},
	{ports.ErrPromiseBookClosed, problem{http.StatusConflict, "book-closed", "The promise book is closed"}},
	{ports.ErrPromiseUnavailable, problem{http.StatusServiceUnavailable, "delivery-promise-unavailable", "delivery-promise is unavailable"}},
	{ports.ErrPromiseGroupSpansNodes, problem{http.StatusUnprocessableEntity, "group-spans-nodes", "A shipment group must ship from one node"}},
	{customerorder.ErrLineWithoutPromise, problem{http.StatusUnprocessableEntity, "line-without-promise", "Every line needs exactly one promise"}},
	{customerorder.ErrPromiseDestinationMismatch, problem{http.StatusUnprocessableEntity, "promise-destination-mismatch", "A promise was made for another destination"}},
	{customerorder.ErrCancelAfterLock, problem{http.StatusConflict, "cancel-after-lock", "Every shipment group is locked; cancellation is now a return"}},
	{shopper.ErrShopperExists, problem{http.StatusConflict, "shopper-exists", "A shopper with this e-mail exists"}},
	{ports.ErrConcurrentUpdate, problem{http.StatusConflict, "concurrent-update", "The resource was modified by another request; retry"}},
	{shared.ErrIllegalTransition, problem{http.StatusConflict, "illegal-transition", "The order does not allow this change in its current state"}},
	{shared.ErrValidation, problem{http.StatusUnprocessableEntity, "validation-failed", "Validation failed"}},
}

// problemFor returns the problem of the first catalogue entry err matches, or
// the internal-error problem.
func problemFor(err error) problem {
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry.p
		}
	}
	return internalProblem
}
