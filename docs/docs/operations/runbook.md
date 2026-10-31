# Customer Ordering runbook

Customer Ordering is the retail network's commerce-tier bounded context that turns a shopper's cart into a customer order: at checkout it obtains a delivery promise for every shipment group from the delivery-promise service, records the order with its shopper and ship-to address (synthetic PII held only here), serves status and tracking read models fed by sibling contexts' events, and handles customer cancellation (`README.md`, `docs/adr/0002`, `docs/adr/0003`). This runbook covers the one deployed process and what to do when it misbehaves.

## Runtime shape (single api Deployment: REST, outbox relay, consumers)

One `api` Deployment (`charts/customer-ordering/templates/deployment.yaml`) runs a single process, `cmd/api` — the composition root that wires, in one binary and one pod:

- **REST API** on `HTTP_ADDR` (default `:8080`): `POST /checkout`, `GET /orders/{orderId}`, `GET /orders/{orderId}/tracking`, `GET /orders/{orderId}/ship-to`, `POST /orders/{orderId}:cancel`, `POST /shoppers` (`internal/adapters/inbound/http/server.go`).
- **Transactional outbox relay**: polls the `outbox_events` table every `OUTBOX_RELAY_INTERVAL` and publishes pending rows to Kafka (`cmd/api/main.go` `startOutboxRelay`, `internal/adapters/outbound/outbox/relay.go`).
- **Three Kafka consumers** (local copies, ADR 0003): delivery-promise, order-sourcing and shipping-and-tracking (`cmd/api/main.go` `consumerSpecs`, `internal/adapters/inbound/kafka/handlers.go`).

There is no in-memory mode and no worker that can be switched off. If the relay or any consumer returns on its own, the process exits non-zero so the orchestrator replaces the pod (`cmd/api/main.go` `serveUntilSignal`/`watchWorkers`).

Graceful shutdown order (`cmd/api/main.go`): `/readyz` flips to 503 → wait `SHUTDOWN_DRAIN_DELAY` (default 5s) → drain HTTP (10s bound) → stop the consumers (10s each) → stop the relay LAST → close the pool. The chart's `terminationGracePeriodSeconds: 60` covers the worst case (`charts/customer-ordering/values.yaml`).

## Dependencies

| Dependency | Used for | Source |
| --- | --- | --- |
| Postgres (via PgBouncer) | every write and read; `DATABASE_URL` is the pooled DSN | Secret `customer-ordering-db`, key `DATABASE_URL` (`database.existingSecret`) |
| Postgres (direct DSN) | boot migrations only; `MIGRATIONS_DATABASE_URL` | same Secret, key `MIGRATIONS_DATABASE_URL`, optional — falls back to `DATABASE_URL` (`cmd/api/main.go` `buildAdapters`) |
| Kafka (`KAFKA_BROKERS`) | outbox relay sink, the three consumers, `/readyz` broker check | chart value `kafka.brokers` |
| delivery-promise (REST) | the one synchronous call: quote, make, confirm, compensating cancel (`internal/adapters/outbound/deliverypromise/gateway.go`) | `config.deliveryPromiseUrl` (in-cluster `http://delivery-promise.warehouse-systems.svc.cluster.local:80`) |

Notes:

- Migrations are embedded (`internal/adapters/outbound/postgres/migrations/*.sql`, golang-migrate) and run at boot inside a bounded retry (`internal/bootretry`) that tolerates a sidecar's first-dial reset; exhaustion refuses boot.
- Kafka topics are NOT pre-created by this service: the broker auto-creates them on the first write (`internal/adapters/outbound/kafka/writer.go`, `AllowAutoTopicCreation: true`).
- Produced topic: `retailnetwork.customer-ordering.events` (`internal/adapters/outbound/kafka/encoder.go`).
- Consumed topics: `retailnetwork.delivery-promise.events`, `retailnetwork.order-sourcing.events`, `retailnetwork.shipping-and-tracking.events`; dead letters go to `<topic>.dlq` (`internal/adapters/inbound/kafka/handlers.go`, `deadletter.go`).

## Health

