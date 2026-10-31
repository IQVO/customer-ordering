---
id: domain-events
title: Domain events
sidebar_label: Domain events
sidebar_position: 10
---

# Domain events

Every event this context raises, publishes and consumes, with the field detail
the [Event catalogue](/docs/api-reference/events) summarises. Sources:
`internal/domain/customerorder/events.go`,
`internal/adapters/outbound/kafka/encoder.go`,
`internal/adapters/kafka/cloudevents/cloudevents.go`, `apis/asyncapi.yaml`.

## Envelope

Every message is a CloudEvents 1.0 event in structured JSON mode with the Kafka
header `content-type: application/cloudevents+json; charset=UTF-8`.

| Attribute | Value |
| --- | --- |
| `specversion` | `1.0` |
| `id` | a UUID v4 minted once by the encoder when the event is written to `outbox_events`; every relay retry repeats it |
| `source` | `/retailnetwork/customer-ordering` |
| `type` | `com.retailnetwork.commerce.customer-ordering.customerorder.<EventName>` (tier `commerce`, entity `customerorder`) |
| `subject` | the `orderId` |
| `time` | the instant the fact happened, UTC |
| `datacontenttype` | `application/json` |
| `dataschema` | `urn:retailnetwork:customer-ordering:events:<EventName>:v1` |
| Kafka topic / key | `retailnetwork.customer-ordering.events` / the `orderId` |

`data` is camelCase, instants are RFC 3339 UTC, optional fields are omitted when
absent and arrays are always present. A breaking change would be a new `.v2`
type, never a mutation of `v1`.

## Raised by the domain

| Event | Raised by | Condition |
| --- | --- | --- |
| `CustomerOrderPlaced` | `customerorder.Place` | the order was built with every line in one group and every group promised |
| `CustomerOrderCancelled` | `CustomerOrder.CancelOrder` and `CancelUnkeepableGroup` | at least one unlocked group became `CANCELLATION_REQUESTED`, or an unkeepable group became `CANCELLED` |

Shoppers raise no events. The consumed events change state but raise nothing
except the cancellation of an unkeepable group.

## Published

### CustomerOrderPlaced

| Field | Type | Meaning |
| --- | --- | --- |
| `orderId` | string | the customer order |
| `shopperId` | string | the shopper (an id, not PII) |
| `destinationPostalPrefix` | string | first 3 letters or digits of the normalised ship-to postal code; never the address |
| `placedAt` | timestamp | when the order was placed |
| `groups[].groupNo` | integer | group number inside the order |
| `groups[].promiseId` | string | the delivery-promise promise of the group |
| `groups[].tier` | enum | `SAME_DAY`, `NEXT_DAY`, `TWO_DAY`, `STANDARD` |
| `groups[].witnessNodeId` | string | the node of the promise's witness path; order-sourcing may choose another node |
| `groups[].shipBy`, `deliverByEarliest`, `deliverByLatest` | timestamp | the promise terms |
| `groups[].timeZone` | string, optional | IANA zone of the destination the window is read in (ADR 0009) |
| `groups[].lines[]` | `{lineNo, catalogItemId, quantity}` | the lines of the group |

Consumers: order-sourcing (queues one assignment per group) and delivery-promise
(reconciling confirm). The per-group promise snapshot lets order-sourcing build
its constraint from this one event.

### CustomerOrderCancelled

| Field | Type | Meaning |
| --- | --- | --- |
| `orderId` | string | the customer order |
| `reason` | string | free text: the shopper's reason (1 to 200 characters) or `PROMISE_UNKEEPABLE: <reason>` from the policy |
| `cancelledAt` | timestamp | when the cancellation was recorded |
| `groupNos` | integer[] | the groups whose cancellation is requested, a subset of the order's groups when others are locked |

Consumers: order-sourcing (cancel request per listed group; it answers
`FulfillmentCancelled` or `FulfillmentCancelRefused`) and delivery-promise
(cancels the promises of the listed groups).

Example envelope (from `apis/asyncapi.yaml`, synthetic ids):

