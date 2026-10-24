// Package mcp is the inbound Model Context Protocol adapter: it exposes this
// bounded context to the AI ecosystem as a second driving adapter over the
// SAME application-layer read use cases the REST adapter uses. It is built on
// the official MCP Go SDK and served over Streamable HTTP only.
//
// Per docs/adr/0005-mcp-server-adoption.md the surface is READ-ONLY: every
// tool reads customer-ordering data (get_order, get_order_tracking,
// get_ship_to); placing, cancelling and registering a shopper stay on REST and
// on the event consumers, and governance_test.go fails the build on a
// write-verb tool name. This package depends inward on the application layer
// and the domain only (plus the ports package's ErrNotFound sentinel to keep
// ONE error vocabulary with REST), never on an outbound adapter or the REST
// adapter, and nothing else may depend on it
// (internal/architecture's TestMCPAdapterDependencyRule;
// TestMCPServerIsReadOnly pins that no write use case is reachable). The
// composition root (cmd/mcp) wires concrete repositories into the use cases.
// There is no auth of any kind (fleet-wide revert 2026-09-11;
// TestNoAuthIsRequired).
package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverName and serverVersion identify this server in the MCP initialize
// handshake.
const (
	serverName    = "customer-ordering-mcp"
	serverVersion = "1.0.0"
)

// instructions is what an MCP client is told about this server at initialize.
const instructions = "Customer ordering of the retail network (read-only): read a placed order with get_order " +
	"(status, cart lines and every shipment group with its promise, tier, ship-by and deliver-by window, revision " +
	"and cancellation facts); follow one with get_order_tracking (each group's promise window, lock and ship times, " +
	"at-risk and unsourceable signals, promise verdict, its packages with carrier, state, scans and exceptions, and " +
	"the lost/returned outcome of a group that got one); and read the ship-to address with get_ship_to (the " +
	"synthetic PII of the delivery address, restricted to label printing). Nothing here changes data: placing an " +
	"order, cancelling one and registering a shopper are REST operations of customer-ordering, and the order " +
	"lifecycle is driven by the events it consumes."

// NewServer builds the MCP server for this bounded context with every tool
// registered.
func NewServer(deps Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion},
		&mcp.ServerOptions{Instructions: instructions},
	)
	deps.registerTools(server)
	return server
}

// Handler returns the Streamable HTTP handler for the MCP server.
func Handler(server *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}
