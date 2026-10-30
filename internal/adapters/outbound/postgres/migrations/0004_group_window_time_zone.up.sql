-- The delivery window's local time zone (ADR 0009): the IANA name of the
-- destination's calendar the window is read in, from delivery-promise's terms
-- and PromiseRevised. NULL = unknown: a group stored before it was carried.
ALTER TABLE order_groups
    ADD COLUMN time_zone text CHECK (time_zone IS NULL OR char_length(time_zone) BETWEEN 1 AND 64);
