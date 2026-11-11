package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/customer-ordering/internal/domain/customerorder"
)

// mimeTypeJSON is the MIME type of every resource this server serves.
const mimeTypeJSON = "application/json"

// orderTrackingURITemplate is the ONE resource this server offers (Agentic
// Standard v2, AS-3): a scoped read model this context owns, not a dump of a
// table. It answers "how is this one order travelling?" — every shipment
// group's promise window, status, verdicts, signals and packages — built from
// the same GetOrderTracking read use case the get_order_tracking tool and GET
// /orders/{orderId}/tracking use. Bounded by construction: one order's groups
// and packages, JSON text, never the tracking table.
const orderTrackingURITemplate = "warehouse://customer-ordering/orders/{orderId}/tracking"

// registerResources adds the resource surface (read-only by nature, L1; the
// charter keeps resources context data an agent can load without a tool
// call). No resource ever writes: Deps holds read use cases only (ADR 0005).
func (d Deps) registerResources(server *mcp.Server) {
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: orderTrackingURITemplate,
		Name:        "order tracking",
		Description: "How one placed order is travelling: per shipment group its promise window (deliverByEarliest, deliverByLatest), status, lock/ship times, the at-risk and unsourceable signals, the promise verdict (and how many minutes late it ran) and its packages with carrier, state, scans and exceptions. The ship-to address is never part of it. An unknown or malformed order id is an error.",
		MIMEType:    mimeTypeJSON,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		id, err := parseOrderTrackingURI(req.Params.URI)
		if err != nil {
			return nil, err
		}
		view, err := d.GetOrderTracking.Execute(ctx, id)
		if err != nil {
			return nil, mapError(ctx, err)
		}
		return resourceResult(req.Params.URI, toTracking(view))
	})
}

// resourceResult marshals one read model into the resource's text contents.
func resourceResult(uri string, view any) (*mcp.ReadResourceResult, error) {
	body, err := json.Marshal(view)
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{
			URI:      uri,
			MIMEType: mimeTypeJSON,
			Text:     string(body),
		}},
	}, nil
}

// parseOrderTrackingURI extracts the orderId from a
// warehouse://customer-ordering/orders/{orderId}/tracking URI. The id is
// model-supplied and therefore untrusted: a malformed URI is rejected rather
// than silently defaulted, and the id then passes through the same 1..64
// character guard the tools and REST use (an id that cannot name an order is
// a miss, never a server error).
func parseOrderTrackingURI(uri string) (customerorder.ID, error) {
	const scheme = "warehouse://customer-ordering/orders/"
	rest, ok := strings.CutPrefix(uri, scheme)
	if !ok {
		return "", fmt.Errorf("resource uri %q must start with %q", uri, scheme)
	}
	id, tail, found := strings.Cut(rest, "/")
	if !found || tail != "tracking" || id == "" {
		return "", fmt.Errorf("resource uri %q must be warehouse://customer-ordering/orders/{orderId}/tracking", uri)
	}
	return orderID(id)
}
