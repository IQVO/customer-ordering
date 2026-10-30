# customer-ordering

[![CI](https://github.com/IQVO/customer-ordering/actions/workflows/ci.yml/badge.svg?branch=develop)](https://github.com/IQVO/customer-ordering/actions/workflows/ci.yml?query=branch%3Adevelop) [![coverage gate](https://img.shields.io/badge/coverage%20gate-90%25-brightgreen)](https://github.com/IQVO/customer-ordering/blob/develop/Makefile) [![Go](https://img.shields.io/badge/go-1.27.3-00ADD8?logo=go&logoColor=white)](https://github.com/IQVO/customer-ordering/blob/develop/go.mod) [![release](https://img.shields.io/github/v/release/IQVO/customer-ordering?sort=semver)](https://github.com/IQVO/customer-ordering/releases) [![docs](https://img.shields.io/badge/docs-iqvo.github.io-blue)](https://iqvo.github.io/customer-ordering/) [![license](https://img.shields.io/badge/license-MIT-blue)](https://github.com/IQVO/customer-ordering/blob/develop/LICENSE)

Retail network, bounded context **Customer Ordering** (Generic, commerce
tier). It turns a shopper's cart into a customer order: it obtains a promise
for every shipment group at checkout, records the order with the shopper and
ship-to address (synthetic PII, held only here), shows status and tracking,
and handles customer cancellation. It does **not** own promises
(`delivery-promise`), fulfilment sourcing (`order-sourcing`), shipping
(`shipping-and-tracking`) or carts — it consumes their events and calls
`delivery-promise` synchronously at checkout. It is an Open Host Service
(REST plus a published CloudEvents language on
`retailnetwork.customer-ordering.events`). This repository is a service of
the retail network, not of the warehouse-systems fleet; the fleet reaches the
network only through the `vendor-connectivity` Vendor API.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

---

## Documentation

This repo has no docs site of its own. The context's design lives on the
warehouse-docs retail-network pages:

| Area | Pages |
|---|---|
| Retail network | [Network overview](https://iqvo.github.io/warehouse-docs/network) |
| This context | [Index / service facts](https://iqvo.github.io/warehouse-docs/network/contexts/customer-ordering), [Business context](https://iqvo.github.io/warehouse-docs/network/contexts/customer-ordering/business-context), [Domain events](https://iqvo.github.io/warehouse-docs/network/contexts/customer-ordering/domain-events), [Use cases](https://iqvo.github.io/warehouse-docs/network/contexts/customer-ordering/use-cases) |
| DDD artifact pack | [Ubiquitous language](https://iqvo.github.io/warehouse-docs/network/contexts/customer-ordering/ubiquitous-language), [Context map](https://iqvo.github.io/warehouse-docs/network/contexts/customer-ordering/context-map), [Core domain chart](https://iqvo.github.io/warehouse-docs/network/contexts/customer-ordering/core-domain-chart), [Bounded context canvas](https://iqvo.github.io/warehouse-docs/network/contexts/customer-ordering/bounded-context-canvas), [Aggregate design canvas](https://iqvo.github.io/warehouse-docs/network/contexts/customer-ordering/aggregate-design-canvas), [EventStorming](https://iqvo.github.io/warehouse-docs/network/contexts/customer-ordering/eventstorming), [Class diagram](https://iqvo.github.io/warehouse-docs/network/contexts/customer-ordering/class-diagram), [Sequence diagrams](https://iqvo.github.io/warehouse-docs/network/contexts/customer-ordering/sequence-diagrams), [ER diagram](https://iqvo.github.io/warehouse-docs/network/contexts/customer-ordering/entity-relationship), [Domain message flow](https://iqvo.github.io/warehouse-docs/network/contexts/customer-ordering/domain-message-flow) |
| Decisions | [Network ADRs](https://iqvo.github.io/warehouse-docs/network/adr) |

In-tree: [`docs/adr/`](docs/adr/index.md) (decisions, index),
[`docs/runtime.md`](docs/runtime.md) (settings, probes, shutdown order,
outbox runbook, Helm chart),
[`docs/operations/runbook.md`](docs/operations/runbook.md),
[`docs/before-first-release.md`](docs/before-first-release.md) (packaging
checklist).

---

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

## Architecture

Hexagonal / ports & adapters. Dependencies point inward only:

```
cmd/api/                     the REST service (composition root): HTTP, the three Kafka consumers and the outbox relay in one process
cmd/mcp/                     read-only MCP server (Streamable HTTP, :8090)
cmd/a2a/                     read-only A2A agent (JSON-RPC over HTTP, Agent Card at /.well-known/agent-card.json)
internal/
  domain/                    pure Go: shared, shipto, shopper, customerorder
  application/               ports, usecases, views, readmodel, promising, outbox, idempotency
  adapters/
    inbound/http/            chi handlers, DTOs, RFC 9457 problem+json errors, /healthz + /readyz
    inbound/kafka/           the delivery-promise, order-sourcing and shipping-and-tracking consumers
    inbound/mcp/  inbound/a2a/   the read-only agent surfaces
    outbound/postgres/       pgx repos, embedded golang-migrate SQL, transactional outbox
    outbound/kafka/          the integration-topic writer
    outbound/outbox/         the relay
    outbound/deliverypromise/   the synchronous delivery-promise client (the only outbound HTTP)
    outbound/clock/ ids/     the wall clock and id source
    outbound/memory/ repocontract/   test doubles, never imported by cmd/** (enforced)
    outbound/telemetry/      OpenTelemetry wiring
  architecture/              the fitness tests
features/                    godog BDD (Gherkin, run from internal/bdd)
web/                         the customer_ordering_mfe console remote
```

The layering rules are executable tests in `internal/architecture/`, not
conventions: `architecture_test.go` (hexagonal dependencies),
`runtime_no_memory_fitness_test.go` (no binary links the in-memory doubles),
`mcp_readonly_fitness_test.go`, `kafka_writer_topic_fitness_test.go` /
`kafka_writer_acks_fitness_test.go`, `outbox_relay_cap_fitness_test.go`,
`a2a_fitness_test.go` and `no_go_files_at_root_fitness_test.go`, among
others — run by `make arch-test`.

## Domain events

Contract: [`apis/asyncapi.yaml`](apis/asyncapi.yaml), catalogue in [ADR 0004](docs/adr/0004-cloudevents-envelope-and-type-catalogue.md).

- **Published** on `retailnetwork.customer-ordering.events` (CloudEvents 1.0 structured JSON, type
  `com.retailnetwork.commerce.customer-ordering.customerorder.<Event>`, `subject` and Kafka key = orderId): `CustomerOrderPlaced`, `CustomerOrderCancelled`.
  Delivery is at-least-once with a stable event id (transactional outbox). Real types, verbatim from the contract:
  `com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderPlaced` /
  `com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderCancelled`.
- **Consumed**: delivery-promise `PromiseAtRisk`, `PromiseRevised`, `PromiseKept`, `PromiseBroken`, `PromiseUnkeepable`; order-sourcing `FulfillmentLocked`,
  `FulfillmentShipped`, `FulfillmentUnsourceable`, `FulfillmentCancelled`, `FulfillmentCancelRefused`; shipping-and-tracking `LabelIssued`, `LabelVoided`,
  `PackageScanned`, `PackageDelivered`, `PackageExceptionRaised`, `PackageLost`, `PackageReturned` (the last two close the group: LOST / RETURNED, ADR 0002 A19). Idempotent on the CloudEvents id, dispatched on the full type, bad messages go to `<topic>.dlq`.
- **Synchronous calls**: delivery-promise `POST /delivery-offers:quote`, `POST /promises`, `POST /promises/{promiseId}:confirm`, and the compensating `POST /promises/{promiseId}:cancel` (only after a later confirm failed).

## Running it

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

All settings, the probes, the shutdown order and the outbox runbook: [docs/runtime.md](docs/runtime.md).

### Configuration

`cmd/api`:

| Env var | Default | Meaning |
|---|---|---|
| `DATABASE_URL` | **required** | Postgres DSN; unset ⇒ boot error naming the variable |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | Direct DSN for the boot migrations |
| `KAFKA_BROKERS` | **required** | Comma-separated brokers; feeds the relay, the consumers and `/readyz` |
| `DELIVERY_PROMISE_URL` | **required** | Base URL of delivery-promise (quote, promise, confirm) |
| `DELIVERY_PROMISE_TIMEOUT` | `5s` | Per-call timeout (Go duration) |
| `DELIVERY_PROMISE_CONSUMER_GROUP` | **required** | Stable group id of the delivery-promise consumer |
| `ORDER_SOURCING_CONSUMER_GROUP` | **required** | Stable group id of the order-sourcing consumer |
| `SHIPPING_CONSUMER_GROUP` | **required** | Stable group id of the shipping-and-tracking consumer |
| `HTTP_ADDR` | `:8080` | Listen address |
| `OUTBOX_RELAY_INTERVAL` | `1s` | Relay poll interval (Go duration or plain seconds) |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Failures before the relay excludes a row (`0` = unlimited) |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Wait after flipping `/readyz` before closing |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |

`cmd/mcp` reads `DATABASE_URL` (**required**), `MIGRATIONS_DATABASE_URL`
(default `DATABASE_URL`), `MCP_ADDR` (default `:8090`) and `LOG_LEVEL`.
`cmd/a2a` reads `DATABASE_URL` (**required**), `A2A_AGENT_URL` (**required**,
the absolute URL advertised on the Agent Card), `MIGRATIONS_DATABASE_URL`
(default `DATABASE_URL`), `A2A_ADDR` (default `:8093`) and `LOG_LEVEL`.

All three binaries export telemetry and read `OTEL_EXPORTER_OTLP_ENDPOINT`
(default `localhost:4317`), `OTEL_SERVICE_NAME` (default `customer-ordering`;
`customer-ordering-mcp` / `customer-ordering-a2a` for the agent binaries),
`SERVICE_VERSION` (default `dev`) and `ENVIRONMENT` (default `local`) — see
[Observability](#observability).

## Observability

Logs, metrics and traces follow the fleet standard ([ADR 0007](docs/adr/0007-fleet-observability-standard.md)): JSON logs on stdout carry `trace_id`/`span_id`
and a `request_id`; `cmd/api` and `cmd/mcp` export OTLP/gRPC to the Collector (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `SERVICE_VERSION`, `ENVIRONMENT`;
export never blocks, so no Collector costs telemetry, nothing else) with HTTP request duration, Go runtime metrics and the business counter
`ordering.orders.placed` (`outcome`, `reason`); the trace continues across Kafka in the `traceparent` header of each published event and is picked up by the
consumers. The chart's `otel.*` values render the env.

## API

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

## Helm chart

On Kubernetes: `helm upgrade --install customer-ordering charts/customer-ordering` with the mandatory values (`database.*`, `kafka.brokers`,
`config.deliveryPromiseUrl`, the three `config.*ConsumerGroup` ids); the chart fails to render, naming the value, without them.

```sh
helm upgrade --install customer-ordering charts/customer-ordering \
  --set database.existingSecret=customer-ordering-db \
  --set kafka.brokers=kafka:9092 \
  --set config.deliveryPromiseUrl=http://delivery-promise:8080 \
  --set config.deliveryPromiseConsumerGroup=customer-ordering-delivery-promise \
  --set config.orderSourcingConsumerGroup=customer-ordering-order-sourcing \
  --set config.shippingConsumerGroup=customer-ordering-shipping
```

Values that matter: `database.url` / `database.existingSecret` (keys
`DATABASE_URL`, `MIGRATIONS_DATABASE_URL`), `kafka.brokers`, `config.*`
(`httpAddr`, `logLevel`, `deliveryPromiseUrl`, `deliveryPromiseTimeout`, the
three consumer groups, `outboxRelayInterval`, `outboxMaxAttempts`,
`shutdownDrainDelay`), the `otel.*` block (`enabled`, `endpoint`,
`serviceName`), `environment`, and the optional `mcp`, `a2a` and `frontend`
components (default off). Render invariants are asserted by
`charts/customer-ordering/tests/render_test.sh` and
`test_service_selectors.py` (every Service selects exactly one Deployment),
over `ci/default-values.yaml` and `ci/all-components-values.yaml`. Linted in
CI with `ct lint --charts charts/customer-ordering`.

### Running the MCP server in Kubernetes

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

### Running the A2A agent in Kubernetes

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
`/api/a2/customer-ordering` through the gateway, rewritten to `/`; `a2a.agentUrl` overrides the advertised URL.

## Operator micro-frontend (`web/`)

`web/` is the `customer_ordering_mfe` Module Federation remote for the warehouse-console shell: three read-only screens (the order with its lines and promises, the
tracking view, the restricted ship-to) over the REST reads above. The API base is resolved at runtime from the console's `window.__WAREHOUSE_CONFIG__.apiOrigin`
(`${apiOrigin}/api/customer-ordering`), never baked in; production assets live under `/mfes/customer-ordering/` behind the Nginx web gateway; the optional
`frontend` chart component (default off) serves them ([ADR 0006](docs/adr/0006-console-remote.md)).

```sh
cd web && npm ci
npm run dev        # standalone on http://localhost:4012 (the shell is not needed)
npm run lint && npx tsc -b && npm test && npm run build
```

## Local development / quality gate

Every CI sensor is a `make` target. `make help` lists them all.

| Command | What it runs |
| --- | --- |
| `make check` | format, vet, build, lint, unit, handler and BDD tests (the pre-push gate) |
| `make check-fast` | fmt-check, vet, arch-test |
| `make check-all` | check + coverage + arch-test + bdd |
| `make coverage` | 90% gate over `internal/domain` and `internal/application` |
| `make mutation` | gremlins on every `internal/domain/<pkg>` |
| `make arch-test` | hexagonal and harness fitness tests |
| `make integration` | testcontainers Postgres and Kafka: repositories, relay, real-wiring test (needs Docker) |
| `make vuln` | govulncheck |

The web remote's gates (run inside `web/`, never at the repo root): `npm run lint`, `npx tsc -b`, `npm test`, `npm run build` (the CI `web` job runs exactly these
against a sibling `warehouse-ui-kit` checkout).

Git hooks are managed by [lefthook](https://github.com/evilmartians/lefthook)
(`lefthook.yml`): pre-commit runs `fmt-check` + `vet` + `lint`; pre-push runs
`make check`. Activate once per clone: `lefthook install`.

## Tests

- **Unit** — `go test ./...` (domain, use cases, handlers; `make test`).
- **BDD acceptance** — godog/Gherkin over the real HTTP API in `features/` (`api_errors`, `api_idempotency`, `api_orders`, `cancellation`, `checkout`, `fulfilment_status`, `orders_history`, `shopper`), run from `internal/bdd` (`make bdd`).
- **Integration** — testcontainers Postgres and Kafka: repositories, relay and real-wiring tests, including checkout compensation (`make integration`, build-tagged, needs only Docker).
- **Coverage gate** — `make coverage`: ≥ 90% over `internal/domain` + `internal/application`.
- **Mutation** — gremlins over every `internal/domain/<pkg>` (`make mutation`; the exhaustive `mutation-full`).
- **Architecture fitness** — `internal/architecture/` (`make arch-test`).
- **Web** — lint, `tsc -b`, vitest and build in `web/`.

CI (`.github/workflows/ci.yml`) runs `lint`, `guide-lint`, `complexity`, `test`
(with the 90% coverage gate), `bdd`, `integration`, `mutation-fast`, `api-lint`,
`vuln`, `chart-tests`, `arch-test` and `web` on every push and PR (`helm-lint`
with `ct lint` on PRs); the exhaustive `mutation` and the advisory `drift`
sensor run only on the weekly schedule or manual dispatch; `docker-publish`
pushes the image to GHCR on `main` once the gate jobs are green, and `release`
then tags the next version and publishes the chart.

## Architecture Decision Records

The ADR index lives at [docs/adr/index.md](docs/adr/index.md) — 8 decisions,
from the bounded context and aggregates to the CloudEvents catalogue, the MCP
server, the console remote and the A2A adapter.

## License

MIT — see [LICENSE](LICENSE).
