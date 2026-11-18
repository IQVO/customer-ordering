# ADR 0018: analytics read side

## Status

Accepted (2026-11-17). Additive: no published payload, topic, OLTP table or REST route of the api changes. Adds two
binaries (`cmd/co-projector`, `cmd/co-reports`), one analytical-database migration set (`analytics/migrations`), one
read-only report route (plus the freshness probe of the read side), two default-off chart components and a second,
separate database. Copies the shape of the pilot, shipping-and-tracking ADR 0012.

## Context

Customer-ordering owns the order-side numbers of the network baseline: how many orders are placed and cancelled, how
many take the consolidated tier (ADR 0013), how many go to a pickup point (ADR 0016), how many ship apart and how many
shopper notices the policy decides (ADR 0017). Nothing answers those as a trend today: the facts leave as CloudEvents
and the OLTP tables hold the current state of each order only.

Every fact the numbers need is already published on this context's own integration topic,
`retailnetwork.customer-ordering.events` (ADR 0004, ADR 0017). `apis/asyncapi.yaml` declares no analytics-only event
and nothing is published on `retailnetwork.customer-ordering.analytics` (ADR 0017: "no analytics topic"). So, as in the
pilot, **nothing new is published**: the projector reads back the topic the service already writes. The event prefix is
the one this repo's own events carry, `com.retailnetwork.commerce.customer-ordering.<entity>.<Event>`; there is no
`warehouse.*` topic or type in this context, ever.

## Decision

### 1. What is projected

The projector consumes ONLY this context's own integration topic. It never reads a sibling's topic and never calls a
sibling over REST or MCP (the consumed delivery-promise, order-sourcing and shipping-and-tracking events are not read:
every number below is derivable from what customer-ordering itself publishes). No event is fanned out and the outbox is
unchanged.

All three published types are projected, dispatched on the FULL CloudEvents `type`:

| type | payload fields read | fact |
| --- | --- | --- |
| `com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderPlaced` | `orderId` (= subject), `placedAt`, `groups[].tier`, whether `pickupPointId` is present | `placed_orders` (one row per order) |
| `com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderCancelled` | `orderId` (= subject), `cancelledAt` | `order_cancellations` (one row per request) |
| `com.retailnetwork.commerce.customer-ordering.order.ShopperNoticeDecided` | `orderId` (= subject), `family`, `noticeKey`, `decidedAt` | `shopper_notices` (one row per notice key) |

**No PII, by construction.** The decoder has no field for, and the tables have no column for: the shopper id, the
destination postal prefix, the consolidation key (a keyed hash of shopper and address), the pickup point id, the
cancellation `reason` (free text, may hold anything the shopper typed), the notice `fact` (which carries the free-text
advisory `reason`), the notifier, group numbers, promise ids, node ids or line items. Only ids (the order id, the opaque
notice key), codes (the tier is compared, never stored; the notice family is one of a closed vocabulary) and instants
are kept. A `family` outside the ADR 0017 vocabulary is stored as `UNRECOGNISED`, never verbatim, so the read side's
vocabulary is bounded and a later family is counted, not dropped (a new family is added to the vocabulary by the ADR
that introduces it).

Tables of the analytical database (`analytics/migrations/0001_ordering_facts`):

- `analytics_processed_events (event_id PK, event_type, occurred_at, applied_at)`: the dedupe set; `occurred_at` is the
  CloudEvents `time`, the basis of the freshness lag.
- `placed_orders (order_id PK, placed_at, group_count >= 1, consolidated_groups, pickup_point, event_id)`.
- `order_cancellations (event_id PK, order_id, cancelled_at)`.
- `shopper_notices (notice_key PK, order_id, family, decided_at, event_id)`.

The model is order-independent: it stores facts and the report joins nothing, it counts per fact table and merges by
day. A cancellation whose placement was not (yet) projected is still counted on its own day.

### 2. The report

