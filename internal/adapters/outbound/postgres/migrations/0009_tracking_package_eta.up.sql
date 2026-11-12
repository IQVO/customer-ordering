-- The day-of ETA window of a package (ADR 0014), from PackageEtaWindowAssigned.
-- The window is a fact of the package row, not of the order: NULL = no window
-- arrived. The three columns move together (a window and the instant that
-- ordered it) and the window is a proper interval. Whether it is still CURRENT
-- (state OUT_FOR_DELIVERY, not older than the last scan) is the view's rule.
ALTER TABLE tracking_packages
    ADD COLUMN eta_start       timestamptz,
    ADD COLUMN eta_end         timestamptz,
    ADD COLUMN eta_assigned_at timestamptz,
    ADD CONSTRAINT tracking_packages_eta_check CHECK (
        (eta_start IS NULL AND eta_end IS NULL AND eta_assigned_at IS NULL)
        OR (eta_start IS NOT NULL AND eta_end IS NOT NULL AND eta_assigned_at IS NOT NULL AND eta_start < eta_end));
