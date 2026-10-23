# ADR 0002: the CustomerOrder and Shopper aggregates, their invariants and the assumptions the model left open

## Status

Accepted (2026-10-23).

## Context

retail-network-docs `model/model.yaml` (context `customer-ordering`) names two
aggregates, their states, commands, invariants with `Err` names and two events.
The Gap Register items G7, G10, G12 and G25 and `contracts-delta.md` (E7, E9, E11,
E16) close what the model left open for this context; where they are silent the
assumptions below apply. Assumptions are numbered A1.. and stay final unless the
orchestrator overrules them.

## Decision

### Aggregates (Go packages `internal/domain/customerorder`, `internal/domain/shopper`)

Pure Go: no I/O, the clock and the ids are passed in, every aggregate has
`Snapshot()` / `Restore()` and an optimistic `version` (the repository raises
`ports.ErrConcurrentUpdate`). `internal/domain/shared` holds the event interface,
`Header` and the shared errors (`ErrValidation`, `ErrIllegalTransition`,
`ErrInvalidSnapshot`).

**CustomerOrder** (root `orderId`)

- Order states: `PLACED`, `IN_FULFILLMENT`, `PARTIALLY_SHIPPED`, `SHIPPED`,
  `PARTIALLY_DELIVERED` (G25), `DELIVERED`, `LOST`, `RETURNED` (A19, A23), `CANCELLED`.
- Group states (per `GroupPromise`, G25): `PROMISED` (no node locked: the group is
  "unlocked"), `LOCKED`, `SHIPPED`, `DELIVERED`, `LOST`, `RETURNED` (terminal, A19),
  `CANCELLATION_REQUESTED`, `CANCELLED`.
- Value objects: `ShipTo{name, line1, line2, city, region, postalCode,
  countryCode, phone}` (synthetic PII), `OrderLine{lineNo, catalogItemId,
  quantity}`, `GroupPromise{groupNo, promiseId, tier, witnessNodeId, shipBy,
  deliverByEarliest, deliverByLatest, lineNos[], status, revisionNo, assignmentId,
  nodeId, lockedAt, shippedAt, trackingNumbers[], deliveredTrackingNumbers[],
  deliveredAt, outcomeTrackingNumber, lostAt, returnedAt, cancelRequestedAt, cancelledAt,
  cancelRefusedReason}`.
- Commands: `PlaceOrder`, `MarkInFulfillment` (`FulfillmentLocked`),
  `MarkGroupShipped` (`FulfillmentShipped`), `MarkGroupDelivered`
  (`PackageDelivered`), `MarkGroupLost` / `MarkGroupReturned` (`PackageLost`,
  `PackageReturned`, A19), `CancelOrder` (REST, and `CancelUnkeepableGroup` for the
  policy `CancelOnUnfulfillable`), `ConfirmGroupCancelled` and
  `RefuseGroupCancellation` (`FulfillmentCancelled`, `FulfillmentCancelRefused`),
  `UpdateGroupPromise` (`PromiseRevised`).
- Events: `CustomerOrderPlaced` (PlaceOrder), `CustomerOrderCancelled` (CancelOrder
  and CancelUnkeepableGroup). The other commands record facts their owners already
  published and raise no integration event.

Order status is DERIVED from the group states after every command, over the
groups that are not `CANCELLED`:

| remaining groups | status |
|---|---|
| none (all `CANCELLED`) | `CANCELLED` |
| every group closed (`DELIVERED`, `LOST` or `RETURNED`): all `DELIVERED` | `DELIVERED` |
| every group closed, some `DELIVERED` | `PARTIALLY_DELIVERED` |
| every group closed, none `DELIVERED`, some `LOST` | `LOST` |
| every group closed, none `DELIVERED`, none `LOST` | `RETURNED` |
| (some group still open) some `DELIVERED` | `PARTIALLY_DELIVERED` |
| every group `SHIPPED`, `LOST` or `RETURNED` | `SHIPPED` |
| some group `SHIPPED`, `LOST` or `RETURNED` | `PARTIALLY_SHIPPED` |
| some `LOCKED` | `IN_FULFILLMENT` |
| otherwise | `PLACED` |

Rows are read top to bottom, first match wins. A `LOST` or `RETURNED` group did ship,
so beside open groups it counts as shipped progress (A23).

