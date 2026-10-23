-- The previous schema has no LOST/RETURNED: such groups and orders go back to SHIPPED.
ALTER TABLE tracking_packages DROP COLUMN outcome_at, DROP COLUMN outcome;

UPDATE order_groups SET status = 'SHIPPED' WHERE status IN ('LOST','RETURNED');
UPDATE customer_orders SET status = 'SHIPPED' WHERE status IN ('LOST','RETURNED');

ALTER TABLE order_groups DROP COLUMN returned_at, DROP COLUMN lost_at, DROP COLUMN outcome_tracking_number;
ALTER TABLE order_groups DROP CONSTRAINT order_groups_status_check;
ALTER TABLE order_groups ADD CONSTRAINT order_groups_status_check
    CHECK (status IN ('PROMISED','LOCKED','SHIPPED','DELIVERED','CANCELLATION_REQUESTED','CANCELLED'));

ALTER TABLE customer_orders DROP CONSTRAINT customer_orders_status_check;
ALTER TABLE customer_orders ADD CONSTRAINT customer_orders_status_check
    CHECK (status IN ('PLACED','IN_FULFILLMENT','PARTIALLY_SHIPPED','SHIPPED','PARTIALLY_DELIVERED','DELIVERED','CANCELLED'));
