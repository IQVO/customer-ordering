package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/claudioed/customer-ordering/internal/adapters/outbound/memory"
	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
)

var t0 = time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)

func at(seconds int) time.Time { return t0.Add(time.Duration(seconds) * time.Second) }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// seededStore holds one placed order, ord-1, with two shipment groups and a
// tracked package on group 1, built through the real aggregate. The write side
// (the domain Place the REST adapter's PlaceOrder drives) exists ONLY here, to
// seed data the way checkout would: the A2A adapter itself has none.
func seededStore(t *testing.T) *memory.Store {
	t.Helper()
	store := memory.NewStore(memory.NewClock(t0))
	group := func(no int, promise string, lineNos ...int) customerorder.GroupInput {
		return customerorder.GroupInput{
			GroupNo: no, PromiseID: promise, Tier: customerorder.TierNextDay, WitnessNodeID: "fc-aus-1", ShipBy: at(600),
			DeliverByEarliest: at(1200), DeliverByLatest: at(1500), LineNos: lineNos, DestinationPostalPrefix: "787",
		}
	}
	o, _, err := customerorder.Place(customerorder.NewOrder{
		ID: "ord-1", ShopperID: "shopper-1",
		ShipTo: shipto.ShipTo{
			Name: "Ana Example", Line1: "1 Sample Street", Line2: "Apt 2", City: "Austin", Region: "TX",
			PostalCode: "78701", CountryCode: "US", Phone: "+1 555 0100",
		},
		Lines:  []customerorder.OrderLine{{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2}, {LineNo: 2, CatalogItemID: "sku-200", Quantity: 1}},
		Groups: []customerorder.GroupInput{group(1, "p-1", 1), group(2, "p-2", 2)},
	}, t0)
	must(t, err)
	ctx := context.Background()
	must(t, store.Orders().Save(ctx, o))

	tracking := store.Tracking()
	must(t, tracking.LinkAssignment(ctx, readmodel.AssignmentLink{AssignmentID: "as-1", OrderID: "ord-1", GroupNo: 1}))
	must(t, tracking.ApplyLabel(ctx, readmodel.Label{TrackingNumber: "TRK-100", AssignmentID: "as-1", Carrier: "CARRIER-1", At: at(700)}))
	must(t, tracking.ApplyScan(ctx, readmodel.Scan{TrackingNumber: "TRK-100", NodeID: "fc-aus-1", State: "IN_TRANSIT", At: at(800)}))
	verdict, late := "AT_RISK", 42
	must(t, tracking.PatchGroupSignal(ctx, readmodel.GroupSignalPatch{OrderID: "ord-1", GroupNo: 1, PromiseVerdict: &verdict, LateByMinutes: &late}))
	return store
}

func depsOver(store *memory.Store) Deps { return NewDeps(store.Orders(), store.Tracking()) }

// ---- the wire: JSON-RPC over HTTP, driven by the SDK's own client ----------

// wireEnvelope is a TEST-LOCAL copy of the answer contract. If the adapter's
// envelope drifts, this suite fails instead of silently following it.
type wireEnvelope struct {
	Skill string `json:"skill"`
	Data  *struct {
		Order    *wireOrder    `json:"order"`
		Tracking *wireTracking `json:"tracking"`
	} `json:"data"`
	Problem *struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Status int    `json:"status"`
	} `json:"problem"`
}

type wireOrder struct {
	OrderID                 string `json:"orderId"`
	ShopperID               string `json:"shopperId"`
	Status                  string `json:"status"`
	DestinationPostalPrefix string `json:"destinationPostalPrefix"`
	PlacedAt                string `json:"placedAt"`
	Lines                   []struct {
		LineNo        int    `json:"lineNo"`
		CatalogItemID string `json:"catalogItemId"`
		Quantity      int    `json:"quantity"`
	} `json:"lines"`
	Groups []struct {
		GroupNo           int    `json:"groupNo"`
		PromiseID         string `json:"promiseId"`
		Tier              string `json:"tier"`
		ShipBy            string `json:"shipBy"`
		DeliverByEarliest string `json:"deliverByEarliest"`
		DeliverByLatest   string `json:"deliverByLatest"`
		LineNos           []int  `json:"lineNos"`
		Status            string `json:"status"`
	} `json:"groups"`
}

