-- Down of 0003: the saved addresses go back into the shoppers JSON column
-- (without their ids; the id is a 0003 concept), and the history index goes.

DROP INDEX customer_orders_history_idx;

ALTER TABLE shoppers ADD COLUMN saved_addresses jsonb NOT NULL DEFAULT '[]'::jsonb;

UPDATE shoppers s
SET saved_addresses = COALESCE(a.list, '[]'::jsonb)
FROM (
    SELECT shopper_id,
           jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
               'name', name, 'line1', line1, 'line2', line2, 'city', city, 'region', region,
               'postalCode', postal_code, 'countryCode', country_code, 'phone', phone
           )) ORDER BY position) AS list
    FROM shopper_addresses
    GROUP BY shopper_id
) a
WHERE a.shopper_id = s.shopper_id;

DROP TABLE shopper_addresses;
