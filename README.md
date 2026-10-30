# customer-ordering

Retail network, bounded context **Customer Ordering** (Generic, commerce tier).

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

Turns a shopper's cart into a customer order: obtains a promise for every shipment group at checkout, records the order with the shopper and ship-to address (synthetic PII, held only here), shows status and tracking, and handles customer cancellation.

The design is proposed in [retail-network docs](https://iqvo.github.io/warehouse-docs/network/contexts/customer-ordering); `model/model.yaml` there is the single source of truth.
This repository is a service of the retail network, not of the warehouse-systems fleet; the fleet reaches the network only through the
`vendor-connectivity` Vendor API.

## What it does

- **Checkout**: a shopper's cart is grouped into shipment groups; for each group the service obtains a promise from delivery-promise
  (re-quote check, make, then confirm synchronously), and only then places the order with the shopper and the ship-to address
  (synthetic PII, held only here). A failed confirm places nothing and returns the re-quoted offers; the promises of the same checkout that were already confirmed are cancelled again (ADR 0002 A14).
- **Status and tracking**: the order status is derived from its groups; tracking shows each group's promise window, lock and ship times,
  packages, scans and promise verdicts, fed by events of delivery-promise, order-sourcing and shipping-and-tracking.
- **Cancellation**: a shopper can cancel while any group is unlocked; the cancel is a request that order-sourcing accepts or refuses.
  An unkeepable promise cancels its group by policy.
- **Ship-to**: the address is served only by a restricted route and never appears in an event.

Aggregates, invariants and every assumption the model left open: [docs/adr/0002](docs/adr/0002-aggregates-and-invariants.md). Consumed contracts and
read models: [docs/adr/0003](docs/adr/0003-consumed-contracts-and-local-read-models.md).

## REST API

Contract: [`apis/openapi.yaml`](apis/openapi.yaml). No authentication. Errors are RFC 9457 `application/problem+json` (`type` is `/problems/<slug>`, for
example `not-found`, `illegal-transition`, `concurrent-update`, `validation-failed`, `cancel-after-lock`, `offer-not-quotable`).
Every state-changing request needs an `Idempotency-Key` header: a repeat with the same request replays the first response for 24 hours; the same key
with another request is 422 `idempotency-key-reused`.

| Method and path | Operation |
| --- | --- |
| `POST /checkout` | place an order (PlaceOrder; inline `shipTo` or a saved `shipToAddressId`) |
| `GET /orders?shopperId=&after=&limit=` | a shopper's order history, newest first (ListOrdersByShopper) |
| `GET /orders/{orderId}`, `GET /orders/{orderId}/tracking` | status, tracking |
| `POST /orders/{orderId}:cancel` | request cancellation |
| `GET /orders/{orderId}/ship-to` | restricted: the address, for shipping-and-tracking |
| `POST /shoppers` | register a shopper |
| `GET /shoppers/{shopperId}`, `GET /shoppers?email=` | shopper profile with saved addresses, shopper by e-mail |
| `POST /shoppers/{shopperId}/addresses` | save an address (max 20, `too-many-addresses`) |
| `GET /healthz`, `GET /readyz` | liveness, readiness |

## MCP server

`cmd/mcp` is a second, read-only deployable: the same three read use cases the REST adapter serves, exposed as MCP tools over Streamable HTTP
(`github.com/modelcontextprotocol/go-sdk`, `/mcp` and `/`, port 8090). No auth of any kind (fleet decision; the boundary is the in-cluster ClusterIP), no Kafka, no
relay, no consumers — it can never write ([ADR 0005](docs/adr/0005-mcp-server-adoption.md)).

| Tool | Read |
| --- | --- |
| `get_order(orderId)` | `GET /orders/{orderId}`: status, lines, the promise of every group |
| `get_order_tracking(orderId)` | `GET /orders/{orderId}/tracking`: verdicts, signals, packages, outcomes |
| `get_ship_to(orderId)` | `GET /orders/{orderId}/ship-to`: the restricted address read |

```sh
export DATABASE_URL=postgres://user:***@localhost:5432/customer_ordering?sslmode=disable
go run ./cmd/mcp                           # listens on :8090 (MCP_ADDR), /healthz + /mcp
```

On Kubernetes the optional `mcp` chart component (default off) renders the Deployment and Service; `mcp.route.enabled` exposes
`/api/mcp/customer-ordering` through the gateway, rewritten to `/mcp`.

## A2A agent

`cmd/a2a` is a third, read-only deployable: an Agent2Agent (A2A) agent (`github.com/a2aproject/a2a-go/v2`, JSON-RPC 2.0 over HTTP at `/` and `/a2a`,
the Agent Card at `/.well-known/agent-card.json`, port 8093) that another agent can **delegate** a goal to. Two goal-shaped skills, both reads over the same
use cases as REST and MCP; the ship-to address is deliberately not offered. No auth of any kind (fleet decision), no Kafka, no relay, no consumers, no
outbound A2A client. A2A carries delegation only: facts between contexts still travel as CloudEvents on Kafka, published by `cmd/api`
([ADR 0008](docs/adr/0008-a2a-inbound-adapter.md)).

| Skill | Read |
| --- | --- |
| `get_order(orderId)` | status, lines, the promise of every shipment group |
| `get_order_tracking(orderId)` | verdicts, signals, packages, outcomes |

Domain rejections are answered in-band as RFC 7807-style `problem`s inside the reply message (404 `not-found`, 400 `invalid-argument`, 500), never as JSON-RPC errors.

```sh
export DATABASE_URL=postgres://user:***@localhost:5432/customer_ordering?sslmode=disable
export A2A_AGENT_URL=http://localhost:8093/   # mandatory: advertised on the Agent Card
go run ./cmd/a2a                              # listens on :8093 (A2A_ADDR), /healthz + /.well-known/agent-card.json
```

On Kubernetes the optional `a2a` chart component (default off) renders the Deployment and Service; `a2a.route.enabled` exposes
`/api/a2a/customer-ordering` through the gateway, rewritten to `/`; `a2a.agentUrl` overrides the advertised URL.

## Events

Contract: [`apis/asyncapi.yaml`](apis/asyncapi.yaml), catalogue in [ADR 0004](docs/adr/0004-cloudevents-envelope-and-type-catalogue.md).

- **Published** on `retailnetwork.customer-ordering.events` (CloudEvents 1.0 structured JSON, type
  `com.retailnetwork.commerce.customer-ordering.customerorder.<Event>`, `subject` and Kafka key = orderId): `CustomerOrderPlaced`, `CustomerOrderCancelled`.
  Delivery is at-least-once with a stable event id (transactional outbox).
- **Consumed**: delivery-promise `PromiseAtRisk`, `PromiseRevised`, `PromiseKept`, `PromiseBroken`, `PromiseUnkeepable`; order-sourcing `FulfillmentLocked`,
  `FulfillmentShipped`, `FulfillmentUnsourceable`, `FulfillmentCancelled`, `FulfillmentCancelRefused`; shipping-and-tracking `LabelIssued`, `LabelVoided`,
  `PackageScanned`, `PackageDelivered`, `PackageExceptionRaised`, `PackageLost`, `PackageReturned` (the last two close the group: LOST / RETURNED, ADR 0002 A19). Idempotent on the CloudEvents id, dispatched on the full type, bad messages go to `<topic>.dlq`.
- **Synchronous calls**: delivery-promise `POST /delivery-offers:quote`, `POST /promises`, `POST /promises/{promiseId}:confirm`, and the compensating `POST /promises/{promiseId}:cancel` (only after a later confirm failed).

## Run it

Real PostgreSQL and Kafka only. `DATABASE_URL`, `KAFKA_BROKERS`, `DELIVERY_PROMISE_URL` and the three consumer groups are mandatory (a boot error names the
missing variable before the listener binds); the embedded migrations run at boot, the outbox relay and the consumers are always on.

```sh
export DATABASE_URL=postgres://user:***@localhost:5432/customer_ordering?sslmode=disable
export KAFKA_BROKERS=localhost:29092   # whatever your broker advertises
export DELIVERY_PROMISE_URL=http://localhost:8081
export DELIVERY_PROMISE_CONSUMER_GROUP=customer-ordering-delivery-promise
export ORDER_SOURCING_CONSUMER_GROUP=customer-ordering-order-sourcing
export SHIPPING_CONSUMER_GROUP=customer-ordering-shipping
go run ./cmd/api                       # listens on :8080 (HTTP_ADDR)
```

All settings, the probes, the shutdown order, the outbox runbook and the Helm chart (`charts/customer-ordering`): [docs/runtime.md](docs/runtime.md).

On Kubernetes: `helm upgrade --install customer-ordering charts/customer-ordering` with the mandatory values (`database.*`, `kafka.brokers`,
`config.deliveryPromiseUrl`, the three `config.*ConsumerGroup` ids); the chart fails to render, naming the value, without them.

## Observability

Logs, metrics and traces follow the fleet standard ([ADR 0007](docs/adr/0007-fleet-observability-standard.md)): JSON logs on stdout carry `trace_id`/`span_id`
and a `request_id`; `cmd/api` and `cmd/mcp` export OTLP/gRPC to the Collector (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `SERVICE_VERSION`, `ENVIRONMENT`;
export never blocks, so no Collector costs telemetry, nothing else) with HTTP request duration, Go runtime metrics and the business counter
`ordering.orders.placed` (`outcome`, `reason`); the trace continues across Kafka in the `traceparent` header of each published event and is picked up by the
consumers. The chart's `otel.*` values render the env.

## Console remote (web)

`web/` is the `customer_ordering_mfe` Module Federation remote for the warehouse-console shell: three read-only screens (the order with its lines and promises, the
tracking view, the restricted ship-to) over the REST reads above. The API base is resolved at runtime from the console's `/config.json` (`apiOrigin`), never baked in;
production assets live under `/mfes/customer-ordering/` behind the Nginx web gateway; the optional `frontend` chart component (default off) serves them
([ADR 0006](docs/adr/0006-console-remote.md)).

```sh
cd web && npm ci
npm run dev        # standalone on http://localhost:4012 (the shell is not needed)
npm run lint && npx tsc -b && npm test && npm run build
```

## Develop

| Command | What it runs |
| --- | --- |
| `make check` | format, vet, build, lint, unit, handler and BDD tests (the pre-push gate) |
| `make coverage` | 90% gate over `internal/domain` and `internal/application` |
| `make mutation` | gremlins on every `internal/domain/<pkg>` |
| `make arch-test` | hexagonal and harness fitness tests |
| `make integration` | testcontainers Postgres and Kafka: repositories, relay, real-wiring test (needs Docker) |
| `make vuln` | govulncheck |

The web remote's gates (run inside `web/`, never at the repo root): `npm run lint`, `npx tsc -b`, `npm test`, `npm run build` (the CI `web` job runs exactly these
against a sibling `warehouse-ui-kit` checkout).

Layout: `internal/domain/{shared,shipto,shopper,customerorder}` (pure Go), `internal/application/{ports,views,usecases,readmodel,promising,outbox,idempotency}`,
`internal/adapters/inbound/{http,kafka,mcp,a2a}`, `internal/adapters/outbound/{postgres,kafka,outbox,deliverypromise,clock,ids,memory,repocontract}`, `cmd/{api,mcp,a2a}`,
`web/` (the console remote), `features/` (godog feature files, run from `internal/bdd`). The `memory` and `repocontract` packages are test doubles; `cmd/**` never
imports them.

Decisions: [docs/adr/](docs/adr/index.md). Packaging checklist: [docs/before-first-release.md](docs/before-first-release.md).
