---
id: use-cases
title: Use cases
sidebar_label: Use cases
sidebar_position: 1
---

# Use cases

Every application use case lives in `internal/application/usecases`. Commands
run in one Postgres transaction through the `UnitOfWork` and retry once when
another writer got in between load and save (`commandAttempts = 2`); consumed
events retry up to five times (`consumerAttempts = 5`). A command that raises an
event appends it to `outbox_events` in the same transaction. Synchronous calls to
delivery-promise happen **before** the transaction. Error mapping is in
`internal/adapters/inbound/http/errors.go`; every error is
`application/problem+json` with `type` `/problems/<slug>`.

## At a glance

| Use case | Trigger | Inbound adapter | Events raised |
| --- | --- | --- | --- |
| `PlaceOrder` | shopper (storefront, console, tests) | REST `POST /checkout` | `CustomerOrderPlaced` |
| `CancelOrder` | shopper | REST `POST /orders/{orderId}/cancellations` | `CustomerOrderCancelled` |
| `RegisterShopper` | storefront | REST `POST /shoppers` | none |
| `AddSavedAddress` | shopper | REST `POST /shoppers/{shopperId}/addresses` | none |
| `GetOrder`, `GetOrderTracking`, `GetShipTo` | any caller | REST, MCP, A2A (`GetShipTo`: REST and MCP only) | none |
| `GetShopper`, `FindShopperByEmail`, `ListOrdersByShopper` | storefront | REST | none |
| `Consumer.Apply*` (17 handlers) | Kafka events of three sibling contexts | `internal/adapters/inbound/kafka` | `CustomerOrderCancelled` from `PromiseUnkeepable` only |

## PlaceOrder

Source: `internal/application/usecases/checkout.go`, `internal/domain/customerorder/place.go`.

- **Inputs:** `Idempotency-Key` (the `orderId` is derived from it), `shopperId`,
  exactly one of an inline `shipTo` or a `shipToAddressId`, `lines[]` (`lineNo`,
  `catalogItemId`, `quantity`) and `groups[]` (`groupNo`, `offerId`, `lineNos[]`)
  from the offers the shopper saw.
