# ADR 0017: The notification policy and `ShopperNoticeDecided`

## Status

Accepted (2026-11-14).

## Context

The network has no way to tell a shopper anything about an order after checkout:
the tracking view is pull-only, no context owns a notification channel, and the
gap register (G62) asked for the group status facts to be published before a
customer-notification context could even be planned. Every fact a shopper notice
would be about already arrives in customer-ordering: delivery-promise's
`PromiseRevised` (a later window), shipping-and-tracking's
`PackageEtaWindowAssigned`, `PackageDeliveryPointFlagged`, `PackageLost`,
`PackageReturned`, `PackageDelivered` and order-sourcing's `FulfillmentShipped`
(ADR 0003). Customer-ordering owns the shopper relationship, and the network
must not send mail (G62/G68): no channel exists anywhere in the estate (F23).

Who notifies for which fact is a product decision that changes without code, so
it is data, not logic: a versioned policy document like the sibling
`SourcingPolicy` of order-sourcing (its `internal/domain/policy` and the
`sourcing_policies` jsonb table) and the `PromisePolicy` of delivery-promise.

## Decision

### The policy is data owned by customer-ordering

`NotificationPolicy` is a versioned document of rows `{family, notifier}`:

- `family` is one of `DELAY_EXPECTED`, `ETA_WINDOW_ASSIGNED`,
  `DELIVERY_POINT_ADVISORY`, `GROUP_SHIPPED`, `GROUP_DELIVERED`, `GROUP_LOST`,
  `GROUP_RETURNED` (the design's family list; the triggers are facts
  customer-ordering already consumes or produces);
- `notifier` is one of `MARKETPLACE`, `SELLER`, `NOBODY`.

Every family defaults to `NOBODY` (version 0, the built-in default): the whole
surface is dormant until an operator publishes version 1. The document is
stored in the `notification_policies` table (migration 0012): one jsonb
snapshot per version, the current policy is the highest version, every version
is kept. A publish is the whole document (a row for every family, each family
exactly once; an unknown family or notifier is refused). There is NO policy
event in v1: the slice is analytics-free, so nothing is written to a topic when
an operator publishes a version.

Operator surface: `GET /notification-policy` and whole-document
`PUT /notification-policy` (operator-facing, like `PUT /sourcing-policy`);
the PUT takes an `Idempotency-Key` because it publishes a new version.

### The fact event: `ShopperNoticeDecided`

When one of the existing consumer use cases records a fact of a family, it
decides the notice in the SAME unit of work and appends one
`ShopperNoticeDecided` to the outbox per family occurrence:

- type `com.retailnetwork.commerce.customer-ordering.order.ShopperNoticeDecided`
  (the `order` entity, the shopper-facing view of the aggregate; every other
  published type of this context keeps the `customerorder` entity);
- dataschema `urn:retailnetwork:customer-ordering:events:ShopperNoticeDecided:v1`;
  topic `retailnetwork.customer-ordering.events`; subject and Kafka key
  `orderId`;
- `data` `{orderId, groupNo, family, notifier, policyVersion, noticeKey,
  decidedAt, fact{...}}`, where `fact` carries only codes already public on
  the CO/DP/ST events: for `DELAY_EXPECTED` the revised `deliverByLatest`, for
  `ETA_WINDOW_ASSIGNED` `etaStart`/`etaEnd`, for `DELIVERY_POINT_ADVISORY` the
  `reason`, for the terminal families the group `status`;
- `noticeKey = sha256(orderId|groupNo|family|factDigest)`: consumers dedupe on
  it. An ETA re-assignment is a new fact (a new key); a replay of the same fact
  is not (the same key), and the emitting side never repeats it: the inbox
  dedupes a redelivered CloudEvent, the read models only move forward (the
  newest ETA window wins), the promise revision only applies when the revision
  number grows, and a group status transition happens once.

Emission is per family occurrence REGARDLESS of the notifier: `notifier` is the
instruction to the marketplace side (MARKETPLACE: the storefront delivers;
SELLER: deferred below; NOBODY: drop the notice — the fact still flows, which
is what the future customer-returns context needs). No optional fields exist;
the absence of an event means no fact occurred, or the fact could not yet be
attributed to a group (an ETA window or a point flag whose assignment link and
tracking-number group set are both still unknown emits nothing).

Triggers, from the handlers that already run (one event per family occurrence,
same transaction as the effect): `ApplyPromiseRevised` → `DELAY_EXPECTED`;
`ApplyPackageEta` → `ETA_WINDOW_ASSIGNED`; `ApplyDeliveryPointFlag` →
`DELIVERY_POINT_ADVISORY`; the `MarkGroupShipped`, `MarkGroupDelivered`,
`MarkGroupLost` and `MarkGroupReturned` transitions (including the settle of a
shipped group and the A19/A21 replays) → the matching `GROUP_*` family, one
event per group the transition reached. The notice path reads the policy and
the read models; it writes no order or group field — the aggregate's snapshot
is byte-identical to the same flow without this ADR.

### Delivery

The notice itself is delivered OUTSIDE the network. The marketplace notifier is
the storefront: it consumes `ShopperNoticeDecided` (it already proxies this
context's tracking body). SELLER-notifier delivery is DEFERRED and stated
plainly: no seller event consumer exists anywhere in the estate, so delivering
a seller notice needs a Vendor API notices poll or a fleet relay — a named
follow-up, not this slice. No context in the network consumes the event today;
the future `customer-notification` context stays in the model as the potential
consumer, and the Wave-4 customer-returns context consumes the terminal
families for its order-group view.

### What is deliberately NOT built

- No `group_notice_ledger` dedupe table: `noticeKey` on the wire plus the
  consumers' inbox is enough.
- No policy event, no analytics topic, no channel, no e-mail/SMS/push sender.
- No change to the order or group aggregate, the promise path or the tracking
  view: the policy only decides the `notifier` stamp of a fact that already
  happened.

## Consequences

- One new published type (catalogued in ADR 0004 and `apis/asyncapi.yaml`) on
  the existing topic; consumers that dispatch on the full type string are
  unaffected until they choose to read it.
- An operator can wake a family up (PUT version 1) without a deploy; the
  default posture is silence.
- `ShopperNoticeDecided` is a decided fact, not a notification: the network
  sends nothing, and the `notifier` field is the contract with whoever does.
- Migration 0012 adds one table; no chart, topic or env-var change.
