---
id: runbook
title: Runbook
sidebar_label: Runbook
sidebar_position: 2
---

# Customer Ordering runbook

Customer Ordering is the retail network's commerce-tier bounded context that
turns a shopper's cart into a customer order: at checkout it obtains a delivery
promise for every shipment group from the delivery-promise service, records the
order with its shopper and ship-to address (synthetic PII held only here),
serves status and tracking read models fed by sibling contexts' events, and
handles customer cancellation ([ADR 0002](/docs/adr/0002-aggregates-and-invariants),
[ADR 0003](/docs/adr/0003-consumed-contracts-and-local-read-models)). This
runbook covers the deployed processes and what to do when they misbehave. For
a symptom-first view see [Troubleshooting](/docs/operations/troubleshooting).

## Runtime shape

The chart deploys up to four workloads from one image
(`charts/customer-ordering/templates/`); only `api` is on by default.

| Workload | Binary | Port | What runs in it | Dependencies |
| --- | --- | --- | --- | --- |
| `api` (Deployment `customer-ordering`) | `cmd/api` | `8080` (`config.httpAddr`; Service port 80) | REST API, the **outbox relay** and the **three Kafka consumers** in one process | Postgres, Kafka, delivery-promise |
| `mcp` (`customer-ordering-mcp`, `mcp.enabled`) | `cmd/mcp` | `8090` | read-only MCP, Streamable HTTP | Postgres only |
| `a2a` (`customer-ordering-a2a`, `a2a.enabled`) | `cmd/a2a` | `8093` | read-only A2A agent, JSON-RPC and Agent Card | Postgres only |
| `frontend` (`customer-ordering-frontend`, `frontend.enabled`) | static assets of `web/` | | the `customer_ordering_mfe` console remote, served under `/mfes/customer-ordering/` | none |

There is no in-memory mode and no worker that can be switched off. If the relay
or any consumer returns on its own, `cmd/api` exits non-zero so the
orchestrator replaces the pod (`cmd/api/main.go`, `serveUntilSignal` and
`watchWorkers`). `cmd/mcp` and `cmd/a2a` can never write: no write use case is
wired, so they cannot insert into the outbox. They also run the idempotent
embedded migrations at boot, so any of the three can boot on a fresh database.

There are **no sweepers or scheduled jobs**: no CronJob template exists in the
chart, and no `cmd/` binary runs a periodic sweep. The only background loops
are the outbox relay and the three consumers inside `cmd/api`.

In the kind cluster the deployment values live in `warehouse-infra`
(`helm-values/customer-ordering.yaml`): `mcp`, `a2a` and `frontend` enabled,
Kong routes `/api/customer-ordering` (prefix stripped),
`/api/mcp/customer-ordering` (rewritten to `/mcp`) and
`/api/a2a/customer-ordering` (rewritten to `/`).

## Dependencies

| Dependency | Used for | Source |
| --- | --- | --- |
| Postgres (via PgBouncer) | every write and read; `DATABASE_URL` is the pooled DSN | Secret `customer-ordering-db`, key `DATABASE_URL` (`database.existingSecret`) |
| Postgres (direct DSN) | boot migrations only; `MIGRATIONS_DATABASE_URL` | same Secret, key `MIGRATIONS_DATABASE_URL`, optional; falls back to `DATABASE_URL` (`cmd/api/main.go`, `buildAdapters`) |
| Kafka (`KAFKA_BROKERS`) | outbox relay sink, the three consumers, the `/readyz` broker check (`cmd/api` only) | chart value `kafka.brokers` |
| delivery-promise (REST) | the one synchronous call: quote, make, confirm, compensating cancel (`internal/adapters/outbound/deliverypromise/gateway.go`) | `config.deliveryPromiseUrl` (in-cluster `http://delivery-promise.warehouse-systems.svc.cluster.local:80`) |

## Health and readiness

- `GET /healthz`: liveness only. 200 while the process is up; never flipped by
  dependencies or by shutdown. The chart's startup and liveness probes use it
  on every workload.
- `GET /readyz` (`cmd/api` only): 200 `{"status":"ready"}` only while (a) the
  pod is not draining AND (b) a Postgres ping AND a Kafka broker metadata
  request both answer. The verdict is cached 3 s and one check round is bounded
  2 s (`internal/adapters/inbound/http/readiness.go`). Otherwise: 503
  `{"status":"not_ready"}` once draining, or 503 problem+json
  `dependency-unavailable` naming the failing dependency (`postgres`,
  `kafka`); the cause is only in the WARN log `readiness: dependency is not
  ready`. The chart's readiness probe for `api` is `/readyz`; for `mcp` and
  `a2a` it is `/healthz`.
- Readiness does **not** wait for topics, consumers or the relay: a consumed
  topic that does not exist yet shows as consumer retry logs, never as 503.
