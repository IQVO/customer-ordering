-- PackageLost / PackageReturned (ADR 0002 A19, A23): two more group states, two
-- more order states, the facts of the terminal outcome and the outcome of a
-- package in the tracking read model.
ALTER TABLE customer_orders DROP CONSTRAINT customer_orders_status_check;
ALTER TABLE customer_orders ADD CONSTRAINT customer_orders_status_check
    CHECK (status IN ('PLACED','IN_FULFILLMENT','PARTIALLY_SHIPPED','SHIPPED','PARTIALLY_DELIVERED','DELIVERED','LOST','RETURNED','CANCELLED'));

ALTER TABLE order_groups DROP CONSTRAINT order_groups_status_check;
ALTER TABLE order_groups ADD CONSTRAINT order_groups_status_check
    CHECK (status IN ('PROMISED','LOCKED','SHIPPED','DELIVERED','LOST','RETURNED','CANCELLATION_REQUESTED','CANCELLED'));
ALTER TABLE order_groups
    ADD COLUMN outcome_tracking_number text,
    ADD COLUMN lost_at                 timestamptz,
    ADD COLUMN returned_at             timestamptz;

ALTER TABLE tracking_packages
    ADD COLUMN outcome    text CHECK (outcome IN ('LOST','RETURNED')),
    ADD COLUMN outcome_at timestamptz;
