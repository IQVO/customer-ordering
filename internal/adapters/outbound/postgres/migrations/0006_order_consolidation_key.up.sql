-- The consolidation key (ADR 0012): the keyed hash of shopper and normalised
-- ship-to that order-sourcing may merge groups by. NULL = none was derived (the
-- service had no secret, or the order was placed before this column): such an
-- order never merges. The key is not the address and is not reversible without
-- the secret.
ALTER TABLE customer_orders
    ADD COLUMN consolidation_key text CHECK (consolidation_key IS NULL OR consolidation_key ~ '^[0-9a-f]{64}$');
