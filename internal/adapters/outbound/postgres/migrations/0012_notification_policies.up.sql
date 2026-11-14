-- ADR 0017: the versioned NotificationPolicy. One row per published version,
-- every version is kept; the current policy is the highest version and
-- version 0 (no row) is the built-in default (every family NOBODY). The
-- snapshot is jsonb, as in the sibling SourcingPolicy of order-sourcing, so a
-- later additive policy field needs no migration.
CREATE TABLE notification_policies (
    version   integer     PRIMARY KEY CHECK (version >= 1),
    snapshot  jsonb       NOT NULL
);
