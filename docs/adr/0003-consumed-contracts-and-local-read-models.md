# ADR 0003: consumed contracts, local read models and relationships

## Status

Accepted (2026-10-23).

## Context

customer-ordering consumes events of delivery-promise, order-sourcing and
shipping-and-tracking, serves two in-organization callers and makes two synchronous
calls to delivery-promise (plus the confirm added by G7 / E7). Hard rules: no live
lookup into a sibling except the model's `sync_calls`; sibling data is an
event-fed local read model; every consumer is idempotent on the CloudEvents id
(inbox) and dispatches on the full type string; consumer groups are mandatory.

## Decision

### Relationships (context map)

| Counterpart | Direction | Relationship | What |
|---|---|---|---|
| shopper / storefront | in (REST) | OHS | `POST /checkout`, `GET /orders/{id}`, `GET /orders/{id}/tracking`, `POST /orders/{id}:cancel`, `POST /shoppers` |
| shipping-and-tracking | in (REST) | Customer/Supplier | `GET /orders/{id}/ship-to` (restricted; the only PII exit) |
| delivery-promise | out (REST) | Customer/Supplier | QuoteDeliveryOffers `POST /delivery-offers:quote`, MakePromise `POST /promises`, ConfirmPromise `POST /promises/{promiseId}:confirm` (E7), CancelPromise `POST /promises/{promiseId}:cancel` (compensation of a failed checkout, A14) |
| delivery-promise | in (events) | Customer/Supplier | PromiseAtRisk, PromiseRevised, PromiseKept, PromiseBroken, PromiseUnkeepable (E9) |
| order-sourcing | in (events) | Conformist | FulfillmentLocked, FulfillmentShipped, FulfillmentUnsourceable, FulfillmentCancelled, FulfillmentCancelRefused (E11) |
| shipping-and-tracking | in (events) | Conformist | LabelIssued, LabelVoided (E16), PackageScanned, PackageDelivered, PackageExceptionRaised, PackageLost, PackageReturned (A19) |
| order-sourcing, delivery-promise | out (events) | published language | CustomerOrderPlaced, CustomerOrderCancelled |

### Consumed events and what each does

Topics: `retailnetwork.delivery-promise.events`, `retailnetwork.order-sourcing.events`,
`retailnetwork.shipping-and-tracking.events`; one consumer group each
(`DELIVERY_PROMISE_CONSUMER_GROUP`, `ORDER_SOURCING_CONSUMER_GROUP`,
`SHIPPING_CONSUMER_GROUP`, all mandatory; `PackageLost` and `PackageReturned` share the shipping-and-tracking consumer and its group). The inbox table `processed_events`
(event id primary key) is written in the same transaction as the effect.

| Full type | Effect |
|---|---|
| `com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentLocked` | links `assignmentId` to (orderId, groupNo) and its tracking rows; `MarkInFulfillment` |
| `...assignment.FulfillmentShipped` | `MarkGroupShipped` (tracking numbers, shippedAt); reconciles deliveries that arrived first (A21) |
| `...assignment.FulfillmentUnsourceable` | tracking: `unsourceableReason` of the group |
| `...assignment.FulfillmentCancelled` | `ConfirmGroupCancelled` |
| `...assignment.FulfillmentCancelRefused` | `RefuseGroupCancellation` |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseAtRisk` | tracking: `atRiskReason` of the group with that promiseId |
| `...promise.PromiseRevised` | `UpdateGroupPromise` (window, shipBy, node, revisionNo) |
| `...promise.PromiseKept` / `PromiseBroken` | tracking: `promiseVerdict`, `lateByMinutes` |
| `...promise.PromiseUnkeepable` | policy `CancelOnUnfulfillable` (A5): `CancelUnkeepableGroup`, publishes `CustomerOrderCancelled` |
| `com.retailnetwork.logistics.shipping-and-tracking.label.LabelIssued` | tracking package row (carrier, state LABELLED), linked by `assignmentId` |
| `...label.LabelVoided` | tracking package state `VOIDED` |
| `...journey.PackageScanned` | tracking package: last scan node/time, journey state (older scans never overwrite newer ones) |
| `...journey.PackageDelivered` | tracking package delivered; `MarkGroupDelivered` |
| `...journey.PackageExceptionRaised` | tracking package `exceptionReason` |
| `...journey.PackageLost` | tracking package outcome LOST; `MarkGroupLost` for the group that announced the package (terminal LOST); an early loss is replayed when `FulfillmentShipped` arrives (A19) |
| `...journey.PackageReturned` | tracking package outcome RETURNED; `MarkGroupReturned` (terminal RETURNED), same replay rule (A19) |

Decoding is tolerant: only the fields listed in `apis/asyncapi.yaml` are read, unknown
fields and unknown types are ignored. A message that is not a valid CloudEvents
event, or whose `data` cannot be decoded, goes to the dead-letter topic
`<consumed topic>.dlq` (for example `retailnetwork.order-sourcing.events.dlq`) with the failure in headers (A7 lists the errors
that are recorded and skipped instead).

### Local read models

- **OrderTrackingView** (`GET /orders/{id}/tracking`): the order's groups (status,
  promise window, lock/ship times) joined with
  - `promise_signals` (promiseId, orderId, atRiskReason, verdict, lateByMinutes,
    unsourceable reason), and
  - `tracking_packages` (trackingNumber primary key, assignmentId, orderId, groupNo,
    carrier, state, lastScanNodeId, lastScanAt, deliveredAt, exceptionReason, outcome, outcomeAt).
  Rows may be created before the order link is known (a scan can precede
  `FulfillmentLocked`); `FulfillmentLocked` fills `orderId` and `groupNo` of every row
  with the assignment id, `FulfillmentShipped` those of its tracking numbers.
- **assignment_links** (assignmentId -> orderId, groupNo, promiseId): written by
  `FulfillmentLocked`; resolves the group of `PackageDelivered`.

### Persistence of the cross-cutting parts

- Outbox: `outbox` rows (encoded CloudEvent, topic, key) are appended in the same
  transaction as the order save; the always-on relay drains them with an attempt cap.
- Idempotency: `idempotency_keys` (route, key, request hash, response, 24 h TTL).
- Inbox: `processed_events`.

## Consequences

- No synchronous call is needed to render an order or its tracking.
- The producers' own AsyncAPI files are the authority; this repo vendors only the
  tolerant subset in `apis/asyncapi.yaml` and must be re-checked when a producer
  publishes (the conformance check of the orchestrator).
- Open points for the producers (see ADR 0002 model gaps): delivery-promise adds
  `destinationPostalPrefix` to its Promise response and fixes the confirm body.