A derived status that ranks below the current one is refused with
`ErrStatusRegression`. Ranks: PLACED 0, IN_FULFILLMENT 1, PARTIALLY_SHIPPED 2,
SHIPPED 3, PARTIALLY_DELIVERED 4, DELIVERED 5; `LOST` and `RETURNED` are terminal
like `DELIVERED` (nothing follows them). `CANCELLED` is terminal and is reached
when every group is `CANCELLED`. A group can only be cancelled while unlocked, so an
order with a locked, shipped or delivered group is never CANCELLED. PLACED -> CANCELLED
is therefore the only reachable cancellation; the model transition
IN_FULFILLMENT -> CANCELLED stays legal in the table but cannot occur (G25 notes it
was unreachable; the per-group cancellation above replaces it).

Group states move forward only: PROMISED 0, LOCKED 1, SHIPPED 2, DELIVERED 3;
`LOST` and `RETURNED` are entered from SHIPPED only (A19).
`CANCELLATION_REQUESTED` is entered from PROMISED only and leaves to `CANCELLED`
(confirmed) or back to LOCKED (refused); `CANCELLED`, `DELIVERED`, `LOST` and
`RETURNED` are terminal.

Invariants (all in the domain, table-tested):

| Rule | Err name | Problem slug |
|---|---|---|
| every line is in exactly one group, every group has a promise | `ErrLineWithoutPromise` | `line-without-promise` (422) |
| the promise's destination prefix matches the ship-to | `ErrPromiseDestinationMismatch` | `promise-destination-mismatch` (422) |
| cancel only while a group is unlocked (advisory: order-sourcing decides) | `ErrCancelAfterLock` | `cancel-after-lock` (409) |
| status moves forward only | `ErrStatusRegression` | `status-regression` (409) |
| the command is not allowed from the current state | `ErrIllegalTransition` | `illegal-transition` (409) |
| a value breaks a value-object rule | `ErrValidation` | `validation-failed` (422) |

**Shopper** (root `shopperId`): state `ACTIVE`; `RegisterShopper`; invariant "email
unique" = `ErrShopperExists` (`shopper-exists`, 409). It holds the normalised e-mail
and up to 20 saved addresses (`ShipTo`). No events (the model lists none).

### Assumptions (where the model or the register is silent)

- **A1 (G25)** Order status `PARTIALLY_DELIVERED` exists; order and group status are
  derived as in the table above. A group is DELIVERED when every tracking number
  announced by `FulfillmentShipped` has a `PackageDelivered`.
- **A2 (G25)** `GroupPromise` also stores `tier`, `shipBy`, `witnessNodeId` (the
  fields `CustomerOrderPlaced` publishes), the assignment/lock/ship/delivery facts
  of its group and a per-group status.
- **A3 (G12, G25)** `CancelOrder` is a REQUEST. It moves every `PROMISED` group to
  `CANCELLATION_REQUESTED` and raises `CustomerOrderCancelled{groupNos}` with
  exactly those groups; locked, shipped and delivered groups are untouched
  ("cancel while any group is unlocked applies to those groups only").
  `FulfillmentCancelled` makes the group `CANCELLED`; the order becomes `CANCELLED`
  when no group remains outside `CANCELLED`. `FulfillmentCancelRefused` returns the
  group to `LOCKED` with `cancelRefusedReason` (the vendor ships; the shopper path
  is a return, outside this context).
- **A4** `CancelOrder` on an order that has no group left to cancel returns it
  unchanged and raises nothing when every group is already `CANCELLATION_REQUESTED`
  or `CANCELLED` (repeat); `ErrStatusRegression` when the order is `DELIVERED`;
  `ErrCancelAfterLock` otherwise (every group locked, shipped or delivered).
- **A5 (G10)** `PromiseUnkeepable{promiseId, orderId, groupNo, reason}` applies the
  policy `CancelOnUnfulfillable`: a `PROMISED` group goes straight to `CANCELLED`
  and `CustomerOrderCancelled{groupNos:[groupNo], reason:"PROMISE_UNKEEPABLE: <reason>"}`
  is published (order-sourcing moves its UNSOURCEABLE assignment to CANCELLED);
  the order becomes `CANCELLED` when nothing remains. In any other group state the
  event is recorded and skipped.
- **A6** `FulfillmentUnsourceable`, `PromiseAtRisk`, `PromiseKept`, `PromiseBroken`,
  `LabelIssued`, `LabelVoided`, `PackageScanned` and `PackageExceptionRaised` change
  no aggregate (`PackageLost` and `PackageReturned` do: A19): they feed the read model `OrderTrackingView` only (ADR 0003).
