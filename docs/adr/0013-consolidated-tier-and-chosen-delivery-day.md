# ADR 0013: the CONSOLIDATED tier and the stored chosen delivery day

## Status

Accepted (2026-11-10). Part of the Wave 2 E6 design (a shopper may choose a
later, consolidated delivery day so the network can merge the order with others).
Builds on [ADR 0002](./0002-aggregates-and-invariants.md) (the GroupPromise
snapshot), [ADR 0004](./0004-cloudevents-envelope-and-type-catalogue.md)
(CustomerOrderPlaced `:v1`, additive evolution only),
[ADR 0009](./0009-delivery-window-time-zone.md) (the window's local time zone) and
[ADR 0012](./0012-consolidation-key-and-shared-tracking-numbers.md) (the
consolidation key). The provider of the tier is delivery-promise (its ADR 0024);
the consumer of the placed tier is order-sourcing (its ADR 0016).

## Context

delivery-promise adds a fifth service tier, `CONSOLIDATED`: an opt-in quote option
whose delivery day is later than the fastest feasible one, leaving the network at
least one local day to merge the order with other orders of the same shopper and
address. It also tells the caller, per option, whether the option actually leaves
that time (`consolidationEligible`, present only for this tier, never `false` by
default). The shopper's choice needs no new checkout field: the choice IS the
`offerId` of the group, and `PlaceOrder` already copies the tier and the window of
the made promise into the group.

What customer-ordering must change is what it does with the value it receives:

- the tier enum is closed at four values in the aggregate (`Tier.Valid`,
  `checkGroup`, `Restore`), in the `order_groups_tier_check` constraint and in the
  OpenAPI `Tier` schema, so a group promised under `CONSOLIDATED` is refused with
  422 and the checkout fails after the promise was made;
- the order must keep WHICH day the shopper chose and whether the network could
  honour a merge, to show it later and to hand it downstream.

## Decision

1. **Accept the tier.** `customerorder.Tier` gains `TierConsolidated`
   (`"CONSOLIDATED"`); `Valid()` accepts five values. Migration
   `0008_group_consolidated_choice` drops and re-adds `order_groups_tier_check`
   with the five tiers (the down migration restores the four and refuses to run
   while a `CONSOLIDATED` group exists, so rolling back never loses a promise).
2. **Store the choice on the group.** `GroupPromise` (and `GroupInput`,
   `newGroup`, `placedGroups`, `Restore`'s clone, the Postgres columns) gains two
   fields:
   - `ChosenDeliveryDay string`: `YYYY-MM-DD`, the LOCAL calendar date of
     `deliverByLatest` in the group's `TimeZone` (ADR 0009). Empty when the tier is
     not `CONSOLIDATED` or the zone is unknown or cannot be resolved. It is the
     day the shopper chose at placement and is NOT recomputed when a later
     `PromiseRevised` moves the window: the revised window is on the group already,
     the day records the choice.
   - `ConsolidationEligible *bool`: delivery-promise's answer, `nil` when the
     tier is not `CONSOLIDATED` or the promise did not say (unknown, never `false`).
   Columns: `order_groups.chosen_delivery_day date NULL` and
   `order_groups.consolidation_eligible boolean NULL`.
3. **Invariants.** `ChosenDeliveryDay` and `ConsolidationEligible` are only
   allowed on a `CONSOLIDATED` group, and the day must be a valid calendar date
   (`ErrValidation` on `Place`, `ErrInvalidSnapshot` on `Restore`).
4. **Terms and derivation.** `promising.Terms` carries `ConsolidationEligible
   *bool`, decoded tolerantly by the delivery-promise gateway (absent or not a
   boolean = unknown). `groupInput()` derives `ChosenDeliveryDay` with the zone
   database; the domain only checks the SHAPE of a zone (ADR 0009) and never
   resolves one, so the derivation lives in the use case. The runtime image ships
   `tzdata`.
5. **REST (additive).** The `GroupPromise` response schema (`POST /checkout` 201,
   `GET /orders/{orderId}`, every item of `GET /orders`) gains optional
   `chosenDeliveryDay` and `consolidationEligible`, omitted when unknown; the
   `Tier` enum gains `CONSOLIDATED`. `CheckoutRequest` is unchanged: the offer id
   carries the choice. The generated REST reference is regenerated in the same
   commit.
6. **CustomerOrderPlaced (additive, same `:v1`).** `groups[].tier` carries the
   REAL tier (`CONSOLIDATED`, never mapped); `groups[].chosenDeliveryDay` and
   `groups[].consolidationEligible` are optional and omitted when unknown. Reading
   rule (stated in the asyncapi): an absent `consolidationEligible` means unknown
   or "not a CONSOLIDATED option", never `false`. order-sourcing must accept the
   tier BEFORE any `CONSOLIDATED` group is placed (activation gate below).
7. **Re-quote keeps the default tiers.** `requote()` asks delivery-promise for the
   default quote set, which does not contain `CONSOLIDATED`. A shopper whose
   `CONSOLIDATED` offer expired re-quotes through the storefront; the 409
   `offer-not-quotable` answer therefore never offers it. This is an accepted
   limitation of this slice.
8. **Out of scope (activation gate Q5).** The storefront callers (cart, checkout,
   pricing, web) validate exactly four tiers and must accept the fifth before
   they ask for it; the incentive amount, the picker copy and the day range shown
   to shoppers belong to pricing and the storefront. The network sends no fee.
   Nothing here asks delivery-promise for `CONSOLIDATED`: it stays invisible until
   a caller opts in.

## Consequences

- A `CONSOLIDATED` promise can be placed, stored, shown and published. Without a
  caller that asks for the tier nothing changes: every existing group stays on
  four tiers, the new columns stay `NULL`.
- Consumers of `CustomerOrderPlaced` that validate the tier strictly must accept
  `CONSOLIDATED` first: order-sourcing's `validatePromise` rejects an unknown
  tier, and the group would never be sourced. Deploy order: order-sourcing
  (ADR 0016) and this slice before any storefront asks delivery-promise for the
  tier.
- Rolling the migration back is only possible while no `CONSOLIDATED` group
  exists; the operator must cancel or let such groups close first.
- The chosen day is a historical fact of the order, like `tier`; the live promise
  window is `deliverByEarliest`/`deliverByLatest` (and `timeZone`).
