package usecases_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/memory"
	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
)

// A14: a confirm that fails after earlier groups were confirmed cancels those
// promises and fails the checkout with the confirm's own error.

func cancelCall(promise string, group int) memory.CancelCall {
	return memory.CancelCall{PromiseID: promise, OrderID: "order-1", GroupNo: group}
}

func assertCancelled(t *testing.T, got []memory.CancelCall, want ...memory.CancelCall) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("cancelled = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cancelled[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestALaterConfirmFailureCancelsTheConfirmedPromises(t *testing.T) {
	f := newFx(t)
	f.gw.ConfirmErr["p-2"] = ports.ErrPromiseOfferNotQuotable
	_, err := usecases.PlaceOrder{Deps: f.deps}.Execute(bg(), checkout("order-1"))
	var rq *usecases.RequoteRequired
	if !errors.As(err, &rq) {
		t.Fatalf("the checkout fails with the failed-confirm answer (re-quote), got %v", err)
	}
	assertCancelled(t, f.gw.Cancelled(), cancelCall("p-1", 1))
	if f.store.Counts().Orders != 0 || f.store.Counts().OutboxEvents != 0 {
		t.Fatalf("nothing may be stored or published: %+v", f.store.Counts())
	}
}

func TestTheFirstConfirmFailingCancelsNothing(t *testing.T) {
	f := newFx(t)
	f.gw.ConfirmErr["p-1"] = ports.ErrPromiseOfferNotQuotable
	if _, err := (usecases.PlaceOrder{Deps: f.deps}).Execute(bg(), checkout("order-1")); !errorsIs(err, ports.ErrPromiseOfferNotQuotable) {
		t.Fatalf("err = %v", err)
	}
	assertCancelled(t, f.gw.Cancelled())
	if got := len(f.gw.Confirmed()); got != 1 {
		t.Fatalf("the loop stops at the first failure, confirms = %d", got)
	}
}

func TestAnAmbiguousConfirmFailureAlsoCancelsTheFailedGroup(t *testing.T) {
	f := newFx(t)
	f.gw.ConfirmErr["p-2"] = ports.ErrPromiseUnavailable
	_, err := usecases.PlaceOrder{Deps: f.deps}.Execute(bg(), checkout("order-1"))
	if !errorsIs(err, ports.ErrPromiseUnavailable) {
		t.Fatalf("err = %v", err)
	}
	assertCancelled(t, f.gw.Cancelled(), cancelCall("p-1", 1), cancelCall("p-2", 2))
}

func TestACancelThatFailsIsLoggedAndTheCheckoutStillFailsWithTheConfirmError(t *testing.T) {
	f := newFx(t)
	var logs bytes.Buffer
	f.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	f.gw.ConfirmErr["p-2"] = ports.ErrPromiseCapacityExhausted
	f.gw.CancelErr["p-1"] = ports.ErrPromiseUnavailable
	_, err := usecases.PlaceOrder{Deps: f.deps}.Execute(bg(), checkout("order-1"))
	if !errorsIs(err, ports.ErrPromiseCapacityExhausted) || errorsIs(err, ports.ErrPromiseUnavailable) {
		t.Fatalf("the checkout fails with the confirm error, not the cancel error: %v", err)
	}
	assertCancelled(t, f.gw.Cancelled(), cancelCall("p-1", 1))
	out := logs.String()
	for _, want := range []string{"compensating cancel", "promiseId=p-1", "orderId=order-1", "groupNo=1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log lacks %q: %s", want, out)
		}
	}
}

func TestTheCompensationSurvivesACancelledRequest(t *testing.T) {
	f := newFx(t)
	f.gw.ConfirmErr["p-2"] = ports.ErrPromiseOfferNotQuotable
	ctx, cancel := context.WithCancel(bg())
	f.gw.OnConfirm = func(promiseID string) {
		if promiseID == "p-2" {
			cancel() // the client hangs up while the second confirm is in flight
		}
	}
	_, _ = usecases.PlaceOrder{Deps: f.deps}.Execute(ctx, checkout("order-1"))
	assertCancelled(t, f.gw.Cancelled(), cancelCall("p-1", 1))
	if f.gw.CancelCtxErr != nil {
		t.Fatalf("the cancel ran on a cancelled context: %v", f.gw.CancelCtxErr)
	}
}

func TestACheckoutThatSucceedsCancelsNothing(t *testing.T) {
	f := newFx(t)
	if _, err := (usecases.PlaceOrder{Deps: f.deps}).Execute(bg(), checkout("order-1")); err != nil {
		t.Fatal(err)
	}
	assertCancelled(t, f.gw.Cancelled())
}

func TestARefusedCancelIsALowKeyWarningNotAnError(t *testing.T) {
	f := newFx(t)
	var logs bytes.Buffer
	f.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	f.gw.ConfirmErr["p-2"] = ports.ErrPromiseUnavailable
	f.gw.CancelErr["p-2"] = ports.ErrPromiseNotCancellable // the confirm never applied: the promise is still PROVISIONAL
	_, err := usecases.PlaceOrder{Deps: f.deps}.Execute(bg(), checkout("order-1"))
	if !errorsIs(err, ports.ErrPromiseUnavailable) {
		t.Fatalf("err = %v", err)
	}
	assertCancelled(t, f.gw.Cancelled(), cancelCall("p-1", 1), cancelCall("p-2", 2))
	out := logs.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "nothing to release") || strings.Contains(out, "level=ERROR") {
		t.Fatalf("a refused cancel is a warning, never an error: %s", out)
	}
}
