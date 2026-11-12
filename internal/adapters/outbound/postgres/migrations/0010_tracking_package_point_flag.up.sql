-- The delivery-point advisory of a package (ADR 0015), from
-- PackageDeliveryPointFlagged. A fact of the package row, not of the order: NULL =
-- no flag arrived. The two columns move together and the reason is one of the two
-- the producer publishes. Whether the advisory is SHOWN (the package has not
-- ended) is the view's rule.
ALTER TABLE tracking_packages
    ADD COLUMN point_flag_reason text,
    ADD COLUMN point_flagged_at  timestamptz,
    ADD CONSTRAINT tracking_packages_point_flag_check CHECK (
        (point_flag_reason IS NULL AND point_flagged_at IS NULL)
        OR (point_flag_reason IS NOT NULL AND point_flag_reason IN ('NOT_HOME', 'ACCESS_PROBLEM')
            AND point_flagged_at IS NOT NULL));
