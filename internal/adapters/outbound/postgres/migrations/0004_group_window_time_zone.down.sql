-- The previous schema has no time zone on a group: the window reads in UTC again.
ALTER TABLE order_groups DROP COLUMN time_zone;
