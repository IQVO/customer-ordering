---
id: events
title: Event catalogue
sidebar_label: Event catalogue
sidebar_position: 2
---

# Event catalogue

Every Kafka message this service produces or consumes is a CloudEvents 1.0
event in **structured mode**: the Kafka value is the whole JSON envelope and the
header `content-type` is `application/cloudevents+json; charset=UTF-8`.
Envelopes are built only in `internal/adapters/kafka/cloudevents` and encoded
in `internal/adapters/outbound/kafka/encoder.go`
([ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue)). The
contract is `apis/asyncapi.yaml` (AsyncAPI 3.0.0); field-level detail is on
[Domain events](/docs/ddd/domain-events).

:::info Built
The outbox relay and the three consumers run inside `cmd/api` and are always
on: the process refuses to boot without `DATABASE_URL`, `KAFKA_BROKERS`,
`DELIVERY_PROMISE_URL` and the three consumer group ids
([Configuration](/docs/operations/configuration)). The tables below are the
pinned contract, not observed traffic. Which sibling reads or writes the other
side of each edge is on [Integration](/docs/ecosystem/integration).
:::

## Published

Topic `retailnetwork.customer-ordering.events`, source
`/retailnetwork/customer-ordering`, `dataschema`
`urn:retailnetwork:customer-ordering:events:<EventName>:v1`. The Kafka key and
the CloudEvents `subject` are the `orderId`, so one order's events stay on one
partition and in order. The `id` is minted once, when the event is written to
`outbox_events`, and a relay retry repeats it.

| Type | Raised when | `data` |
| --- | --- | --- |
| `com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderPlaced` | `PlaceOrder` saved the order in state `PLACED` | `orderId`, `shopperId`, `destinationPostalPrefix`, `placedAt`, `groups[{groupNo, promiseId, tier, witnessNodeId, shipBy, deliverByEarliest, deliverByLatest, timeZone?, lines[{lineNo, catalogItemId, quantity}]}]` |
| `com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderCancelled` | the shopper cancelled (`CancelOrder`) or the `CancelOnUnfulfillable` policy cancelled a group (`PromiseUnkeepable`) | `orderId`, `reason`, `cancelledAt`, `groupNos[]` |

`tier` is `SAME_DAY`, `NEXT_DAY`, `TWO_DAY` or `STANDARD`. `timeZone` is the
IANA zone of the destination, optional and additive on v1
([ADR 0009](/docs/adr/0009-delivery-window-time-zone)); it is omitted when
unknown. No event carries the ship-to address. A cancellation is a **request**:
order-sourcing answers with `FulfillmentCancelled` or `FulfillmentCancelRefused`.
Nothing is published on `retailnetwork.customer-ordering.analytics`.

## Consumed

Dispatch is on the full `type`; unknown types on a topic are ignored. Every
handler claims the CloudEvents `id` in `processed_events` in the same
transaction as its effect, so an event is applied at most once.

| Topic (consumer group variable) | Type | Effect (handler in `usecases.Consumer`) |
| --- | --- | --- |
| `retailnetwork.delivery-promise.events` (`DELIVERY_PROMISE_CONSUMER_GROUP`) | `com.retailnetwork.commerce.delivery-promise.promise.PromiseAtRisk` | records the at-risk reason (`ApplyPromiseAtRisk`) |
| same | `...promise.PromiseRevised` | new window, ship-by, node, zone; stale `revisionNo` ignored (`ApplyPromiseRevised`) |
| same | `...promise.PromiseKept`, `...promise.PromiseBroken` | records the verdict and `lateByMinutes` |
| same | `...promise.PromiseUnkeepable` | policy `CancelOnUnfulfillable`: the group becomes `CANCELLED` and `CustomerOrderCancelled` is raised |
| `retailnetwork.order-sourcing.events` (`ORDER_SOURCING_CONSUMER_GROUP`) | `com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentLocked` | group `LOCKED`, order `IN_FULFILLMENT` |
| same | `...assignment.FulfillmentShipped` | tracking numbers stored, group `SHIPPED` |
| same | `...assignment.FulfillmentUnsourceable` | reason recorded in the tracking view |
| same | `...assignment.FulfillmentCancelled` | group `CANCELLED` |
| same | `...assignment.FulfillmentCancelRefused` | group back to `LOCKED`, refusal reason kept |
| `retailnetwork.shipping-and-tracking.events` (`SHIPPING_CONSUMER_GROUP`) | `com.retailnetwork.logistics.shipping-and-tracking.label.LabelIssued`, `...label.LabelVoided` | package created or completed / voided |
| same | `...journey.PackageScanned`, `...journey.PackageExceptionRaised` | newest scan / exception reason |
| same | `...journey.PackageDelivered` | package delivered; group `DELIVERED` when all its packages are |
| same | `...journey.PackageLost`, `...journey.PackageReturned` | group `LOST` / `RETURNED` (terminal) |

Seventeen types are consumed (five from delivery-promise, five from
order-sourcing, seven from shipping-and-tracking), one `Consumer.Apply*` handler
each. The prose in `apis/asyncapi.yaml` says "Fifteen events" but its channels
and operations list seventeen; the code and the channel lists agree on
seventeen. The messages list only the fields this service relies on; unknown
fields are ignored (tolerant reader).

A failed handler is retried (capped backoff, 5 attempts) and then the message
goes to `<topic>.dlq` with `x-dlq-*` headers; a payload that can never be
applied goes there at once
(`internal/adapters/inbound/kafka/deadletter.go`). See the
[Runbook](/docs/operations/runbook#consumers-and-the-dlq).
