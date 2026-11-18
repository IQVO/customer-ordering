---
paths:
  - "cmd/co-projector/**"
  - "cmd/co-reports/**"
  - "analytics/**"
  - "internal/analytics/**"
  - "internal/adapters/outbound/analyticsstore/**"
  - "internal/adapters/inbound/kafka/analytics_*.go"
  - "internal/adapters/inbound/reportshttp/**"
---

# Analytics read side (projector + reports)

Record: `docs/adr/0018-analytics-read-side.md`. The read side consumes the facts this context already publishes; do not
change the encoder, the outbox or the `.events` contract from here.

- Source: ONLY `retailnetwork.customer-ordering.events` (never a sibling's topic, never a REST or MCP call to a sibling;
  `internal/architecture/analytics_fitness_test.go` enforces it). Three projected types, dispatched on the FULL
  CloudEvents `type` (`internal/adapters/inbound/kafka/analytics_decode.go`); anything else (and non-CloudEvents) is
  skipped with a sampled WARN. There is no `warehouse.*` topic or type in this repo.
- **No PII, ever**: ids, codes, counts and instants only. Never read or store the shopper, the postal prefix, the
  consolidation key, the pickup point id, the cancellation `reason`, the notice `fact` or any free text. A notice
  `family` is stored normalised (`report.NormalizeFamily`), never verbatim.
- `cmd/co-projector` is the ONLY writer of the SEPARATE analytical database (`ANALYTICS_DATABASE_URL`, `KAFKA_BROKERS`,
  `ANALYTICS_CONSUMER_GROUP` default `customer-ordering-analytics`, `ADMIN_ADDR` `:8091`). It never opens the OLTP
  database. `cmd/co-reports` (`HTTP_ADDR` `:8092`) reads through a read-only pool (`ANALYTICS_READER_DATABASE_URL`,
  falling back to `ANALYTICS_DATABASE_URL`).
- Dedupe on the CloudEvents `id` and the fact write are ONE analytical-database transaction (`Projection.Apply`); test
  atomicity with a trigger that RAISEs (`postgres_integration_test.go`).
- Delivery policy: dead-letter ONLY deterministic poison (bad payload, subject mismatch, missing field, Postgres class
  22/23) to `retailnetwork.customer-ordering.analytics.dlq`; retry transient failures forever with capped backoff.
- `internal/analytics/report` is pure and imports no other internal package (arch test). SQL only counts; the
  vocabulary, the bound of 5 notice kinds, the merge by day, shares, range rules and the freshness lag live there, in
  `make mutation` and in the coverage scope. One contract suite (`analyticsstore/contract_test.go`) runs against
  `MemoryStore` and Postgres: change both twins together. `features/analytics_reports.feature` drives the REAL encoder
  into the REAL projector consumer and the REAL reports router.
- `analytics/migrations` is embedded (`analytics/migrations.go`), additive, with down files; never edit an applied
  migration, add the next number.
- Reports are unauthenticated, read-only GETs under `/reports/...` (plus `/healthz`, `/readyz`), **snake_case** (the one
  exception to the camelCase of the rest of the REST contract), UTC days, `from`/`to` inclusive dates. Any route change
  edits `apis/openapi.yaml` (tag `reports`) and regenerates the REST reference in the same commit.
- Chart: the `analytics:` block is opt-in; keep `tests/render_test.sh` and `tests/test_service_selectors.py` green and
  the default render byte-identical.
