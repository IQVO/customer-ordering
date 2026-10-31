---
id: introduction
title: Introduction
sidebar_label: Introduction
sidebar_position: 1
---

# Customer Ordering

`customer-ordering` is the **Generic** bounded context that turns a shopper's
cart into a **customer order**. It belongs to the retail network's commerce tier
(CloudEvents segment `commerce`, the Storefront side of the portfolio): it
obtains a delivery promise for every shipment group at checkout, records the
order with the shopper and ship-to address (synthetic PII held only here), shows
the order's status and tracking, and handles customer cancellation. Shoppers and
their saved addresses live here too.

Cart-to-order is commodity commerce, so the context is kept thin: the promise
that makes the network different is owned by `delivery-promise`, and the choice
of the building that ships is owned by `order-sourcing`
([Subdomain classification](/docs/ddd/subdomain-classification),
[ADR 0001](/docs/adr/0001-customer-ordering-bounded-context)). The context is an
Open Host Service: REST plus a published CloudEvents language on
`retailnetwork.customer-ordering.events`.

:::info What runs on develop
Three binaries over one Postgres database: `cmd/api` (REST, the transactional
outbox relay and three Kafka consumers in one process), and the read-only
`cmd/mcp` ([ADR 0005](/docs/adr/0005-mcp-server-adoption)) and `cmd/a2a`
([ADR 0008](/docs/adr/0008-a2a-inbound-adapter)). All run on real Postgres; the
API also on real Kafka and a real delivery-promise endpoint. There is no
in-memory mode. `web/` is the `customer_ordering_mfe` console remote
([ADR 0006](/docs/adr/0006-console-remote)). A Helm chart,
`charts/customer-ordering`, packages them (`mcp`, `a2a` and `frontend` are
optional components, off by default).
:::

## What the context owns

| Concept | Where it lives | Rule in one line |
| --- | --- | --- |
| **CustomerOrder** (aggregate) | `internal/domain/customerorder` | Lines, one promise per shipment group, the ship-to, and the customer-facing status derived from the group states. Every line is in exactly one group; the promise destination matches the ship-to; status only moves forward. |
| **Shopper** (aggregate) | `internal/domain/shopper` | A registered, synthetic shopper with a unique lower-cased e-mail and at most 20 saved addresses. |
| **ShipTo** (value object) | `internal/domain/shipto` | The delivery address. Synthetic PII, served by one restricted route and never carried by an event; events carry only the destination postal prefix. |
| **Order tracking** (read model) | tables `group_signals`, `tracking_packages`, `assignment_links` | Event-fed: promise signals, verdicts, packages, scans, exceptions and outcomes of every group. |
| **Inbox, idempotency, outbox** | tables `processed_events`, `idempotency_keys`, `outbox_events` | At-most-once consumption, replayed POSTs for 24 h, at-least-once publication. |

## What it does NOT own

| Not here | Owner |
| --- | --- |
| Computing delivery dates, holding capacity, judging a promise kept or broken | `delivery-promise` (called synchronously at checkout; events consumed afterwards) |
| Choosing the shipping node, locking the assignment, accepting or refusing a cancel | `order-sourcing` |
| Labels, scans, deliveries, the package journey | `shipping-and-tracking` (the only reader of the ship-to address, for the label) |
| Carts, catalogue, prices, availability | the other Storefront contexts |
| Payments, invoices, tax, notifications, returns after the lock | not built (future contexts) |
| Authentication | nothing: REST, MCP and A2A are unauthenticated fleet-wide; the shopper's identity is only a `shopperId` in the request |

This context never calls `order-sourcing` or `shipping-and-tracking`. Its one
outbound call is to `delivery-promise`
([Integration](/docs/ecosystem/integration)).

## Main capabilities

1. **Checkout** (`POST /checkout`): quote check, make and confirm a promise per
   group at `delivery-promise`, then store the order and publish
   `CustomerOrderPlaced` in one transaction. A failed confirm places nothing and
   cancels the promises of the same checkout that were already confirmed. An
   expired offer answers 409 with fresh offers.
2. **Order history and status**: `GET /orders?shopperId=` (newest first, keyset
   cursor), `GET /orders/{orderId}`, `GET /orders/{orderId}/tracking`.
3. **Cancellation** (`POST /orders/{orderId}:cancel`): a request to
   order-sourcing for every unlocked group; order-sourcing accepts or refuses.
   An unkeepable promise cancels its group by policy.
4. **Ship-to**: `GET /orders/{orderId}/ship-to`, the one restricted route that
   returns the address (`Cache-Control: no-store`).
5. **Shoppers and saved addresses**: register, look up by id or e-mail, add up to
   20 saved addresses; checkout may use `shipToAddressId` instead of an inline
   `shipTo`.
6. **Read-only agent surfaces**: three MCP tools and two A2A skills over the same
   read use cases ([MCP tools](/docs/mcp/tools),
   [API overview](/docs/api-reference)).
7. **Events**: publishes `CustomerOrderPlaced` and `CustomerOrderCancelled`;
   consumes five delivery-promise, five order-sourcing and seven
   shipping-and-tracking events ([Event catalogue](/docs/api-reference/events)).

## Where to go next

| You want to | Read |
| --- | --- |
| See the layers, packages, binaries and the rules that police them | [Architecture](/docs/overview/architecture), [Runtime](/docs/overview/runtime) |
| Run it and make the first calls | [Quickstart](/docs/overview/quickstart) |
| Know every setting | [Configuration](/docs/operations/configuration) |
| Deploy and operate it | [Runbook](/docs/operations/runbook), [Observability](/docs/operations/observability), [Before the first release](/docs/operations/before-first-release) |
| Fix something that broke | [Troubleshooting](/docs/operations/troubleshooting) |
| Know what tests and CI gates exist | [Testing](/docs/development/testing) |
| See who talks to whom | [Integration](/docs/ecosystem/integration) |
| See the application use cases and their invariants | [Use cases](/docs/ddd/use-cases) |
| Read the design in ddd-crew artifacts | [DDD artifacts](/docs/ddd/ddd-artifacts), [Ubiquitous language](/docs/ddd/ubiquitous-language) |
| Read the contracts | [API overview](/docs/api-reference), [Event catalogue](/docs/api-reference/events), [MCP tools](/docs/mcp/tools) |
| Read the decisions | [ADR index](/docs/adr) |

Published at [https://iqvo.github.io/customer-ordering/](https://iqvo.github.io/customer-ordering/).
