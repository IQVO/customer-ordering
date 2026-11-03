---
id: domain-message-flow
title: Domain message flow
sidebar_label: Domain message flow
sidebar_position: 5
---

# Domain message flow

Following ddd-crew [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling):
the key scenarios as numbered commands (`cmd`), queries (`qry`) and events (`evt`)
between the context and its collaborators. The same scenarios as timed sequences,
with error branches, are on [Sequence diagrams](/docs/ddd/sequence-diagrams).

## Flow 1: a shopper places an order

```mermaid
flowchart LR
  classDef ctx fill:#e7f5ff,stroke:#1c7ed6,color:#000
  classDef actor fill:#fff4e6,stroke:#e8590c,color:#000
  classDef bc fill:#fff3bf,stroke:#e67700,color:#000
  S([Shopper via checkout or storefront-bff]):::actor
  CO[customer-ordering]:::bc
  DP[delivery-promise]:::ctx
  OS[order-sourcing]:::ctx
  S -->|"1 cmd PlaceOrder: POST /checkout"| CO
  CO -->|"2 cmd MakePromise per group: POST /promises"| DP
  CO -->|"3 cmd ConfirmPromise per group"| DP
  DP -.->|"3a on a refusal: 409 offer-not-quotable, fresh offers"| CO
  CO ==>|"4 evt CustomerOrderPlaced"| OS
  CO ==>|"4 evt CustomerOrderPlaced"| DP
```

`2` and `3` are separated on purpose: every promise is made and the order checked
against them before any is confirmed. On a failed confirm the promises already
confirmed are cancelled (`POST /promises/{id}/cancellations`). Source:
`internal/application/usecases/checkout.go`.

## Flow 2: the order moves through fulfilment

```mermaid
flowchart LR
  classDef ctx fill:#e7f5ff,stroke:#1c7ed6,color:#000
  classDef bc fill:#fff3bf,stroke:#e67700,color:#000
  OS[order-sourcing]:::ctx
  ST[shipping-and-tracking]:::ctx
  DP[delivery-promise]:::ctx
  CO[customer-ordering]:::bc
  OS ==>|"1 evt FulfillmentLocked: group LOCKED"| CO
  OS ==>|"2 evt FulfillmentShipped: group SHIPPED"| CO
  ST ==>|"3 evt LabelIssued, PackageScanned, PackageExceptionRaised"| CO
  ST ==>|"4 evt PackageDelivered: group DELIVERED"| CO
  ST ==>|"4 evt PackageLost or PackageReturned: group closed"| CO
  DP ==>|"5 evt PromiseRevised, AtRisk, Kept, Broken"| CO
```

Each arrow is applied in one transaction with the claim of the CloudEvents id.
The order status is derived from the group states after every change. Source:
`internal/application/usecases/consume_*.go`.

## Flow 3: the shopper cancels

```mermaid
flowchart LR
  classDef ctx fill:#e7f5ff,stroke:#1c7ed6,color:#000
  classDef actor fill:#fff4e6,stroke:#e8590c,color:#000
  classDef bc fill:#fff3bf,stroke:#e67700,color:#000
  S([Shopper]):::actor
  CO[customer-ordering]:::bc
  OS[order-sourcing]:::ctx
  DP[delivery-promise]:::ctx
  S -->|"1 cmd CancelOrder: POST /orders/id/cancellations"| CO
  CO ==>|"2 evt CustomerOrderCancelled: groupNos"| OS
  CO ==>|"2 evt CustomerOrderCancelled: groupNos"| DP
  OS ==>|"3 evt FulfillmentCancelled: group CANCELLED"| CO
  OS ==>|"3 evt FulfillmentCancelRefused: group LOCKED"| CO
```

Source: `internal/application/usecases/cancel.go`, `consume_sourcing.go`.

## Flow 4: an unkeepable promise

```mermaid
flowchart LR
  classDef ctx fill:#e7f5ff,stroke:#1c7ed6,color:#000
  classDef bc fill:#fff3bf,stroke:#e67700,color:#000
  DP[delivery-promise]:::ctx
  CO[customer-ordering]:::bc
  OS[order-sourcing]:::ctx
  DP ==>|"1 evt PromiseUnkeepable"| CO
  CO ==>|"2 evt CustomerOrderCancelled: the group"| OS
  CO ==>|"2 evt CustomerOrderCancelled: the group"| DP
```

Policy `CancelOnUnfulfillable`: the `PROMISED` group becomes `CANCELLED` at once.
Source: `internal/application/usecases/consume_promise.go`.

## Flow 5: reads

```mermaid
flowchart LR
  classDef ctx fill:#e7f5ff,stroke:#1c7ed6,color:#000
  classDef actor fill:#fff4e6,stroke:#e8590c,color:#000
  classDef bc fill:#fff3bf,stroke:#e67700,color:#000
  S([Shopper]):::actor
  A([AI agent or warehouse-ops-agent]):::actor
  ST[shipping-and-tracking]:::ctx
  CO[customer-ordering]:::bc
  S -.->|"1 qry GetOrder, GetOrderTracking, ListOrdersByShopper"| CO
  A -.->|"2 qry MCP get_order, get_order_tracking, get_ship_to"| CO
  A -.->|"2 qry A2A skills get_order, get_order_tracking"| CO
  ST -.->|"3 qry GetShipTo: GET /orders/id/ship-to"| CO
```

Every read returns from Postgres through the same read use cases; only
`GetShipTo` returns the address. Source: `internal/application/usecases/queries.go`,
`internal/adapters/inbound/{http,mcp,a2a}`.
