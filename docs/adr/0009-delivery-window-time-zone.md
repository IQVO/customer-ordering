# ADR 0009: the delivery window carries its local time zone

## Status

Accepted (2026-10-30). Builds on [ADR 0002](./0002-aggregates-and-invariants.md)
(the GroupPromise snapshot), [ADR 0003](./0003-consumed-contracts-and-local-read-models.md)
(the consumed delivery-promise contracts) and
[ADR 0004](./0004-cloudevents-envelope-and-type-catalogue.md) (CustomerOrderPlaced
`:v1`, additive evolution only).

## Context

A delivery window is two instants (`deliverByEarliest`, `deliverByLatest`, RFC 3339
UTC). A shopper reads it as local days and hours of the DESTINATION: the window
`2026-10-31T14:00:00Z..2026-11-01T02:00:00Z` is "Saturday, Oct 31, 9 AM – 9 PM" in
Austin, but two calendar days if formatted in UTC. delivery-promise (its ADR 0011)
now names the zone: an optional IANA `timeZone` on the `DeliveryWindow` of quoted
offers, on the flat `PromiseTerms` of a promise (the `POST /promises` answer this
service reads at checkout) and on `PromiseRevised:v1`. It is absent only for rows
it stored before, which read as UTC.

customer-ordering is where every later screen reads the window from (order,
history, tracking, the 409 re-quote, CustomerOrderPlaced), so unless it keeps the
zone the storefront can only guess one.

## Decision

- **`GroupPromise.TimeZone`** (domain): an optional IANA name next to the window,
  set at `Place` from the made promise's `terms.timeZone`, moved by
  `UpdateGroupPromise` only when the revision names one (an empty zone keeps the
  group's own: the destination does not move). Empty = unknown (a group stored
  before this ADR). The domain checks the shape only (`shared.ValidTimeZone`:
  1-64 characters `A-Z a-z 0-9 / _ + -`, starting with a letter) and never
  resolves the zone; a malformed one is `ErrValidation` on a command and
  `ErrInvalidSnapshot` on `Restore`.
- **Tolerant at the edges.** The delivery-promise gateway and the PromiseRevised
  handler keep a well-formed name and drop a malformed or absent one to empty: a
  display hint never fails a checkout or dead-letters a revision.
- **Persistence**: migration `0004_group_window_time_zone` adds the nullable
  `order_groups.time_zone` (`NULL` = unknown); the down migration drops it. Rows
  written before read back with an empty zone. Proved up/down/up
  (`migrate_time_zone_integration_test.go`) and by the repository contract (a
  zoned and an unzoned group round-trip on memory and Postgres).
- **REST (additive)**: optional `timeZone` (shared `TimeZone` schema) on each group
  of `CustomerOrder` (`POST /checkout` 201, `GET /orders/{orderId}`, every item of
  `GET /orders`), each `TrackingGroup` of `GET /orders/{orderId}/tracking`, and each
  `RequotedOffer` of the 409 `offer-not-quotable` problem (from the quoted offer's
  `window.timeZone`). Omitted when unknown.
- **CustomerOrderPlaced (additive, same `:v1`)**: each group carries an optional
  `timeZone`, omitted when unknown. The `dataschema` stays
  `urn:retailnetwork:customer-ordering:events:CustomerOrderPlaced:v1`: consumers
  are tolerant readers and ignore unknown fields. The golden file shows a zoned
  and an unzoned group.
- **Out of scope**: the MCP tools and the A2A skills keep their current output
  shape; they can add the field the same way later.

## Consequences

- Consumers turning a window into dates or text use `timeZone` when present and
  UTC when absent, never the server's or browser's zone.
- Old orders keep showing their window in UTC until a PromiseRevised names the zone.
- One more column on `order_groups`; no backfill (the zone of an old promise is
  not knowable here).
