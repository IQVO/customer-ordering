package mcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// triageOrderStatusSOP is the operator standard-operating-procedure the
// triage_order_status prompt hands to the model (Agentic Standard v2, AS-3).
// Per the charter, prompts encode how to interpret results and what "done"
// means; this one names tools but NEVER calls them, and it cannot write:
// every tool it names is read-only (ADR 0005).
const triageOrderStatusSOP = `You are triaging one customer's question about their order ORDERID. Use only the MCP tools of customer-ordering; never assume state — read it.

Procedure:
1. Call get_order(orderId = ORDERID) to read the order itself: its status (PLACED, IN_FULFILLMENT, PARTIALLY_SHIPPED, SHIPPED, PARTIALLY_DELIVERED, DELIVERED, LOST, RETURNED or CANCELLED), the shopper, when it was placed, the cart lines, and every shipment group with its promise (promiseId, tier, shipBy, deliverByEarliest, deliverByLatest) and any cancellation request or refusal.
2. Call get_order_tracking(orderId = ORDERID) to see how it is travelling: per group the status (PROMISED, LOCKED, SHIPPED, DELIVERED, LOST, RETURNED, CANCELLATION_REQUESTED or CANCELLED), the at-risk and unsourceable signals, the promise verdict and how many minutes late it ran, and each package's carrier, state, last scan node and time.
3. Never call get_ship_to for a status question: the ship-to address is synthetic PII this context serves only for label printing; locating or reassuring a customer never needs it.

Interpretation:
- A group is on track when its status is DELIVERED inside the window, or SHIPPED/LOCKED with no at-risk signal and a scan inside the promise window.
- An AT_RISK verdict with lateByMinutes is a lateness fact, not a failure: report the window and the minutes.
- A LOST or RETURNED group carries lostAt/returnedAt and the outcome package — that is a recovery conversation, not a delivery update.
- A CANCELLATION_REQUESTED group with a cancelRefusedReason means the cancellation was refused and the group still ships.

Escalate to a human when: a group is LOST, a promise verdict says the window will be missed by a lot, or the customer asks to change, cancel or redirect the order — placing, cancelling and changing orders are REST operations of customer-ordering, not something this surface does.

Done means: you have named the order's status, each group's promise window and where its packages are, flagged any at-risk, lost or refused-cancellation facts with their timestamps, and said clearly what remains to happen.`

// registerPrompts adds the workflow prompt (the operator SOP of this
// context). Read-only by construction: a prompt returns text.
func (d Deps) registerPrompts(server *mcp.Server) {
	server.AddPrompt(&mcp.Prompt{
		Name:        "triage_order_status",
		Description: "Standard operating procedure for answering a customer's question about one order: read the order, read its tracking, interpret the promise verdicts and signals, and know what to escalate.",
		Arguments: []*mcp.PromptArgument{{
			Name:        "orderId",
			Description: "the order the question is about, e.g. ord-3f9c1a2b",
			Required:    true,
		}},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		id := strings.TrimSpace(req.Params.Arguments["orderId"])
		if id == "" {
			id = "<orderId>"
		}
		return &mcp.GetPromptResult{
			Description: "How to triage an order question: read the order, read the tracking, interpret the verdicts, escalate the exceptions.",
			Messages: []*mcp.PromptMessage{{
				Role:    "user",
				Content: &mcp.TextContent{Text: strings.ReplaceAll(triageOrderStatusSOP, "ORDERID", id)},
			}},
		}, nil
	})
}
