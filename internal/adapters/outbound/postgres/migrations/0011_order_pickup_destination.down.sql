ALTER TABLE tracking_packages
    DROP CONSTRAINT tracking_packages_pickup_proof_check,
    DROP CONSTRAINT tracking_packages_pickup_ready_check,
    DROP COLUMN proof_code,
    DROP COLUMN collected_at,
    DROP COLUMN hold_until,
    DROP COLUMN ready_for_collection_at,
    DROP COLUMN pickup_point_id;

-- A pickup order cannot live without the point column: remove the orders (and
-- their lines and groups) before the ship-to becomes mandatory again.
DELETE FROM order_lines
WHERE order_id IN (SELECT order_id FROM customer_orders WHERE ship_to IS NULL);
DELETE FROM order_groups
WHERE order_id IN (SELECT order_id FROM customer_orders WHERE ship_to IS NULL);
DELETE FROM customer_orders WHERE ship_to IS NULL;

ALTER TABLE customer_orders
    DROP CONSTRAINT customer_orders_one_destination_check,
    DROP COLUMN pickup_point_id,
    ALTER COLUMN ship_to SET NOT NULL;