- **Flow:**
  1. Exactly one of `shipTo` / `shipToAddressId` (else `validation-failed`); a
     replay of the same `orderId` returns the stored order.
  2. A saved address is resolved against the shopper and **copied** into the
     order (an unknown id or another shopper's address is `validation-failed`).
  3. Preflight: a throw-away order with placeholder promises runs every check
     that needs no promise, so a bad request never reaches delivery-promise.
  4. The shopper must be registered (`validation-failed` otherwise).
  5. For each group: `MakePromise`; then the order is checked against the
     promises (destination, coverage); then `ConfirmPromise` for each group.
  6. If an offer is expired, consumed or cannot be confirmed: nothing is placed
     and fresh offers are quoted for every group (policy `CheckoutPromising`);
     the answer is 409 `offer-not-quotable` with the re-quoted offers.
  7. If a later confirm fails, the promises already confirmed are cancelled
     again (compensation, ADR 0002 A14). When the failed confirm's answer is
     unknown (timeout, transport error, 5xx) its own group is cancelled too.
  8. In one transaction: save the order, append `CustomerOrderPlaced`.
- **Invariants:** every line is in exactly one group and every group has a
  promise (`ErrLineWithoutPromise`); the promise destination postal prefix equals
  the ship-to's (`ErrPromiseDestinationMismatch`); at most 100 lines and 50 groups.
- **Errors:** 409 `offer-not-quotable` / `promise-capacity-exhausted` /
  `book-closed`, 422 `line-without-promise` / `group-spans-nodes` /
  `promise-destination-mismatch` / `validation-failed`, 503
  `delivery-promise-unavailable`, 409 `concurrent-update`.
- **Events:** `CustomerOrderPlaced` (carries the destination postal prefix and a
  promise snapshot per group, never the address).
- **Metric:** every non-replayed attempt increments `ordering.orders.placed`
  with `outcome` and, when rejected, `reason`.

## CancelOrder

Source: `internal/application/usecases/cancel.go`, `internal/domain/customerorder/cancel.go`.

- **Inputs:** `orderId`, `reason` (1 to 200 trimmed characters, no control
  characters), `Idempotency-Key`.
- **Behaviour:** a cancel is a **request**. Every `PROMISED` group becomes
  `CANCELLATION_REQUESTED` and `CustomerOrderCancelled` lists exactly those
  groups; locked, shipped and delivered groups are untouched. order-sourcing
  answers later with `FulfillmentCancelled` (the group becomes `CANCELLED`) or
  `FulfillmentCancelRefused` (back to `LOCKED`, the vendor ships).
- **Invariants:** a repeat (every group already requested or cancelled) changes
  and publishes nothing; if no group can be requested the answer is 409
  `cancel-after-lock` (advisory: a lock not consumed yet is invisible here); a
  `DELIVERED`, `LOST` or `RETURNED` order is 409 `status-regression`.
- **Errors:** 404 `not-found`, 409 `cancel-after-lock` / `status-regression` /
  `concurrent-update`, 422 `validation-failed`.
- **Events:** `CustomerOrderCancelled` (`orderId`, `reason`, `cancelledAt`,
  `groupNos`).

## RegisterShopper and AddSavedAddress

Source: `internal/application/usecases/shopper.go`, `internal/domain/shopper`.

- **RegisterShopper:** input `email` (up to 254 characters) and optional
  `savedAddresses[]`; the e-mail is lower-cased; the service mints the shopper
  id and the address ids. Invariants: unique e-mail (409 `shopper-exists`, the
  unique index is the last guard), at most 20 saved addresses. Events: none.
- **AddSavedAddress:** input `shopperId`, a `ShipTo` and the `Idempotency-Key`;
  the address id derives from the key, so a replay after the idempotency record
  expired returns the same address. Errors: 404 `not-found`, 422
  `too-many-addresses` / `validation-failed`. Events: none.

## Queries

| Use case | Route | Notes |
| --- | --- | --- |
| `GetOrder` | `GET /orders/{orderId}` | status, shopper, destination prefix, lines, groups with promises; never the address |
| `GetOrderTracking` | `GET /orders/{orderId}/tracking` | per group: window, lock and ship times, at-risk and unsourceable signals, verdict and `lateByMinutes`, packages, lost and returned outcomes |
| `GetShipTo` | `GET /orders/{orderId}/ship-to` | the only order read that returns the address; `Cache-Control: no-store`; restricted to label printing |
| `ListOrdersByShopper` | `GET /orders?shopperId=&limit=&cursor=` | newest first, `{items, nextCursor}`, keyset on (placedAt, orderId), default 100, maximum 500, unknown shopper 404, no address |
| `GetShopper` | `GET /shoppers/{shopperId}` | profile with `savedAddresses[]` |
| `FindShopperByEmail` | `GET /shoppers?email=` | trimmed, lower-cased match; no match 404 |

## Consumed events

`usecases.Consumer` applies the events of the three upstream contexts. Every
handler runs in one unit of work with the inbox claim on the CloudEvents id, so
an event is applied at most once. Errors that mean "nothing to do" (unknown
order, unknown group, a status regression, an illegal transition) are logged as
`consumed event skipped` and acknowledged (ADR 0002 A7).

| Event (producer) | Handler | Effect |
| --- | --- | --- |
| `PromiseAtRisk` (delivery-promise) | `ApplyPromiseAtRisk` | records the at-risk reason on the group's signal row |
| `PromiseRevised` | `ApplyPromiseRevised` | `UpdateGroupPromise`: new window, ship-by, node, zone and `revisionNo` (stale revisions ignored); no status change |
| `PromiseKept`, `PromiseBroken` | `ApplyPromiseKept`, `ApplyPromiseBroken` | records the verdict (and minutes late) in the tracking view |
| `PromiseUnkeepable` | `ApplyPromiseUnkeepable` | policy `CancelOnUnfulfillable`: the `PROMISED` group becomes `CANCELLED`, `CustomerOrderCancelled` is raised; the order is `CANCELLED` when nothing remains |
| `FulfillmentLocked` (order-sourcing) | `ApplyFulfillmentLocked` | links the assignment, `MarkInFulfillment`: group `LOCKED`, order `IN_FULFILLMENT` at the first lock |
| `FulfillmentShipped` | `ApplyFulfillmentShipped` | stores the tracking numbers, `MarkGroupShipped`; replays deliveries and outcomes reported before the shipment was known |
| `FulfillmentUnsourceable` | `ApplyFulfillmentUnsourceable` | records the reason on the tracking view only |
| `FulfillmentCancelled` | `ApplyFulfillmentCancelled` | `ConfirmGroupCancelled`: group `CANCELLED` |
| `FulfillmentCancelRefused` | `ApplyFulfillmentCancelRefused` | `RefuseGroupCancellation`: `CANCELLATION_REQUESTED` goes back to `LOCKED` and the refusal reason is kept |
| `LabelIssued`, `LabelVoided`, `PackageScanned` (shipping-and-tracking) | `ApplyLabelIssued`, `ApplyLabelVoided`, `ApplyPackageScanned` | create or complete the package row; void it; record the newest scan |
| `PackageExceptionRaised` | `ApplyPackageException` | records the exception reason |
| `PackageDelivered` | `ApplyPackageDelivered` | marks the package delivered; `MarkGroupDelivered` when every package of the group is; a delivery for a not yet known group is replayed later |
| `PackageLost`, `PackageReturned` | `ApplyPackageLost`, `ApplyPackageReturned` | `MarkGroupLost` / `MarkGroupReturned`: the group closes (terminal) |

The order status is derived from the group states (`status.go`, `derive`):
`CANCELLED` groups do not count; with no live group the order is `CANCELLED`;
when every live group is `DELIVERED`, `LOST` or `RETURNED` the order is
`DELIVERED`, `PARTIALLY_DELIVERED`, `LOST` (when any is lost) or `RETURNED`;
otherwise any delivered group gives `PARTIALLY_DELIVERED`, all shipped (or lost
or returned) `SHIPPED`, some `PARTIALLY_SHIPPED`, any locked `IN_FULFILLMENT`,
else `PLACED`.
