# Runtime: settings, probes, shutdown, outbox runbook

`cmd/api` (`run(ctx)`) is the only binary. It runs on real PostgreSQL and real Kafka; there is no in-memory mode.

## Environment

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATABASE_URL` | required | Postgres DSN. Missing = boot error naming it, before the listener binds. |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | Direct DSN for the embedded boot migrations (use it behind a pooler). |
| `KAFKA_BROKERS` | required | Comma-separated brokers: outbox relay, consumers and `/readyz`. |
| `DELIVERY_PROMISE_URL` | required | Base URL of delivery-promise (quote, make, confirm). |
| `DELIVERY_PROMISE_TIMEOUT` | `5s` | Per-call timeout (Go duration). |
| `DELIVERY_PROMISE_CONSUMER_GROUP` | required | Group of the delivery-promise consumer. |
| `ORDER_SOURCING_CONSUMER_GROUP` | required | Group of the order-sourcing consumer. |
| `SHIPPING_CONSUMER_GROUP` | required | Group of the shipping-and-tracking consumer. |
| `HTTP_ADDR` | `:8080` | Listen address. |
| `OUTBOX_RELAY_INTERVAL` | `1s` | Relay poll interval (Go duration or seconds). |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Failures before the relay excludes a row; `0` = unlimited. |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Wait between flipping `/readyz` to 503 and closing the listener. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. |

Consumer group ids are stable and service-prefixed; they are never derived from the pod name. The first Postgres dial retries with backoff
(`internal/bootretry`); on exhaustion the process exits non-zero.

## Probes

- `GET /healthz`: liveness only; stays 200 while a dependency is down.
- `GET /readyz`: 200 only while the drain gate is open AND Postgres answers a ping AND a Kafka broker answers a metadata request with an empty,
  non-nil topic list (a fresh connection per check; it never needs a topic to exist). The verdict is cached 3 s and bounded to 2 s. A failing
  dependency is 503 `dependency-unavailable` naming the dependency. Writes keep being accepted while Kafka is down: the outbox holds the events.

## Shutdown order

SIGINT/SIGTERM: (1) `/readyz` 503, (2) wait `SHUTDOWN_DRAIN_DELAY`, (3) drain HTTP (10 s), (4) stop and await the consumers (10 s), (5) stop and await the
relay (10 s), (6) close the pool. A relay or consumer that returns on its own makes the process exit non-zero so the orchestrator restarts it.
A pod's `terminationGracePeriodSeconds` must exceed 5 + 10 + 10 + 10 = 35 s (use 45 s or more).

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
