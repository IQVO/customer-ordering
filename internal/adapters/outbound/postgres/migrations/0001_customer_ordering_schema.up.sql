-- customer-ordering schema (ADR 0002, 0003). Ship-to data is synthetic PII held
-- ONLY here (customer_orders.ship_to, shoppers.saved_addresses).

CREATE TABLE shoppers (
    shopper_id      text        PRIMARY KEY,
    email           text        NOT NULL,
    status          text        NOT NULL CHECK (status IN ('ACTIVE')),
    saved_addresses jsonb       NOT NULL DEFAULT '[]'::jsonb,
    registered_at   timestamptz NOT NULL,
    version         bigint      NOT NULL CHECK (version >= 1)
);
CREATE UNIQUE INDEX shoppers_email_uq ON shoppers (lower(email));

CREATE TABLE customer_orders (
    order_id                  text        PRIMARY KEY,
    shopper_id                text        NOT NULL,
    ship_to                   jsonb       NOT NULL,
    destination_postal_prefix text        NOT NULL,
    status                    text        NOT NULL CHECK (status IN ('PLACED','IN_FULFILLMENT','PARTIALLY_SHIPPED','SHIPPED','PARTIALLY_DELIVERED','DELIVERED','CANCELLED')),
    placed_at                 timestamptz NOT NULL,
    cancelled_at              timestamptz,
    version                   bigint      NOT NULL CHECK (version >= 1)
);
CREATE INDEX customer_orders_shopper_idx ON customer_orders (shopper_id);

CREATE TABLE order_lines (
    order_id        text    NOT NULL REFERENCES customer_orders (order_id) ON DELETE CASCADE,
    line_no         integer NOT NULL CHECK (line_no >= 1),
    catalog_item_id text    NOT NULL,
    quantity        integer NOT NULL CHECK (quantity >= 1),
    PRIMARY KEY (order_id, line_no)
);

CREATE TABLE order_groups (
    order_id                   text        NOT NULL REFERENCES customer_orders (order_id) ON DELETE CASCADE,
    group_no                   integer     NOT NULL CHECK (group_no >= 1),
    promise_id                 text        NOT NULL,
    tier                       text        NOT NULL CHECK (tier IN ('SAME_DAY','NEXT_DAY','TWO_DAY','STANDARD')),
    witness_node_id            text        NOT NULL,
    ship_by                    timestamptz NOT NULL,
    deliver_by_earliest        timestamptz NOT NULL,
    deliver_by_latest          timestamptz NOT NULL,
    line_nos                   integer[]   NOT NULL,
    status                     text        NOT NULL CHECK (status IN ('PROMISED','LOCKED','SHIPPED','DELIVERED','CANCELLATION_REQUESTED','CANCELLED')),
    revision_no                integer     NOT NULL DEFAULT 0,
    assignment_id              text,
    node_id                    text,
    locked_at                  timestamptz,
    shipped_at                 timestamptz,
    tracking_numbers           text[]      NOT NULL DEFAULT '{}',
    delivered_tracking_numbers text[]      NOT NULL DEFAULT '{}',
    delivered_at               timestamptz,
    cancel_reason              text,
    cancel_requested_at        timestamptz,
    cancelled_at               timestamptz,
    cancel_refused_reason      text,
    PRIMARY KEY (order_id, group_no)
);
CREATE INDEX order_groups_promise_idx ON order_groups (promise_id);

-- OrderTrackingView read models (ADR 0003).
CREATE TABLE assignment_links (
    assignment_id text    PRIMARY KEY,
    order_id      text    NOT NULL,
    group_no      integer NOT NULL,
    promise_id    text    NOT NULL DEFAULT ''
);

CREATE TABLE tracking_packages (
    tracking_number   text PRIMARY KEY,
    assignment_id     text,
    order_id          text,
    group_no          integer,
    carrier           text,
    state             text,
    last_scan_node_id text,
    last_scan_at      timestamptz,
    delivered_at      timestamptz,
    exception_reason  text
);
CREATE INDEX tracking_packages_order_idx ON tracking_packages (order_id, group_no);
CREATE INDEX tracking_packages_assignment_idx ON tracking_packages (assignment_id);

CREATE TABLE group_signals (
    order_id            text    NOT NULL,
    group_no            integer NOT NULL,
    at_risk_reason      text,
    unsourceable_reason text,
    promise_verdict     text,
    late_by_minutes     integer,
    PRIMARY KEY (order_id, group_no)
);

-- Infrastructure tables: inbox, idempotency keys, transactional outbox.
CREATE TABLE processed_events (
    event_id     text        PRIMARY KEY,
    event_type   text        NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE idempotency_keys (
    route        text        NOT NULL,
    key          text        NOT NULL,
    request_hash text        NOT NULL,
    status       integer     NOT NULL,
    content_type text        NOT NULL,
    body         bytea       NOT NULL,
    created_at   timestamptz NOT NULL,
    PRIMARY KEY (route, key)
);
CREATE INDEX idempotency_keys_created_idx ON idempotency_keys (created_at);

CREATE TABLE outbox_events (
    id           bigserial   PRIMARY KEY,
    event_id     text        NOT NULL UNIQUE,
    topic        text        NOT NULL,
    event_type   text        NOT NULL,
    subject      text        NOT NULL,
    key          bytea       NOT NULL,
    dataschema   text        NOT NULL,
    value        bytea       NOT NULL,
    headers      jsonb       NOT NULL DEFAULT '[]'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    attempts     integer     NOT NULL DEFAULT 0,
    last_error   text
);
CREATE INDEX outbox_events_unpublished_idx ON outbox_events (id) WHERE published_at IS NULL;
