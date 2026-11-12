# ADR 0014: the day-of ETA window on the tracking view

## Status

Accepted (2026-11-11). Part of the Wave 3 E3 design (a two-hour delivery window
on the day of delivery). Builds on
[ADR 0003](./0003-consumed-contracts-and-local-read-models.md) (consumed
contracts and the tracking read model),
[ADR 0004](./0004-cloudevents-envelope-and-type-catalogue.md) (the catalogue; this
slice publishes nothing) and
[ADR 0012](./0012-consolidation-key-and-shared-tracking-numbers.md) (a package
shared by merged groups is listed once per group). The provider of the window is
last-mile-delivery (its ADR 0013, `StopEtaWindowAssigned`); the relay is
shipping-and-tracking (its ADR 0013, `PackageEtaWindowAssigned`). This repo
builds against the WRITTEN event contract below, never against a sibling's code.

## Context

Once a package is out for delivery the shopper wants to know WHEN, not only that
it is on its way. last-mile-delivery spreads the stops of a route over the wave's
delivery window and assigns each stop a window of at most two hours;
shipping-and-tracking keeps the window on the package journey and republishes it
per tracking number. customer-ordering holds the order and the tracking view, and
is the context the shopper reads.

What customer-ordering has today: `GET /orders/{orderId}/tracking` joins the order
aggregate with the `GroupSignals` and `Packages` read models (ADR 0003). A package
row is `tracking_packages`, keyed by `tracking_number` and upserted by every
consumer in any arrival order. The group promise (`deliverByEarliest`,
`deliverByLatest`, status, verdict) is a coarse promise; the window is a finer,
later, day-of estimate and must never touch it.

## Decision

1. **Consume the window.** The shipping-and-tracking consumer handles the FULL type
   `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageEtaWindowAssigned`
   (topic `retailnetwork.shipping-and-tracking.events`, subject and key = the
   tracking number, dataschema
   `urn:retailnetwork:shipping-and-tracking:events:PackageEtaWindowAssigned:v1`),
   data `trackingNumber`, `assignmentId`, `orderId`, `etaStart`, `etaEnd`,
   `assignedAt` (RFC 3339 UTC). Decoding is tolerant (unknown fields are ignored).
   A blank tracking number, or `etaStart >= etaEnd`, is a bad payload and is
   dead-lettered. No new topic, consumer group or environment variable.
2. **Apply it in the inbox unit.** `Consumer.ApplyPackageEta` (new file
   `consume_eta.go`) claims the CloudEvents id and calls
   `TrackingRepository.ApplyEta(ctx, readmodel.Eta{TrackingNumber, AssignmentID,
   OrderID, Start, End, AssignedAt})`. `AssignedAt` is the event's `assignedAt`,
   falling back to the CloudEvents `time` when the payload lacks it.
3. **Store it on the package row.** `ApplyEta` upserts on `tracking_number`,
   creating the row when absent (the same arrival-order tolerance as
   `ApplyException`: `assignment_id` and `order_id` are coalesced, then the
   assignment link is applied). The three fields are stored only when `AssignedAt`
   is not older than the stored `eta_assigned_at` (latest wins; an equal instant
   rewrites the same values). Migration `0009_tracking_package_eta` adds
   `tracking_packages.eta_start`, `eta_end`, `eta_assigned_at` (`timestamptz NULL`,
   a CHECK that the three are all null or all non-null and `eta_start < eta_end`).
   `readmodel.Package` gains `EtaStart`, `EtaEnd`, `EtaAssignedAt`.
4. **One view rule, in one place (`views.TrackingFrom`).** A package shows
   `etaStart` and `etaEnd` only when its `state` is `OUT_FOR_DELIVERY`, `etaAssignedAt`
   is non-zero and `etaAssignedAt` is not before `lastScanAt`. Otherwise both are
   omitted. This is the same definition as `EtaCurrent` in shipping-and-tracking
   (both compare the scan `at` and the window's event time, which last-mile-delivery
   sets to the same instant). Consequences of the one rule: yesterday's window is
   stale as soon as a newer scan exists, a failed attempt (state back to
   `AT_STATION`) hides it, and no terminal state shows it. A package shared by
   merged groups is listed once per group (ADR 0012) and shows the window in each.
5. **No clipping.** The window is shown as received, even when it ends after the
   group's `deliverByLatest`: hiding a late window would contradict the judgement
   the promise still uses.
6. **The promise is never read or written by this path.** The group's promise
   snapshot, status and verdict are byte-identical before and after a window event
   (a test asserts it). `UpdateGroupPromise` stays the only way the snapshot moves.
7. **REST, MCP, A2A (additive).** The OpenAPI `TrackingPackage` schema gains optional
   `etaStart` and `etaEnd` (date-time) with the description of rule 4; the MCP
   `get_order_tracking` tool, the order-tracking resource and the A2A
   `get_order_tracking` skill mirror them. The storefront BFF passes the tracking
   body through unchanged, so it needs no change. The generated REST reference is
   regenerated in the same commit as the OpenAPI edit.
8. **Consumed-contract registration.** `apis/asyncapi.yaml` (consumed section),
   the consumed list of ADR 0003, the docs and the ADR index list the new type;
   the consumed count goes from seventeen to eighteen. This service still
   publishes the same two events.

## Consequences

- With the producers silent nothing changes: no row gains a window, the view omits
  both fields. Rollback of the migration is additive-safe (the columns are nullable).
- The window is a read-model fact, not an order fact: no aggregate, no outbox event
  and no new state.
- A window that arrives before the package is `OUT_FOR_DELIVERY`, or before the
  assignment link, is stored and stays hidden until the scan and the rule agree.
- Deploy order: this service first, then shipping-and-tracking, then
  last-mile-delivery. A consumer ignores a type it has no handler for and commits
  past it, so an event published before its consumer is deployed is lost for that
  package, not queued.
- The wording shown to the shopper belongs to the storefront; the API carries
  instants only.
