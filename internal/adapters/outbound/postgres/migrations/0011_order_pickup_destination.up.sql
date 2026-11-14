-- The pickup point as the order's second destination kind (ADR 0016): exactly
-- one destination per order — the ship-to (synthetic PII, only here) or the
-- pickup point (no PII at all). Every existing row keeps its ship-to; the
-- migration only widens the null. The same slice adds the collection columns
-- of the tracking view: PackageReadyForCollection and PackageCollected
-- (shipping-and-tracking) upsert the state and the instants of a pickup
-- package, and the pickup LabelIssued names the point.
ALTER TABLE customer_orders
    ADD COLUMN pickup_point_id text,
    ALTER COLUMN ship_to DROP NOT NULL,
    ADD CONSTRAINT customer_orders_one_destination_check CHECK (
        (ship_to IS NOT NULL AND pickup_point_id IS NULL)
        OR (ship_to IS NULL AND pickup_point_id IS NOT NULL));

ALTER TABLE tracking_packages
    ADD COLUMN pickup_point_id         text,
    ADD COLUMN ready_for_collection_at timestamptz,
    ADD COLUMN hold_until              timestamptz,
    ADD COLUMN collected_at            timestamptz,
    ADD COLUMN proof_code              text,
    ADD CONSTRAINT tracking_packages_pickup_ready_check CHECK (
        (ready_for_collection_at IS NULL) = (hold_until IS NULL)),
    ADD CONSTRAINT tracking_packages_pickup_proof_check CHECK (
        proof_code IS NULL OR collected_at IS NOT NULL);
