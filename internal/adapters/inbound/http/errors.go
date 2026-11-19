package http

import (
	"errors"
	"net/http"

	"github.com/claudioed/customer-ordering/internal/adapters/inbound/http/page"
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

// errUnknownQueryParameter marks a query parameter a collection does not declare
// (REST guideline 4.2).
var errUnknownQueryParameter = errors.New("unknown query parameter")

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
	idempotencyKeyInvalid  = problem{http.StatusBadRequest, "idempotency-key-invalid", "Idempotency-Key header is not valid"}
	idempotencyKeyReused   = problem{http.StatusConflict, "idempotency-key-reused", "Idempotency-Key was already used with a different request"}
	invalidLimit           = problem{http.StatusBadRequest, "invalid-limit", "limit must be an integer between 1 and 500"}
	invalidCursor          = problem{http.StatusBadRequest, "invalid-cursor", "cursor is not a valid page token"}
	unknownQueryParameter  = problem{http.StatusBadRequest, "unknown-query-parameter", "The query string carries a parameter this collection does not declare"}
)

// replayClass is what the Idempotency-Key middleware does with the answer of a
// problem type (ADR 0019): FINAL and OUTCOME answers are stored and replayed,
// a RETRYABLE one is not stored and its key is released. A 2xx is stored and a
// 5xx never is, whatever the class. The empty class is "unclassified".
type replayClass string

const (
	// replayFinal: the answer is a function of the request (or of state that
	// only ever moves one way) alone; stored and replayed.
	replayFinal replayClass = "FINAL"
	// replayOutcome: the command committed state and then refused; the writes
	// commit with the stored answer.
	replayOutcome replayClass = "OUTCOME"
	// replayRetryable: nothing committed and the answer depends on state that
	// can change; not stored, the key is released, a retry executes again.
	replayRetryable replayClass = "RETRYABLE"
)

// stored reports whether an error answer of this class is remembered under its key.
func (c replayClass) stored() bool { return c == replayFinal || c == replayOutcome }

// catalogueEntry is one typed error, its problem and the replay class of that
// problem.
type catalogueEntry struct {
	err    error
	p      problem
	replay replayClass
}

// problemCatalogue maps every typed error to its problem and its replay class,
// in match order. The slugs are the ones of apis/openapi.yaml and
// .claude/rules/rest-api.md. ADR 0019 holds the reason of every class and a
// test keeps its table equal to this one; a new entry without a class fails
// TestEveryProblemTypeDeclaresAReplayClass. When unsure the class is
// RETRYABLE: a refusal that rolled everything back costs one more execution,
// a wrongly stored one pins a stale answer for 24 hours.
var problemCatalogue = []catalogueEntry{
	{errMalformedRequest, problem{http.StatusBadRequest, "malformed-request", "Malformed request"}, replayFinal},
	{page.ErrInvalidLimit, invalidLimit, replayFinal},
	{page.ErrInvalidCursor, invalidCursor, replayFinal},
	{errUnknownQueryParameter, unknownQueryParameter, replayFinal},
	// An order id derives from a checkout's key and a shopper may be registered by another request: the target can appear later.
	{ports.ErrNotFound, problem{http.StatusNotFound, "not-found", "Not found"}, replayRetryable},
	// The one OUTCOME: a refused confirm cancels the promises confirmed before it and enqueues the failed cancels in the request's transaction (checkout.go 249-253, 275-314).
	{ports.ErrPromiseOfferNotQuotable, problem{http.StatusConflict, "offer-not-quotable", "The offer can no longer be used; choose a new one"}, replayOutcome},
	// Capacity frees up and a book reopens; only POST /promises answers them, so nothing is confirmed yet.
	{ports.ErrPromiseCapacityExhausted, problem{http.StatusConflict, "promise-capacity-exhausted", "No promise capacity is left"}, replayRetryable},
	{ports.ErrPromiseBookClosed, problem{http.StatusConflict, "book-closed", "The promise book is closed"}, replayRetryable},
	{ports.ErrPromiseUnavailable, problem{http.StatusServiceUnavailable, "delivery-promise-unavailable", "delivery-promise is unavailable"}, replayRetryable},
	{ports.ErrPromiseGroupSpansNodes, problem{http.StatusUnprocessableEntity, "group-spans-nodes", "A shipment group must ship from one node"}, replayRetryable},
	// Coverage of the lines by the groups: the request alone (place.go checkGroup, preflight).
	{customerorder.ErrLineWithoutPromise, problem{http.StatusUnprocessableEntity, "line-without-promise", "Every line needs exactly one promise"}, replayFinal},
	// Compares the order's destination with the terms delivery-promise answered (place.go 210).
	{customerorder.ErrPromiseDestinationMismatch, problem{http.StatusUnprocessableEntity, "promise-destination-mismatch", "A promise was made for another destination"}, replayRetryable},
	// A DELIVERED, LOST or RETURNED order stays closed (cancel.go 25-27, commands.go 43 and 92).
	{customerorder.ErrStatusRegression, problem{http.StatusConflict, "status-regression", "The order status can only move forward"}, replayFinal},
	// Passes once an event cancels a locked group: the repeat is then a no-op (cancel.go 37-43, 79-95).
	{customerorder.ErrCancelAfterLock, problem{http.StatusConflict, "cancel-after-lock", "Every shipment group is locked; cancellation is now a return"}, replayRetryable},
	// Nothing is ever deleted and an e-mail never changes: once taken, always taken (shoppers.go 113 and 150).
	{shopper.ErrShopperExists, problem{http.StatusConflict, "shopper-exists", "A shopper with this e-mail exists"}, replayFinal},
	// Saved addresses only grow (shopper.go 178-181).
	{shopper.ErrTooManyAddresses, problem{http.StatusUnprocessableEntity, "too-many-addresses", "A shopper holds at most 20 saved addresses"}, replayFinal},
	{ports.ErrConcurrentUpdate, problem{http.StatusConflict, "concurrent-update", "The resource was modified by another request; retry"}, replayRetryable},
	{shared.ErrIllegalTransition, problem{http.StatusConflict, "illegal-transition", "The order does not allow this change in its current state"}, replayRetryable},
	// Mixed: a field of the request, but also "shopper is not registered" and "not a saved address" (checkout.go 154, 161, 213), facts another request can create.
	{shared.ErrValidation, problem{http.StatusUnprocessableEntity, "validation-failed", "Validation failed"}, replayRetryable},
}

// entryFor returns the first catalogue entry err matches.
// An unmatched error is the internal-error problem with no class.
func entryFor(err error) catalogueEntry {
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry
		}
	}
	return catalogueEntry{p: internalProblem}
}

// problemFor returns the problem of the first catalogue entry err matches, or
// the internal-error problem.
func problemFor(err error) problem { return entryFor(err).p }
