---
id: eventstorming
title: EventStorming
sidebar_label: EventStorming
sidebar_position: 6
---

# EventStorming

Design-level boards in the ddd-crew
[EventStorming glossary and cheat sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet)
notation, drawn from the code on `develop`. Events of a neighbour are orange
stickies prefixed with the producer. Every hotspot is a gap or risk visible in
the code or the contracts; the table at the end names its source. The big picture
for the whole network is in the aggregator at
[EventStorming big picture](https://iqvo.github.io/warehouse-docs/network/strategic-design/eventstorming-big-picture).

## Legend

| Colour | Sticky | Meaning here |
| --- | --- | --- |
| Pale yellow `#fff59d` | Actor | The shopper, through `checkout`, `storefront-bff`, the console remote or a test |
| Blue `#a5d8ff` | Command | A use case or a synchronous call to delivery-promise |
| Yellow `#ffec99` | Aggregate | `CustomerOrder` or `Shopper` |
| Orange `#ffc078` | Domain event | Ours, or a neighbour's prefixed with its producer |
| Lilac `#d0bfff` | Policy | `CheckoutPromising`, `CancelOnUnfulfillable`, or the reaction of a handler to an event |
| Green `#b2f2bb` | Read model | The tracking view, or the restricted ship-to read |
| Pink `#fcc2d7` | External system | A sibling context |
| Red `#ff8787` | Hotspot | A gap or risk (table at the end) |

## 1. Registration and checkout

```mermaid
flowchart TB
    SH["Shopper"]:::actor
    C0["RegisterShopper<br/>POST /shoppers"]:::command
    C0b["AddSavedAddress<br/>POST /shoppers/id/addresses"]:::command
    G0["Shopper"]:::aggregate
    C1["PlaceOrder<br/>POST /checkout"]:::command
    P1["CheckoutPromising<br/>make then confirm a promise per group"]:::policy
    DP["delivery-promise<br/>quote, make, confirm, cancel"]:::external
    G1["CustomerOrder"]:::aggregate
    E1["CustomerOrderPlaced"]:::event
    OS["order-sourcing<br/>queues one assignment per group"]:::external
    DP2["delivery-promise<br/>reconciling ConfirmPromise"]:::external
    H1["H1 delivery-promise down:<br/>503, nothing placed"]:::hotspot
    H2["H2 failed confirm:<br/>compensating cancels, may fail"]:::hotspot

    SH --> C0 --> G0
    SH --> C0b --> G0
    SH --> C1 --> P1
    P1 --> DP
    DP -->|"offer expired or unusable"| RQ["409 offer-not-quotable<br/>with fresh offers"]:::readmodel
    DP -->|"promise per group"| G1
    G1 --> E1
    E1 --> OS
    E1 --> DP2
    DP --- H1
    P1 --- H2

    classDef actor fill:#fff59d,stroke:#b59f00,color:#000
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#e67700,color:#000
    classDef event fill:#ffc078,stroke:#d9480f,color:#000
    classDef policy fill:#d0bfff,stroke:#6741d9,color:#000
    classDef readmodel fill:#b2f2bb,stroke:#2f9e44,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
    classDef hotspot fill:#ff8787,stroke:#c92a2a,color:#000
```

Source: `internal/application/usecases/checkout.go`, `shopper.go`,
`internal/domain/customerorder/place.go`,
`internal/adapters/outbound/deliverypromise/gateway.go`.

## 2. Fulfilment progress and tracking

```mermaid
flowchart TB
    subgraph PR["delivery-promise events"]
      DE1["delivery-promise:<br/>PromiseRevised"]:::event
      DE2["delivery-promise:<br/>PromiseAtRisk, Kept, Broken"]:::event
    end
    subgraph SO["order-sourcing events"]
      SE1["order-sourcing:<br/>FulfillmentLocked"]:::event
      SE2["order-sourcing:<br/>FulfillmentShipped"]:::event
      SE3["order-sourcing:<br/>FulfillmentUnsourceable"]:::event
    end
    subgraph SH["shipping-and-tracking events"]
      TE1["LabelIssued, LabelVoided,<br/>PackageScanned, ExceptionRaised"]:::event
      TE2["PackageDelivered"]:::event
      TE3["PackageLost, PackageReturned"]:::event
    end
    P0["Consumer handler:<br/>claim event id, then apply"]:::policy
    G["CustomerOrder"]:::aggregate
    V["Tracking view<br/>group_signals, tracking_packages,<br/>assignment_links"]:::readmodel
    Q1["GET /orders/id/tracking"]:::readmodel
    H3["H3 no ordering across topics:<br/>early deliveries are replayed"]:::hotspot

    DE1 --> P0
    DE2 --> P0
    SE1 --> P0
    SE2 --> P0
    SE3 --> P0
    TE1 --> P0
    TE2 --> P0
    TE3 --> P0
    P0 -->|"UpdateGroupPromise, MarkInFulfillment,<br/>MarkGroupShipped, MarkGroupDelivered,<br/>MarkGroupLost, MarkGroupReturned"| G
    P0 -->|"signals, packages, links"| V
    V --> Q1
    G --> Q1
    P0 --- H3

    classDef event fill:#ffc078,stroke:#d9480f,color:#000
    classDef policy fill:#d0bfff,stroke:#6741d9,color:#000
    classDef aggregate fill:#ffec99,stroke:#e67700,color:#000
    classDef readmodel fill:#b2f2bb,stroke:#2f9e44,color:#000
    classDef hotspot fill:#ff8787,stroke:#c92a2a,color:#000
```

Source: `internal/adapters/inbound/kafka/handlers.go`,
`internal/application/usecases/consume_promise.go`, `consume_sourcing.go`,
`consume_tracking.go`, `internal/domain/customerorder/commands.go`.

## 3. Cancellation

```mermaid
flowchart TB
    SH["Shopper"]:::actor
    C1["CancelOrder<br/>POST /orders/id/cancellations"]:::command
    G["CustomerOrder"]:::aggregate
    E1["CustomerOrderCancelled<br/>groupNos of unlocked groups"]:::event
    OS["order-sourcing"]:::external
    OE1["order-sourcing:<br/>FulfillmentCancelled"]:::event
    OE2["order-sourcing:<br/>FulfillmentCancelRefused"]:::event
    DP["delivery-promise<br/>CancelPromise"]:::external
    UK["delivery-promise:<br/>PromiseUnkeepable"]:::event
    P2["CancelOnUnfulfillable"]:::policy
    H4["H4 cancel check is advisory:<br/>a lock not yet consumed is invisible"]:::hotspot

    SH --> C1 --> G --> E1
    E1 --> OS
    E1 --> DP
    OS --> OE1 -->|"ConfirmGroupCancelled: group CANCELLED"| G
    OS --> OE2 -->|"RefuseGroupCancellation: back to LOCKED"| G
    UK --> P2 -->|"CancelUnkeepableGroup"| G
    C1 --- H4

    classDef actor fill:#fff59d,stroke:#b59f00,color:#000
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#e67700,color:#000
    classDef event fill:#ffc078,stroke:#d9480f,color:#000
    classDef policy fill:#d0bfff,stroke:#6741d9,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
    classDef hotspot fill:#ff8787,stroke:#c92a2a,color:#000
```

Source: `internal/application/usecases/cancel.go`,
`internal/domain/customerorder/cancel.go`, `consume_promise.go`,
`consume_sourcing.go`.

## Reading it

- The shopper's cancel is a **request**: the group stays in
  `CANCELLATION_REQUESTED` until order-sourcing answers, and a vendor that ships
  first wins.
- `Shopper` raises no event and `GetShipTo` is a read, so neither appears as an
  orange sticky.
- The synchronous replies and the outbox are omitted from the boards.

## Hotspots

| # | Hotspot | Source |
| --- | --- | --- |
| H1 | If delivery-promise is slow or down the checkout answers `503 delivery-promise-unavailable` after `DELIVERY_PROMISE_TIMEOUT`; there is no fallback promise and the caller must retry with the same `Idempotency-Key`. | `deliverypromise/gateway.go`, `http/errors.go` |
| H2 | A confirm that fails after earlier groups were confirmed triggers compensating `CancelPromise` calls; one that fails is only logged (`compensating cancel of a confirmed promise failed; the hold stays at delivery-promise`) and skipped, so a capacity hold can outlive a failed checkout. When the failed confirm's own answer is unknown its group is cancelled too. | `usecases/checkout.go` (`compensate`), ADR 0002 A14 |
| H3 | Producers key by their own ids, so a delivery or outcome can arrive before `FulfillmentShipped`; the code stores and replays it, an unknown order is skipped, not retried. | `consume_tracking.go`, ADR 0002 A7 |
| H4 | `ErrCancelAfterLock` is advisory: a lock this service has not consumed yet cannot be seen, and order-sourcing's answer decides. | `customerorder/cancel.go` |
| H5 | If publishing to a `.dlq` topic fails, the consumer keeps retrying and the partition is blocked. | [Runbook](/docs/operations/runbook#consumers-and-the-dlq) |
| H6 | `apis/asyncapi.yaml` says the service consumes "Fifteen events" but lists seventeen. | `apis/asyncapi.yaml` info description |
