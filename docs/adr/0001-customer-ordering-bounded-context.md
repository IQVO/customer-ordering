# 1. Customer Ordering is a bounded context of the retail network

Status: Accepted (bootstrap). Design source: retail-network-docs `model/model.yaml`, context `customer-ordering`, and its ADRs RN-0001 and RN-0007.

## Context

The retail network is modelled as ten bounded contexts, each its own service and database. Turns a shopper's cart into a customer order: obtains a promise for every shipment group at checkout, records the order with the shopper and ship-to address (synthetic PII, held only here), shows status and tracking, and handles customer cancellation.

## Decision

- This repo owns the aggregates CustomerOrder, Shopper; the invariants and error names are those of the model.
- Business decisions of the context:
- No order is placed unless every line belongs to exactly one confirmed-able promise.
- The ship-to address never leaves this context except to shipping-and-tracking for a label.
- A customer may cancel until every shipment group is locked; after that, cancellation is a return.
- Events use the network CloudEvents namespace `com.retailnetwork.commerce.customer-ordering.<entity>.<Event>` on `retailnetwork.customer-ordering.events`
  (analytics-only events on `.analytics`). Published: CustomerOrderPlaced, CustomerOrderCancelled. No `warehouse.*` topic is consumed or produced.
- No live REST lookup into sibling contexts except the calls the model lists under `sync_calls`.
- No authentication on REST, consistent with the fleet. PII exists only in customer-ordering and shipping-and-tracking.

## Consequences

Relationships and the full consumed-event list are in the model and in the AsyncAPI contract added with the first feature PR.
