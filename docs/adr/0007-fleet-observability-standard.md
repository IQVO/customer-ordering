# ADR 0007: Adopt the fleet observability standard (logs, metrics, traces)

## Status

Accepted (2026-10-25).

## Context

customer-ordering logged JSON with `log/slog` and nothing else: no trace or
span id on a log line, no request id, no request log, no metrics, no traces, no
`traceparent` on the events it publishes, and no OpenTelemetry settings in the
Helm chart. The warehouse-systems services (inventory-storage, wes-work-planning)
already share one pipeline and one convention, recorded in their ADRs "standard
metrics convention" (inventory-storage ADR 0016, wes-work-planning ADR 0013):

- pods export OTLP/gRPC to the in-cluster Collector
  (`otel-collector.observability.svc.cluster.local:4317`), which feeds Jaeger
  (traces) and a Prometheus exporter (metrics);
- logs stay on stdout as JSON for Alloy and Loki.

Without the same wiring here an order could not be followed from the REST call
through the Kafka event to the consumer in another service, a log line could not
be joined to its trace, and no dashboard could show checkout rate, errors or
latency. The alternatives weighed were a service-specific setup (rejected: the
point is that every repo answers the same query the same way) and the OTel Go
auto-instrumentation agents (rejected: they cannot follow a trace across
`segmentio/kafka-go`, which has no instrumentation, nor name spans by the chi
route pattern).

## Decision

Tier 1 (identical in every repo) and Tier 2 (this context's business metric):

1. **Telemetry adapter.** `internal/adapters/outbound/telemetry` holds `Setup`
   (OTLP/gRPC traces and metrics, W3C TraceContext + Baggage propagator, Go
   runtime metrics; exporters dial lazily, so no Collector means dropped
   telemetry, never a blocked boot or request; returns a shutdown func) and
   `WithTraceContext` (stamps `trace_id`/`span_id` on a log record written with
   a span-carrying context). Only the composition roots (`cmd/api`, `cmd/mcp`)
   and the other adapters import it; `internal/domain` and `internal/application`
   never import OpenTelemetry.
2. **Logger.** `slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(os.Stdout, ...)))`,
   `LOG_LEVEL`, uppercase levels. Code on a request or message path uses the
   `*Context` slog variants; the bare variants have no span.
3. **HTTP middleware order** on both chi routers (REST and MCP):
   `RequestID` -> `otelchi.Middleware(serviceName, WithChiRoutes(r))` ->
   `otelchimetric.NewServerRequestDuration` -> `RequestLogger` -> `Recoverer`;
   the existing `rejectNUL` and Idempotency-Key middleware stay after them.
   `telemetry.Setup` runs before the router is built (otelchimetric binds the
   global MeterProvider at construction) and is flushed last on shutdown. The
   request line carries `method`, `route` (the chi pattern, never the raw path,
   which holds ids), `status`, `duration_ms`, `bytes` and `request_id`; 5xx is
   ERROR, 4xx WARN, the rest INFO; `/healthz` and `/readyz` are not logged.
   `RequestLogger` lives in `internal/adapters/inbound/requestlog`, not in the
   REST adapter, because `cmd/mcp` may not import the REST adapter
   (`TestMCPServerIsReadOnly`) and both routers must log identically.
4. **Traces over Kafka.** `OutboxRepo.Append` stamps `traceparent`/`tracestate`
   from the active span into the stored outbox headers, inside the transaction
   that writes the aggregate (`internal/adapters/outbound/postgres/outbox_trace.go`).
   The relay publishes the headers unchanged as Kafka headers; each inbound
   consumer extracts them and runs every handling attempt in a consumer span
   named `<topic> process`, so the use case's logs carry the publisher's trace
   id and an event enqueued while handling continues the trace. Only extra
   headers are added: the CloudEvents attributes and body, and the encoder's
   output, are unchanged (ADR 0004).
5. **Environment contract.** `cmd/api` and `cmd/mcp` read `OTEL_SERVICE_NAME`
   (defaults `customer-ordering` and `customer-ordering-mcp`),
   `OTEL_EXPORTER_OTLP_ENDPOINT` (default `telemetry.DefaultEndpoint`, a bare
   `host:port` or a URL), `SERVICE_VERSION`, `ENVIRONMENT` (read by `Setup`) and
   `LOG_LEVEL`.
6. **Chart.** `otel.enabled`, `otel.endpoint`, `otel.serviceName` render
   `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_SERVICE_NAME` (`<name>-mcp` for the
   mcp component); `SERVICE_VERSION` is the image tag, or the chart appVersion
   when empty, for the api and the mcp component; the api also renders
   `ENVIRONMENT`. `charts/customer-ordering/tests/render_test.sh` asserts it and
   scans `cmd/` for the variables the binaries read.
7. **Tier 2 business metric.** One counter, `ordering.orders.placed`, unit
   `{order}`, attribute `outcome` (`placed` | `rejected`) and, on a rejection,
   `reason` (the problem slug the REST adapter answers with: `offer-not-quotable`,
   `promise-capacity-exhausted`, `book-closed`, `delivery-promise-unavailable`,
   `group-spans-nodes`, `validation-failed`, `concurrent-update`,
   `internal-error`, ...). `ports.OrderMetrics` is the application port;
   `PlaceOrder` calls it once per checkout attempt (a replay of a stored order is
   not an attempt); the OTel implementation is in the telemetry adapter and is
   injected from `cmd/api`; a nil `Deps.Metrics` is a no-op. It follows the
   fleet convention: dotted lowercase name, one counter with an `outcome`
   attribute rather than two counters, UCUM unit, a description that says what a
   human should conclude when it moves.

Not part of this ADR: dashboards and alert rules, which live in warehouse-infra
and are generated per deployed context from the instrument names above.

## Consequences

- A request, its log lines, the events it enqueued and the consumers that handle
  them in other services share one trace id; `trace_id` in Loki jumps to Jaeger.
- Request rate, errors and duration come from `http.server.request.duration`
  (otelchimetric) with no hand-rolled counter; checkout health from
  `ordering.orders.placed`.
- Every new log call on a request or message path must use a `*Context` variant,
  or its line carries no trace id.
- A consumer span is created per handling attempt, so a retried message shows as
  several spans under the same parent.
- The trace parent of a consumed event is the span that was active at the outbox
  write (the REST request or the upstream consumer span), not a separate publish
  span: the relay is asynchronous and adds no span of its own.
- `OutboxRepo.Insert` (the raw path used by tests) stamps nothing; only `Append`,
  the path every use case takes, does.
