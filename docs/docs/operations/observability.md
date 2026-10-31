---
id: observability
title: Observability
sidebar_label: Observability
sidebar_position: 3
---

# Observability

The context follows the fleet observability standard
([ADR 0007](/docs/adr/0007-fleet-observability-standard)): JSON logs on stdout
with trace correlation, OTLP/gRPC traces and metrics to the Collector, and the
trace carried across Kafka in the W3C `traceparent` header. All three binaries
(`cmd/api`, `cmd/mcp`, `cmd/a2a`) bring telemetry up the same way
(`cmd/*/telemetry.go`, `internal/adapters/outbound/telemetry`). Export never
blocks: a missing Collector costs telemetry, not availability.

## Exporter configuration

| Variable | Default | Effect |
| --- | --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | OTLP/gRPC endpoint for traces and metrics; a bare `host:port` is rewritten to `http://host:port` |
| `OTEL_SERVICE_NAME` | `customer-ordering` (`cmd/api`), `customer-ordering-mcp`, `customer-ordering-a2a` | `service.name`, also the Loki `app` label |
| `SERVICE_VERSION` | `dev` | `service.version` |
| `ENVIRONMENT` | `local` | `deployment.environment.name` |
| `OTEL_RESOURCE_ATTRIBUTES` | none | extra resource attributes, read by the SDK's default resource |

Metrics are pushed every 15 s (`PeriodicReader`). In the chart the `otel.*`
values render these variables (`otel.endpoint`, `otel.serviceName`,
`environment`). When no Collector listens, exporter errors are logged at DEBUG
as `opentelemetry error`, so a normal info-level stream stays quiet.

## Metrics

| Instrument | Type | Unit | Attributes | Source |
| --- | --- | --- | --- | --- |
| `ordering.orders.placed` | counter (int64) | `{order}` | `outcome` (`placed` or `rejected`), `reason` (only when rejected) | `internal/adapters/outbound/telemetry/order_metrics.go` |
| `http.server.request.duration` | histogram | `s` | `http.method`, `http.scheme`, `http.route` (semconv v1.20 names, set by `otelchimetric`) | `otelchimetric` in the REST, MCP and A2A routers |
| Go runtime (`runtime.Start`) | gauges and counters | various | none | `internal/adapters/outbound/telemetry/telemetry.go` |

`ordering.orders.placed` is recorded once per checkout attempt, by
`PlaceOrder` (`internal/application/usecases/checkout_metrics.go`) and only in
`cmd/api`. `reason` is the problem slug the REST adapter answers with:
`offer-not-quotable`, `promise-capacity-exhausted`, `book-closed`,
`delivery-promise-unavailable`, `group-spans-nodes`, `line-without-promise`,
`promise-destination-mismatch`, `concurrent-update`, `validation-failed`, and
`internal-error` for anything else (a database error, a bug). A falling
`placed` share with rising `offer-not-quotable` or
`promise-capacity-exhausted` means shoppers are refused by delivery promising,
not by their own input; rising `internal-error` means the service itself is
failing. In Prometheus the counter appears as `ordering_orders_placed_total`.