- `GET /healthz` — liveness only. 200 while the process is up; never flipped by dependencies or by shutdown (`internal/adapters/inbound/http/server.go`, `readiness.go`). The chart's startup and liveness probes use it (`charts/customer-ordering/values.yaml`).
- `GET /readyz` — readiness. 200 `{"status":"ready"}` only while (a) the pod is not draining AND (b) a Postgres ping AND a Kafka broker metadata request both answer. A verdict is cached 3s and one check round is bounded 2s (`cmd/api/main.go` `newReadiness`; `internal/adapters/inbound/http/readiness.go` `DefaultReadinessTTL`/`DefaultReadinessTimeout`). Otherwise: 503 `{"status":"not_ready"}` once draining, or 503 problem+json `dependency-unavailable` naming the failing dependencies (`postgres`, `kafka`).
- Writes while a dependency is down, per code and the e2e pin (`cmd/api/e2e_integration_test.go` `assertKafkaDownReadiness`):
  - Kafka down → `/readyz` 503 but REST writes are STILL accepted (201); the event waits in `outbox_events`.
  - Postgres down → `/readyz` 503 naming `postgres`; REST writes fail (every write is a Postgres transaction).
  - In both cases `/healthz` stays 200, so probes do not restart the pod.

## Configuration

Env vars `cmd/api` reads (contract: `cmd/api/main.go` header comment, validated in `cmd/api/config.go`; rendered in `charts/customer-ordering/templates/deployment.yaml`):

| Env var | Chart value | Default | Notes |
| --- | --- | --- | --- |
| `HTTP_ADDR` | `config.httpAddr` | `:8080` | port must equal `service.targetPort` |
| `LOG_LEVEL` | `config.logLevel` | `info` | `debug`, `info`, `warn`/`warning` or `error`; JSON logs on stdout |
| `DATABASE_URL` | Secret key `database.existingSecretKey` | required | pooled DSN (PgBouncer); never in the ConfigMap |
| `MIGRATIONS_DATABASE_URL` | Secret key `database.migrationsExistingSecretKey` | `DATABASE_URL` | direct (session-mode) DSN, boot migrations only |
| `KAFKA_BROKERS` | `kafka.brokers` | required | comma-separated broker list |
| `DELIVERY_PROMISE_URL` | `config.deliveryPromiseUrl` | required | base URL of delivery-promise |
| `DELIVERY_PROMISE_TIMEOUT` | `config.deliveryPromiseTimeout` | `5s` | per-call timeout (Go duration); empty = not rendered |
| `DELIVERY_PROMISE_CONSUMER_GROUP` | `config.deliveryPromiseConsumerGroup` | required | e.g. `customer-ordering-delivery-promise` |
| `ORDER_SOURCING_CONSUMER_GROUP` | `config.orderSourcingConsumerGroup` | required | e.g. `customer-ordering-order-sourcing` |
| `SHIPPING_CONSUMER_GROUP` | `config.shippingConsumerGroup` | required | e.g. `customer-ordering-shipping` |
| `OUTBOX_RELAY_INTERVAL` | `config.outboxRelayInterval` | `1s` | Go duration or plain seconds |
| `OUTBOX_MAX_ATTEMPTS` | `config.outboxMaxAttempts` | `20` | failures before the relay excludes a row; `0` = unlimited |
| `SHUTDOWN_DRAIN_DELAY` | `config.shutdownDrainDelay` | `5s` | wait after `/readyz` flips before the listener closes; `0` disables |

A missing required variable is a boot error that names the variable (`cmd/api/config.go`). The consumer group ids must be stable and service-prefixed — a rescheduled pod resumes from its committed offsets under the same group (`charts/customer-ordering/values.yaml`).

Behavior — Idempotency-Key middleware (`internal/adapters/inbound/http/idempotency.go`):

- Every POST route (`/checkout`, `/orders/{orderId}:cancel`, `/shoppers`) requires an `Idempotency-Key` header of 1–255 characters.
- The first response below 500 is stored per `(route, key)` for 24h (`internal/application/idempotency/idempotency.go` `TTL`; table `idempotency_keys` in `migrations/0001_customer_ordering_schema.up.sql`).
- A repeat with the same request replays the stored response with header `Idempotent-Replayed: true`; the same key with a different request is 422 problem `idempotency-key-reused`.
- 5xx responses are never stored, so the key stays retryable; two concurrent first requests may both run (every command is idempotent by itself).

## Alerts and dashboards

- Grafana dashboard uid **`warehouse-customer-ordering`** (fleet-provisioned).
- Pod-restart alerts exist at the fleet level and are the main automatic signal for this service today.
- The service instruments **no business metrics yet** (no metrics registry anywhere in `cmd/` or `internal/`): business-metric panels on the dashboard stay empty until the service exports its own metrics. Infra signals (restarts, CPU/memory via kube-state) are all that can be expected until then.

