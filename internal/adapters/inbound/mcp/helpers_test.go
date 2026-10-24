package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/customer-ordering/internal/adapters/inbound/mcp"
	"github.com/claudioed/customer-ordering/internal/adapters/outbound/memory"
	"github.com/claudioed/customer-ordering/internal/application/readmodel"
	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
	"github.com/claudioed/customer-ordering/internal/domain/shipto"
)

// t0 is the fixture's fixed "now".
var t0 = time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)

// at is t0 plus seconds, for the promise windows of the fixture order.
func at(seconds int) time.Time { return t0.Add(time.Duration(seconds) * time.Second) }

// address is the synthetic ship-to of the fixture order.
func address() shipto.ShipTo {
	return shipto.ShipTo{
		Name: "Ana Example", Line1: "1 Sample Street", Line2: "Apt 2", City: "Austin", Region: "TX",
		PostalCode: "78701", CountryCode: "US", Phone: "+1 555 0100",
	}
}

// harness is a connected in-memory MCP client over the real server wired to
// in-memory repositories, exactly as cmd/mcp wires them (minus Postgres). The
// write side (the domain Place that the REST adapter's PlaceOrder drives)
// exists ONLY here, to seed data the way checkout would: the MCP surface
// itself has none.
type harness struct {
	session *sdk.ClientSession
	store   *memory.Store
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store := memory.NewStore(memory.NewClock(t0))
	deps := inboundmcp.NewDeps(store.Orders(), store.Tracking())
	return &harness{session: connectSession(t, deps), store: store}
}

func connectSession(t *testing.T, deps inboundmcp.Deps) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := inboundmcp.NewServer(deps).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// groupInput is one confirmed promise of the fixture checkout.
func groupInput(no int, promise string, lineNos ...int) customerorder.GroupInput {
	return customerorder.GroupInput{
		GroupNo: no, PromiseID: promise, Tier: customerorder.TierNextDay, WitnessNodeID: "fc-aus-1", ShipBy: at(600),
		DeliverByEarliest: at(1200), DeliverByLatest: at(1500), LineNos: lineNos, DestinationPostalPrefix: "787",
	}
}

// place is what checkout's PlaceOrder does to the domain: it places the
// order; the repository Save is what persists it.
func place(t *testing.T, id string) *customerorder.CustomerOrder {
	t.Helper()
	o, _, err := customerorder.Place(customerorder.NewOrder{
		ID: customerorder.ID(id), ShopperID: "shopper-1", ShipTo: address(),
		Lines: []customerorder.OrderLine{
			{LineNo: 1, CatalogItemID: "sku-100", Quantity: 2},
			{LineNo: 2, CatalogItemID: "sku-200", Quantity: 1},
		},
		Groups: []customerorder.GroupInput{groupInput(1, "p-"+id+"-1", 1), groupInput(2, "p-"+id+"-2", 2)},
	}, t0)
	must(t, err)
	return o
}

// seedOrder places and stores an order the way the REST adapter's PlaceOrder
// would (domain Place through the repository), so the read tools have data.
func (h *harness) seedOrder(t *testing.T, id string) *customerorder.CustomerOrder {
	t.Helper()
	o := place(t, id)
	must(t, h.store.Orders().Save(t.Context(), o))
	return o
}

// seedTracking gives group 1 of the order one label, one scan and an at-risk
// promise verdict, the way the shipping-and-tracking consumer would.
func (h *harness) seedTracking(t *testing.T, orderID string) {
	t.Helper()
	ctx := t.Context()
	tracking := h.store.Tracking()
	assignment := "as-" + orderID + "-1"
	must(t, tracking.LinkAssignment(ctx, readmodel.AssignmentLink{AssignmentID: assignment, OrderID: orderID, GroupNo: 1}))
	must(t, tracking.ApplyLabel(ctx, readmodel.Label{TrackingNumber: "TRK-100", AssignmentID: assignment, Carrier: "CARRIER-1", At: at(700)}))
	must(t, tracking.ApplyScan(ctx, readmodel.Scan{TrackingNumber: "TRK-100", NodeID: "fc-aus-1", State: "IN_TRANSIT", At: at(800)}))
	verdict, late := "AT_RISK", 42
	must(t, tracking.PatchGroupSignal(ctx, readmodel.GroupSignalPatch{
		OrderID: orderID, GroupNo: 1, PromiseVerdict: &verdict, LateByMinutes: &late,
	}))
}

// call invokes a tool and fails the test on a TRANSPORT/protocol error;
// tool-level failures come back as res.IsError.
func call(t *testing.T, session *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: transport/protocol error (want a tool result): %v", name, err)
	}
	return res
}

// ok calls a tool, requires success, and returns its structured content.
func (h *harness) ok(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	res := call(t, h.session, name, args)
	if res.IsError {
		t.Fatalf("%s returned a tool error: %s", name, text(res))
	}
	sc, isMap := res.StructuredContent.(map[string]any)
	if !isMap {
		t.Fatalf("%s: structured content = %#v, want an object", name, res.StructuredContent)
	}
	return sc
}

// failWith calls a tool and requires an isError result whose text starts
// with the slug want.
func failWith(t *testing.T, session *sdk.ClientSession, name string, args map[string]any, want string) {
	t.Helper()
	res := call(t, session, name, args)
	if !res.IsError {
		t.Fatalf("%s: expected an isError tool result, got %#v", name, res.StructuredContent)
	}
	if got := text(res); !strings.HasPrefix(got, want+": ") {
		t.Fatalf("%s: error text %q does not start with %q", name, got, want+": ")
	}
}

func text(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, isText := c.(*sdk.TextContent); isText {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
