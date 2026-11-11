# ADR 0012: the consolidation key and shared tracking numbers

## Status

Accepted (2026-11-10). Part of the Wave 2 C1 design (cross-order consolidation:
order-sourcing may release several groups of one shopper and one address as ONE
purchase order). Builds on [ADR 0002](./0002-aggregates-and-invariants.md) (the
CustomerOrder snapshot, A18 no address in an event) and
[ADR 0004](./0004-cloudevents-envelope-and-type-catalogue.md) (CustomerOrderPlaced
`:v1`, additive evolution only). This slice is the PROVIDER of the key; the
consumer is order-sourcing.

## Context

### The key

Order-sourcing can only merge groups that belong to the same shopper and ship to
the same place. It must decide that without the address: the address is synthetic
PII held only here and never leaves except through `GET /orders/{orderId}/ship-to`
(A18). The shipping context's stop key cannot be reused: it hashes the address
alone, not the shopper, and it is computed downstream, after the release.

customer-ordering is the only context that owns both facts (`shopperId` and the
ship-to), so it derives the key and hands order-sourcing an opaque value.

### Shared tracking numbers

Once order-sourcing merges groups, ONE package (one tracking number) carries the
goods of several groups, usually of several orders of one shopper. The tracking
read model assumed the opposite: `tracking_packages` has ONE `(order_id,
group_no)` per tracking number, `AttachTrackingNumbers` overwrote it with the
last writer, and a delivery, loss or return reached one group. Order-sourcing
keeps one event stream per member of a merge: every member group raises its own
`FulfillmentLocked` and `FulfillmentShipped` (each with its own `orderId` and
`groupNo` and the SAME tracking numbers), while shipping-and-tracking raises the
package events (`LabelIssued`, `PackageDelivered`, `PackageLost`,
`PackageReturned`, ...) once, under the carrier's `assignmentId` and `orderId`.
Today the other members would stay `SHIPPED` forever.

## Decision

### The consolidation key (slice C1-CO-A)

1. **Derivation.** `consolidationKey = hex(HMAC-SHA-256(secret, "consolidation-key/v1" || 0x00 || shopperId || 0x00 || NormalizedAddress()))`,
   64 lower-case hex characters. `shipto.ShipTo.NormalizedAddress()` is
   `Normalize(line1) | Normalize(line2) | Normalize(city) | Normalize(region) | Normalize(postalCode) | countryCode`
   joined with `|`, where `Normalize` upper-cases and keeps ASCII letters and
   digits (`"12 Main St."` and `"12 main st"` give the same part). The recipient
   name and the phone are excluded: it is the stop-key rule of the shipping
   context on purpose, so two orders of one shopper that the network treats as one
   stop share a key. The key is a keyed hash: it carries no PII and is not
   reversible without the secret; it links only orders of ONE shopper to ONE
   address, and only inside order-sourcing.
2. **Secret.** Env `CONSOLIDATION_KEY_SECRET`, at least 16 bytes, read in
   `cmd/api/config.go` (`loadRuntime`; a set but shorter value is a boot error
   naming the variable). Unset means the feature is off: no key is computed, the
   event omits the field and startup logs ONE warning. Rotating the secret only
   stops merges between orders keyed under different secrets (in-flight groups
   release alone). The chart renders it from a Secret (`consolidation.existingSecret`),
   never from the ConfigMap.
3. **Ports and wiring.** Application port `ports.ConsolidationKeyer`
   (`KeyFor(shopperID, normalizedShipTo string) string`, nil allowed); the HMAC
   adapter lives in `internal/adapters/outbound/crypto`. `PlaceOrder` computes the
   key after `resolveAddress` (a saved address is already copied into the order)
   and passes it as `NewOrder.ConsolidationKey`.
4. **Aggregate, persistence.** `CustomerOrder.consolidationKey`,
   `Snapshot.ConsolidationKey`, `Place`, `Restore` and the `Snapshot()` builder
   carry it; the value is `""` or 64 lower-case hex (`ErrValidation` on `Place`,
   `ErrInvalidSnapshot` on `Restore`). Migration `0006_order_consolidation_key`
   adds `customer_orders.consolidation_key text` (NULL = none) with a CHECK on the
   same shape. Rows written before read back with an empty key and never merge.
