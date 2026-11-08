// The MCP surface beyond tools (Agentic Standard v2, AS-3): ONE resource —
// the scoped order-tracking read model — and ONE prompt — this context's
// operator SOP. Pinned through the same in-memory client the tool tests use.
package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The resource template is advertised with the fleet URI shape and reads the
// same read model as the get_order_tracking tool.
func TestSurface_ResourceTemplateListedAndRead(t *testing.T) {
	h := newHarness(t)
	h.seedOrder(t, "ord-1")
	h.seedTracking(t, "ord-1")
	ctx := t.Context()

	listed, err := h.session.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatalf("list resource templates: %v", err)
	}
	found := false
	for _, rt := range listed.ResourceTemplates {
		if rt.URITemplate == "warehouse://customer-ordering/orders/{orderId}/tracking" {
			found = true
			if rt.MIMEType != "application/json" {
				t.Errorf("template MIMEType = %q, want application/json", rt.MIMEType)
			}
		}
	}
	if !found {
		t.Fatalf("order-tracking resource template not advertised: %+v", listed.ResourceTemplates)
	}

	res, err := h.session.ReadResource(ctx, &sdk.ReadResourceParams{
		URI: "warehouse://customer-ordering/orders/ord-1/tracking",
	})
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if len(res.Contents) != 1 {
		t.Fatalf("contents = %d entries, want 1", len(res.Contents))
	}
	c := res.Contents[0]
	if c.URI != "warehouse://customer-ordering/orders/ord-1/tracking" {
		t.Errorf("content URI = %q", c.URI)
	}
	var tracking struct {
		OrderID string `json:"orderId"`
		Status  string `json:"status"`
		Groups  []struct {
			GroupNo        int    `json:"groupNo"`
			PromiseID      string `json:"promiseId"`
			Status         string `json:"status"`
			PromiseVerdict string `json:"promiseVerdict,omitempty"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(c.Text), &tracking); err != nil {
		t.Fatalf("resource text is not the tracking JSON: %v (%s)", err, c.Text)
	}
	if tracking.OrderID != "ord-1" || len(tracking.Groups) != 2 {
		t.Fatalf("tracking = %+v, want order ord-1 with 2 groups", tracking)
	}
	if tracking.Groups[0].PromiseVerdict != "AT_RISK" {
		t.Errorf("group 1 promiseVerdict = %q, want AT_RISK (the seeded signal)", tracking.Groups[0].PromiseVerdict)
	}
}

// A malformed URI and an unknown order are errors, never silent defaults.
func TestSurface_ResourceRejectsMalformedAndUnknown(t *testing.T) {
	h := newHarness(t)
	h.seedOrder(t, "ord-1")
	for _, uri := range []string{
		"staffing://customer-ordering/orders/ord-1/tracking",              // wrong scheme
		"warehouse://workforce/orders/ord-1/tracking",                     // wrong context
		"warehouse://customer-ordering/ship-to/ord-1",                     // wrong model
		"warehouse://customer-ordering/orders/",                           // empty orderId
		"warehouse://customer-ordering/orders/ord-1",                      // missing /tracking
		"warehouse://customer-ordering/orders/ord-1/tracking/extra",       // trailing segment
		"warehouse://customer-ordering/orders/ord-nope/tracking",          // unknown order
		"warehouse://customer-ordering/orders/" + strings.Repeat("x", 65), // over-long id
	} {
		if _, err := h.session.ReadResource(t.Context(), &sdk.ReadResourceParams{URI: uri}); err == nil {
			t.Errorf("read resource %q: expected an error", uri)
		}
	}
}

// The prompt is advertised with its typed argument and returns the SOP text.
func TestSurface_PromptListedAndGet(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()

	listed, err := h.session.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(listed.Prompts) != 1 || listed.Prompts[0].Name != "triage_order_status" {
		t.Fatalf("prompts = %+v, want exactly triage_order_status", listed.Prompts)
	}
	if len(listed.Prompts[0].Arguments) != 1 || listed.Prompts[0].Arguments[0].Name != "orderId" || !listed.Prompts[0].Arguments[0].Required {
		t.Fatalf("prompt arguments = %+v, want one required orderId", listed.Prompts[0].Arguments)
	}

	res, err := h.session.GetPrompt(ctx, &sdk.GetPromptParams{
		Name:      "triage_order_status",
		Arguments: map[string]string{"orderId": "ord-3f9c1a2b"},
	})
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(res.Messages))
	}
	text := res.Messages[0].Content.(*sdk.TextContent).Text
	for _, needle := range []string{"get_order", "get_order_tracking", "get_ship_to", "ord-3f9c1a2b"} {
		if !strings.Contains(text, needle) {
			t.Errorf("SOP text does not mention %q", needle)
		}
	}

	// A missing argument degrades to the placeholder, never to a hidden call.
	noArg, err := h.session.GetPrompt(ctx, &sdk.GetPromptParams{Name: "triage_order_status"})
	if err != nil {
		t.Fatalf("get prompt without arguments: %v", err)
	}
	if plain := noArg.Messages[0].Content.(*sdk.TextContent).Text; !strings.Contains(plain, "<orderId>") {
		t.Errorf("SOP without an argument should carry the <orderId> placeholder")
	}
}
