---
id: ubiquitous-language
title: Ubiquitous language
sidebar_label: Ubiquitous language
sidebar_position: 1
---

# Ubiquitous language

The terms `customer-ordering` speaks, each with the identifier that implements
it in the code of this repo, plus the neighbour terms it uses and the words most
often confused with them. Where the glossary and the code disagree the code is
right. Source: `internal/domain/*`, `internal/application/*`, `apis/openapi.yaml`.

## Own terms

| Term | Definition | Code identifier | Not to be confused with |
| --- | --- | --- | --- |
| **Shopper** | The synthetic customer who places orders: a unique lower-cased e-mail and up to 20 saved addresses; state `ACTIVE` only, no events. | `shopper.Shopper` (`internal/domain/shopper`), table `shoppers` | **Vendor** (`vendor-connectivity`): the organisation that runs a fulfilment node. A shopper never meets a vendor. |
| **Saved address** | A ship-to under a stable `addressId`; checkout may name it with `shipToAddressId` and it is **copied** into the order. | `shopper.SavedAddress`, table `shopper_addresses` | The order's own ship-to, which no later edit of the saved address changes. |
| **Customer order** | What the shopper bought: lines, a ship-to, one promise per shipment group and a derived status. | `customerorder.CustomerOrder` (aggregate root), table `customer_orders` | **Purchase order** at order-sourcing / vendor: one instruction per group. |
| **Ship-to** | The delivery address. Synthetic PII held only here; served by one restricted route and never carried by an event. | `shipto.ShipTo` (value object) | **Destination postal prefix**: the first 3 letters or digits of the normalised postal code, the only part that leaves this context in an event. |
| **Order line** | One catalogue item and a quantity, numbered `lineNo` (at most 100 per order). | `customerorder.OrderLine`, table `order_lines` | A PO line at a vendor, which names a node-specific listing. |
| **Shipment group** / **group promise** | The lines promised and shipped together from one node, with the snapshot of their promise and what events reported since (at most 50 per order). | `customerorder.GroupPromise`, table `order_groups` | **Promise** (`delivery-promise`): the authoritative commitment. This is a snapshot, refreshed by `UpdateGroupPromise` on `PromiseRevised`. **Package**: a group can ship as several packages. |
| **Order status** | The customer-facing summary: `PLACED`, `IN_FULFILLMENT`, `PARTIALLY_SHIPPED`, `SHIPPED`, `PARTIALLY_DELIVERED`, `DELIVERED`, `LOST`, `RETURNED`, `CANCELLED`, derived from the group states and never set directly. | `customerorder.Status`, function `derive` (`status.go`) | The assignment state at order-sourcing and the promise state at delivery-promise. |
| **Group status** | `PROMISED`, `LOCKED`, `SHIPPED`, `DELIVERED`, `LOST`, `RETURNED`, `CANCELLATION_REQUESTED`, `CANCELLED`. | `customerorder.GroupStatus` | The order status. |
| **Checkout** | The act of turning a cart into an order: make and confirm a promise per group, then store the order and raise `CustomerOrderPlaced`. | use case `usecases.PlaceOrder` (`POST /checkout`) | **Quote**: asking delivery-promise for offers, which holds no capacity. |
| **Offer** | A quoted delivery window the shopper saw; checkout consumes it into a promise. | `promising.Offer`, `RequoteRequired` | **Promise**. An expired or consumed offer is answered `409 offer-not-quotable` with fresh offers. |
| **Cancellation request** | A cancel is a request: unlocked groups become `CANCELLATION_REQUESTED` and order-sourcing accepts or refuses. | `CustomerOrder.CancelOrder`, `ConfirmGroupCancelled`, `RefuseGroupCancellation`; `POST /orders/{orderId}:cancel` | A cancelled group: `CANCELLED` only after `FulfillmentCancelled`. |
| **Lock** | The moment order-sourcing commits a group to a node (`FulfillmentLocked`); after it the shopper can no longer cancel the group. | `CustomerOrder.MarkInFulfillment` | Placement. |
| **Order tracking view** | The event-fed read model behind `GET /orders/{orderId}/tracking`: per-group signals, packages, assignment links. | `views.Tracking`, tables `group_signals`, `tracking_packages`, `assignment_links` | The package journey that shipping-and-tracking owns. |
| **Policy `CheckoutPromising`** | For each group make the promise with the offer the shopper saw; if an offer is unusable re-quote every group and place nothing. | `usecases.PlaceOrder` | Re-promise (delivery-promise revising a confirmed promise): checkout never revises. |
| **Policy `CancelOnUnfulfillable`** | When delivery-promise reports `PromiseUnkeepable` the `PROMISED` group is cancelled and `CustomerOrderCancelled` raised. | `CustomerOrder.CancelUnkeepableGroup`, `Consumer.ApplyPromiseUnkeepable` | A shopper's cancellation. |
| **Inbox** | The claim of a CloudEvents id so an event is applied at most once. | table `processed_events`, port `ports.Inbox` | The idempotency key of a REST POST. |
| **Idempotency record** | The stored answer of a POST, replayed for 24 h. | table `idempotency_keys`, `idempotency.Record` | The inbox. |
| **Outbox** | Events written in the order's transaction and published by the relay. | table `outbox_events`, port `ports.EventOutbox` | The topic itself. |

## Neighbour terms this context uses

Definitions belong to the owning context; this context only holds copies of
their ids and values.

| Term | Owner | What it means here |
| --- | --- | --- |
| Delivery offer, promise, `promiseId` | delivery-promise | quoted and made synchronously at checkout; `promiseId` is stored on the group |
| Service tier | delivery-promise | `SAME_DAY`, `NEXT_DAY`, `TWO_DAY`, `STANDARD` (`customerorder.Tier`) |
| Ship-by, deliver-by window, time zone | delivery-promise | `ShipBy`, `DeliverByEarliest..DeliverByLatest`, optional IANA `TimeZone` (ADR 0009) |
| Witness node | delivery-promise | `witnessNodeId`: the node that proved feasibility; the shipping node is the one in `FulfillmentLocked` |
| Revision number | delivery-promise | `revisionNo`; a stale `PromiseRevised` is ignored |
| Promise kept / broken | delivery-promise | the verdict shown in tracking with `lateByMinutes`; it does not change the order status |
| Fulfillment assignment, `assignmentId` | order-sourcing | linked to a group by `FulfillmentLocked` (`assignment_links`) |
| Unsourceable | order-sourcing | no node can meet the promise; recorded in tracking only |
| Tracking number, label, scan | shipping-and-tracking | `tracking_packages` rows; the label is the only artefact outside this context that carries the address |
| Catalog item | the storefront catalogue | order lines name `catalogItemId` only |

## Words not to use

- **"Customer"** for the aggregate: the code says *shopper*.
- **"Account"**, **"user"**, **"login"**: there is no authentication; a shopper is identified by `shopperId` in the request.
- **"Delivered"** for a kept promise: an order is `DELIVERED` on `PackageDelivered`; kept or broken is a separate verdict.
- **"Cancelled"** for a pending cancellation: that is `CANCELLATION_REQUESTED`.
- **"Return"** for `RETURNED`: the group state records that a package was returned to the sender (`PackageReturned`); there is no returns workflow in this context.
