-- A tracking number may belong to several shipment groups (ADR 0012): once
-- order-sourcing merges the groups of one shopper and one address into one
-- package, every member group names the SAME tracking numbers. The package FACTS
-- stay one row per tracking number in tracking_packages; this table is the SET of
-- groups each number belongs to. Rows are only ever added.
CREATE TABLE tracking_package_groups (
    tracking_number text    NOT NULL,
    order_id        text    NOT NULL,
    group_no        integer NOT NULL,
    PRIMARY KEY (tracking_number, order_id, group_no)
);
CREATE INDEX tracking_package_groups_group_idx ON tracking_package_groups (order_id, group_no);

-- Every package that already knows its group keeps it. tracking_packages.order_id
-- and group_no stay (now "the first group that named the package"): the down
-- migration needs nothing to restore them.
INSERT INTO tracking_package_groups (tracking_number, order_id, group_no)
SELECT tracking_number, order_id, group_no FROM tracking_packages
WHERE order_id IS NOT NULL AND group_no IS NOT NULL;
