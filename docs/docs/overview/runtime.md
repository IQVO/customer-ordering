# Runtime: settings, probes, shutdown, outbox runbook

`cmd/api` (`run(ctx)`) is the only binary. It runs on real PostgreSQL and real Kafka; there is no in-memory mode.

## Environment

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATABASE_URL` | required | Postgres DSN. Missing = boot error naming it, before the listener binds. |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | Direct DSN for the embedded boot migrations (use it behind a pooler). |
| `KAFKA_BROKERS` | required | Comma-separated brokers: outbox relay, consumers and `/readyz`. |
| `DELIVERY_PROMISE_URL` | required | Base URL of delivery-promise (quote, make, confirm, and the compensating cancel of a failed checkout). |
| `DELIVERY_PROMISE_TIMEOUT` | `5s` | Per-call timeout (Go duration). |
| `DELIVERY_PROMISE_CONSUMER_GROUP` | required | Group of the delivery-promise consumer. |
| `ORDER_SOURCING_CONSUMER_GROUP` | required | Group of the order-sourcing consumer. |
| `SHIPPING_CONSUMER_GROUP` | required | Group of the shipping-and-tracking consumer. |
| `HTTP_ADDR` | `:8080` | Listen address. |
| `OUTBOX_RELAY_INTERVAL` | `1s` | Relay poll interval (Go duration or seconds). |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Failures before the relay excludes a row; `0` = unlimited. |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Wait between flipping `/readyz` to 503 and closing the listener. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. |
| `OTEL_SERVICE_NAME` | `customer-ordering` | `service.name` of spans and metrics (ADR 0007). |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | Collector OTLP/gRPC, `host:port` or URL; export never blocks. |
| `SERVICE_VERSION` / `ENVIRONMENT` | `dev` / `local` | `service.version` / `deployment.environment.name`. |

Consumer group ids are stable and service-prefixed; they are never derived from the pod name. The first Postgres dial retries with backoff
(`internal/bootretry`); on exhaustion the process exits non-zero.

## Probes

- `GET /healthz`: liveness only; stays 200 while a dependency is down.
- `GET /readyz`: 200 only while the drain gate is open AND Postgres answers a ping AND a Kafka broker answers a metadata request with an empty,
  non-nil topic list (a fresh connection per check; it never needs a topic to exist). The verdict is cached 3 s and bounded to 2 s. A failing
  dependency is 503 `dependency-unavailable` naming the dependency. Writes keep being accepted while Kafka is down: the outbox holds the events.

## Routes

The routes of `apis/openapi.yaml` (wired in `cmd/api` `buildServer`; errors are RFC 9457 problems, see `.claude/rules/rest-api.md`):

| Route | Use case | Notes |
| --- | --- | --- |
| `POST /checkout` | `PlaceOrder` | `Idempotency-Key` required; exactly one of `shipTo` / `shipToAddressId` (ADR 0002 A25). |
| `GET /orders?shopperId=&limit=&cursor=` | `ListOrdersByShopper` | Order history, newest first, `{items, nextCursor}` keyset-paged; limit default 100 max 500; unknown shopper 404 (A26). |
| `GET /orders/{orderId}` | `GetOrder` | Never carries the address. |
| `GET /orders/{orderId}/tracking` | `GetOrderTracking` | Event-fed read model; one view of one order, not paginated. |
| `GET /orders/{orderId}/ship-to` | `GetShipTo` | Restricted, in-organization (label printing); the only order route returning the address; `Cache-Control: no-store`. |
| `POST /orders/{orderId}/cancellations` | `CancelOrder` | `Idempotency-Key` required. |
| `POST /shoppers` | `RegisterShopper` | `Idempotency-Key` required; unique lower-cased e-mail. |
| `GET /shoppers/{shopperId}` | `GetShopper` | Profile with `savedAddresses[]` (synthetic PII, A27). |
| `GET /shoppers?email=` | `FindShopperByEmail` | Trimmed, lower-cased match; no match 404. |
| `POST /shoppers/{shopperId}/addresses` | `AddSavedAddress` | `Idempotency-Key` required; `addressId` derives from it (A24); max 20 addresses (`too-many-addresses`). |
| `GET /healthz`, `GET /readyz` | — | Probes, not business operations. |

## Shutdown order

SIGINT/SIGTERM: (1) `/readyz` 503, (2) wait `SHUTDOWN_DRAIN_DELAY`, (3) drain HTTP (10 s), (4) stop and await the consumers (10 s), (5) stop and await the
relay (10 s), (6) close the pool. A relay or consumer that returns on its own makes the process exit non-zero so the orchestrator restarts it.
The consumers are awaited one after the other, each bounded to 10 s, so the worst case is 5 + 10 + 3 x 10 + 10 = 55 s: a pod's
`terminationGracePeriodSeconds` must exceed `SHUTDOWN_DRAIN_DELAY` + 50 s (the chart default is 60 and the chart refuses less).

## Transactional outbox runbook

A use case saves the aggregates and appends the encoded CloudEvents to `outbox_events` in ONE transaction; the relay publishes in id order to
`retailnetwork.customer-ordering.events`, at-least-once, with the event id minted at append (a redelivery carries the same id). A row that fails
`OUTBOX_MAX_ATTEMPTS` times is excluded from claims and logged; fix the cause (broker, topic ACL), then re-arm it:

```sql
SELECT id, attempts, last_error FROM outbox_events WHERE published_at IS NULL AND attempts >= 20;
UPDATE outbox_events SET attempts = 0 WHERE id = <id>;
```

The first write to a topic that does not exist yet can fail once with an unknown-topic error; the relay retries it with the same id.

## Consumers

Inbox table `processed_events` (CloudEvents id) is written in the same transaction as the effect. A message that is not a valid CloudEvent, or
whose `data` cannot be decoded, is published to `<consumed topic>.dlq` with the failure in headers and committed past. A message for an unknown
order, or one that would move a status backwards, is logged and skipped (ADR 0002, A7).

## Deployment (Helm)

`charts/customer-ordering` deploys the `api` component only (Deployment, Service, ConfigMap, Secret or `database.existingSecret`, ServiceAccount; HPA,
Ingress and Gateway API HTTPRoute exist and are off by default). Install it with the mandatory values; the chart refuses to render, naming the
value, without any of them:

```sh
helm upgrade --install customer-ordering charts/customer-ordering \
  --set database.existingSecret=customer-ordering-db \
  --set kafka.brokers=kafka:9092 \
  --set config.deliveryPromiseUrl=http://delivery-promise \
  --set config.deliveryPromiseConsumerGroup=customer-ordering-delivery-promise \
  --set config.orderSourcingConsumerGroup=customer-ordering-order-sourcing \
  --set config.shippingConsumerGroup=customer-ordering-shipping