5. **Event (additive, same `:v1`).** `CustomerOrderPlaced.data.consolidationKey`
   (string), omitted when the key was not derived. It is not in REST, MCP or A2A.
   The event still carries no address field (a contract test pins that). The
   type is unchanged, so the ADR 0004 catalogue needs no new row. Reading
   rule for consumers: an absent key means the order never merges.

### Shared tracking numbers (slice C1-CO-B)

6. **Many groups per tracking number.** Migration
   `0007_tracking_package_groups` adds `tracking_package_groups (tracking_number,
   order_id, group_no)`, primary key on all three, backfilled from
   `tracking_packages WHERE order_id IS NOT NULL AND group_no IS NOT NULL`. A
   tracking number now belongs to a SET of groups. The package FACTS (carrier,
   scans, delivery, exception, outcome) stay one row per tracking number in
   `tracking_packages`, as the packages themselves are one.
7. **Writes only add.** `AttachTrackingNumbers` inserts a link row with
   `ON CONFLICT DO NOTHING` and no longer overwrites
   `tracking_packages.order_id/group_no`: they are kept for rollback and now mean
   "the first group that named the package" (filled only while the group is
   unknown, never replaced). The assignment link (`LinkAssignment` and the
   label, scan, delivery and exception paths that fill a row from it) also
   adds the link row; nothing ever removes one.
8. **Reads go through the link table.** `DeliveredPackages(order, group)`,
   `OutcomePackages(order, group)` and the tracking view (`Packages(order)`) read
   through it, so every group of a shared package lists it. A package that only
   names an order (no group known yet) is still listed for that order with group
   0, exactly as before. New port method
   `GroupsOfTracking(ctx, trackingNumber) ([]readmodel.GroupRef, error)`, sorted
   by order id then group number; empty when no group named the number yet.
9. **One package event reaches every group.** `ApplyPackageDelivered`,
   `ApplyPackageLost` and `ApplyPackageReturned` record the package fact once and
   then run `MarkGroupDelivered`/`MarkGroupLost`/`MarkGroupReturned` for EVERY
   `(order, group)` of `GroupsOfTracking`, plus the group the event itself
   resolves (the carrier's, as before), each in its own `mutate` inside the
   event's unit of work. A group that has not shipped yet is not in the set; it is
   replayed by `reconcile` when its own `FulfillmentShipped` arrives (A21), which
   now finds the package through the link table. `ApplyFulfillmentLocked` and
   `ApplyFulfillmentShipped` need no change: followers send their own events.
10. **Tolerance and rollback.** Without merges every tracking number has exactly
    one link row and every path behaves as before. Rolling the migration back
    drops only the table; the kept `order_id/group_no` columns still describe the
    first group of each package.

## Consequences

- Order-sourcing can group an order's later siblings without ever seeing an
  address; a leaked key exposes nothing without the secret.
- Two orders of one shopper to one address always share a key under one secret,
  even when placed on different days: the merge window is order-sourcing's
  decision, not part of the key.
- Until `CONSOLIDATION_KEY_SECRET` is set the behaviour of every consumer is
  unchanged (no key, no merge). Deploying this slice alone is safe.
- The key is stable per (secret, shopper, address). Changing the normalisation
  rule is a new derivation version (`consolidation-key/v2`), never an edit of v1.
- A merged shipment closes every member group on one `PackageDelivered`, and
  marks every member LOST or RETURNED on one `PackageLost`/`PackageReturned`
  (the package is one: the outcome is the same for all of it).
- The tracking view of a follower order shows the shared package under the
  follower's group; the package's `assignmentId` stays the carrier's.
- A package that is shared but whose follower groups have not shipped yet is
  closed for them later (replay), never lost; a follower cancelled before its
  `FulfillmentShipped` simply never gets a link row.
- Deploy order (activation gate for consolidation): this slice must be live
  before order-sourcing merges anything, or follower groups stay `SHIPPED`.