type wireTrackedGroup struct {
	GroupNo        int    `json:"groupNo"`
	PromiseID      string `json:"promiseId"`
	Status         string `json:"status"`
	PromiseVerdict string `json:"promiseVerdict"`
	LateByMinutes  *int   `json:"lateByMinutes"`
	Packages       []struct {
		TrackingNumber string `json:"trackingNumber"`
		Carrier        string `json:"carrier"`
		State          string `json:"state"`
		LastScanNodeID string `json:"lastScanNodeId"`
	} `json:"packages"`
}

type wireTracking struct {
	OrderID string             `json:"orderId"`
	Status  string             `json:"status"`
	Groups  []wireTrackedGroup `json:"groups"`
}

// answer is one decoded reply: the envelope, the text summary and every
// string the agent put on the wire (for leak checks).
type answerOf struct {
	env  wireEnvelope
	text string
	raw  string
}

type wire struct{ client *a2aclient.Client }

func newWire(t *testing.T, deps Deps) *wire {
	t.Helper()
	srv := httptest.NewServer(Handler(NewExecutor(deps)))
	t.Cleanup(srv.Close)
	card := Card(Config{AgentURL: srv.URL + "/", Version: "test"})
	client, err := a2aclient.NewFromCard(context.Background(), card)
	if err != nil {
		t.Fatalf("NewFromCard: %v", err)
	}
	return &wire{client: client}
}

// call sends one message part and returns the decoded reply.
func (w *wire) call(t *testing.T, part *a2a.Part) answerOf {
	t.Helper()
	resp, err := w.client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) < 2 {
		t.Fatalf("reply = %T, want a message with a data and a text part", resp)
	}
	data, ok := msg.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("first reply part is %T, want data", msg.Parts[0].Content)
	}
	raw, _ := json.Marshal(data.Value)
	var env wireEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("reply is not the skill envelope: %v (%s)", err, raw)
	}
	text, _ := msg.Parts[1].Content.(a2a.Text)
	return answerOf{env: env, text: string(text), raw: string(raw) + "\n" + string(text)}
}

func (w *wire) skill(t *testing.T, skill string, args map[string]any) answerOf {
	t.Helper()
	return w.call(t, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))
}

func (a answerOf) wantProblem(t *testing.T, status int, slug string) {
	t.Helper()
	if a.env.Problem == nil {
		t.Fatalf("expected a %d problem, got data: %s", status, a.raw)
	}
	if a.env.Problem.Status != status || a.env.Problem.Type != problemTypeBase+slug {
		t.Fatalf("problem = %+v, want %d %s", a.env.Problem, status, slug)
	}
	if a.env.Data != nil {
		t.Fatalf("a problem answer must carry no data: %s", a.raw)
	}
}

func TestWireGetOrderReturnsTheOrder(t *testing.T) {
	w := newWire(t, depsOver(seededStore(t)))

	a := w.skill(t, skillGetOrder, map[string]any{"orderId": "ord-1"})
	o := (*wireOrder)(nil)
	if a.env.Data != nil {
		o = a.env.Data.Order
	}
	if a.env.Problem != nil || o == nil || a.env.Skill != skillGetOrder {
		t.Fatalf("envelope = %s", a.raw)
	}
	checkOrderHeader(t, o)
	checkOrderGroups(t, o)
	if a.text != "ord-1: PLACED, 2 lines in 2 shipment groups" {
		t.Fatalf("summary = %q", a.text)
	}
}

