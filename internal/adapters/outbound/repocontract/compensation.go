package repocontract

import (
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/compensation"
)

// testCompensationQueue pins the write side of the durable compensation
// queue: a cancel is one row, a replayed checkout does not duplicate it and
// enqueueing nothing is a no-op (A14).
func testCompensationQueue(t *testing.T, e Env) {
	first := compensation.Cancel{OrderID: "co-1", GroupNo: 1, PromiseID: "pc-1"}
	must(t, e.Compensations.Enqueue(bg(), first))
	if got := e.CompensationCount(t); got != 1 {
		t.Fatalf("rows = %d, want 1", got)
	}
	// The same failed checkout enqueued its cancel again (a replay, or the
	// row is still pending): no duplicate, the retry count is not reset.
	must(t, e.Compensations.Enqueue(bg(), first))
	if got := e.CompensationCount(t); got != 1 {
		t.Fatalf("a re-enqueued cancel must not duplicate its row, rows = %d", got)
	}
	must(t, e.Compensations.Enqueue(bg()))
	if got := e.CompensationCount(t); got != 1 {
		t.Fatalf("enqueueing nothing is a no-op, rows = %d", got)
	}
	second := compensation.Cancel{OrderID: "co-2", GroupNo: 3, PromiseID: "pc-2"}
	must(t, e.Compensations.Enqueue(bg(), first, second))
	if got := e.CompensationCount(t); got != 2 {
		t.Fatalf("a distinct cancel gets its own row, rows = %d", got)
	}
}
