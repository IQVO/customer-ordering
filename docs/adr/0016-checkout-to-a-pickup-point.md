# ADR 0016: checkout to a pickup point

## Status

Accepted (2026-11-13). Part of the network's pickup-points wave (RN-0010/RN-0011):
node-topology's ADR 0017 declares the PICKUP_POINT node, delivery-promise's ADR
0027 quotes to it and shipping-and-tracking's ADR 0016 labels, deposits and
collects at it. This repo builds against the WRITTEN contracts below, never
against a sibling's code. Builds on
[ADR 0002](./0002-aggregates-and-invariants.md) (the destination invariant),
[ADR 0003](./0003-consumed-contracts-and-local-read-models.md) (the tracking
read model), [ADR 0010](./0010-adopt-the-fleet-rest-api-guideline.md) (REST)
and [ADR 0012](./0012-consolidation-key-and-shared-tracking-numbers.md) (the
consolidation key).

## Context

A shopper may check out to a pickup point instead of a ship-to address. The
quote names the point (delivery-promise resolves it to its own postal prefix
and its serving delivery station, and refuses an unknown, suspended or
station-less point with DESTINATION_NOT_SERVED — exactly how checkout fails for
a bad point; customer-ordering needs NO node-topology consumer of its own). The
label is issued against the point: the PII exit that feeds it must answer a
destination kind, not an address. Collection at the point is what the shopper
sees next on the tracking view.

## Decision

1. **Checkout input.** `CheckoutRequest` gains `pickupPointId`. Validation
   becomes exactly-one-of `shipTo` | `shipToAddressId` | `pickupPointId`
   (422 `validation-failed`, the message names all three). A saved-address id
   and a point are refused together like any other pair.

2. **The aggregate.** `NewOrder` gains `PickupPointID string` as the alternative
   to `ShipTo` (four places: the struct, the `Place` validation, the
   `Snapshot`/`Restore` pair, and the `CustomerOrderPlaced` builder). `Place`
   requires exactly one destination: a ship-to order validates the address as
   today; a pickup order requires a valid id and NO ship-to. The order's
   `destinationPostalPrefix` for a pickup order is the confirmed promise's
   destination prefix — delivery-promise quotes the point under the point's own
   prefix (its ADR 0027), so the promise already carries it; every group's
   promise must name the same prefix, which becomes the order's.
   `ErrPromiseDestinationMismatch` additionally compares the pickup point: a
   group whose promise names a `pickupPointId` (an optional field of the
   promise answer this service decodes tolerantly; absent today) must name the
   order's point, and a ship-to order refuses a group whose promise names one.

3. **Persistence.** Migration `0011_order_pickup_destination`: `customer_orders`
   gains `pickup_point_id TEXT NULL`, `ship_to` becomes nullable, and a CHECK
   enforces exactly one destination (`ship_to IS NULL` exactly when
   `pickup_point_id IS NOT NULL`). The same migration adds the tracking columns
   of point 6 (the slice's one allocated migration number).

4. **The PII exit.** `GET /orders/{orderId}/ship-to` answers a pickup order with
   `{"destinationKind": "PICKUP_POINT", "pickupPointId": "..."}` and NO address
   fields (shipping-and-tracking's label branch consumes exactly these field
   names, `destinationKind` + `pickupPointId`; an absent kind is SHIP_TO, the
   shape of every order today). The restricted route stays the one exit of the
   destination and now carries no PII at all for these orders.

5. **The placed event.** `CustomerOrderPlaced` gains optional `pickupPointId`
   (omitted for a ship-to order; the address never leaves, as ever).

6. **The consolidation key.** For a pickup order the keyed hash runs over the
   shopper and `point:<pickupPointId>` — the same secret, the same HMAC-SHA-256
   shape and the same ADR 0012 semantics (same destination -> same key), in a
   SEPARATE key space: a pickup order and an address order NEVER merge (the
   `point:` prefix cannot be a normalised address, which never contains a
   lower-case word or a colon), and two pickup orders merge exactly when they
   share the shopper and the point.

7. **The tracking view.** Consume
   `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageReadyForCollection`
   (data `trackingNumber`, `assignmentId`, `orderId`, `pickupPointId`,
   `readyForCollectionAt`, `holdUntil`) and
   `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageCollected`
   (data `trackingNumber`, `assignmentId`, `orderId`, `pickupPointId`,
   `collectedAt`, optional `proofCode`), both on
   `retailnetwork.shipping-and-tracking.events` under SHIPPING_CONSUMER_GROUP.
   They upsert `tracking_packages`: the state becomes `READY_FOR_COLLECTION`
   then `COLLECTED`, and the new columns `ready_for_collection_at`, `hold_until`,
   `collected_at`, `proof_code` and `pickup_point_id` (also fed by the pickup
   `LabelIssued`, which carries the point) record the collection facts. The
   tracking view shows `destinationKind`, `pickupPointId` and the optional
   `pickupPoint {readyForCollectionAt, holdUntil, collectedAt?}` object —
   collection is what the shopper sees next. The events change no aggregate:
   the promise is judged at the deposit (delivery-promise) and the order keeps
   moving on the shipment events; an uncollected package returns through the
   existing `PackageReturned`.

## Consequences

- Checkout of a bad point never reaches promise-making with that point: the
  shopper holds no quotable offer, so `offer-not-quotable` requotes as today.
- The destination CHECK makes the two destination kinds explicit in the store;
  every existing row keeps its ship-to (the migration only widens the null).
- No new consumer group, topic, env var or chart value: the two events ride the
  existing shipping-and-tracking consumer, the keyer is the existing secret.
- The feature is dormant until a pickup order exists; nothing changes for
  ship-to orders on any path.
