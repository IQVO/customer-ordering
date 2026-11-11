-- The CONSOLIDATED tier and the shopper's choice of it (ADR 0013).
-- 1. order_groups.tier accepts a fifth value (the constraint PostgreSQL named
--    after the inline CHECK of migration 0001).
ALTER TABLE order_groups DROP CONSTRAINT order_groups_tier_check;
ALTER TABLE order_groups
    ADD CONSTRAINT order_groups_tier_check CHECK (tier IN ('SAME_DAY','NEXT_DAY','TWO_DAY','STANDARD','CONSOLIDATED'));

-- 2. The choice, stored on the group: the local calendar date of deliverByLatest
--    the shopper chose, and delivery-promise's answer whether the option leaves
--    the network time to merge. NULL = unknown (a group stored before this
--    migration, a group of another tier, or an answer the promise did not give);
--    never false by default.
ALTER TABLE order_groups
    ADD COLUMN chosen_delivery_day date,
    ADD COLUMN consolidation_eligible boolean;
