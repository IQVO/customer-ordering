---
id: entity-relationship
title: Entity-relationship diagram
sidebar_label: Entity-relationship
sidebar_position: 9
---

# Entity-relationship diagram

The Postgres schema as the embedded migrations create it
(`internal/adapters/outbound/postgres/migrations/0001` to `0004`, applied at boot
by golang-migrate). Eleven tables of the context plus golang-migrate's own
`schema_migrations`. Foreign keys exist only where the diagram draws a
relationship; the tracking read model and the infrastructure tables are keyed by
ids copied from events and carry no foreign key.

```mermaid
erDiagram
  shoppers ||--o{ shopper_addresses : "saves up to 20"
  customer_orders ||--|{ order_lines : contains
  customer_orders ||--|{ order_groups : "shipped in"
  customer_orders }o..o| shoppers : "placed by (shopper_id, no FK)"
  order_groups ||..o| group_signals : "(order_id, group_no)"
  order_groups ||..o{ tracking_packages : "(order_id, group_no)"
  order_groups ||..o| assignment_links : "(order_id, group_no)"

  shoppers {
    text shopper_id PK
    text email "unique on lower(email)"
    text status "ACTIVE"
    timestamptz registered_at
    bigint version
  }
  shopper_addresses {
    text shopper_id PK
    text address_id PK
    int position "unique per shopper"
    text name
    text line1
    text line2
    text city
    text region
    text postal_code
    text country_code
    text phone
  }
  customer_orders {
    text order_id PK
    text shopper_id "index with placed_at DESC"
    jsonb ship_to "synthetic PII"
    text destination_postal_prefix
    text status "9 order states"
    timestamptz placed_at
    timestamptz cancelled_at
    bigint version
  }
  order_lines {
    text order_id PK
    int line_no PK
    text catalog_item_id
    int quantity
  }
  order_groups {
    text order_id PK
    int group_no PK
    text promise_id
    text tier
    text witness_node_id
    timestamptz ship_by
    timestamptz deliver_by_earliest
    timestamptz deliver_by_latest
    text time_zone "nullable, ADR 0009"
    int_array line_nos
    text status "8 group states"
    int revision_no
    text assignment_id
    text_array tracking_numbers
    text_array delivered_tracking_numbers
    text cancel_reason
    text outcome_tracking_number
  }
  group_signals {
    text order_id PK
    int group_no PK
    text at_risk_reason
    text unsourceable_reason
    text promise_verdict
    int late_by_minutes
  }
  tracking_packages {
    text tracking_number PK
    text assignment_id
    text order_id
    int group_no
    text carrier
    text state
    timestamptz last_scan_at
    timestamptz delivered_at
    text exception_reason
    text outcome "LOST or RETURNED"
  }
  assignment_links {
    text assignment_id PK
    text order_id
    int group_no
    text promise_id
  }
  processed_events {
    text event_id PK
    text event_type
    timestamptz processed_at
  }
  idempotency_keys {
    text route PK
    text key PK
    text request_hash
    int status
    bytea body
    timestamptz created_at
  }
  outbox_events {
    bigserial id PK
    text event_id "unique"
    text topic
    text event_type
    bytea key
    bytea value
    jsonb headers
    timestamptz published_at "null until sent"
    int attempts
    text last_error
  }
```

Source: `internal/adapters/outbound/postgres/migrations/0001_customer_ordering_schema.up.sql`,
`0002_package_lost_and_returned.up.sql`, `0003_shopper_addresses_and_order_history.up.sql`,
`0004_group_window_time_zone.up.sql`. Columns not drawn: `order_groups` also holds
`node_id`, `locked_at`, `shipped_at`, `delivered_at`, `cancel_requested_at`,
`cancelled_at`, `cancel_refused_reason`, `lost_at`, `returned_at`;
`tracking_packages` also holds `last_scan_node_id`, `outcome_at`; `outbox_events`
also holds `subject`, `dataschema`, `created_at`.

## Reading it

- **Aggregates.** `customer_orders`, `order_lines` and `order_groups` are the
  `CustomerOrder` aggregate, saved together under the optimistic rule "stored
  `version` equals the version loaded". `shoppers` and `shopper_addresses` are
  the `Shopper` aggregate; `shopper_addresses` replaced the old JSON column in
  migration 0003 (legacy addresses got ids `legacy-<md5>`).
- **PII.** `customer_orders.ship_to` and `shopper_addresses` are the only
  places an address is stored. The order list index
  `customer_orders_history_idx (shopper_id, placed_at DESC, order_id)` serves
  `GET /orders?shopperId=` keyset paging.
- **Read model.** `group_signals`, `tracking_packages` and `assignment_links` are
  event-fed upserts that tolerate any arrival order; they are joined with the
  order's groups by `GET /orders/{orderId}/tracking`.
- **Infrastructure.** `processed_events` (inbox) is written in the same
  transaction as the effect it guards. `idempotency_keys` holds a POST answer
  for 24 hours. `outbox_events` has a partial index on rows not yet published;
  the relay claims them, and rows past `OUTBOX_MAX_ATTEMPTS` failures are
  excluded from claims (see the [Runbook](/docs/operations/runbook#outbox-relay)).
- **Down migrations** exist for all four steps; there is no cross-service
  database access (`MIGRATIONS_DATABASE_URL` is the direct DSN, `DATABASE_URL`
  goes through PgBouncer).