func checkOrderHeader(t *testing.T, o *wireOrder) {
	t.Helper()
	if o.OrderID != "ord-1" || o.ShopperID != "shopper-1" || o.Status != "PLACED" || o.DestinationPostalPrefix != "787" ||
		o.PlacedAt != "2026-10-24T10:00:00Z" || len(o.Lines) != 2 || o.Lines[0].Quantity != 2 || o.Lines[0].CatalogItemID != "sku-100" {
		t.Fatalf("order = %+v", o)
	}
}

func checkOrderGroups(t *testing.T, o *wireOrder) {
	t.Helper()
	if len(o.Groups) != 2 || o.Groups[0].PromiseID != "p-1" || o.Groups[0].Tier != "NEXT_DAY" ||
		o.Groups[0].ShipBy != "2026-10-24T10:10:00Z" || o.Groups[0].DeliverByLatest != "2026-10-24T10:25:00Z" ||
		len(o.Groups[0].LineNos) != 1 || o.Groups[0].LineNos[0] != 1 || o.Groups[1].PromiseID != "p-2" {
		t.Fatalf("groups = %+v", o.Groups)
	}
}

func TestWireGetOrderTrackingReturnsTheTracking(t *testing.T) {
	w := newWire(t, depsOver(seededStore(t)))

	a := w.skill(t, skillGetOrderTracking, map[string]any{"orderId": "ord-1"})
	tr := (*wireTracking)(nil)
	if a.env.Data != nil {
		tr = a.env.Data.Tracking
	}
	if a.env.Problem != nil || tr == nil || a.env.Skill != skillGetOrderTracking {
		t.Fatalf("envelope = %s", a.raw)
	}
	if tr.OrderID != "ord-1" || tr.Status != "PLACED" || len(tr.Groups) != 2 {
		t.Fatalf("tracking = %+v", tr)
	}
	checkTrackedGroup(t, tr.Groups[0])
	if tr.Groups[1].Packages == nil || len(tr.Groups[1].Packages) != 0 {
		t.Fatalf("a group without packages must carry an empty array, got %+v", tr.Groups[1].Packages)
	}
	if a.text != "ord-1 tracking: PLACED, 2 shipment groups (2 PROMISED)" {
		t.Fatalf("summary = %q", a.text)
	}
}

// The synthetic ship-to address has one exit in this service (REST, label
// printing). No answer of this agent, data or text, may carry any part of it.
func TestWireNeverLeaksTheShipTo(t *testing.T) {
	w := newWire(t, depsOver(seededStore(t)))

	for _, skill := range []string{skillGetOrder, skillGetOrderTracking} {
		a := w.skill(t, skill, map[string]any{"orderId": "ord-1"})
		for _, pii := range []string{"Ana Example", "Sample Street", "Apt 2", "Austin", "78701", "555 0100", "shipTo", "ship-to"} {
			if strings.Contains(a.raw, pii) {
				t.Errorf("%s answer leaks the ship-to (%q): %s", skill, pii, a.raw)
			}
		}
	}
}

// An unknown order is a 404 answered in-band with the type and status the
// REST adapter gives GET /orders/{orderId}.
func TestWireUnknownOrderIsAnsweredAs404NotRaised(t *testing.T) {
	w := newWire(t, depsOver(seededStore(t)))

	for _, skill := range []string{skillGetOrder, skillGetOrderTracking} {
		t.Run(skill, func(t *testing.T) {
			a := w.skill(t, skill, map[string]any{"orderId": "ord-ghost"})
			a.wantProblem(t, 404, "not-found")
			if a.env.Skill != skill || a.env.Problem.Detail != `no order exists with orderId "ord-ghost"` {
				t.Fatalf("envelope = %s", a.raw)
			}
			// The shared sentinel's text is bare "not found"; the adapter owns the answer text.
			if strings.Contains(a.text, "usecases") || !strings.HasPrefix(a.text, "Not found (404): ") {
				t.Fatalf("summary = %q", a.text)
			}
		})
	}
}

