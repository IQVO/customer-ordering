# customer-ordering

Retail network, bounded context **Customer Ordering** (Generic, commerce tier).

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

Turns a shopper's cart into a customer order: obtains a promise for every shipment group at checkout, records the order with the shopper and ship-to address (synthetic PII, held only here), shows status and tracking, and handles customer cancellation.

The design is proposed in [retail-network-docs](https://iqvo.github.io/retail-network-docs/contexts/customer-ordering); `model/model.yaml` there is the single source of truth.
This repository is a service of the retail network, not of the warehouse-systems fleet; the fleet reaches the network only through the
`vendor-connectivity` Vendor API.

## What it does

- **Checkout**: a shopper's cart is grouped into shipment groups; for each group the service obtains a promise from delivery-promise
  (re-quote check, make, then confirm synchronously), and only then places the order with the shopper and the ship-to address
  (synthetic PII, held only here). A failed confirm places nothing and returns the re-quoted offers.
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
| `POST /checkout` | place an order (PlaceOrder) |
| `GET /orders/{orderId}`, `GET /orders/{orderId}/tracking` | status, tracking |
| `POST /orders/{orderId}:cancel` | request cancellation |
| `GET /orders/{orderId}/ship-to` | restricted: the address, for shipping-and-tracking |
| `POST /shoppers` | register a shopper |
| `GET /healthz`, `GET /readyz` | liveness, readiness |

## Events

Contract: [`apis/asyncapi.yaml`](apis/asyncapi.yaml), catalogue in [ADR 0004](docs/adr/0004-cloudevents-envelope-and-type-catalogue.md).

- **Published** on `retailnetwork.customer-ordering.events` (CloudEvents 1.0 structured JSON, type
  `com.retailnetwork.commerce.customer-ordering.customerorder.<Event>`, `subject` and Kafka key = orderId): `CustomerOrderPlaced`, `CustomerOrderCancelled`.
  Delivery is at-least-once with a stable event id (transactional outbox).
- **Consumed**: delivery-promise `PromiseAtRisk`, `PromiseRevised`, `PromiseKept`, `PromiseBroken`, `PromiseUnkeepable`; order-sourcing `FulfillmentLocked`,
  `FulfillmentShipped`, `FulfillmentUnsourceable`, `FulfillmentCancelled`, `FulfillmentCancelRefused`; shipping-and-tracking `LabelIssued`, `LabelVoided`,
  `PackageScanned`, `PackageDelivered`, `PackageExceptionRaised`. Idempotent on the CloudEvents id, dispatched on the full type, bad messages go to `<topic>.dlq`.
- **Synchronous calls**: delivery-promise `POST /delivery-offers:quote`, `POST /promises`, `POST /promises/{promiseId}:confirm`.

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

All settings, the probes, the shutdown order and the outbox runbook: [docs/runtime.md](docs/runtime.md).

## Develop

| Command | What it runs |
| --- | --- |
| `make check` | format, vet, build, lint, unit, handler and BDD tests (the pre-push gate) |
| `make coverage` | 90% gate over `internal/domain` and `internal/application` |
| `make mutation` | gremlins on every `internal/domain/<pkg>` |
| `make arch-test` | hexagonal and harness fitness tests |
| `make integration` | testcontainers Postgres and Kafka: repositories, relay, real-wiring test (needs Docker) |
| `make vuln` | govulncheck |

Layout: `internal/domain/{shared,shipto,shopper,customerorder}` (pure Go), `internal/application/{ports,views,usecases,readmodel,promising,outbox,idempotency}`,
`internal/adapters/inbound/{http,kafka}`, `internal/adapters/outbound/{postgres,kafka,outbox,deliverypromise,clock,ids,memory,repocontract}`, `cmd/api`, `features/` (godog
feature files, run from `internal/bdd`). The `memory` and `repocontract` packages are test doubles; `cmd/**` never imports them.

Decisions: [docs/adr/](docs/adr/index.md). Packaging checklist: [docs/before-first-release.md](docs/before-first-release.md).
