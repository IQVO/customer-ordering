---
id: overview
title: API overview
sidebar_label: API overview
slug: /api-reference
sidebar_position: 1
---

# API overview

:::info Implemented by cmd/api, cmd/mcp and cmd/a2a
`cmd/api` serves every REST route below (`internal/adapters/inbound/http/server.go`)
on `HTTP_ADDR` (default `:8080`). The contracts are `apis/openapi.yaml` and
`apis/asyncapi.yaml`, pinned by
[ADR 0001](/docs/adr/0001-customer-ordering-bounded-context) and
[ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue). The router
registers exactly the nine business routes of the spec plus `/healthz` and
`/readyz`; no route exists in the code that the spec does not list. Real
responses are in the [Quickstart](/docs/overview/quickstart).
:::

| Interface | Source of truth | Notes |
| --- | --- | --- |
| REST | `apis/openapi.yaml` (OpenAPI 3.0.3, version 0.1.0) | The pages under *REST* are generated from the spec with `npm run gen-api-docs`; do not edit them by hand. Server named in the spec: `http://localhost:8080`. Through Kong in the kind cluster the base path is `/api/customer-ordering` (prefix stripped). |
| Events | `apis/asyncapi.yaml` (AsyncAPI 3.0.0, version 1.0.0) | Summarised on the [event catalogue](/docs/api-reference/events); every field on [Domain events](/docs/ddd/domain-events). |
| MCP | [ADR 0005](/docs/adr/0005-mcp-server-adoption), `internal/adapters/inbound/mcp` | `cmd/mcp` serves Streamable HTTP on `/mcp` and `/` (`MCP_ADDR`, default `:8090`; through Kong `/api/mcp/customer-ordering`). Three read-only tools, no write tool: `get_order`, `get_order_tracking`, `get_ship_to` (see [MCP tools](/docs/mcp/tools)). No auth, by fleet decision. |
| A2A | [ADR 0008](/docs/adr/0008-a2a-inbound-adapter), `internal/adapters/inbound/a2a` | `cmd/a2a` serves JSON-RPC 2.0 on `/` and `/a2a` (`A2A_ADDR`, default `:8093`; through Kong `/api/a2a/customer-ordering`) and the Agent Card at `/.well-known/agent-card.json`, which advertises `A2A_AGENT_URL`. Two read-only skills, no write skill: `get_order` and `get_order_tracking` (argument `orderId`). A skill request is the first message part, `{"skill": "...", "arguments": {"orderId": "..."}}`, and unknown fields are rejected. Streaming is off. No auth, by fleet decision. |

`cmd/mcp` and `cmd/a2a` also answer `GET /healthz` (`{"status":"ok"}`); they
have no `/readyz`.

## Endpoints

