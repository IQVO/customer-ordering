# Domain model: ubiquitous language, aggregates, events, use cases

Source of truth: retail-network-docs `model/model.yaml` (context `customer-ordering`).
Invariants, transitions and every assumption the model left open are in
`docs/adr/0002-aggregates-and-invariants.md` (assumptions A1-A23). Consumed
contracts and local read models are in `docs/adr/0003-consumed-contracts-and-local-read-models.md`.

## Ubiquitous Language (use these exact names)

- **CustomerOrder** — a shopper's cart turned into an order (`orderId`). Holds the
  lines, the ship-to, one `GroupPromise` per shipment group and a DERIVED status.
  Invariant: every line belongs to exactly one group and every group has a promise.
- **Shopper** — a registered buyer (`shopperId`), unique by lower-cased e-mail.
- **ShipTo** — the delivery address: synthetic PII held only in this service and
  returned only by `GET /orders/{orderId}/ship-to`; never in an event, a log or the
  order view. The events carry `destinationPostalPrefix` (first 3 characters).
- **Shipment group** — the lines that ship together from one node; the unit of
  promising, locking, shipping, delivery and cancellation (`groupNo`).
- **GroupPromise** — the promise of one group: `promiseId`, `tier`, `shipBy`,
  delivery window, witness node, `revisionNo`, and the group status.
- **Group status** — `PROMISED` (no node locked: "unlocked"), `LOCKED`, `SHIPPED`,
  `DELIVERED`, `LOST`, `RETURNED` (terminal, from `PackageLost` / `PackageReturned`),
  `CANCELLATION_REQUESTED`, `CANCELLED`. Moves forward only.
- **Order status** — `PLACED`, `IN_FULFILLMENT`, `PARTIALLY_SHIPPED`, `SHIPPED`,
  `PARTIALLY_DELIVERED`, `DELIVERED`, `LOST`, `RETURNED`, `CANCELLED`; derived from the
  non-cancelled groups after every command (table in ADR 0002, A1 and A23).
- **Tracking** — the read model of `GET /orders/{orderId}/tracking`: groups plus
  package rows and promise signals fed by consumed events.

## Aggregates

- **CustomerOrder** (`internal/domain/customerorder`): `place.go` (PlaceOrder and
  its checks), `commands.go` (MarkInFulfillment, MarkGroupShipped,
  MarkGroupDelivered, MarkGroupLost, MarkGroupReturned, UpdateGroupPromise), `cancel.go` (CancelOrder,
  CancelUnkeepableGroup, ConfirmGroupCancelled, RefuseGroupCancellation),
  `status.go` (derivation and the forward-only ranks), `errors.go`, `events.go`,
  `restore.go` (validating `Restore`). Errors: `ErrLineWithoutPromise`,
  `ErrPromiseDestinationMismatch`, `ErrCancelAfterLock` (advisory),
  `ErrStatusRegression`, `ErrUnknownGroup`.
- **Shopper** (`internal/domain/shopper`): `RegisterShopper`; `ErrShopperExists`.
- **ShipTo** (`internal/domain/shipto`): value object with field limits.
- `internal/domain/shared`: `Event`, `Header`, `ErrValidation`,
  `ErrIllegalTransition`, `ErrInvalidSnapshot`, id and text checks.

Every aggregate has `Snapshot()`, a validating `Restore(Snapshot)` and an optimistic
`Version()`; `now` and ids are passed in. `ErrNotFound` and `ErrConcurrentUpdate`
are port errors (`internal/application/ports/errors.go`), not domain errors. The
domain imports nothing outside `internal/domain`. Domain bounds are plain literal
constants.

## Domain events

Both are integration events on `retailnetwork.customer-ordering.events`
(`internal/domain/customerorder/events.go`; wire shape in `apis/asyncapi.yaml`):

- `CustomerOrderPlaced` — PlaceOrder; groups with promise terms and lines, no address.
- `CustomerOrderCancelled` — CancelOrder and the CancelOnUnfulfillable policy; it is
  a REQUEST: order-sourcing answers `FulfillmentCancelled` or `FulfillmentCancelRefused`.

The other commands record facts their owners already published and raise no event.
Shopper raises none.

## Key use cases (`internal/application/usecases`)

One per REST operation and per consumed event; commands run in ONE
`ports.UnitOfWork` (load, domain call, save, outbox append) and retry on
`ErrConcurrentUpdate` (`deps.go` `transact`).

- `PlaceOrder` (`checkout.go`): quote check, MakePromise per group, ConfirmPromise
  per group, then place; a failed confirm returns `RequoteRequired` and no order.
- `CancelOrder` (`cancel.go`), `RegisterShopper` (`shopper.go`).
- Queries (`queries.go`): `GetOrder`, `GetOrderTracking`, `GetShipTo`.
- Consumers (`consume_promise.go`, `consume_sourcing.go`, `consume_tracking.go`):
  `Consumer.Apply<Event>` for each consumed event, deduplicated by the inbox
  (`processed_events`) in the same transaction as the effect.
- Ports (`internal/application/ports/ports.go`) are interfaces only; read DTOs are in
  `internal/application/views`; the memory test doubles in
  `internal/adapters/outbound/memory` must pass the same suite as Postgres
  (`internal/adapters/outbound/repocontract`).
