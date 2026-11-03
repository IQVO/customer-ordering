# ADR 0005: a read-only MCP server

## Status

Accepted (2026-10-24).

## Context

Every other bounded context of the fleet that exposes an HTTP API also ships an
MCP server so that agents can read its data without a bespoke REST client.
customer-ordering had none. The service already has the read use cases behind
its REST adapter (`GetOrder`, `GetOrderTracking`, `GetShipTo`), so an MCP
adapter is another driving adapter over them, not new domain.

## Decision

- **A second driving adapter, a separate binary.**
  `internal/adapters/inbound/mcp` is built on the official MCP Go SDK
  (`github.com/modelcontextprotocol/go-sdk`) and served over Streamable HTTP
  only (no stdio, no SSE) by `cmd/mcp`, mounted at `/mcp` and `/` with an open
  `/healthz`. It is a second deployable beside `cmd/api`; the Helm chart gains
  an mcp Deployment and Service (default off) and an optional gateway HTTPRoute
  (`/api/mcp/customer-ordering` rewritten to `/mcp`).
- **Three read tools over the existing queries, nothing else.**
  `get_order(orderId)` (status, lines, the promise of every shipment group),
  `get_order_tracking(orderId)` (per group the verdicts, signals, packages and
  a lost/returned outcome) and `get_ship_to(orderId)` (the restricted address
  read). Arguments and results are lowerCamelCase like the REST contract; the
  typed error of a read carries the REST problem slug (`not-found`) as the
  prefix of the tool error, and the REST adapter's id guard (1..64 characters)
  reads the same over both surfaces. `POST /checkout`,
  `POST /orders/{orderId}/cancellations` and `POST /shoppers` stay REST-only.
- **Read-only is structural.** The adapter's `Deps` holds the three read use
  cases and nothing that can write; `NewDeps` hands over only the two read
  repositories, so the composition root never names the shared `usecases.Deps`
  bundle that carries the write seams. The adapter imports no outbox, no unit
  of work, no event sink, no Kafka adapter and no delivery-promise client
  (the `ports` import is the `ErrNotFound` sentinel only, keeping one error
  vocabulary with REST). `cmd/mcp` never dials Kafka and never runs the
  consumers or the relay: those stay in `cmd/api`.
  `TestMCPServerIsReadOnly` pins the imports, the `Deps` fields and the use
  cases the MCP sources may name; `TestToolSurface` pins the exact tool set
  (budget 3), the read-only annotations and the absence of write verbs in tool
  names; `TestToolRegistryGolden` pins the whole advertised registry; the
  integration test proves the binary reads what checkout's domain Place
  persisted in a real Postgres and adds no outbox row. The fleet's
  optional-input sensor (`TestMCPOptionalToolInputsCarryOmitempty`) activates
  with the adapter and passes (every tool input is required).
- **No sibling calls, no auth.** The server reads this context's own tables
  only, calls no other context over REST or MCP (this is a retail-network
  service; the fleet is reached only through vendor-connectivity), and has no
  auth layer (the fleet-wide revert of 2026-09-11). Access control is the
  in-cluster ClusterIP boundary.
- **Boot.** `DATABASE_URL` is mandatory (no in-memory mode); like `cmd/api` the
  binary runs the idempotent embedded migrations on start (with
  `MIGRATIONS_DATABASE_URL` for a direct DSN behind PgBouncer) so it can start
  against a fresh database before `cmd/api` has. `MCP_ADDR` (default `:8090`),
  `LOG_LEVEL`, `SERVICE_VERSION` and `ENVIRONMENT` complete the contract the
  chart renders.

## Consequences

- Agents can answer "where is order X, which group is at risk, what is its
  promise window" without REST glue.
- Adding a tool is a reviewed act: the tool budget, the curated list and the
  golden file all change together (an amendment of this ADR).
- Placing, cancelling and registering a shopper stay REST operations with
  `Idempotency-Key`; the order lifecycle stays with the consumers.
