---
id: subdomain-classification
title: Subdomain classification
sidebar_label: Subdomain classification
sidebar_position: 2
---

# Subdomain classification

`customer-ordering` is a **Generic** subdomain. Two views of the fleet agree on
the class and differ on the label of the area:

- The retail-network model in the warehouse-docs aggregator
  (`network/docs/strategic-design/subdomain-classification.md` on `main`) lists it
  under **Order Capture** with class **Generic**, evolution **Commodity**,
  business model **Engagement** and CloudEvents tier `commerce`.
- The application portfolio (`docs/fleet-health/application-portfolio.md` on
  `main`) files it under area **Storefront**, class **Generic**.

The class is the same in both and matches [ADR 0001](/docs/adr/0001-customer-ordering-bounded-context).
The CloudEvents tier `commerce` is not the class: it only decides the event type
prefix `com.retailnetwork.commerce.customer-ordering.`.

## Why Generic

- **Not Core**: cart-to-order is commodity commerce. The context computes no
  delivery date, holds no capacity and chooses no node; the promise belongs to
  `delivery-promise` and the choice of building to `order-sourcing`, both Core.
  Shoppers choose the network for the date on the order, not for how its order
  record works.
- **Not Supporting**: no part of it is network-specific enough to differentiate.
  The only custom rules are the per-group promise snapshot in
  `CustomerOrderPlaced` and the single-reader ship-to rule, and they are about
  where decisions are stored, not about making them.
- **Kept thin on purpose.** The model says: "Cart-to-order is commodity commerce.
  It is kept thin and holds the PII so nothing else has to." The aggregates are
  small (`CustomerOrder`, `Shopper`) and the invariants are bookkeeping
  ([Aggregate design canvas](/docs/ddd/aggregate-design-canvas)).
- **Complexity is moderate** because of the synchronous multi-call to
  delivery-promise with compensation and three event streams without a common
  order ([Core domain chart](/docs/ddd/core-domain-chart)).

What would move it: bought order-management software behind an adapter keeps it
Generic and shrinks it; nothing in the model pushes it up. A custom
re-promising or sourcing decision made here would, and belongs in the Core
contexts instead.

## Neighbours (from the aggregator)

Rows copied from the network table (`network/docs/strategic-design/subdomain-classification.md`),
the contexts this one touches:

| Bounded context | Classification | CloudEvents tier | Relation to this context |
| --- | --- | --- | --- |
| `delivery-promise` | Core | `commerce` | upstream (REST at checkout, promise events) and downstream (consumes the order events) |
| `order-sourcing` | Core | `commerce` | upstream (fulfilment events) and downstream (consumes the order events) |
| `shipping-and-tracking` | Generic | `logistics` | upstream (label and package events) and downstream (reads the ship-to) |
| `customer-ordering` | **Generic** | `commerce` | this context |

Contexts of the same model with no edge here (class in the network table):
`node-topology` Supporting, `offer-availability` Supporting,
`vendor-connectivity` Supporting, `linehaul-transportation` Supporting,
`last-mile-delivery` Supporting, `node-performance` Supporting,
`seller-inbound` Supporting. The network counts two Core, seven Supporting and
two Generic contexts.

Storefront side, from `storefront/docs/strategic-design/subdomain-classification.md`:
the storefront deliberately has no order-management or customer-account context
because this one owns them. Its callers found in the code are `checkout`
(Supporting) and `storefront-bff` (no domain model); the other storefront
contexts are `pricing` (Core), `shopping-cart` (Supporting), `product-discovery`
(Supporting), `product-content` (Generic) and `customer-payments` (Generic).

## Tier is not classification

`commerce` and `logistics` group contexts by area of the business; Core,
Supporting and Generic say where investment goes. For this context the two axes
read: tier `commerce`, class Generic.