- **A7** Consumers: an unknown order, `ErrStatusRegression`, `ErrIllegalTransition`
  and an unknown group are recorded in the inbox and skipped ("a status-regressing
  event is recorded and skipped"); `ErrConcurrentUpdate` is retried in-process up to
  five times and then fails the message (dead-letter); other errors fail the message.
- **A8** `UpdateGroupPromise` applies to a group in `PROMISED`, `LOCKED` or `SHIPPED`
  when `revisionNo` is greater than the stored one; an older or equal revision is a
  no-op, a group in another state is skipped. It never changes the order status.
- **A9** A checkout names a shopper that must exist; otherwise 422 `validation-failed`.
- **A10** `destinationPostalPrefix` of an order is the first 3 characters of the
  normalised postal code (upper-case letters and digits only; a code with fewer than
  3 such characters is invalid). The promise destination matches when it is a
  non-empty prefix of the normalised postal code.
- **A11** The promise destination is read from the field `destinationPostalPrefix` of
  delivery-promise's `Promise` response to MakePromise. The published OpenAPI of
  delivery-promise does not list it yet (it lists it on `DeliveryOffer` only); it is
  requested as an additive field (model gap). When it is absent the check fails
  closed with `ErrPromiseDestinationMismatch`.
- **A12** `ConfirmPromise` is `POST /promises/{promiseId}:confirm` with body
  `{orderId, groupNo}`, `Idempotency-Key` `<orderId>:<groupNo>:confirm`, answering
  the promise (200/201). 404, 409 and 422 from MakePromise or ConfirmPromise mean the
  offer or hold is no longer valid: checkout re-quotes (all four tiers) and answers
  409 `offer-not-quotable`; `promise-capacity-exhausted` and `book-closed` are passed
  through as 409; `group-spans-nodes` as 422; a timeout, a transport error or a 5xx
  is 503 `delivery-promise-unavailable`. The exact body is requested from
  delivery-promise (model gap).
- **A13** The order id is a UUID v5 of the `Idempotency-Key` (namespace fixed in
  the code), so a retried checkout re-uses the same promises (MakePromise is
  idempotent per `<orderId>:<groupNo>`) and the same order.
- **A14 (final)** Accepted. `CancelPromise` is `POST /promises/{promiseId}:cancel`
  (delivery-promise contract, final): header `Idempotency-Key` (here
  `<orderId>:<groupNo>:cancel`), no body, 200 returns the promise with
  `status: CANCELLED` and releases its hold, a repeat on a CANCELLED promise is 200,
  404 `not-found`, 409 `illegal-transition` when the promise is KEPT or BROKEN.
  When a LATER confirm of a checkout fails, `PlaceOrder` cancels the promises it
  already confirmed (in order, on a context detached from the request so a client that
  hung up does not leave holds behind) and then fails with the confirm's own error
  (the problem type already used for it: `offer-not-quotable`, `promise-capacity-exhausted`,
  `book-closed` or `delivery-promise-unavailable`). If the failed confirm's outcome is
  unknown (timeout, transport error, 5xx) its own group is cancelled too, because it may
  have been applied. A refused or failed cancel (`ports.ErrPromiseNotCancellable`,
  `ErrPromiseUnavailable`) is logged at error level (`orderId`, `groupNo`, `promiseId`)
  and skipped; the checkout answer does not change.
  *Residual risk, final decision:* no compensation record is kept. A cancel that fails
  leaves that promise confirmed for an order that never appears; its capacity hold
  stays until delivery-promise's own hold expiry, and the same `Idempotency-Key` retried
  by the shopper re-runs MakePromise (idempotent per `<orderId>:<groupNo>`) and meets the
  promise again, so the retry either re-confirms it (hold still valid) or re-quotes.
  The same holds if the order cannot be stored after every confirm succeeded: the retry
  with the same key re-confirms the same promises (confirm is idempotent). A cancelled
  promise answers the retry's confirm with a refusal, so the shopper sees fresh offers.
  Model sentence: *when checkout fails after some groups were confirmed, the confirmed
  promises are cancelled and the failure is reported as a failed confirm.*
- **A15** Limits (plain literal constants): up to 100 lines, 50 groups, quantity and
  numbers <= 2147483647, names/lines/cities <= 100 characters, postal code <= 20,
  phone <= 40, e-mail <= 254, cancel reason <= 200, 20 saved addresses.
- **A16** The e-mail is trimmed and lower-cased; uniqueness is on the lower-cased value.
- **A17** Instants are UTC; the domain keeps them with microsecond precision truncated
  to what Postgres stores (`timestamptz`, microseconds).
- **A18** Ship-to is never returned by `GET /orders/{orderId}`, never logged and never
  put in an event; it is stored in the `customer_orders` row.
- **A19 (final)** Accepted. `PackageLost` and `PackageReturned` (shipping-and-tracking
  `journey`, same topic and consumer group as the other package events; `data` is
  `{trackingNumber, orderId, declaredAt | returnedAt}`) are consumed. They name a
  package, not a group: the group is the one whose `FulfillmentShipped` announced the
  tracking number. `SHIPPED` -> `LOST` / `RETURNED` (`MarkGroupLost`,
  `MarkGroupReturned`), terminal, with `outcomeTrackingNumber` and `lostAt` /
  `returnedAt`. One lost or returned package closes the whole group (the promise is
  per group; delivery-promise judges it BROKEN on the same event); the packages
  already delivered stay recorded. A repeat of the same outcome is a no-op (and the
  inbox makes the same event id a no-op anyway); a different outcome after the first,
  a delivery, a promise revision, a lock, a shipment or a cancel confirmation for a
  `LOST` / `RETURNED` group, and a loss for a `DELIVERED`, `CANCELLED` or not yet
  shipped group are recorded and skipped (A7). A package already reported delivered
  cannot be lost. If the tracking number is not announced yet (cross-topic reordering,
  like A21) the outcome is stored on the tracking package row (`outcome`,
  `outcomeAt`; state `LOST` / `RETURNED`, a later scan never overwrites it) and replayed,
  oldest first and after the early deliveries, when `FulfillmentShipped` is applied.
  Decoding is tolerant (unknown fields ignored, a missing instant falls back to the
  event time); a missing `trackingNumber` or `orderId` is a poison message (DLQ).
  The tracking view shows the group status, `lostAt` / `returnedAt`,
  `outcomePackage` and the package state. Returns and refunds stay outside this context.
  Model sentence: *a package declared lost or returned closes its shipment group as
  LOST or RETURNED, and the order status is derived from its groups.*
- **A20** Group numbers are positive integers chosen by the storefront; line numbers
  likewise; both unique within the order.
- **A21** A `PackageDelivered` for a group that is not yet `SHIPPED` (cross-topic
  reordering) is remembered on the tracking row and reconciled when
  `FulfillmentShipped` is applied.
- **A22** Wire types: ids strings, instants RFC 3339 UTC, enums UPPER_SNAKE, optional
  fields omitted (`apis/asyncapi.yaml`).
- **A23 (final)** Order status with LOST / RETURNED groups (derivation table above).
  The order enum gains `LOST` and `RETURNED` (REST `OrderStatus`, group `GroupStatus`,
  database checks: migration `0002`). `CANCELLED` groups do not count. While any
  counted group is open (`PROMISED`, `LOCKED`, `SHIPPED`, `CANCELLATION_REQUESTED`) the
  order is never `LOST` / `RETURNED`; a lost or returned group is shipped progress
  there (`SHIPPED` / `PARTIALLY_SHIPPED`) and a delivered group still gives
  `PARTIALLY_DELIVERED`. Once every counted group is closed: all delivered
  `DELIVERED`; some delivered `PARTIALLY_DELIVERED` (terminal, the groups say which
  were lost or returned); none delivered `LOST` if any group is lost, else `RETURNED`
  (a loss outranks a return). Examples, tested in `TestDeriveStatus`: `[LOST]` ->
  `LOST`; `[RETURNED, RETURNED]` -> `RETURNED`; `[RETURNED, LOST]` -> `LOST`;
  `[DELIVERED, LOST]` -> `PARTIALLY_DELIVERED`; `[LOST, SHIPPED]` -> `SHIPPED`;
  `[LOST, LOCKED]` -> `PARTIALLY_SHIPPED`; `[CANCELLED, RETURNED]` -> `RETURNED`.
  `CancelOrder` on a `LOST` or `RETURNED` order is `ErrStatusRegression`, like on a
  `DELIVERED` one. The enum change is additive for REST clients and is a model gap:
  retail-network-docs `OrderStatus` / `GroupStatus` need the two values.

## Consequences

- The status derivation is one pure function; every consumed event is a command with
  a table of legal and illegal transitions.
- `model_gaps` for the orchestrator: A11 (destination on the promise), A12 (confirm
  body), A14 (resolved by the cancel route; the residual risk above), A19 and A23 (LOST /
  RETURNED are new group and order states, absent from the model's `OrderStatus`), and the model's `GroupPromise` /
  `OrderStatus` back-fill (A1, A2, A3).
