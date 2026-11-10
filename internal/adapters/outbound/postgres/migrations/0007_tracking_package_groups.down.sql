-- tracking_packages kept order_id and group_no (the first group of each package),
-- so dropping the group set restores the one-group-per-package model.
DROP TABLE tracking_package_groups;
