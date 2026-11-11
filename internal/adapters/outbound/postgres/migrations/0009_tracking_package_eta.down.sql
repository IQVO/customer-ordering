ALTER TABLE tracking_packages
    DROP CONSTRAINT tracking_packages_eta_check,
    DROP COLUMN eta_assigned_at,
    DROP COLUMN eta_end,
    DROP COLUMN eta_start;