```

| Variable | Chart value |
| --- | --- |
| `DATABASE_URL` | Secret key `database.existingSecretKey` of `database.existingSecret`, or of the Secret the chart builds from `database.url` (always `secretKeyRef`, never the ConfigMap) |
| `MIGRATIONS_DATABASE_URL` | Secret key `database.migrationsExistingSecretKey` (`optional: true`; the binary falls back to `DATABASE_URL`), filled from `database.migrationsUrl` when the chart builds the Secret |
| `KAFKA_BROKERS` | `kafka.brokers` |
| `DELIVERY_PROMISE_URL`, `DELIVERY_PROMISE_TIMEOUT` | `config.deliveryPromiseUrl`, `config.deliveryPromiseTimeout` |
| `DELIVERY_PROMISE_CONSUMER_GROUP`, `ORDER_SOURCING_CONSUMER_GROUP`, `SHIPPING_CONSUMER_GROUP` | `config.deliveryPromiseConsumerGroup`, `config.orderSourcingConsumerGroup`, `config.shippingConsumerGroup` |
| `HTTP_ADDR`, `LOG_LEVEL` | `config.httpAddr` (its port must equal `service.targetPort`), `config.logLevel` |
| `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `SERVICE_VERSION`, `ENVIRONMENT` | `otel.endpoint`, `otel.serviceName` (`<name>-mcp` for the mcp component), `image.tag` or appVersion, `environment` |
| `OUTBOX_RELAY_INTERVAL`, `OUTBOX_MAX_ATTEMPTS`, `SHUTDOWN_DRAIN_DELAY` | `config.outboxRelayInterval`, `config.outboxMaxAttempts`, `config.shutdownDrainDelay` |

The chart also refuses a `*_MODE` or `*_CONSUMER_GROUP` variable (or one it renders itself) in `extraEnv`, and a `terminationGracePeriodSeconds` that does
not exceed `config.shutdownDrainDelay` + 50 s. The pod runs as uid 1000, non-root, with a read-only root filesystem (a 16 Mi `emptyDir` on `/tmp`), no
privilege escalation, all capabilities dropped and the `RuntimeDefault` seccomp profile; liveness is `/healthz`, readiness `/readyz`. An empty `image.tag`
means `v` + `appVersion`, the tag the release job publishes. Tests: `bash charts/customer-ordering/tests/render_test.sh`,
`python3 charts/customer-ordering/tests/test_service_selectors.py`; CI runs them in `chart-tests` and lints the chart in `helm-lint` (pull requests).
