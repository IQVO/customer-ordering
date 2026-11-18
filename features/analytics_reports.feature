Feature: The ordering performance report (analytics read side, ADR 0018)
  The projector reads this context's OWN published events and nothing else; the report answers per UTC day from them:
  orders placed and cancelled, consolidated-tier orders, pickup-point orders, split shipments and the shopper notices
  decided by kind. Ids, codes and counts only: no name, address, postal prefix or free text.

  Background:
    Given a registered shopper
    And delivery-promise holds offer "offer-1" for group 1 and offer "offer-2" for group 2

  Scenario: A day without any event is an empty report with unknown freshness
    When the analytics projector has read the published events
    And the ordering performance report is requested from "2026-10-20" to "2026-10-24"
    Then the report answers 200 for "2026-10-20" to "2026-10-24"
    And the report has no days
    And the report freshness is unknown

  Scenario: An order of two shipment groups is a split shipment
    When the shopper "shopper-1" places order "order-a" shipping to:
      | name        | line1           | city   | region | postalCode | countryCode | phone       |
      | Ana Example | 1 Sample Street | Austin | TX     | 78701      | US          | +1 555 0100 |
    And the analytics projector has read the published events
    And the ordering performance report is requested from "2026-10-23" to "2026-10-23"
    Then the report has 1 day
    And the report day "2026-10-23" shows:
      | orders_placed            | 1   |
      | orders_cancelled         | 0   |
      | split_shipment_orders    | 1   |
      | split_shipment_share     | 1   |
      | consolidated_tier_orders | 0   |
      | pickup_point_orders      | 0   |
      | shopper_notices_decided  | 0   |
    And the report freshness is 93600 seconds behind
    And the report body names no shopper, address, postal prefix or free text

  Scenario: A single-group order is not a split shipment and the share is the fraction of the day's orders
    When the shopper "shopper-1" places order "order-a" in 2 groups
    And the shopper "shopper-1" places order "order-b" in 1 group
    And the shopper "shopper-1" places order "order-c" in 1 group
    And the shopper "shopper-1" places order "order-d" in 1 group
    And the analytics projector has read the published events
    And the ordering performance report is requested from "2026-10-23" to "2026-10-23"
    Then the report day "2026-10-23" shows:
      | orders_placed         | 4    |
      | split_shipment_orders | 1    |
      | split_shipment_share  | 0.25 |

  Scenario: An order with a CONSOLIDATED group is a consolidated-tier order
    Given delivery-promise promises offer "offer-1" as CONSOLIDATED in zone "America/Chicago" delivering by "2026-10-26T16:00:00Z" and eligible
    When the shopper "shopper-1" places order "order-a" in 2 groups
    And the analytics projector has read the published events
    And the ordering performance report is requested from "2026-10-23" to "2026-10-23"
    Then the report day "2026-10-23" shows:
      | orders_placed            | 1 |
      | consolidated_tier_orders | 1 |

  Scenario: A checkout to a pickup point counts without naming the point
    Given delivery-promise answers offer "offer-1" with the pickup point "pp-77"
    When the shopper "shopper-1" places order "order-p" for pickup point "pp-77"
    And the analytics projector has read the published events
    And the ordering performance report is requested from "2026-10-23" to "2026-10-23"
    Then the report day "2026-10-23" shows:
      | orders_placed       | 1 |
      | pickup_point_orders | 1 |
    And the report body names no shopper, address, postal prefix or free text

  Scenario: A cancellation request is counted on its own day, without the reason
    Given the shopper has placed a cart of 3 lines in 2 groups
    When the shopper cancels with "changed my mind"
    And the analytics projector has read the published events
    And the ordering performance report is requested from "2026-10-23" to "2026-10-23"
    Then the report day "2026-10-23" shows:
      | orders_placed    | 1 |
      | orders_cancelled | 1 |
    And the report body names no shopper, address, postal prefix or free text

  Scenario: A day with a cancellation and no placed order has no share, not a zero
    Given the shopper has placed a cart of 3 lines in 2 groups
    And the clock moves to "2026-10-24T09:00:00Z"
    When the shopper cancels with "changed my mind"
    And the analytics projector has read the published events
    And the ordering performance report is requested from "2026-10-23" to "2026-10-24"
    Then the report has 2 days
    And the report day "2026-10-23" shows:
      | orders_placed        | 1    |
      | orders_cancelled     | 0    |
      | split_shipment_share | 1    |
    And the report day "2026-10-24" shows:
      | orders_placed        | 0    |
      | orders_cancelled     | 1    |
      | split_shipment_share | null |

  Scenario: Notices are counted whatever the notifier, by kind
    Given the shopper has placed a cart of 3 lines in 2 groups
    And delivery-promise revises the promise of group 1 one day later
    And order-sourcing locks group 1
    And order-sourcing locks group 2
    And order-sourcing ships group 1 with packages "pkg-1"
    And order-sourcing ships group 2 with packages "pkg-2"
    And shipping-and-tracking delivers package "pkg-1"
    And shipping-and-tracking declares package "pkg-2" lost
    When the analytics projector has read the published events
    And the ordering performance report is requested from "2026-10-23" to "2026-10-23"
    Then the report day "2026-10-23" lists the notice kinds "GROUP_SHIPPED:2,DELAY_EXPECTED:1,GROUP_DELIVERED:1,GROUP_LOST:1" and 0 others
    And the report day "2026-10-23" shows:
      | shopper_notices_decided | 5 |

  Scenario: At most five kinds are listed and the rest is one remainder
    Given shopper notices are decided on "2026-10-23T12:00:00Z" with the counts:
      | family                  | count |
      | GROUP_SHIPPED           | 7     |
      | GROUP_DELIVERED         | 6     |
      | DELAY_EXPECTED          | 5     |
      | GROUP_LOST              | 4     |
      | GROUP_RETURNED          | 3     |
      | ETA_WINDOW_ASSIGNED     | 2     |
      | DELIVERY_POINT_ADVISORY | 1     |
    When the analytics projector has read the published events
    And the ordering performance report is requested from "2026-10-23" to "2026-10-23"
    Then the report day "2026-10-23" lists the notice kinds "GROUP_SHIPPED:7,GROUP_DELIVERED:6,DELAY_EXPECTED:5,GROUP_LOST:4,GROUP_RETURNED:3" and 3 others
    And the report day "2026-10-23" shows:
      | shopper_notices_decided | 28 |

  Scenario: Reading the same events twice changes nothing
    Given the shopper has placed a cart of 3 lines in 2 groups
    When the analytics projector has read the published events
    And the analytics projector reads every published event again
    And the ordering performance report is requested from "2026-10-23" to "2026-10-23"
    Then the report day "2026-10-23" shows:
      | orders_placed | 1 |

  Scenario: A range outside the retained events is empty even when other days have facts
    Given the shopper has placed a cart of 3 lines in 2 groups
    When the analytics projector has read the published events
    And the ordering performance report is requested from "2026-10-01" to "2026-10-22"
    Then the report has no days

  Scenario: A malformed range is a problem, not a report
    When the ordering performance report is requested from "2026-10-24" to "2026-10-23"
    Then the report answers 400 with problem "invalid-report-range"
