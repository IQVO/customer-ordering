---
id: tools
title: MCP tools
sidebar_label: MCP tools
sidebar_position: 2
---

# MCP tools

`cmd/mcp` is a second, independent deployable that exposes the read side of the
context to AI agents over the Model Context Protocol
([ADR 0005](/docs/adr/0005-mcp-server-adoption)). It serves **Streamable HTTP
only** (no stdio, no SSE) and is **unauthenticated**: access control is the
in-cluster ClusterIP boundary, like REST and A2A.

| Property | Value | Source |
| --- | --- | --- |
| Listen address | `MCP_ADDR`, default `:8090` | `cmd/mcp/main.go` |
| Endpoint | `POST /mcp` and `POST /` (both mounted) | `cmd/mcp/router.go` |
| Probe | `GET /healthz` returns `{"status":"ok"}` | `cmd/mcp/router.go` |
| Server name / version | `customer-ordering-mcp` / `1.0.0` | `initialize` answer below |
| Behind the gateway | the optional `mcp` chart component; `mcp.route.enabled` exposes `/api/mcp/customer-ordering`, rewritten to `/mcp` | `charts/customer-ordering/templates/mcp-route.yaml` |
| Dependencies | Postgres only: it starts no relay or consumer, never dials Kafka or delivery-promise and wires no write use case, so it can never insert into the outbox | `cmd/mcp/main.go`, `internal/architecture/mcp_readonly_fitness_test.go` |

The three tools call the **same read use cases** the REST adapter uses
(`GetOrder`, `GetOrderTracking`, `GetShipTo`), so an answer is the REST body of
the corresponding route. All three carry the annotations `readOnlyHint: true`,
`idempotentHint: true`, `openWorldHint: false`.

## Tools

The answers below were captured from a live `tools/list` against `cmd/mcp`
built from this branch.

| Tool | Input | Annotation | Reads | REST equivalent |
| --- | --- | --- | --- | --- |
| `get_order` | `orderId` (string, required) | read-only, idempotent | status, shopper, destination postal prefix, placed time, the cart lines (`lineNo`, `catalogItemId`, `quantity`) and every shipment group with its promise (`promiseId`, `tier`, `shipBy`, `deliverByEarliest`, `deliverByLatest`), line numbers, `status`, `revisionNo` and cancellation facts. **No address.** | `GET /orders/{orderId}` |
| `get_order_tracking` | `orderId` (string, required) | read-only, idempotent | per group: promise window, status, `lockedAt`, `shippedAt`, at-risk and unsourceable reasons, `promiseVerdict` and `lateByMinutes`, `lostAt`/`returnedAt` and `outcomePackage`, and the packages (`trackingNumber`, `carrier`, `state`, last scan node and time, `deliveredAt`, `exceptionReason`). | `GET /orders/{orderId}/tracking` |
| `get_ship_to` | `orderId` (string, required) | read-only, idempotent | the synthetic ship-to address (`name`, `line1`, `line2`, `city`, `region`, `postalCode`, `countryCode`, `phone`). Restricted to label printing in REST; it never appears in an event. | `GET /orders/{orderId}/ship-to` |

Every input schema is `{"type":"object","properties":{"orderId":{"type":"string","description":"the order id, e.g. ord-3f9c1a2b"}},"required":["orderId"],"additionalProperties":false}`;
every tool also declares an output schema with `additionalProperties: false`.

There are no write tools. Placing an order, cancelling one and registering a
shopper are REST operations of `cmd/api`; the order lifecycle is driven by the
events it consumes ([Integration](/docs/ecosystem/integration)).

## Try it

```bash
curl -s -X POST localhost:8090/mcp \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"docs","version":"0"}}}' -i
```

The answer carries an `Mcp-Session-Id` header; send it, with
`Mcp-Protocol-Version: 2025-06-18`, on every later request (first the
`notifications/initialized` notification, then `tools/list` or `tools/call`).
Real `initialize` result (abridged):

```text
HTTP/1.1 200 OK
Content-Type: text/event-stream
Mcp-Session-Id: 47PK4I7I2YMPF5MQ23P4QRB3JI

event: message
data: {"jsonrpc":"2.0","id":1,"result":{"capabilities":{"logging":{},"tools":{"listChanged":true}},"instructions":"Customer ordering of the retail network (read-only): read a placed order with get_order ... Nothing here changes data: placing an order, cancelling one and registering a shopper are REST operations of customer-ordering ...","protocolVersion":"2025-06-18","serverInfo":{"name":"customer-ordering-mcp","version":"1.0.0"}}}
```

## Errors

A domain failure is a tool result with `isError: true`, not a JSON-RPC error.
Real answer for an unknown order:

```json
{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"not-found: order ord-ghost: not found"}],"isError":true}}
```

An unknown or malformed order id is always `not-found`.

## Configuration and observability

`cmd/mcp` reads `MCP_ADDR`, `DATABASE_URL` (required), `MIGRATIONS_DATABASE_URL`,
`LOG_LEVEL` and the four `OTEL_*`/version variables: see
[Configuration](/docs/operations/configuration#cmdmcp). Its `service.name`
defaults to `customer-ordering-mcp`. Source: `cmd/mcp/main.go`,
`internal/adapters/inbound/mcp/`.