`GET /reports/ordering-performance?from=&to=`, one row per UTC day of the **business instant in the payload**
(`placedAt`, `cancelledAt`, `decidedAt`), not of the CloudEvents `time`. JSON fields are **snake_case** (the instruction
of this ADR's brief; see Consequences for the contrast with the camelCase of the rest of the REST contract).

| field | definition |
| --- | --- |
| `orders_placed` | `CustomerOrderPlaced` of that day |
| `orders_cancelled` | DISTINCT orders with a `CustomerOrderCancelled` of that day. It is a **request** (ADR 0002 A3: order-sourcing may still refuse it) and it is **not a cohort** of that day's placements: an order placed on Monday and cancelled on Tuesday counts on Tuesday |
| `consolidated_tier_orders` | placed orders with at least one `CONSOLIDATED` group (ADR 0013) |
| `pickup_point_orders` | placed orders with a `pickupPointId` (ADR 0016) |
| `split_shipment_orders` | placed orders with two or more shipment groups |
| `split_shipment_share` | `split_shipment_orders / orders_placed`, `null` on a day without placed orders |
| `shopper_notices_decided` | `ShopperNoticeDecided` of that day, whatever the notifier (a `NOBODY` notice is a decided fact too, ADR 0017); always the exact total |
| `shopper_notices_by_kind` | `[{kind, count}]` of the notice families, **at most 5**, most frequent first, ties by `kind` |
| `shopper_notices_other` | the notices of the families left out by the bound of 5 |

The report also carries `from`, `to` and `freshness` `{as_of, lag_seconds}`; `GET /reports/freshness` serves the same
`freshness` alone, like the pilot (`as_of` = the newest CloudEvents `time` the projection applied, `lag_seconds` = now -
`as_of`, never negative; both `null` until the first event).

**Split shipment is derivable from own facts, so it is reported.** `CustomerOrderPlaced.groups` lists the order's
shipment groups, each with its own promise and its own shipment (ADR 0001, ADR 0002). Two or more groups is an order
that ships apart. What this does NOT see: one group delivered in several packages (the tracking numbers of a group are
consumed facts from shipping-and-tracking, ADR 0003, and are outside the own-events rule) and a group merged with another
order later (ADR 0012). The share is "ordered in several shipments", not "arrived in several parcels".

**The bound of five.** ADR 0017 has seven notice families, so a bound of five means a busy day lists its five most
frequent families and folds the other two into `shopper_notices_other`; the bound is a function of the pure report
package (`MaxNoticeKinds`), not a SQL limit, so the total stays exact. This is the reading of the brief's "by kind
bounded to 5" that this ADR chose; a different reading (e.g. only five families exist) would change one function.

**Days are sparse**: a day with no placement, no cancellation and no notice has no row (like the pilot); an empty range
is `days: []` and an empty projection has `null` freshness. Range rules are the pilot's: `from` and `to` are UTC dates
(`YYYY-MM-DD`), inclusive; both omitted = the 30 days ending today; only `to` = the 30 days ending on `to`; only
`from` = `from` .. today; `from` after `to` or a range over 366 days is a 400 `invalid-report-range` problem. The
route is a collection-shaped report, `x-pagination-exempt: report`.

SQL only counts. The vocabulary (consolidated tier, split threshold, notice families), the bound of five, the merge by
day, the shares, the range rules and the freshness lag live in `internal/analytics/report`, a pure package that imports
no other internal package (arch test), is in the coverage scope and in `make mutation`. ONE contract suite runs against
the in-memory twin and against real Postgres.

### 3. Delivery policy (the projector)

- Consumer group `ANALYTICS_CONSUMER_GROUP` (default `customer-ordering-analytics`), fixed and stable; at-least-once,
  the offset is committed only after the message was handled. A brand-new group starts at the earliest offset, so the
  model rebuilds from retained history.
- **Dedupe and projection in ONE analytical-database transaction**: `INSERT ... ON CONFLICT DO NOTHING` into
  `analytics_processed_events`; the fact is written only if that insert claimed the id. A failure leaves neither; a
  replay of an applied id is a no-op. Two further natural keys guard a fact published twice under different ids: an
  order is placed once (`order_id`), a notice counts once (`notice_key`).
- Not a CloudEvents 1.0 message, or a CloudEvent of a type that is not projected: skipped and committed past with a
  **sampled WARN** (the first, then at most one per minute with the count suppressed).
- **Dead-letter ONLY deterministic poison**: an undecodable payload, a subject that is not the order id, a missing
  required field or instant, or a store rejection in Postgres class 22 or 23, to
  `retailnetwork.customer-ordering.analytics.dlq` (NOT `<events>.dlq`: that name belongs to the sibling consumers of the
  same topic) with `x-dlq-*` headers. A failed DLQ write is retried, so poison is never lost.
- **Transient failures** are retried forever with capped exponential backoff (200ms doubling to 5s) and never
  dead-lettered: a database outage must not turn into silently missing analytics.
- **A publisher defect the projector tolerates.** The notice use case (ADR 0017) builds `ShopperNoticeDecided`
  without its aggregate header, so the CloudEvents `time` of every notice published today is the zero instant
  (`0001-01-01T00:00:00Z`; the golden file hides it because it sets a header). Dead-lettering every notice for it would
  make the report blind to them, so for this one type a zero `time` takes `decidedAt` as the freshness instant. The
  publisher is deliberately NOT changed here (a visible change to a published contract, outside this ADR): the fix is
  one line in `usecases/notice.go` and a golden, and the tolerance can then be dropped.

### 4. The binaries

- `cmd/co-projector`: the ONLY writer of the analytical database. Env: `ANALYTICS_DATABASE_URL` (required, a direct
  DSN: it runs the embedded migrations), `KAFKA_BROKERS` (required), `ANALYTICS_CONSUMER_GROUP`, `ADMIN_ADDR` (default
  `:8091`, `/healthz` and `/readyz`), `LOG_LEVEL`, `OTEL_SERVICE_NAME` (default `customer-ordering-projector`),
  `OTEL_EXPORTER_OTLP_ENDPOINT`, `SERVICE_VERSION`, `ENVIRONMENT`. It never opens the OLTP database. The migrations are
  embedded (`analytics/migrations.go`), so there is no `ANALYTICS_MIGRATIONS_PATH`.
- `cmd/co-reports`: read-only HTTP on `HTTP_ADDR` (default `:8092`): `GET /reports/ordering-performance`,
  `GET /reports/freshness`, `/healthz`, `/readyz` (analytical-database ping). It reads `ANALYTICS_READER_DATABASE_URL`,
  falling back to `ANALYTICS_DATABASE_URL` for single-role local runs, through a pool whose sessions default to
  read-only transactions with a 15s statement timeout. No Kafka, no OLTP database, no call to any sibling, **no auth**
  (every endpoint of the network is unauthenticated by decision; none is added). Errors are problem+json. The routes
  live in `apis/openapi.yaml` under the tag `reports` with a per-path server on `:8092`.
- Both ship in the one image (`/app/co-projector`, `/app/co-reports`; the Dockerfile builds every `cmd/*`).

### 5. Why a separate analytical database

The projection is a derived copy that can be dropped and rebuilt from the topic. Keeping it out of the OLTP database
means a heavy report cannot take OLTP connections, the two schemas evolve independently, the reports role can be
read-only on a database that holds nothing transactional, and the projector holds no OLTP credentials. Both DSNs are
direct (PgBouncer is not involved: the projector runs migrations).

### 6. Packaging (rollout safe)

The chart gains an opt-in `analytics:` block (`analytics.enabled: false`; with it off the render is byte-identical to
before, default and all-components): components `analytics-projector` (Deployment, no Service) and `analytics-reports`
(Deployment + ClusterIP Service on :80, an opt-in Kong HTTPRoute `/api/customer-ordering/reports` rewritten to
`/reports` and annotated `konghq.com/regex-priority: "10"` because it sits inside the API route's prefix), an `analytics`
Secret or `analytics.database.existingSecret`. Render-time guards refuse `analytics.enabled` without a DSN source,
without `kafka.brokers` or without a consumer group. The projector pod never gets the OLTP `DATABASE_URL`; the reports
pod gets neither Kafka nor any DSN but the reader's. Nothing in the api, its consumers, its outbox or its tables changes.

## Consequences

- The `.events` topic gains one service-owned consumer group (`customer-ordering-analytics`); its contract is
  unchanged. The reports are only as fresh as the projector, which `freshness` makes observable.
- The model starts at the earliest retained offset; orders placed before that have no history.
- `apis/openapi.yaml` and `apis/asyncapi.yaml` change additively (a path, schemas, one `receive` operation and prose);
  consumers that pin either file byte for byte (contracts-gate pins in shipping-and-tracking, checkout and
  storefront-bff at the time of writing) refresh their pins after this merges.
- The reports use **snake_case**, while every other body of this service's REST contract (ADR 0010) and every event is
  camelCase. The brief asked for snake_case; the exception is confined to the `reports` tag and stated in the spec's
  description. Revisiting it is a rename of the wire structs and the OpenAPI schemas, nothing else.
- `orders_cancelled` counts requests, not completed cancellations (ADR 0002 A3), and is not a rate against that day's
  placements; no cancellation rate is published because the cohorts differ.
- A deploy needs a second database, two roles (projector read-write, reports read-only), the DLQ topic and the consumer
  group above (`warehouse-infra`).
- The mutation gate: the pure `internal/analytics/report` package is run by `make mutation`; the CI `mutation-fast` job
  loops `./internal/domain/*/` only, and this change does not edit the workflow, so CI does not run gremlins on the
  report package until the job's loop adds it.

## Alternatives considered and rejected

- **Read the siblings' topics (e.g. package outcomes) for richer KPIs**: a projector reads only its own context's topic.
- **Fan the facts out to an analytics topic (`FanoutEncoder`)**: every fact the KPIs need is already on `.events`;
  doubling them would need an outbox change for no new information.
- **Store the cancellation reason or the pickup point id for a breakdown**: free text and identifiers of a place a
  person chose are not needed for a count, and the read side holds ids and codes only.
- **Fix the zero `time` of `ShopperNoticeDecided` in this change**: a change to a published event, not part of an
  additive read side; recorded instead (section 3).
- **Zero-fill the days**: the pilot omits empty days and a consumer can fill them; a row of zeros would also have to
  carry a `null` share to stay honest.
- **Dead-letter after N attempts for every failure**: drops analytics for events that failed only because the database
  was briefly down.
