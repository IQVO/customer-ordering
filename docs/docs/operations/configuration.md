---
id: configuration
title: Configuration
sidebar_label: Configuration
sidebar_position: 1
---

# Configuration

`customer-ordering` ships **three binaries** (`cmd/api`, `cmd/mcp`, `cmd/a2a`).
All are configured only through environment variables: there is no config
file, no flag and no secret-manager integration. REST, MCP and A2A are
unauthenticated fleet-wide, so there is no token or key to configure.

All three run on real Postgres only; `cmd/api` additionally runs on real Kafka
and a real delivery-promise endpoint. A missing mandatory variable is a boot
error that names it, raised before the listener binds. The semantics below
were read from `cmd/*/main.go`, `cmd/api/config.go`, `cmd/*/telemetry.go` and
`internal/adapters/outbound/telemetry/telemetry.go`; test-only variables are
excluded.

## `cmd/api`

Source: `cmd/api/main.go` (`run`), `cmd/api/config.go` (`loadRuntime`),
`cmd/api/telemetry.go`.

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `HTTP_ADDR` | `:8080` | no | Listen address of the REST server (also serves `/healthz` and `/readyz`). |
| `DATABASE_URL` | none | **yes** | Postgres DSN. Missing = boot error `DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode`. The embedded migrations run at boot; the outbox, the idempotency store and every repository live here. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct (non-pooled) DSN used only for the boot migrations, because golang-migrate's advisory lock does not survive PgBouncer transaction pooling. Empty falls back to `DATABASE_URL`. |
| `KAFKA_BROKERS` | none | **yes** | Comma-separated broker list (entries trimmed, blanks dropped). Missing = boot error `KAFKA_BROKERS is required`. Used by the three consumers, the outbox relay and the `/readyz` metadata check. In the fleet's kind cluster the single broker is `localhost:9092` from the host. |
| `DELIVERY_PROMISE_URL` | none | **yes** | Base URL of the delivery-promise service, called at checkout to quote, make and confirm a promise (and to cancel it when a checkout fails). Missing = boot error `DELIVERY_PROMISE_URL is required`. |
| `DELIVERY_PROMISE_TIMEOUT` | `5s` | no | Per-call HTTP timeout toward delivery-promise, a positive Go duration. An unparsable or non-positive value is a **boot error** naming the variable. |
| `DELIVERY_PROMISE_CONSUMER_GROUP` | none | **yes** | Kafka consumer group id of the delivery-promise consumer (topic `retailnetwork.delivery-promise.events`). No default and no literal in code; the deployment chooses it. Missing = boot error `DELIVERY_PROMISE_CONSUMER_GROUP is required`. |
| `ORDER_SOURCING_CONSUMER_GROUP` | none | **yes** | Group id of the order-sourcing consumer (topic `retailnetwork.order-sourcing.events`). |
| `SHIPPING_CONSUMER_GROUP` | none | **yes** | Group id of the shipping-and-tracking consumer (topic `retailnetwork.shipping-and-tracking.events`). |
| `OUTBOX_RELAY_INTERVAL` | `1s` | no | Pause between relay passes that did not fill a batch. A Go duration (`500ms`) or plain seconds (`2`, `0.5`). Unparsable or non-positive values log a WARN and use the default. |
| `OUTBOX_MAX_ATTEMPTS` | `20` | no | Failures after which the relay stops claiming an outbox row. `0` = unlimited. Negative or unparsable values log a WARN and use `20`. |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | no | After SIGINT/SIGTERM `/readyz` flips to 503 and the process waits this long before closing the listener. `0` disables the wait. Negative or unparsable values log a WARN and use `5s`. Read when the signal arrives. |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn` (or `warning`), `error`, case-insensitive; anything else falls back to `info`. Logs are JSON on stdout. |
| `OTEL_SERVICE_NAME` | `customer-ordering` | no | `service.name` of spans and metrics. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint, bare `host:port` (rewritten to `http://host:port`) or a URL. Export never blocks: an absent Collector drops telemetry, it never fails the service. |
| `SERVICE_VERSION` | `dev` | no | `service.version` resource attribute. |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` resource attribute (read by the telemetry adapter). |

With the six mandatory variables set (`DATABASE_URL`, `KAFKA_BROKERS`,
`DELIVERY_PROMISE_URL` and the three group ids) all consumers and the outbox
relay start; there is no mode switch and no in-memory fallback.

## `cmd/mcp`

Source: `cmd/mcp/main.go`, `cmd/mcp/telemetry.go`. Read-only MCP server over
Streamable HTTP; it starts no relay or consumer and never dials Kafka or
delivery-promise.

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `MCP_ADDR` | `:8090` | no | Listen address. |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the service database `cmd/api` owns. Missing = boot error `DATABASE_URL is required: this context runs on real Postgres`. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the idempotent boot migrations (this binary also runs them, so it can boot on a fresh database). |
| `LOG_LEVEL` | `info` | no | As `cmd/api`. |
| `OTEL_SERVICE_NAME` | `customer-ordering-mcp` | no | `service.name` of spans and metrics. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint; export never blocks. |
| `SERVICE_VERSION` | `dev` | no | `service.version`, also printed in the boot line. |
| `ENVIRONMENT` | `production` in the boot log line, `local` in the telemetry resource | no | `deployment.environment.name`. The two defaults differ in code (`cmd/mcp/main.go` vs `telemetry.DefaultEnvironment`). |

## `cmd/a2a`

Source: `cmd/a2a/main.go`, `cmd/a2a/telemetry.go`. Read-only A2A agent
(JSON-RPC over HTTP, Agent Card at the well-known path); same restrictions as
`cmd/mcp`.

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `A2A_ADDR` | `:8093` | no | Listen address. |
| `A2A_AGENT_URL` | none | **yes** | Absolute `http(s)` URL of this agent's JSON-RPC endpoint, advertised on the Agent Card. Missing or not an absolute http(s) URL = boot error naming the variable, raised before telemetry or any connection. |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the service database. Missing = boot error. Missing variables are reported together. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the boot migrations. |
| `LOG_LEVEL` | `info` | no | As `cmd/api`. |
| `OTEL_SERVICE_NAME` | `customer-ordering-a2a` | no | `service.name` of spans and metrics. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | Collector OTLP/gRPC endpoint; export never blocks. |
| `SERVICE_VERSION` | `dev` | no | `service.version`; also the version on the Agent Card. |
| `ENVIRONMENT` | `production` in the boot log line, `local` in the telemetry resource | no | `deployment.environment.name`. |

## Compiled-in settings (not configurable)

| Setting | Value | Source |
| --- | --- | --- |
| HTTP `ReadHeaderTimeout` | 5 s (all three binaries) | `cmd/*/main.go` |
| HTTP graceful shutdown / per-worker drain timeout | 10 s / 10 s | `cmd/api/main.go` (`shutdownTimeout`, `workerDrainTimeout`) |
| Postgres pool | 10 connections per process, `statement_timeout` set per connection | `internal/adapters/outbound/postgres/pool.go` |
| Boot retry (migrations, first ping) | exponential backoff, then exit non-zero | `internal/bootretry` |
| Outbox relay batch | 100 rows per pass | `internal/adapters/outbound/outbox/relay.go` (`DefaultBatchSize`) |
| Attempts when another writer got in between load and save: command / consumed event | 2 / 5 | `internal/application/usecases/deps.go` |
| Consumer retry backoff | 200 ms doubling to a 5 s cap | `internal/adapters/inbound/kafka/kafka.go` |
| `/readyz` verdict cache / check timeout | 3 s / 2 s | `internal/adapters/inbound/http/readiness.go` |
| delivery-promise response cap | 1 MiB | `internal/adapters/outbound/deliverypromise/gateway.go` |
| Order history page | default 20, maximum 100 | `internal/application/usecases/orders_history.go` |
| Topic produced | `retailnetwork.customer-ordering.events` | `internal/adapters/outbound/kafka/encoder.go` |
| Topics consumed | `retailnetwork.delivery-promise.events`, `retailnetwork.order-sourcing.events`, `retailnetwork.shipping-and-tracking.events` | `internal/adapters/inbound/kafka/handlers.go` |

## What is deliberately absent

- **No auth variables.** REST, MCP and A2A are unauthenticated fleet-wide.
- **No `*_MODE` variables.** The consumers always run; the chart refuses a
  `*_MODE` or `*_CONSUMER_GROUP` variable in `extraEnv`.
- **No Kafka variable on `cmd/mcp` and `cmd/a2a`.**
- **Chart values.** `charts/customer-ordering` maps these variables onto the
  pod; see the table in the [Runbook](/docs/operations/runbook#deployment-helm).
