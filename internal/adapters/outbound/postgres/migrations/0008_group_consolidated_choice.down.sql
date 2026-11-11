-- The previous schema has four tiers and no stored choice. Re-adding the CHECK
-- validates every row, so rolling back FAILS while a CONSOLIDATED group exists
-- (a promise is never rewritten to another tier): cancel such groups or let them
-- close first.
ALTER TABLE order_groups DROP COLUMN consolidation_eligible, DROP COLUMN chosen_delivery_day;
ALTER TABLE order_groups DROP CONSTRAINT order_groups_tier_check;
ALTER TABLE order_groups
    ADD CONSTRAINT order_groups_tier_check CHECK (tier IN ('SAME_DAY','NEXT_DAY','TWO_DAY','STANDARD'));