The service exports **no consumer-lag, outbox-depth or DLQ metric**: use broker
tooling and the SQL in the [Runbook](/docs/operations/runbook#outbox-relay).

## Traces

| Span | Kind | Where | Notes |
| --- | --- | --- | --- |
| HTTP server span named after the chi route pattern | server | REST (`cmd/api`), MCP and A2A routers | `otelchi`; carries `http.route` and the status code |
| `<topic> process` | consumer | one per handling attempt in `internal/adapters/inbound/kafka/tracing.go` | attributes `messaging.system=kafka`, `messaging.destination.name`, `messaging.operation.name=process`; parented to the trace in the message headers, a new trace when absent; an error marks the span |
| `a2a.skill <skill>` | internal | `internal/adapters/inbound/a2a/skills.go` | attribute `a2a.skill` |

Trace continuity across Kafka: when a use case enqueues an event, the active
span context is injected as `traceparent`/`tracestate` into the outbox row's
headers inside the same transaction (`internal/adapters/outbound/postgres/outbox_trace.go`);
the relay publishes them as Kafka headers and the consumers of other contexts
extract them. The CloudEvents attributes and body are untouched.

## Logs

JSON via `log/slog` on stdout, level from `LOG_LEVEL`. Records written with a
span-carrying context carry `trace_id` and `span_id`
(`internal/adapters/outbound/telemetry/slogotel.go`). Every request is logged
once by `requestlog` (skipping `/healthz` and `/readyz`):

| Message | Level | Fields |
| --- | --- | --- |
| `http request` | INFO (2xx/3xx), WARN (4xx), ERROR (5xx) | `method`, `route` (the pattern, never the raw path), `status`, `duration_ms`, `bytes`, `request_id`, `trace_id`, `span_id` |

Messages by binary (all in the code, searchable in Loki as `{app="customer-ordering"}`):

| Message | Level | Where |
| --- | --- | --- |
| `telemetry configured` (`service_name`, `otlp_endpoint`) | INFO | all binaries at boot |
| `postgres adapters configured` / `postgres repositories configured` | INFO | `cmd/api` / `cmd/mcp`, `cmd/a2a` |
| `run migrations`, `ping postgres` as `retrying` (`op`, `attempt`, `in`, `err`) and `succeeded after retry` | WARN, INFO | boot retry, `internal/bootretry` |
| `consumer running` (`consumer`, `topic`, `group_id`, `brokers`) | INFO | `cmd/api`, one per consumer |
| `outbox relay running` (`interval`, `max_attempts`, `topic`) | INFO | `cmd/api` |
| `http server listening` / `mcp server listening (Streamable HTTP)` / `a2a agent listening (JSON-RPC over HTTP)` (`addr`) | INFO | listeners |
| `mcp server starting`, `a2a agent starting` (`version`, `environment`) | INFO | agent binaries |
| `shutdown: readiness flipped to not-ready; waiting for traffic to drain` (`drain_delay`) | INFO | `cmd/api` on SIGTERM |
| `worker did not stop before the shutdown drain deadline` (`worker`) | WARN | `cmd/api` |
| `service exited with error`, `mcp server exited with error`, `a2a agent exited with error` (`error`) | ERROR | boot failures, a worker that stopped |
| `consumer stopped`, `outbox relay stopped`, `kafka relay writer close failed` | ERROR | `cmd/api` |
| `invalid OUTBOX_RELAY_INTERVAL; using the default`, `invalid OUTBOX_MAX_ATTEMPTS; using the default`, `ignoring invalid SHUTDOWN_DRAIN_DELAY` | WARN | configuration typos |
| `outbox relay pass failed` (`error`) | ERROR | relay, every failed pass |
| `outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause` | ERROR | relay, once per parked row |
| `skipping a message that is not a valid CloudEvent` (`consumer`, `topic`, `error`) | WARN | consumers |
| `dead-lettering an event that can never be applied`, `skipping an event that can never be applied` (`consumer`, `type`, `event_id`, `error`) | WARN | consumers |
| `<consumer> handling failed; retrying the same message` (`error`, `partition`, `offset`, `attempt`, `max_attempts`, `retry_in`) | ERROR | consumers, per failed attempt (at most 5) |
| `<consumer> handling failed after the maximum attempts; dead-lettering the message`, `<consumer> message can never be applied; dead-lettering it without retry` | ERROR | consumers, just before the publish to `<topic>.dlq` |
| `<consumer> dead-letter publish failed; retrying the same message` | ERROR | the DLQ write itself failed; the partition is blocked until it succeeds |
| `<consumer> broker connection stalled or lost; fetching again after the backoff` | WARN | consumers |
| `consumed event skipped` (`eventId`, `type`, `reason`) | INFO | consumers: unknown order or a status that would move backwards |
| `event processed` (`type`, `event_id`, `what`) | DEBUG | consumers |
| `readiness: dependency is not ready` (`dependency`, `error`) | WARN | `/readyz`; the cause of a 503 lives here, never in the response |
| `compensating cancel refused by delivery-promise; nothing to release`, `compensating cancel of a confirmed promise failed; the hold stays at delivery-promise`, `cancelled a confirmed promise of a failed checkout` | WARN, ERROR, INFO | checkout compensation |
| `mcp tool failed with an unexpected error`, `a2a skill failed` | ERROR | agent adapters |

## Dashboards and alerts

Only what exists in `warehouse-infra` on `develop`:

- **Dashboard** `warehouse-customer-ordering` ("Warehouse — Customer Ordering",
  `terraform/dashboards/contexts/customer-ordering.json`): Kong request rate,
  5xx ratio and p95 latency (request vs. upstream) for the API, MCP and reports
  routes; HTTP RED by route and p95 duration by process from
  `http_server_request_duration_seconds_*` filtered on
  `service_name=~"customer-ordering.*"`; the business panel
  `ordering_orders_placed_total` by `outcome`; Go goroutines and memory; and
  Loki panels (live stream, volume by level, errors and warnings) on
  `{app="customer-ordering"}`.
- **Alert rules** (`terraform/alerting/warehouse-alert-rules.json`): group
  `warehouse-customer-ordering` has `WarehouseKong5xxRatioHigh` (more than 5%
  5xx on a Kong route for 5 min, warning) and `WarehousePodRestarting` (two or
  more restarts of a `customer-ordering-.+` container in 15 min, warning);
  group `warehouse-customer-ordering-slo` has the multi-window burn-rate alerts
  `WarehouseApiAvailabilityBurnFast`/`Slow` (1% error budget) and
  `WarehouseApiLatencyBurnFast`/`Slow` (95% of upstream requests under 400 ms).
  Their runbook link points at the [Runbook](/docs/operations/runbook).
- **Not present:** no alert on outbox depth, parked outbox rows, consumer lag
  or the DLQ topics, and no panel for them. Watch those with the SQL and broker
  commands in the Runbook.
