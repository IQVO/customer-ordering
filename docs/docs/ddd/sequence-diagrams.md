---
id: sequence-diagrams
title: Sequence diagrams
sidebar_label: Sequence diagrams
sidebar_position: 9
---

# Sequence diagrams

The key scenarios with their error branches, drawn from the code on `develop`.
Every diagram has a `Source:` line.

## Place an order

```mermaid
sequenceDiagram
  autonumber
  actor S as Shopper caller
  participant API as cmd/api HTTP
  participant UC as PlaceOrder
  participant DP as delivery-promise
  participant PG as Postgres
  participant R as Outbox relay
  participant K as Kafka
  S->>API: POST /checkout with Idempotency-Key
  API->>UC: Execute(orderId from key, shopperId, shipTo or shipToAddressId, lines, groups)
  UC->>PG: replay check, load shopper and saved address
  Note over UC: preflight with placeholder promises
  loop each group
    UC->>DP: POST /promises (offerId, lines)
    DP-->>UC: promiseId and terms
  end
  Note over UC: check lines, groups and destination prefix
  loop each group
    UC->>DP: POST /promises/id/confirmations
  end
  alt offer expired or unusable (404, 409, 422)
    UC->>DP: POST /delivery-quotes per group
    UC-->>S: 409 offer-not-quotable with fresh offers
  else confirm failed after earlier confirms
    UC->>DP: POST /promises/id/cancellations for confirmed groups
    UC-->>S: the confirm error (409, 422 or 503)
  else all confirmed
    UC->>PG: one transaction: save order and append CustomerOrderPlaced to outbox_events
    UC-->>API: order PLACED
    API-->>S: 201 with the order
  end
  R->>PG: claim unpublished outbox rows
  R->>K: CustomerOrderPlaced on retailnetwork.customer-ordering.events
  R->>PG: mark published
```

Source: `internal/application/usecases/checkout.go`,
`internal/adapters/outbound/deliverypromise/gateway.go`,
`internal/adapters/outbound/outbox/relay.go`,
`internal/adapters/inbound/http/handlers.go`. A timeout or 5xx from
delivery-promise is `503 delivery-promise-unavailable`; a replay of the same
`Idempotency-Key` returns the stored answer.

## Cancel an order

```mermaid
sequenceDiagram
  autonumber
  actor S as Shopper caller
  participant API as cmd/api HTTP
  participant UC as CancelOrder
  participant PG as Postgres
  participant K as Kafka
  participant OS as order-sourcing
  participant C as Consumer
  S->>API: POST /orders/id/cancellations (reason) with Idempotency-Key
  API->>UC: Execute
  UC->>PG: load order
  alt no group is PROMISED and none requested before
    UC-->>S: 409 cancel-after-lock
  else order DELIVERED, LOST or RETURNED
    UC-->>S: 409 status-regression
  else some groups PROMISED
    UC->>PG: one transaction: groups become CANCELLATION_REQUESTED and CustomerOrderCancelled is appended
    UC-->>S: 200 with the order
    K-->>OS: CustomerOrderCancelled with groupNos
    alt accepted
      OS->>K: FulfillmentCancelled
      K->>C: FulfillmentCancelled
      C->>PG: group CANCELLED (order CANCELLED when nothing remains)
    else refused
      OS->>K: FulfillmentCancelRefused
      K->>C: FulfillmentCancelRefused
      C->>PG: group back to LOCKED and the refusal reason is kept
    end
  end
```

Source: `internal/application/usecases/cancel.go`,
`internal/domain/customerorder/cancel.go`,
`internal/application/usecases/consume_sourcing.go`. A repeat cancel changes and
publishes nothing.

## A consumed event moves the order forward

```mermaid
sequenceDiagram
  autonumber
  participant OS as order-sourcing
  participant ST as shipping-and-tracking
  participant K as Kafka
  participant KC as Kafka consumer
  participant C as usecases.Consumer
  participant PG as Postgres
  OS->>K: FulfillmentLocked
  K->>KC: message on order-sourcing topic
  KC->>C: ApplyFulfillmentLocked
  C->>PG: one transaction: claim event id in processed_events, link assignment, MarkInFulfillment
  OS->>K: FulfillmentShipped with tracking numbers
  KC->>C: ApplyFulfillmentShipped
  C->>PG: group SHIPPED, replay deliveries that came early
  ST->>K: PackageDelivered
  KC->>C: ApplyPackageDelivered
  C->>PG: package delivered, group DELIVERED when every package is
  Note over KC,C: a failure is retried 5 times with backoff, then the message goes to topic.dlq
  Note over C,PG: unknown order, regression or illegal transition is logged as consumed event skipped and acknowledged
```

Source: `internal/adapters/inbound/kafka/consumer.go`, `deadletter.go`,
`internal/application/usecases/consume_sourcing.go`, `consume_tracking.go`,
`deps.go`.

## Read the ship-to for a label

```mermaid
sequenceDiagram
  autonumber
  participant ST as shipping-and-tracking
  participant API as cmd/api HTTP
  participant UC as GetShipTo
  participant PG as Postgres
  ST->>API: GET /orders/id/ship-to
  API->>UC: Execute(orderId)
  UC->>PG: load order
  alt unknown order
    API-->>ST: 404 not-found
  else found
    API-->>ST: 200 the address with Cache-Control no-store
  end
```

Source: `internal/application/usecases/queries.go`,
`internal/adapters/inbound/http/handlers.go`. If this call fails,
shipping-and-tracking refuses the label (`ship-to-unavailable` on its side); the
same read is available to agents as the MCP tool `get_ship_to`.

## Unkeepable promise

```mermaid
sequenceDiagram
  autonumber
  participant DP as delivery-promise
  participant K as Kafka
  participant C as usecases.Consumer
  participant PG as Postgres
  participant OS as order-sourcing
  DP->>K: PromiseUnkeepable (orderId, groupNo)
  K->>C: ApplyPromiseUnkeepable
  alt group is PROMISED
    C->>PG: one transaction: group CANCELLED, CustomerOrderCancelled appended
    K-->>OS: CustomerOrderCancelled with the group
  else any other state
    C->>PG: nothing changes, event logged as skipped
  end
```

Source: `internal/application/usecases/consume_promise.go`,
`internal/domain/customerorder/cancel.go` (`CancelUnkeepableGroup`).
