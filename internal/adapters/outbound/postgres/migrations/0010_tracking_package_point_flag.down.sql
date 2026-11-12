ALTER TABLE tracking_packages
    DROP CONSTRAINT tracking_packages_point_flag_check,
    DROP COLUMN point_flagged_at,
    DROP COLUMN point_flag_reason;
