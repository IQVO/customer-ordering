// Package compensation defines the durable record of one promise cancel a
// failed checkout could not perform (A14): the compensating CancelPromise call
// errored, so the cancel is enqueued here and a background worker retries it
// until delivery-promise releases the hold. It lives in the application layer
// because the use case that enqueues and the worker adapter that retries both
// speak it; it carries no Postgres types.
package compensation

import (
	"fmt"
	"strconv"
)

// Cancel is one promise cancel to retry: the identity of the confirmed
// promise whose capacity hold must be released at delivery-promise.
type Cancel struct {
	OrderID   string
	GroupNo   int
	PromiseID string
}

// RetryFailureError is the error a queue's Drain returns when the retry of one
// row failed: the pass stopped there, the failure was recorded on the row and
// Attempts is the row's failure count AFTER this one.
type RetryFailureError struct {
	Cancel   Cancel
	Attempts int
	Err      error
}

// Error keeps the "compensation cancel <promise> of <order>/<group>: <cause>"
// wording runbooks grep for.
func (f *RetryFailureError) Error() string {
	return fmt.Sprintf("compensation cancel %s of %s/%d: %v", f.Cancel.PromiseID, f.Cancel.OrderID, f.Cancel.GroupNo, f.Err)
}

// Unwrap exposes the retry error.
func (f *RetryFailureError) Unwrap() error { return f.Err }

// GroupNoText is the group number as text, for logs and metrics.
func (c Cancel) GroupNoText() string { return strconv.Itoa(c.GroupNo) }
