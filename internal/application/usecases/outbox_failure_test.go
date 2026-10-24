package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// failingOutbox makes every Append (or the first n) fail with an injected
// infrastructure error; the rest of the store keeps working.
type failingOutbox struct {
	ports.EventOutbox
	err       error
	failures  int
	appendsOK int
}

func (o *failingOutbox) Append(ctx context.Context, events ...shared.Event) error {
	if o.failures > 0 {
		o.failures--
		return o.err
	}
	o.appendsOK++
	return o.EventOutbox.Append(ctx, events...)
}

// A failing outbox append is NOT a concurrent update (no retry) and never a
// success: the command fails with the infrastructure error wrapped, so the
// unit of work rolls the aggregate save back with it (transactional outbox).
func TestAFailingOutboxAppendFailsTheCommand(t *testing.T) {
	f := newFx(t)
	id := f.place()
	injected := errors.New("outbox insert: disk full")
	fo := &failingOutbox{EventOutbox: f.deps.Outbox, err: injected, failures: 1}
	f.deps.Outbox = fo
	before := len(f.store.Outbox().Events())
	_, err := usecases.CancelOrder{Deps: f.deps}.Execute(context.Background(), usecases.CancelOrderInput{OrderID: customerorder.ID(id), Reason: "changed my mind"})
	if !errors.Is(err, injected) {
		t.Fatalf("Execute = %v, want the outbox error passed through", err)
	}
	if fo.appendsOK != 0 {
		t.Fatalf("Append succeeded %d times, want 0", fo.appendsOK)
	}
	if after := len(f.store.Outbox().Events()); after != before {
		t.Fatalf("outbox grew from %d to %d events, want no growth", before, after)
	}
}