- Writes while a dependency is down (pinned by `cmd/api/e2e_integration_test.go`,
  `assertKafkaDownReadiness`): Kafka down gives `/readyz` 503 but REST writes are
  STILL accepted (201), the events wait in `outbox_events`; Postgres down gives
  `/readyz` 503 naming `postgres` and REST writes fail. In both cases
  `/healthz` stays 200, so probes do not restart the pod.

## Shutdown order

SIGINT/SIGTERM on `cmd/api`: (1) `/readyz` 503, (2) wait `SHUTDOWN_DRAIN_DELAY`
(5 s), (3) drain HTTP (10 s), (4) stop and await each consumer (10 s each),
(5) stop and await the relay LAST so nothing committed above is stranded,
(6) close the pool, then flush telemetry (5 s). Worst case 5 + 10 + 3 x 10 + 10
= 55 s, so `terminationGracePeriodSeconds` must exceed `SHUTDOWN_DRAIN_DELAY`
plus 50 s: the chart default is 60 and the chart refuses less. `cmd/mcp` and
`cmd/a2a` drain their HTTP server (10 s) and close the pool.

## Migrations

Embedded SQL (`internal/adapters/outbound/postgres/migrations/`, golang-migrate,
four versions: `0001_customer_ordering_schema`, `0002_package_lost_and_returned`,
`0003_shopper_addresses_and_order_history`, `0004_group_window_time_zone`) runs at
every boot inside a bounded retry (`internal/bootretry`) that tolerates a sidecar's
first-dial reset; exhaustion refuses boot. Concurrent starts are safe
(golang-migrate's advisory lock), which is why `MIGRATIONS_DATABASE_URL` must be a
**direct** DSN when `DATABASE_URL` goes through PgBouncer. Check the state with
`SELECT version, dirty FROM schema_migrations;` (a real fresh run reads `4 | f`).
A `dirty = t` row means a migration died midway: repair the schema by hand,
clear the flag (golang-migrate `force <version>`) and restart. The runtime only
migrates up.

## Kafka topics and consumer groups

Topics are **not** pre-created by this service: the broker auto-creates them on
the first write (`AllowAutoTopicCreation` in `internal/adapters/outbound/kafka/writer.go`).

| Direction | Topic | Group / writer |
| --- | --- | --- |
| produced | `retailnetwork.customer-ordering.events` | the outbox relay of `cmd/api`; key and CloudEvents `subject` = orderId |
| consumed | `retailnetwork.delivery-promise.events` | `DELIVERY_PROMISE_CONSUMER_GROUP` (`customer-ordering-delivery-promise` in the cluster) |
| consumed | `retailnetwork.order-sourcing.events` | `ORDER_SOURCING_CONSUMER_GROUP` (`customer-ordering-order-sourcing`) |
| consumed | `retailnetwork.shipping-and-tracking.events` | `SHIPPING_CONSUMER_GROUP` (`customer-ordering-shipping`) |
| dead letters | `<consumed topic>.dlq` | written by the consumer that gave up on a message |

Group ids are stable and service-prefixed and never derived from the pod name:
a rescheduled pod resumes from its committed offsets, while a brand-new group
re-reads the topic from the first offset (every historical event is then seen
again; ones for unknown orders are logged as `consumed event skipped` and
passed). Never reuse the deployed group ids from a laptop.

## Outbox relay

A use case saves the aggregates and appends the encoded CloudEvents to
`outbox_events` in ONE transaction; the relay publishes in id order,
at-least-once, with the event id minted at append (a redelivery carries the
same id). It claims rows with `FOR UPDATE SKIP LOCKED`, 100 per pass, polls
every `OUTBOX_RELAY_INTERVAL`, and sets `published_at`. A row that fails
`OUTBOX_MAX_ATTEMPTS` times (default 20) is excluded from claims and logged once
as `outbox relay: row reached the attempt cap and is excluded from future
claims; re-arm it with attempts = 0 after fixing the cause`; later rows overtake
it. Inspect and re-arm:

```sql
SELECT count(*) FROM outbox_events WHERE published_at IS NULL;
SELECT id, event_type, attempts, last_error FROM outbox_events
  WHERE published_at IS NULL ORDER BY id;
UPDATE outbox_events SET attempts = 0 WHERE id = <id>;
```

The first write to a topic that does not exist yet can fail once with an
unknown-topic error; the relay retries it with the same id. W3C `traceparent`
headers are stored with the row and published as Kafka headers.

## Consumers and the DLQ

Inbox table `processed_events` (CloudEvents id) is written in the same
transaction as the effect, so a redelivery changes nothing. A handling failure
is retried with capped exponential backoff (200 ms doubling to 5 s), up to 5
attempts, then the message is published to `<consumed topic>.dlq` with
`x-dlq-*` headers (including `x-dlq-error`) and committed past. A message that
is not a valid CloudEvent is WARN-logged and skipped; one that can never be
applied (a payload that cannot be decoded) is dead-lettered at once. A message
for an unknown order, or one that would move a status backwards, is logged as
`consumed event skipped` and skipped (ADR 0002, A7). If the DLQ publish itself
fails the consumer keeps retrying and the partition is blocked. To replay,
read the `.dlq` topic with broker tooling and re-publish the original value to
the source topic after fixing the cause. The service exports no consumer-lag
metric.

## Deployment (Helm)

`charts/customer-ordering` deploys the `api` component, and optionally `mcp`,
`a2a` and `frontend` (Deployment, Service, ConfigMap, Secret or
`database.existingSecret`, ServiceAccount; HPA, Ingress and Gateway API
HTTPRoute exist and are off by default). Install it with the mandatory values;
the chart refuses to render, naming the value, without any of them:

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
| `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `SERVICE_VERSION`, `ENVIRONMENT` | `otel.endpoint`, `otel.serviceName` (`<name>-mcp` and `<name>-a2a` for those components), `image.tag` or appVersion, `environment` (chart default `production`) |
| `OUTBOX_RELAY_INTERVAL`, `OUTBOX_MAX_ATTEMPTS`, `SHUTDOWN_DRAIN_DELAY` | `config.outboxRelayInterval`, `config.outboxMaxAttempts`, `config.shutdownDrainDelay` |
| `MCP_ADDR` | `mcp.service.targetPort` (component `mcp`) |
| `A2A_ADDR`, `A2A_AGENT_URL` | `a2a.service.targetPort`; `a2a.agentUrl` overrides the advertised URL, else derived from the release and namespace (component `a2a`) |

The chart also refuses a `*_MODE` or `*_CONSUMER_GROUP` variable (or one it
renders itself) in `extraEnv`, and a `terminationGracePeriodSeconds` that does
not exceed `config.shutdownDrainDelay` + 50 s. The pod runs as uid 1000,
non-root, with a read-only root filesystem (a 16 Mi `emptyDir` on `/tmp`), no
privilege escalation, all capabilities dropped and the `RuntimeDefault` seccomp
profile. An empty `image.tag` means `v` + `appVersion`, the tag the release job
publishes. Tests: `bash charts/customer-ordering/tests/render_test.sh`,
`python3 charts/customer-ordering/tests/test_service_selectors.py`; CI runs
them in `chart-tests` and lints the chart in `helm-lint` (pull requests).
Packaging before the first release: [Before the first release](/docs/operations/before-first-release).

### Idempotency-Key behaviour

Every POST route (`/checkout`, `/orders/{orderId}/cancellations`, `/shoppers`,
`/shoppers/{shopperId}/addresses`) requires an `Idempotency-Key` header of
1 to 128 printable ASCII characters (`internal/adapters/inbound/http/idempotency.go`);
absent is 400 `idempotency-key-required`, malformed 400 `idempotency-key-invalid`.
The key is global, not per route. The first response below 500 (a 4xx too) is
stored for 24 h in table `idempotency_keys` (`route` is always the empty string;
`internal/application/idempotency/idempotency.go`), in the SAME transaction as
the order change and its outbox rows. A repeat with the same method, path and body
replays it with header `Idempotency-Replayed: true`; the same key with anything
else is 409 `idempotency-key-reused`. 5xx responses and panics roll back and are
never stored. Concurrent requests with one key are serialised by the unique
index: the loser replays the winner's answer.

## Alerts and dashboards

See [Observability](/docs/operations/observability#dashboards-and-alerts):
Grafana dashboard `warehouse-customer-ordering` (it now includes the business
counter `ordering_orders_placed_total`), Kong 5xx and burn-rate alerts and the
pod-restart alert. There is no alert on parked outbox rows, consumer lag or the
DLQ topics: check them with the SQL above and broker tooling.

## Routine procedures

| Task | How |
| --- | --- |
| Restart | `kubectl -n warehouse-systems rollout restart deploy/customer-ordering` (also `-mcp`, `-a2a`) |
| Logs | Grafana Loki, `{app="customer-ordering"}` (JSON slog on stdout; raise `config.logLevel` to `debug` for a noisy pod; `kubectl logs deploy/customer-ordering --previous` after a crash) |
| In-cluster REST | Service port 80 to container 8080; the external path is the Kong route `/api/customer-ordering` (prefix stripped) |
| Scale | HPA off by default (`autoscaling.api`); N>1 is safe for the relay (row claims via `FOR UPDATE SKIP LOCKED`), order writes (version guard) and consumers (stable groups plus inbox dedupe), but has not been load-tested |
| Re-arm a parked outbox row | the `UPDATE outbox_events SET attempts = 0` above, after fixing the cause |
| Check migration state | `SELECT version, dirty FROM schema_migrations;` |
| Change a consumer group | a new id re-reads the topic from the start; harmless but noisy. Never reuse a deployed id elsewhere |
