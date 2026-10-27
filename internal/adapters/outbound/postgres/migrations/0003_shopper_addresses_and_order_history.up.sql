-- NG1 / NG2 (ADR 0002 A24-A26): saved addresses leave the shoppers JSON column
-- and become rows carrying their stable addressId, and the order-history keyset
-- gets its index.

CREATE TABLE shopper_addresses (
    shopper_id   text        NOT NULL REFERENCES shoppers (shopper_id) ON DELETE CASCADE,
    address_id   text        NOT NULL,
    position     integer     NOT NULL CHECK (position >= 1),
    name         text        NOT NULL,
    line1        text        NOT NULL,
    line2        text,
    city         text        NOT NULL,
    region       text        NOT NULL,
    postal_code  text        NOT NULL,
    country_code text        NOT NULL,
    phone        text,
    PRIMARY KEY (shopper_id, address_id),
    UNIQUE (shopper_id, position)
);

-- Addresses saved at registration before address ids existed keep theirs,
-- derived deterministically from (shopper id, position): the migration is
-- repeatable and the id is stable for the life of the row.
INSERT INTO shopper_addresses (shopper_id, address_id, position, name, line1, line2, city, region, postal_code, country_code, phone)
SELECT s.shopper_id,
       'legacy-' || md5(s.shopper_id || ':' || (o.ordinality - 1)::text),
       o.ordinality::integer,
       a->>'name', a->>'line1', NULLIF(a->>'line2', ''), a->>'city', a->>'region',
       a->>'postalCode', a->>'countryCode', NULLIF(a->>'phone', '')
FROM shoppers s
CROSS JOIN LATERAL jsonb_array_elements(s.saved_addresses) WITH ORDINALITY AS o(a, ordinality);

ALTER TABLE shoppers DROP COLUMN saved_addresses;

CREATE INDEX customer_orders_history_idx ON customer_orders (shopper_id, placed_at DESC, order_id);