// The REST adapter's id guard: an id longer than 64 characters can never name
// an order (404, not a server error); 64 is still a plain lookup.
func TestWireOrderIDLengthGuardMirrorsREST(t *testing.T) {
	w := newWire(t, depsOver(seededStore(t)))

	long := strings.Repeat("a", 65)
	for _, skill := range []string{skillGetOrder, skillGetOrderTracking} {
		a := w.skill(t, skill, map[string]any{"orderId": long})
		a.wantProblem(t, 404, "not-found")
		if strings.Contains(a.raw, long) {
			t.Fatalf("the over-long id is echoed back: %s", a.raw)
		}
		w.skill(t, skill, map[string]any{"orderId": strings.Repeat("a", 64)}).wantProblem(t, 404, "not-found")
	}
}

// failingOrders makes every order read fail, standing in for a database outage.
type failingOrders struct{ ports.OrderRepository }

var errBoom = errors.New("pgx: connection refused to 10.0.0.7:5432 password=hunter2")

func (failingOrders) Get(context.Context, customerorder.ID) (*customerorder.CustomerOrder, error) {
	return nil, errBoom
}

// An unexpected failure is answered as a 500 problem, never a JSON-RPC error,
// and its raw text (which may carry infrastructure detail) is not echoed.
func TestWireUnexpectedFailureIs500AndDoesNotLeakTheCause(t *testing.T) {
	store := seededStore(t)
	w := newWire(t, NewDeps(failingOrders{store.Orders()}, store.Tracking()))

	for _, skill := range []string{skillGetOrder, skillGetOrderTracking} {
		t.Run(skill, func(t *testing.T) {
			a := w.skill(t, skill, map[string]any{"orderId": "ord-1"})
			a.wantProblem(t, 500, "internal-error")
			if strings.Contains(a.raw, "hunter2") || strings.Contains(a.raw, "10.0.0.7") {
				t.Fatalf("a 500 answer leaks the underlying error: %s", a.raw)
			}
		})
	}
}

func TestWireArgumentValidation(t *testing.T) {
	w := newWire(t, depsOver(seededStore(t)))
	cases := map[string]map[string]any{
		"missing orderId":    {},
		"blank orderId":      {"orderId": "   "},
		"empty orderId":      {"orderId": ""},
		"orderId wrong type": {"orderId": 7},
		"unknown argument":   {"orderId": "ord-1", "force": true},
		"typo'd argument":    {"orderID": "ord-1", "orderIdd": "ord-1"},
		"ship-to argument":   {"orderId": "ord-1", "includeShipTo": true},
		"NUL in orderId":     {"orderId": "ord-\u0000-1"},
	}
	for _, skill := range []string{skillGetOrder, skillGetOrderTracking} {
		for name, args := range cases {
			t.Run(skill+": "+name, func(t *testing.T) {
				w.skill(t, skill, args).wantProblem(t, 400, "invalid-argument")
			})
		}
	}
}

func TestWireRequestShapeProblems(t *testing.T) {
	w := newWire(t, depsOver(seededStore(t)))

	w.call(t, a2a.NewTextPart("not json at all")).wantProblem(t, 400, "malformed-skill-request")
	w.call(t, a2a.NewDataPart(map[string]any{"arguments": map[string]any{}})).wantProblem(t, 400, "malformed-skill-request")
	w.call(t, a2a.NewDataPart(map[string]any{"skill": skillGetOrder, "arguments": map[string]any{"orderId": "ord-1"}, "extra": 1})).
		wantProblem(t, 400, "malformed-skill-request")
	// Trailing data after the JSON document is refused too.
	w.call(t, a2a.NewTextPart(`{"skill":"get_order","arguments":{"orderId":"ord-1"}} {"skill":"get_order_tracking"}`)).
		wantProblem(t, 400, "malformed-skill-request")
	w.skill(t, "launch_rocket", map[string]any{}).wantProblem(t, 400, "unknown-skill")

	// A text part holding the same JSON is accepted (documented contract).
	a := w.call(t, a2a.NewTextPart(`{"skill":"get_order","arguments":{"orderId":"ord-1"}}`))
	if a.env.Problem != nil || a.env.Data == nil || a.env.Data.Order == nil || a.env.Data.Order.OrderID != "ord-1" {
		t.Fatalf("a text part holding the skill JSON must be accepted, got %s", a.raw)
	}
}

