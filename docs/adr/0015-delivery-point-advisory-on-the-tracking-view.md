# ADR 0015: the delivery-point advisory on the tracking view

## Status

Accepted (2026-11-11). Part of the Wave 3 C5 design (a delivery point that failed
twice is flagged, and the shopper whose package is still on the way is told).
Builds on [ADR 0003](./0003-consumed-contracts-and-local-read-models.md)
(consumed contracts and the tracking read model),
[ADR 0012](./0012-consolidation-key-and-shared-tracking-numbers.md) (a package
shared by merged groups is listed once per group) and
[ADR 0014](./0014-day-of-eta-window-on-the-tracking-view.md) (the day-of window,
whose consumer pattern this slice repeats). The provider of the flag is
last-mile-delivery (its ADR 0014, `DeliveryPointFlagged`); the relay is
shipping-and-tracking (its ADR 0014, `PackageDeliveryPointFlagged`, by tracking
number). This repo builds against the WRITTEN event contract below, never against
a sibling's code.

## Context

last-mile-delivery keeps a profile per delivery point (an opaque stop key, never
an address) and flags the point once it has two counted failures. It publishes
`DeliveryPointFlagged` with the tracking numbers newly affected, and
shipping-and-tracking relays it per package by order id, without the stop key
(minimum disclosure). The feature stays dormant until the driver client reports an
outcome detail; until then no flag is published and this slice shows nothing.

customer-ordering has no instructions field, no way to change a ship-to
(ADR 0002 A25) and no notification channel, and the address is PII that must not
cross into a free-text instruction for the driver. So the smallest honest shopper
loop for this wave is information on the tracking view, not an action.

## Decision

1. **Consume the flag.** The shipping-and-tracking consumer handles the FULL type
   `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageDeliveryPointFlagged`
   (topic `retailnetwork.shipping-and-tracking.events`, subject and key = the
   tracking number, dataschema
   `urn:retailnetwork:shipping-and-tracking:events:PackageDeliveryPointFlagged:v1`),
   data `trackingNumber`, `assignmentId`, `orderId`, `reason`
   (`NOT_HOME` | `ACCESS_PROBLEM`), `flaggedAt` (RFC 3339 UTC). Decoding is
   tolerant (unknown fields are ignored). A blank tracking number, or a `reason`
   outside the two values, is a bad payload and is dead-lettered. No new topic,
   consumer group or environment variable.
2. **Apply it in the inbox unit.** `Consumer.ApplyDeliveryPointFlag` (new file
   `consume_point_flag.go`) claims the CloudEvents id and calls
   `TrackingRepository.ApplyPointFlag(ctx, readmodel.PointFlag{TrackingNumber,
   AssignmentID, OrderID, Reason, FlaggedAt})`. `FlaggedAt` is the event's
   `flaggedAt`, falling back to the CloudEvents `time` when the payload lacks it.
3. **Store it on the package row.** `ApplyPointFlag` upserts on `tracking_number`
   exactly like the window of ADR 0014 (the row is created when absent, identity
   is coalesced, the assignment link is applied). The two fields are stored only
   when `FlaggedAt` is not older than the stored `point_flagged_at` (the newest
   wins; an equal instant rewrites). Migration `0010_tracking_package_point_flag`
   adds `tracking_packages.point_flag_reason text NULL` and `point_flagged_at
   timestamptz NULL`, both null together, with a CHECK that the reason is one of
   the two values. `readmodel.Package` gains `PointFlagReason`, `PointFlaggedAt`.
4. **One view rule (`views.TrackingFrom`).** A package carries
   `deliveryPointAdvisory{reason, flaggedAt}` while its state is not `DELIVERED`,
   `RETURNED`, `LOST` or `VOIDED` and a flag is stored. The advisory ends when the
   package ends: there is no "cleared" event. A package shared by merged groups is
   listed once per group (ADR 0012) and carries the advisory in each.
5. **Codes only.** The API carries the reason code and the instant; the words the
   shopper reads belong to the storefront.
6. **The promise and the window are untouched.** The advisory changes no ETA
   window, no group, no promise field and no status; a flagged point does not
   change a window (a test asserts the group is byte-identical and the window
   rule of ADR 0014 is unchanged).
7. **REST, MCP, A2A (additive).** The OpenAPI `TrackingPackage` schema gains the
   optional object `deliveryPointAdvisory` (`reason`, `flaggedAt`); the MCP
   `get_order_tracking` tool, the order-tracking resource and the A2A
   `get_order_tracking` skill mirror it. The storefront BFF passes the body
   through unchanged. The generated REST reference is regenerated in the same
   commit as the OpenAPI edit.
8. **Consumed-contract registration.** `apis/asyncapi.yaml` (consumed section), the
   consumed list of ADR 0003, the docs and the ADR index list the new type; the
   consumed count goes from eighteen to nineteen. This service still publishes the
   same two events.

## What this is NOT

- Not an address change: the ship-to stays as placed (ADR 0002 A25).
- Not an instructions field: nothing the shopper types reaches the driver.
- Not a notification: no e-mail, push or message is sent.

The fix path (an instructions command in this context and its route to the driver,
which would touch the PII rule and the "out of scope" of shipping-and-tracking's
label-time address check) is outside this wave.

## Consequences

- Dormant until the driver client sends the outcome detail: with no flag published,
  no row gains a field and the view omits the object.
- Rollback of the migration is additive-safe (nullable columns).
- Deploy order: this service first, then shipping-and-tracking, then
  last-mile-delivery, because a consumer ignores a type it has no handler for and
  commits past it.
- The advisory is a read-model fact, not an order fact: no aggregate, no outbox
  event and no new state.