| Tag | Endpoint | Operation id | Success |
| --- | --- | --- | --- |
| Checkout | `POST /checkout` | `placeOrder` | `201`, the order in state `PLACED` with one promise per group; `409 offer-not-quotable` carries fresh offers |
| Orders | `GET /orders?shopperId=&limit=&cursor=` | `listOrdersByShopper` | `200`, one page `{items, nextCursor}` newest first; `404 not-found` for an unknown shopper; `400 invalid-limit` / `invalid-cursor` / `unknown-query-parameter` |
| Orders | `GET /orders/{orderId}` | `getOrder` | `200`, status, shopper, destination postal prefix, lines, groups with promises; never the address |
| Orders | `GET /orders/{orderId}/tracking` | `getOrderTracking` | `200`, per group: window, lock and ship times, signals, verdict, packages, outcomes |
| Orders | `POST /orders/{orderId}/cancellations` | `cancelOrder` | `200`, the order with its groups in `CANCELLATION_REQUESTED`; `409 cancel-after-lock` when no group can be requested |
| Ship-To | `GET /orders/{orderId}/ship-to` | `getShipTo` | `200`, the synthetic address, `Cache-Control: no-store` |
| Shoppers | `POST /shoppers` | `registerShopper` | `201`, the shopper with generated ids; `409 shopper-exists` |
| Shoppers | `GET /shoppers?email=` | `findShopperByEmail` | `200`, the profile; `404 not-found` when no e-mail matches |
| Shoppers | `GET /shoppers/{shopperId}` | `getShopper` | `200`, the profile with `savedAddresses[]` |
| Shoppers | `POST /shoppers/{shopperId}/addresses` | `addSavedAddress` | `201`, the saved address; `422 too-many-addresses` past 20 |
| Operations | `GET /healthz`, `GET /readyz` | `getHealth`, `getReadiness` | `200` / `200`, or `503` while shutting down or when Postgres or Kafka does not answer ([Runbook](/docs/operations/runbook#health-and-readiness)) |

Exact status codes, bodies and schemas of every route are in the generated REST
pages of this section.

## Conventions in the contract

- **`Idempotency-Key`.** Required on the four POST routes (`/checkout`,
  `/orders/{orderId}/cancellations`, `/shoppers`, `/shoppers/{shopperId}/addresses`),
  1 to 128 printable ASCII characters; absent is `400 idempotency-key-required`,
  malformed is `400 idempotency-key-invalid`. The key is global. The first
  answer below 500 (a 4xx too) is stored for 24 hours, committed with the order
  and its outbox rows, and replayed with `Idempotency-Replayed: true`; the same
  key with a different method, path or body is `409 idempotency-key-reused`. On
  `/checkout` the order id is derived from the key, so a retry after a timeout
  reuses the same promises
  ([Runbook](/docs/operations/runbook#idempotency-key-behaviour)).
- **Paging.** `GET /orders` is keyset-paged and answers `{items, nextCursor}`
  (`nextCursor` is absent on the last page): `limit` defaults to 100 with a
  maximum of 500, `cursor` is the opaque `nextCursor` of the previous page. A bad
  `limit` is `400 invalid-limit`, an undecodable cursor `400 invalid-cursor` and
  any other query parameter (the retired `after`, `offset`, `nextToken`) is
  `400 unknown-query-parameter`. `GET /orders/{orderId}/tracking` and
  `GET /shoppers?email=` are single-resource views (`x-pagination-exempt: view`).
- **Errors** are RFC 9457 `application/problem+json` with
  `type = /problems/<slug>` (`internal/adapters/inbound/http/errors.go`). The
  slugs: `malformed-request` (400), `idempotency-key-required` (400),
  `idempotency-key-invalid` (400), `invalid-limit` (400), `invalid-cursor` (400),
  `unknown-query-parameter` (400), `idempotency-key-reused` (409),
  `not-found` (404), `offer-not-quotable` (409), `promise-capacity-exhausted`
  (409), `book-closed` (409), `status-regression` (409), `cancel-after-lock`
  (409), `shopper-exists` (409), `concurrent-update` (409),
  `illegal-transition` (409), `group-spans-nodes` (422),
  `line-without-promise` (422), `promise-destination-mismatch` (422),
  `too-many-addresses` (422),
  `validation-failed` (422), `delivery-promise-unavailable` (503) and
  `internal-error` (500). Causes and remedies are on
  [Troubleshooting](/docs/operations/troubleshooting#rest-answers); the use
  cases that raise them are on [Use cases](/docs/ddd/use-cases).
- **JSON.** camelCase, instants in RFC 3339 UTC, enums in UPPER_SNAKE, optional
  fields omitted when absent (never `null`), arrays always present. A NUL
  character (U+0000) in the path, the query or a body is a uniform client error
  (`internal/adapters/inbound/http/nul.go`).
- **Auth**: none. The fleet removed REST, MCP and A2A authentication on
  2026-09-11 and `TestNoAuthMiddlewareReintroduced` fails the build if auth
  middleware returns. Consequently the "restricted" ship-to route is restricted
  by documentation and network placement, not by a check: the spec names
  `shipping-and-tracking` as its only caller and the route is the only one that
  returns the address.
- **PII.** The ship-to address is synthetic and appears only on
  `GET /orders/{orderId}/ship-to`, in the checkout request and the saved-address
  routes; no order read and no event carries it. Events carry the
  destination postal prefix only.