// Placing, cancelling, registering a shopper and the restricted ship-to read
// are not offered: asking for one is an unknown skill, answered in-band, and
// nothing is written.
func TestWireOffersNeitherWritesNorTheShipToRead(t *testing.T) {
	store := seededStore(t)
	w := newWire(t, depsOver(store))
	before := store.Counts()

	for _, skill := range []string{"place_order", "checkout", "cancel_order", "register_shopper", "get_ship_to"} {
		w.skill(t, skill, map[string]any{"orderId": "ord-1"}).wantProblem(t, 400, "unknown-skill")
	}
	if after := store.Counts(); after != before {
		t.Fatalf("store counts %+v -> %+v: an unknown skill name must write nothing", before, after)
	}
}

// Reads add no outbox row, no inbox claim and no idempotency record.
func TestWireReadsWriteNothing(t *testing.T) {
	store := seededStore(t)
	w := newWire(t, depsOver(store))
	before := store.Counts()

	w.skill(t, skillGetOrder, map[string]any{"orderId": "ord-1"})
	w.skill(t, skillGetOrderTracking, map[string]any{"orderId": "ord-1"})
	w.skill(t, skillGetOrder, map[string]any{"orderId": "ord-ghost"})
	if after := store.Counts(); after != before {
		t.Fatalf("store counts %+v -> %+v: a read skill must write nothing", before, after)
	}
}

// TestCardIsServedAndDialable proves discovery end to end: the card handler
// serves the card at the well-known path and a client built from the FETCHED
// card reaches the agent.
func TestCardIsServedAndDialable(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/", Handler(NewExecutor(depsOver(seededStore(t)))))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(Card(Config{AgentURL: srv.URL + "/", Version: "test"})))

	res, err := http.Get(srv.URL + a2asrv.WellKnownAgentCardPath)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("card fetch = %v / %v", res, err)
	}
	defer func() { _ = res.Body.Close() }()
	var card a2a.AgentCard
	if err := json.NewDecoder(res.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	client, err := a2aclient.NewFromCard(context.Background(), &card)
	if err != nil {
		t.Fatal(err)
	}
	a := (&wire{client: client}).skill(t, skillGetOrder, map[string]any{"orderId": "ord-1"})
	if a.env.Data == nil || a.env.Data.Order == nil {
		t.Fatalf("a client built from the served card could not call the agent: %s", a.raw)
	}
}

// One span per skill call, carrying the a2a.skill attribute (the A2A twin of
// the MCP adapter's mcp.tool span).
func TestSkillCallsAreTraced(t *testing.T) {
	previous := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	w := newWire(t, depsOver(seededStore(t)))

	w.skill(t, skillGetOrderTracking, map[string]any{"orderId": "ord-1"})

	var found bool
	for _, span := range recorder.Ended() {
		if span.Name() != "a2a.skill get_order_tracking" {
			continue
		}
		for _, attr := range span.Attributes() {
			found = found || (string(attr.Key) == "a2a.skill" && attr.Value.AsString() == skillGetOrderTracking)
		}
	}
	if !found {
		t.Fatalf("no span %q with the a2a.skill attribute among %d spans", "a2a.skill get_order_tracking", len(recorder.Ended()))
	}
}

func checkTrackedGroup(t *testing.T, g wireTrackedGroup) {
	t.Helper()
	if g.PromiseVerdict != "AT_RISK" || g.LateByMinutes == nil || *g.LateByMinutes != 42 || len(g.Packages) != 1 ||
		g.Packages[0].TrackingNumber != "TRK-100" || g.Packages[0].Carrier != "CARRIER-1" || g.Packages[0].State != "IN_TRANSIT" ||
		g.Packages[0].LastScanNodeID != "fc-aus-1" {
		t.Fatalf("group 1 = %+v", g)
	}
}