## Failure modes

### Pod crash-loop

- Symptom: restarts climb, the pod-restart alert fires.
- Diagnosis: `kubectl -n warehouse-systems logs deploy/customer-ordering --previous` — the process logs `service exited with error` naming the missing env var (`cmd/api/config.go`), migrations/ping exhaustion (`internal/bootretry`), or `<worker> stopped unexpectedly` (`cmd/api/main.go`).
- Action: fix the named chart value or Secret key, then roll again.

### Postgres unreachable

- Symptom: `/readyz` 503 `dependency-unavailable` naming `postgres`; REST writes fail; consumers retry handling; relay logs `outbox relay pass failed`.
- Diagnosis: check the Secret's `DATABASE_URL` (pooled) and PgBouncer; the readiness verdict refreshes within 3s of recovery.
- Action: restore Postgres/PgBouncer — consumers and the relay resume on their own and nothing needs replaying; the pod is not killed (`/healthz` stays 200).

### Kafka unreachable

- Symptom: `/readyz` 503 naming `kafka`; `/healthz` 200; REST writes still accepted and queued.
- Diagnosis: `SELECT count(*) FROM outbox_events WHERE published_at IS NULL;` grows while brokers are down; the relay logs `outbox relay pass failed` and retries every `OUTBOX_RELAY_INTERVAL` (`internal/adapters/outbound/outbox/relay.go` `Run`).
- Action: restore the brokers; the relay drains in `id` order automatically. If a consumer's reader instead returns, the process exits non-zero by design and the pod restarts.

### Outbox relay stuck / row parked after max attempts

- Symptom: repeating relay-pass ERRORs, or the one-time ERROR `outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause` (`internal/adapters/outbound/outbox/relay.go` `AttemptCapReachedMsg`).
- Diagnosis: parked rows keep their counters for inspection — `SELECT id, event_type, attempts, last_error FROM outbox_events WHERE published_at IS NULL ORDER BY id;`. A row whose `attempts` reached `OUTBOX_MAX_ATTEMPTS` (columns `published_at`, `attempts`, `last_error` per `migrations/0001_customer_ordering_schema.up.sql`) is excluded from claims and later rows overtake it.
- Action: fix the cause (usually Kafka), then re-arm the row: `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;`.

### Consumer lag or retry logs

- Symptom: repeated `handling failed; retrying the same message` (`attempt`, `max_attempts`, `retry_in`), or silence on a topic.
- Diagnosis: consumed topics are auto-created by the first write from their producer — a topic whose producer has not published yet has no messages, so the consumer only logs retries; check the producing sibling before assuming this consumer is broken. Transient handler failures retry with capped exponential backoff (200ms → 5s) and then dead-letter to `<topic>.dlq`; unknown CloudEvents types are skipped, non-CloudEvents are WARN-logged, a poison payload is dead-lettered at once (`internal/adapters/inbound/kafka/kafka.go`, `consumer.go`, `deadletter.go`). The service exports no consumer-lag metric; use broker tooling.
- Action: inspect `<topic>.dlq` for poison messages and replay from there; a lagging-but-quiet topic is a producer-side matter.

### idempotency-key-reused storms

- Symptom: a burst of 422 `idempotency-key-reused` from the POST routes.
- Diagnosis: the middleware hashes method+path+query+body, so a storm means callers reuse a key while regenerating the body (timestamps, ids); the store keeps the first record for 24h (`idempotency_keys`, PK `(route, key)`). Correct replays come back with `Idempotent-Replayed: true` and are not a fault.
- Action: fix the caller to keep the body byte-stable per logical request or mint a fresh key; records age out after 24h — nothing to clear server-side.

## Operational actions

- Restart: `kubectl -n warehouse-systems rollout restart deploy/customer-ordering`
- Logs: Grafana Loki, `app=customer-ordering` (JSON slog on stdout; raise `config.logLevel` to `debug` for a noisy pod).
- In-cluster REST: Service port 80 → container 8080; the external path is the Kong route `/api/customer-ordering` (prefix stripped).
- Scale: HPA off by default (`autoscaling.api`); N>1 is safe for the relay (row claims via `FOR UPDATE SKIP LOCKED`), order writes (version guard) and consumers (stable groups + inbox dedupe), but has not been load-tested (`charts/customer-ordering/values.yaml`).
