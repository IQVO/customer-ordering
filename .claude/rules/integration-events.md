---
paths:
  - "internal/adapters/**/kafka/**"
  - "internal/adapters/outbound/outbox/**"
  - "apis/asyncapi*"
---

# Cross-service integration events (Kafka)

customer-ordering PUBLISHES two integration events to
`retailnetwork.customer-ordering.events` and CONSUMES fourteen events from three
topics (ADR 0003): `retailnetwork.delivery-promise.events`,
`retailnetwork.order-sourcing.events`, `retailnetwork.shipping-and-tracking.events`.
The topic `retailnetwork.customer-ordering.analytics` is unused: no event is
analytics-only. Never produce or consume a `warehouse.*` topic.

## Events: CloudEvents 1.0 is MANDATORY

Every Kafka message this service produces or consumes is a CloudEvents 1.0 event in
structured content mode. This is a hard rule, not a preference:

- No flat envelope (`event_id`/`event_type`/`occurred_at`), no dual-write, no
  envelope toggle env var. `internal/architecture/fitness_test.go`'s
  `TestNoEventEnvelopeToggleOrFlatEnvelope` fails CI on any of them.
- Build and validate the envelope with `github.com/cloudevents/sdk-go/v2/event`
  through ONE helper package, `internal/adapters/kafka/cloudevents` (`New`,
  `Decode`, `ContentTypeHeader`). Transport stays `segmentio/kafka-go`; no
  hand-rolled CloudEvent structs. Producer side: the encoder
  `internal/adapters/outbound/kafka/encoder.go`; consumer side:
  `internal/adapters/inbound/kafka/consumer.go`.
- Every produced message carries the Kafka header
  `content-type: application/cloudevents+json; charset=UTF-8`. Message key =
  aggregate id (the orderId), `kafkago.Hash{}` balancer
  (`internal/adapters/outbound/kafka/writer.go`).
- Attributes: `specversion=1.0`; `id` (UUID v4 minted ONCE per event at outbox append
  and stored in `outbox_events`, so a redelivery carries the same id);
  `source=/retailnetwork/customer-ordering`; `type`; `subject` (the orderId, never
  empty); `time` (the domain occurred-at, UTC); `datacontenttype=application/json`;
  `dataschema=urn:retailnetwork:customer-ordering:events:<EventName>:v1`. No custom
  extension attributes without an ADR.
- `type` = `com.retailnetwork.commerce.customer-ordering.customerorder.<EventName>`.
  A breaking payload change is a new `.v2` dataschema, never a mutation of v1;
  evolution is additive only.
- Payloads follow the wire conventions of the network: ids are strings, instants
  RFC 3339 UTC, enums UPPER_SNAKE, optional fields omitted, arrays always present.
  No payload carries the ship-to address.
- Tests: a golden exact-JSON test per published type
  (`internal/adapters/outbound/kafka/encoder_test.go` with
  `testdata/*.golden.json`), the catalogue-vs-contract sensor
  (`TestEventCatalogueMatchesContract`), and the Kafka proof against a real broker
  (`internal/adapters/outbound/outbox/relay_integration_test.go`,
  `cmd/api/e2e_integration_test.go`), all via testcontainers.

### Published types

All on `retailnetwork.customer-ordering.events`; `subject` and Kafka key = orderId;
`dataschema` is `urn:retailnetwork:customer-ordering:events:<Event>:v1`.

| `type` | `subject` |
| --- | --- |
| `com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderPlaced` | orderId |
| `com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderCancelled` | orderId |

### Consumed types

Dispatch is on the FULL type string (`internal/adapters/inbound/kafka/handlers.go`);
unknown types are ignored, decoding is tolerant, the contract of each is restated in
`apis/asyncapi.yaml` from the producer's AsyncAPI.

| `type` | topic | producer |
| --- | --- | --- |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseAtRisk` | `retailnetwork.delivery-promise.events` | delivery-promise |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseRevised` | `retailnetwork.delivery-promise.events` | delivery-promise |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseKept` | `retailnetwork.delivery-promise.events` | delivery-promise |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseBroken` | `retailnetwork.delivery-promise.events` | delivery-promise |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseUnkeepable` | `retailnetwork.delivery-promise.events` | delivery-promise |
| `com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentLocked` | `retailnetwork.order-sourcing.events` | order-sourcing |
| `com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentShipped` | `retailnetwork.order-sourcing.events` | order-sourcing |
| `com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentUnsourceable` | `retailnetwork.order-sourcing.events` | order-sourcing |
| `com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentCancelled` | `retailnetwork.order-sourcing.events` | order-sourcing |
| `com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentCancelRefused` | `retailnetwork.order-sourcing.events` | order-sourcing |
| `com.retailnetwork.logistics.shipping-and-tracking.label.LabelIssued` | `retailnetwork.shipping-and-tracking.events` | shipping-and-tracking |
| `com.retailnetwork.logistics.shipping-and-tracking.label.LabelVoided` | `retailnetwork.shipping-and-tracking.events` | shipping-and-tracking |
| `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageScanned` | `retailnetwork.shipping-and-tracking.events` | shipping-and-tracking |
| `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageDelivered` | `retailnetwork.shipping-and-tracking.events` | shipping-and-tracking |
| `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageExceptionRaised` | `retailnetwork.shipping-and-tracking.events` | shipping-and-tracking |

`TestConsumedTypeStringsAreTheProducersContracts` pins these strings.

## Transactional outbox

A use case saves the aggregates and appends the encoded events in ONE Postgres
transaction (`ports.EventOutbox`, `internal/adapters/outbound/postgres/outbox.go`).
The relay (`internal/adapters/outbound/outbox/relay.go`, always on, started by
`cmd/api/main.go` `startOutboxRelay`) drains `outbox_events` in id order,
at-least-once. The cap `OUTBOX_MAX_ATTEMPTS` (default 20, 0 = unlimited) excludes a
poison row; re-arm it with `UPDATE outbox_events SET attempts = 0 WHERE id = ...`.
The first write to a topic that does not exist yet can fail once with an
unknown-topic error: the relay retries it with the same id. The caller of
`NewOutboxRelay` must pass `WithMaxAttempts(...)` inline (`TestOutboxRelaysPassAttemptCap`).

## Consumers and consumer group ids

Three consumers (`internal/adapters/inbound/kafka`), one per consumed topic, started
by `cmd/api/main.go` `startConsumers`. Each group id comes from a MANDATORY env var
(a boot error names it, raised before the listener binds), never a literal
(`TestKafkaConsumerGroupNeverHardcodedInline`):

| consumer | variable |
| --- | --- |
| delivery-promise | `DELIVERY_PROMISE_CONSUMER_GROUP` |
| order-sourcing | `ORDER_SOURCING_CONSUMER_GROUP` |
| shipping-and-tracking | `SHIPPING_CONSUMER_GROUP` |

Every handler is idempotent on the CloudEvents `id` (`processed_events`, written in
the same transaction as the effect). A message that is not a valid CloudEvent or whose
`data` cannot be decoded is dead-lettered to `<consumed topic>.dlq` with the failure in
headers; the DLQ write guards shutdown (`TestDeadLetterCallsGuardCancellation`).
The consumers do not replay a read model, so the group is stable, never per instance.