```json
{
  "specversion": "1.0",
  "id": "3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c40",
  "source": "/retailnetwork/customer-ordering",
  "type": "com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderPlaced",
  "subject": "7a1e6c52-0d2b-4f0e-9d6a-5c6f1b8e2a10",
  "time": "2026-10-23T10:15:30Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:customer-ordering:events:CustomerOrderPlaced:v1",
  "data": {
    "orderId": "7a1e6c52-0d2b-4f0e-9d6a-5c6f1b8e2a10",
    "shopperId": "3b0c1f0e-6f0a-4a39-8a55-2b7f0d8c9e11",
    "destinationPostalPrefix": "787",
    "placedAt": "2026-10-23T10:15:30Z",
    "groups": [
      {
        "groupNo": 1,
        "promiseId": "p-1001",
        "tier": "NEXT_DAY",
        "witnessNodeId": "fc-aus-1",
        "shipBy": "2026-10-23T22:00:00Z",
        "deliverByEarliest": "2026-10-24T13:00:00Z",
        "deliverByLatest": "2026-10-24T22:00:00Z",
        "timeZone": "America/Chicago",
        "lines": [{"lineNo": 1, "catalogItemId": "sku-100", "quantity": 2}]
      }
    ]
  }
}
```

There is no address, name or phone anywhere in the envelope.

## Consumed

Fields the handlers rely on (the producers' own AsyncAPI files are
authoritative; unknown fields are ignored).

| Event | Producer type segment | Fields used | Effect |
| --- | --- | --- | --- |
| `PromiseAtRisk` | `commerce.delivery-promise.promise` | `promiseId`, `orderId`, `reason`, `detectedAt` | at-risk reason on the group's signal row |
| `PromiseRevised` | same | `promiseId`, `orderId`, `revisionNo`, `deliverByEarliest`, `deliverByLatest`, `timeZone?`, `shipBy`, `nodeId`, `reason` | `UpdateGroupPromise`; a `revisionNo` not above the stored one is ignored |
| `PromiseKept` | same | `promiseId`, `orderId`, `nodeId`, `deliveredAt`, `deliverByLatest` | verdict `KEPT` in the tracking view |
| `PromiseBroken` | same | as above plus `lateByMinutes`, `cause` | verdict `BROKEN` with minutes late |
| `PromiseUnkeepable` | same | `promiseId`, `orderId`, `groupNo`, `reason` | `CancelUnkeepableGroup` (policy `CancelOnUnfulfillable`) |
| `FulfillmentLocked` | `commerce.order-sourcing.assignment` | `assignmentId`, `orderId`, `groupNo`, `nodeId`, `lockedAt`, `promiseId` | `MarkInFulfillment`; stores the assignment link |
| `FulfillmentShipped` | same | `assignmentId`, `orderId`, `groupNo`, `nodeId`, `trackingNumbers`, `shippedAt` | `MarkGroupShipped`; replays early package events |
| `FulfillmentUnsourceable` | same | `orderId`, `groupNo`, `promiseId`, `excludedNodes`, `reason` | reason in the tracking view only |
| `FulfillmentCancelled` | same | `orderId`, `groupNo`, `reason` | `ConfirmGroupCancelled` |
| `FulfillmentCancelRefused` | same | `orderId`, `groupNo`, `reason` | `RefuseGroupCancellation`: back to `LOCKED` |
| `LabelIssued` | `logistics.shipping-and-tracking.label` | `trackingNumber`, `assignmentId`, `packageNo`, `tier`, `carrier` | package row created or completed |
| `LabelVoided` | same | `trackingNumber`, `reason` | package marked voided |
| `PackageScanned` | `logistics.shipping-and-tracking.journey` | `trackingNumber`, `scanId`, `nodeId`, `scanType`, `at`, `state` | newest scan on the package |
| `PackageExceptionRaised` | same | `trackingNumber`, `assignmentId`, `orderId`, `reason`, `at` | exception reason |
| `PackageDelivered` | same | `trackingNumber`, `assignmentId`, `orderId`, `deliveredAt` | `MarkGroupDelivered` once every package of the group is delivered |
| `PackageLost` | same | `trackingNumber`, `orderId`, `declaredAt` (falls back to the event time) | `MarkGroupLost`: terminal |
| `PackageReturned` | same | `trackingNumber`, `orderId`, `returnedAt` (falls back to the event time) | `MarkGroupReturned`: terminal |

**Ordering.** Producers key by their own root ids, so there is no order
guarantee across groups or topics. An event for an unknown order, an unknown
group, a regression or an illegal transition is logged as
`consumed event skipped` and acknowledged (ADR 0002, A7); a delivery or package
outcome that arrives before the shipment is known is stored and replayed when
`FulfillmentShipped` arrives. Dedupe is on the CloudEvents `id`
(`processed_events`).
