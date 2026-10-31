// Package a2a is the inbound Agent2Agent adapter: it exposes this bounded
// context to the agent ecosystem as a third driving adapter over the same
// application-layer read use cases the REST and MCP adapters use. It is built
// on the official A2A Go SDK (a2aproject/a2a-go) and speaks JSON-RPC 2.0 over
// HTTP, with the Agent Card served at the well-known path.
//
// Per docs/adr/0008-a2a-inbound-adapter.md, A2A is the DELEGATION channel: an
// agent client sends a goal-shaped skill request as a structured message part
// and this agent answers from its own state through its own use cases. It is
// not a data-sync channel: facts between contexts still travel exclusively as
// CloudEvents on Kafka (ADR 0004), published through the transactional outbox.
//
// This agent is READ-ONLY, exactly like the MCP adapter (ADR 0005): both
// skills read an order this context already holds. This package never writes,
// never calls another bounded context and contains no outbound A2A client.
// The synthetic ship-to address is deliberately NOT offered: REST restricts it
// to label printing and delegation is not that purpose (ADR 0008).
//
// Like the MCP adapter, this package depends inward on the application layer
// (use cases) and the domain only, never on an outbound adapter or the REST
// adapter (the ports package is imported for its ErrNotFound sentinel only,
// so the error vocabulary stays the same as REST). The composition root
// (cmd/a2a) wires the concrete repositories into the use cases. Domain
// structs never leak across the message boundary.
package a2a

import (
	"context"
	"iter"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/claudioed/customer-ordering/internal/application/ports"
	"github.com/claudioed/customer-ordering/internal/application/usecases"
)

// Config carries the deployment-specific identity of this agent: the absolute
// URL clients use to reach its JSON-RPC endpoint (advertised on the Agent
// Card) and the card version. It is supplied by the composition root from
// A2A_AGENT_URL; the card MUST advertise a URL the agent clients can actually
// dial (in-cluster: http://<release>-a2a.<ns>.svc.cluster.local:8093/).
type Config struct {
	AgentURL string
	Version  string
}

// Deps is everything the A2A skills need, injected by the composition root.
// It carries the SAME read use cases the REST and MCP adapters use and holds
// no write use case at all (docs/adr/0008); the adapter never constructs an
// outbound adapter itself. GetShipTo is intentionally absent.
type Deps struct {
	GetOrder         *usecases.GetOrder
	GetOrderTracking *usecases.GetOrderTracking
}

// NewDeps wires the two read use cases over the two read repositories they
// need. It is the only construction path the composition root needs: handing
// the repositories over this way keeps cmd/a2a from ever naming the shared
// usecases.Deps bundle, which also carries the write seams (a unit of work,
// the outbox, the promise gateway) no A2A binary may reach.
func NewDeps(orders ports.OrderRepository, tracking ports.TrackingRepository) Deps {
	deps := usecases.Deps{Orders: orders, Tracking: tracking}
	return Deps{
		GetOrder:         &usecases.GetOrder{Deps: deps},
		GetOrderTracking: &usecases.GetOrderTracking{Deps: deps},
	}
}

// Card builds this agent's Agent Card (ADR 0008). The surface is two curated,
// goal-shaped READ skills derived from the MCP tools get_order and
// get_order_tracking (ADR 0005): the MCP charter of this context is entirely
// read-only, so the agent is read-only too and never widens what the context
// exposes. The card declares no security schemes on purpose: this surface is
// unauthenticated by the same fleet-wide decision as REST and MCP; schemes
// are added here, not in a redesign, if that decision is ever reversed.
func Card(cfg Config) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name: "customer-ordering",
		Description: "Customer Ordering agent of the retail network: the read-only source of truth for placed customer orders. " +
			"It answers where an order stands (its status, cart lines and the promise of every shipment group) and how it is " +
			"travelling (per shipment group the promise window, verdict, at-risk signals and packages). It never places, cancels " +
			"or changes an order, it does not register shoppers, and it does not expose the ship-to address.",
		Version: cfg.Version,
		Provider: &a2a.AgentProvider{
			Org: "IQVO",
			URL: "https://github.com/IQVO/customer-ordering",
		},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(cfg.AgentURL, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"application/json", "text/plain"},
		DefaultOutputModes: []string{"application/json", "text/plain"},
		Capabilities:       a2a.AgentCapabilities{Streaming: false},
		Skills: []a2a.AgentSkill{
			{
				ID:   skillGetOrder,
				Name: "Look up a placed order",
				Description: "Find out where one customer order stands, by its orderId: its status (PLACED, IN_FULFILLMENT, PARTIALLY_SHIPPED, SHIPPED, " +
					"PARTIALLY_DELIVERED, DELIVERED, LOST, RETURNED or CANCELLED), the shopper, the destination postal prefix, when it was placed, the cart " +
					"lines and every shipment group with its promise (promiseId, tier, shipBy, deliverByEarliest, deliverByLatest), its status and revision " +
					"and any cancellation request or refusal. The ship-to address is not part of it. Answers a 404 problem when no order has that id. Persists nothing.",
				Tags:       []string{"order", "customer-ordering", "read"},
				InputModes: []string{"application/json"},
				Examples: []string{
					`{"skill":"get_order","arguments":{"orderId":"ord-3f9c1a2b"}}`,
				},
			},
			{
				ID:   skillGetOrderTracking,
				Name: "Follow an order's delivery",
				Description: "Find out how one order is travelling, by its orderId: for every shipment group its promise window, status, lock and ship times, " +
					"the at-risk and unsourceable signals, the promise verdict (and how many minutes late it ran) and its packages with carrier, state, last scan " +
					"and exception; a lost or returned group also carries the outcome. Answers a 404 problem when no order has that id. Persists nothing.",
				Tags:       []string{"order", "tracking", "customer-ordering", "read"},
				InputModes: []string{"application/json"},
				Examples: []string{
					`{"skill":"get_order_tracking","arguments":{"orderId":"ord-3f9c1a2b"}}`,
				},
			},
		},
	}
}

// NewExecutor returns the AgentExecutor that maps an incoming message to a
// skill and answers with a single agent Message (a message-only agent in A2A
// terms): both curated skills are transactional request/response reads, so
// there is no long-running Task lifecycle to track in this first A2A surface.
// The response carries the caller's contextId (and taskId when the client
// referenced one) so a delegating saga can group its interactions.
func NewExecutor(deps Deps) a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			yield(answer(ctx, deps, ec), nil)
		}
	})
}

// Handler wires the executor into the transport-agnostic A2A request handler
// and wraps it in the JSON-RPC 2.0 HTTP handler, the only transport this
// agent ships (ADR 0008), mirroring ADR 0005's "Streamable HTTP only"
// minimalism for MCP.
func Handler(executor a2asrv.AgentExecutor) http.Handler {
	return a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor))
}
