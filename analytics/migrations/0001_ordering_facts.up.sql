-- customer-ordering analytics read model (ADR 0018).
--
-- This is the ANALYTICAL database, separate from the OLTP database. It is
-- written only by cmd/co-projector and read (read-only) by cmd/co-reports.
-- Everything here is a projection of this context's own integration topic
-- (retailnetwork.customer-ordering.events), not a source of truth, and can be
-- dropped and rebuilt from the topic.
--
-- NO PII: ids, codes, counts and instants only. No shopper, no name, no
-- address, no postal prefix, no consolidation key, no cancellation reason (free
-- text), no pickup point id.

-- Idempotency: every applied CloudEvents id is recorded here exactly once, in
-- the SAME transaction as its effect on a fact table. occurred_at is the
-- event's CloudEvents `time` (the basis of /reports/freshness).
CREATE TABLE analytics_processed_events (
    event_id    TEXT        PRIMARY KEY,
    event_type  TEXT        NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per order, from CustomerOrderPlaced.
--   placed_at            payload `placedAt`, the basis of the report day
--   group_count          shipment groups of the order (>= 1)
--   consolidated_groups  groups promised under the CONSOLIDATED tier (ADR 0013)
--   pickup_point         the order was placed to a pickup point (ADR 0016)
CREATE TABLE placed_orders (
    order_id            TEXT        PRIMARY KEY CHECK (order_id <> ''),
    placed_at           TIMESTAMPTZ NOT NULL,
    group_count         INTEGER     NOT NULL CHECK (group_count >= 1),
    consolidated_groups INTEGER     NOT NULL CHECK (consolidated_groups >= 0),
    pickup_point        BOOLEAN     NOT NULL,
    event_id            TEXT        NOT NULL,
    -- A table constraint: it reads two columns.
    CONSTRAINT placed_orders_consolidated_within_groups CHECK (consolidated_groups <= group_count)
);

-- One row per CustomerOrderCancelled. An order can be cancelled in several
-- requests (a subset of its groups each time), so the report counts DISTINCT
-- orders.
CREATE TABLE order_cancellations (
    event_id     TEXT        PRIMARY KEY,
    order_id     TEXT        NOT NULL CHECK (order_id <> ''),
    cancelled_at TIMESTAMPTZ NOT NULL
);

-- One row per ShopperNoticeDecided, keyed by the notice's own dedupe key
-- (sha256 of orderId|groupNo|family|factDigest, ADR 0017). family is one of
-- the ADR 0017 codes or UNRECOGNISED: a bounded vocabulary, never free text.
CREATE TABLE shopper_notices (
    notice_key TEXT        PRIMARY KEY CHECK (notice_key <> ''),
    order_id   TEXT        NOT NULL CHECK (order_id <> ''),
    family     TEXT        NOT NULL CHECK (family <> ''),
    decided_at TIMESTAMPTZ NOT NULL,
    event_id   TEXT        NOT NULL
);

-- The reports filter each fact on its business instant.
CREATE INDEX idx_placed_orders_placed_at ON placed_orders (placed_at);
CREATE INDEX idx_order_cancellations_cancelled_at ON order_cancellations (cancelled_at);
CREATE INDEX idx_shopper_notices_decided_at ON shopper_notices (decided_at);
CREATE INDEX idx_analytics_processed_events_occurred_at ON analytics_processed_events (occurred_at);
