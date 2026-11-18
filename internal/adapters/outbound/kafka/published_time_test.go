package kafka_test

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/memory"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/notice"
	"github.com/claudioed/customer-ordering/internal/domain/shared"
)

// noticeFromTheUseCase returns the ShopperNoticeDecided the consumer use case
// itself raises (a group shipped), exactly as it reaches the encoder: NOT a
// hand-built event with a header, which is how the golden files once hid a
// zero CloudEvents `time`.
func noticeFromTheUseCase(t *testing.T) shared.Event {
	t.Helper()
	ctx := context.Background()
	store := memory.NewStore(memory.NewClock(t0))
	o, _ := placedOrder(t)
	if err := store.Orders().Save(ctx, o); err != nil {
		t.Fatal(err)
	}
	c := usecases.Consumer{Deps: usecases.Deps{
		Orders: store.Orders(), Tracking: store.Tracking(), Policies: store.Policies(), Inbox: store.Inbox(),
		Outbox: store.Outbox(), UoW: store.UnitOfWork(), Clock: memory.NewClock(t0), IDs: &memory.SequentialIDs{},
	}}
	id := string(o.ID())
	meta := func(typ, evID string, m time.Duration) usecases.Meta {
		return usecases.Meta{EventID: evID, Type: typ, At: t0.Add(m)}
	}
	if err := c.ApplyFulfillmentLocked(ctx, meta("Locked", "ev-1", time.Hour), usecases.FulfillmentLocked{AssignmentID: "as-1", OrderID: id, GroupNo: 1, NodeID: "fc-aus-1"}); err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyFulfillmentShipped(ctx, meta("Shipped", "ev-2", 2*time.Hour), usecases.FulfillmentShipped{AssignmentID: "as-1", OrderID: id, GroupNo: 1, TrackingNumbers: []string{"TRK-1"}, ShippedAt: t0.Add(2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	for _, ev := range store.Outbox().Events() {
		if _, ok := ev.(notice.ShopperNoticeDecided); ok {
			return ev
		}
	}
	t.Fatal("the shipment decided no ShopperNoticeDecided")
	return nil
}

// publishedCatalogue is the table over EVERY event type the encoder publishes:
// each entry builds the event through its REAL producer (the aggregate or the
// use case), never by hand. TestCatalogueCoversEveryEncodedType ties it to the
// encoder's own type switch, so a new published type cannot skip the check.
func publishedCatalogue() map[string]func(t *testing.T) shared.Event {
	return map[string]func(t *testing.T) shared.Event{
		"CustomerOrderPlaced": func(t *testing.T) shared.Event {
			_, evs := placedOrder(t)
			return evs[0]
		},
		"CustomerOrderCancelled": func(t *testing.T) shared.Event {
			o, _ := placedOrder(t)
			evs, err := o.CancelOrder("changed my mind", t0.Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			return evs[0]
		},
		"ShopperNoticeDecided": noticeFromTheUseCase,
	}
}

// Every published event type carries a non-zero occurred-at, and it is the
// CloudEvents `time` of its message. A zero time marshals as 0001-01-01T00:00:00Z.
func TestEveryPublishedEventCarriesANonZeroTime(t *testing.T) {
	for name, build := range publishedCatalogue() {
		t.Run(name, func(t *testing.T) {
			ev := build(t)
			if reflect.TypeOf(ev).Name() != name {
				t.Fatalf("the %s entry produced a %T", name, ev)
			}
			if ev.OccurredAt().IsZero() {
				t.Fatalf("%s is raised with the zero occurred-at (no aggregate header): its CloudEvents time would be 0001-01-01T00:00:00Z", name)
			}
			var got ce.Event
			if err := json.Unmarshal(encodeOne(t, ev).Value, &got); err != nil {
				t.Fatal(err)
			}
			if got.Time().IsZero() || got.Time().Year() < 2000 || !got.Time().Equal(ev.OccurredAt()) {
				t.Fatalf("%s CloudEvents time = %s, want the event's occurred-at %s", name, got.Time().Format(time.RFC3339Nano), ev.OccurredAt().Format(time.RFC3339Nano))
			}
		})
	}
}

// The notice's CloudEvents time IS its decision instant, the decidedAt of the
// payload, not merely some non-zero instant.
func TestNoticeTimeIsTheDecisionInstant(t *testing.T) {
	ev := noticeFromTheUseCase(t).(notice.ShopperNoticeDecided)
	want := t0.Add(2 * time.Hour)
	if !ev.OccurredAt().Equal(want) || !ev.DecidedAt.Equal(want) {
		t.Fatalf("occurred-at = %s, decidedAt = %s, want both %s", ev.OccurredAt(), ev.DecidedAt, want)
	}
}

// encodedTypes parses encoder.go and returns the event types of payloadFor's
// type switch: what the encoder really publishes.
func encodedTypes(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "encoder.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "payloadFor" {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			if cc, ok := n.(*ast.CaseClause); ok {
				for _, e := range cc.List {
					if sel, ok := e.(*ast.SelectorExpr); ok {
						names = append(names, sel.Sel.Name)
					}
				}
			}
			return true
		})
	}
	sort.Strings(names)
	return names
}

func TestCatalogueCoversEveryEncodedType(t *testing.T) {
	var want []string
	for name := range publishedCatalogue() {
		want = append(want, name)
	}
	sort.Strings(want)
	if got := encodedTypes(t); len(got) == 0 || !reflect.DeepEqual(got, want) {
		t.Fatalf("the encoder publishes %v but the time table covers %v: add the new type to publishedCatalogue", got, want)
	}
}

// Keeps the compiler honest that the aggregate events satisfy shared.Event.
var _ = []shared.Event{customerorder.CustomerOrderPlaced{}, customerorder.CustomerOrderCancelled{}, notice.ShopperNoticeDecided{}}
