# ADR 0004: CloudEvents envelope and type catalogue

## Status

Accepted (2026-10-23).

## Context

Every Kafka message in the retail network is a CloudEvents 1.0 event in structured
mode (retail-network-docs `model/model.yaml` `conventions.cloudevents`; the repo
rule `.claude/rules/fleet/cloudevents.md`). This ADR is the catalogue of every
type customer-ordering publishes. `TestEventCatalogueMatchesContract` checks it
against `apis/asyncapi.yaml`. The types customer-ordering only consumes are listed
in ADR 0003, not here.

## Decision

### Envelope

- `specversion` `1.0`; `id` a UUID persisted with the outbox row (stable across
  relay retries; consumers dedupe on it); `source`
  `/retailnetwork/customer-ordering`; `subject` the aggregate root id (`orderId`);
  `time` the instant the change was committed, in UTC; `datacontenttype`
  `application/json`; `dataschema`
  `urn:retailnetwork:customer-ordering:events:<EventName>:v1`.
- Kafka value = the JSON event format; header
  `content-type: application/cloudevents+json; charset=UTF-8`; Kafka key = the
  subject, so all events of one order stay ordered on one partition.
- `data` is camelCase; optional fields are omitted when unset; timestamps are
  RFC 3339 UTC.
- Built and decoded only through `internal/adapters/kafka/cloudevents`.
- `<entity>` is the raising aggregate, lowercase, no separators: `customerorder`.
  Tier is `commerce`.

### Published types

Both types go to the topic `retailnetwork.customer-ordering.events`. The model
declares no analytics-only event for this context, so nothing is written to
`retailnetwork.customer-ordering.analytics`.

```text
type: com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderPlaced stream: events
type: com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderCancelled stream: events
```

| Event | Subject / key | Raised when | `data` |
|---|---|---|---|
| `CustomerOrderPlaced` | `orderId` | PlaceOrder is saved (state PLACED) | `{orderId, shopperId, destinationPostalPrefix, placedAt, groups[{groupNo, promiseId, tier, witnessNodeId, shipBy, deliverByEarliest, deliverByLatest, lines[{lineNo, catalogItemId, quantity}]}]}` |
| `CustomerOrderCancelled` | `orderId` | CancelOrder (shopper) or the CancelOnUnfulfillable policy is saved | `{orderId, reason, cancelledAt, groupNos[]}` |

Payload rules:

- The `data` field lists are the model's `publishes[].data` lists. Wire types
  are recorded in `apis/asyncapi.yaml` (ADR 0002, assumption A22).
- `CustomerOrderCancelled` is a REQUEST (Gap Register G12, contracts-delta E11):
  order-sourcing decides and answers `FulfillmentCancelled` or
  `FulfillmentCancelRefused`. `groupNos` lists only the groups whose cancellation
  is requested.
- No event carries the ship-to address, the shopper's name, e-mail or phone: the
  address leaves the context only through `GET /orders/{orderId}/ship-to`
  (ADR RN-0006).
- A breaking payload change is a new `.v2` type and dataschema, never a mutation
  of v1.

## Consequences

- Consumers need nothing but this catalogue and `apis/asyncapi.yaml`; there is
  no shared Go code.
- Adding a published type requires a line here in the same commit as its
  asyncapi message, or the architecture fitness test fails.
