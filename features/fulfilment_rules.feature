Feature: Fulfilment rules of the documentation: consumed facts, the status table and the terminal groups
  Derived from use-cases (MarkInFulfillment, MarkGroupShipped, MarkGroupDelivered, UpdateGroupPromise), the aggregate
  canvas (state transitions, corrective policy Idempotent consumption, SharedTrackingNumbers) and ADR 0002 A1, A7, A8,
  A19, A21, A23 and ADR 0012. Complements fulfilment_status.feature.

  Background:
    Given a registered shopper
    And delivery-promise holds offer "offer-1" for group 1 and offer "offer-2" for group 2
    And the shopper has placed a cart of 3 lines in 2 groups

  # --- Idempotent consumption: a fact that cannot apply is recorded and skipped, never failed (A7) ---

  Scenario Outline: A consumed fact that names an order, a group or a promise this service does not hold is acknowledged and skipped
    When <fact>
    Then the order is "PLACED"
    And every group is "PROMISED"
    And the inbox recorded 1 event

    Examples:
      | fact                                                                |
      | a "FulfillmentLocked" fact arrives for the unknown order "order-ghost"        |
      | a "FulfillmentShipped" fact arrives for the unknown order "order-ghost"       |
      | a "FulfillmentCancelled" fact arrives for the unknown order "order-ghost"     |
      | a "FulfillmentCancelRefused" fact arrives for the unknown order "order-ghost" |
      | a "PackageDelivered" fact arrives for the unknown order "order-ghost"         |
      | a "PackageLost" fact arrives for the unknown order "order-ghost"              |
      | a "PackageReturned" fact arrives for the unknown order "order-ghost"          |
      | a "PromiseRevised" fact arrives for the unknown order "order-ghost"           |
      | a "PromiseUnkeepable" fact arrives for the unknown order "order-ghost"        |
      | a "PromiseAtRisk" fact arrives for the unknown order "order-ghost"            |
      | order-sourcing locks the unknown group 9                                      |
      | delivery-promise revises the unknown promise "p-404"                          |

  Scenario: A redelivered event is applied once and recorded once
    When order-sourcing delivers the lock of group 1 as event "ev-lock-1" twice
    Then group 1 is "LOCKED"
    And the order is "IN_FULFILLMENT"
    And the order was saved 2 times
    And the inbox recorded 1 event

  # --- the status table: derived from the groups that are not CANCELLED (A1, A23) ---

  Scenario: A delivered group beside an open group makes the order PARTIALLY_DELIVERED, never DELIVERED
    Given order-sourcing ships group 1 with packages "TRK-1"
    When shipping-and-tracking delivers package "TRK-1"
    Then group 1 is "DELIVERED"
    And group 2 is "PROMISED"
    And the order is "PARTIALLY_DELIVERED"
    When order-sourcing ships group 2 with packages "TRK-2"
    And shipping-and-tracking delivers package "TRK-2"
    Then the order is "DELIVERED"

  Scenario: A delivered group and a lost group close the order as PARTIALLY_DELIVERED
    Given order-sourcing ships group 1 with packages "TRK-1"
    And order-sourcing ships group 2 with packages "TRK-2"
    When shipping-and-tracking delivers package "TRK-1"
    And shipping-and-tracking declares package "TRK-2" lost
    Then group 1 is "DELIVERED"
    And group 2 is "LOST"
    And the order is "PARTIALLY_DELIVERED"

  Scenario: A lost group beside a locked group is shipped progress, and beside a shipped group the order is SHIPPED
    Given order-sourcing ships group 1 with packages "TRK-1"
    And shipping-and-tracking declares package "TRK-1" lost
    When order-sourcing locks group 2
    Then the order is "PARTIALLY_SHIPPED"
    When order-sourcing ships group 2 with packages "TRK-2"
    Then the order is "SHIPPED"

  Scenario Outline: A cancelled group does not count towards the status of the others
    Given the shopper cancels with "changed my mind"
    And order-sourcing ships group 1 with packages "TRK-1"
    And order-sourcing accepts the cancellation of group 2
    When <outcome>
    Then group 2 is "CANCELLED"
    And the order is "<status>"

    Examples:
      | outcome                                                     | status    |
      | shipping-and-tracking delivers package "TRK-1"              | DELIVERED |
      | shipping-and-tracking declares package "TRK-1" lost         | LOST      |
      | shipping-and-tracking returns package "TRK-1" to the sender | RETURNED  |

  # --- groups only move forward: late facts for a closed or cancelled group are skipped (A8, A19) ---

  Scenario Outline: A closed group is terminal, a late lock, shipment or revision is skipped
    Given order-sourcing ships group 1 with packages "TRK-1"
    And <close>
    When order-sourcing locks group 1 again
    And order-sourcing ships group 1 with packages "TRK-7"
    And delivery-promise revises the promise of group 1 one day later
    Then group 1 is "<state>"
    And group 1 is at revision 0

    Examples:
      | close                                                       | state     |
      | shipping-and-tracking delivers package "TRK-1"              | DELIVERED |
      | shipping-and-tracking declares package "TRK-1" lost         | LOST      |
      | shipping-and-tracking returns package "TRK-1" to the sender | RETURNED  |

  Scenario: A cancelled group takes no lock, shipment, delivery or revision
    Given the shopper cancels with "changed my mind"
    And order-sourcing accepts the cancellation of group 1
    When order-sourcing locks group 1
    And order-sourcing ships group 1 with packages "TRK-1"
    And shipping-and-tracking delivers package "TRK-1"
    And delivery-promise revises the promise of group 1 one day later
    Then group 1 is "CANCELLED"
    And group 1 is at revision 0
    And the order is "PLACED"

  Scenario: A package already reported delivered cannot be declared lost, another package of the group can
    Given order-sourcing ships group 1 with packages "TRK-1,TRK-1b"
    And shipping-and-tracking delivers package "TRK-1"
    When shipping-and-tracking declares package "TRK-1" lost
    Then group 1 is "SHIPPED"
    When shipping-and-tracking declares package "TRK-1b" lost
    Then group 1 is "LOST"

  Scenario: A package reported delivered twice is counted once
    Given order-sourcing ships group 1 with packages "TRK-1,TRK-1b"
    When shipping-and-tracking delivers package "TRK-1"
    And shipping-and-tracking delivers package "TRK-1"
    Then group 1 is "SHIPPED"
    When shipping-and-tracking delivers package "TRK-1b"
    Then group 1 is "DELIVERED"

  Scenario: A loss and a delivery of one package that both arrive before its shipment settle on the delivery
    When shipping-and-tracking delivers package "TRK-1"
    And shipping-and-tracking declares package "TRK-1" lost
    And order-sourcing ships group 1 with packages "TRK-1"
    Then group 1 is "DELIVERED"

  Scenario: One group that cannot take a shared package does not stop the other groups that can
    Given the shopper cancels with "changed my mind"
    And order-sourcing accepts the cancellation of group 1
    And the shopper places order "order-2" at minute 5
    And order-sourcing ships group 1 of order "order-1" with packages "TRK-S"
    And order-sourcing ships group 1 of order "order-2" with packages "TRK-S"
    When shipping-and-tracking delivers package "TRK-S" for order "order-2"
    Then group 1 of order "order-1" is "CANCELLED"
    And group 1 of order "order-2" is "DELIVERED"

  # --- UpdateGroupPromise: only a newer revision applies and it never moves the status (A8) ---

  Scenario: A revision newer than the stored one applies, an older or equal one is ignored
    When delivery-promise revises the promise of group 1 to revision 2 delivering 48 hours later
    And delivery-promise revises the promise of group 1 to revision 1 delivering 24 hours later
    And delivery-promise revises the promise of group 1 to revision 2 delivering 72 hours later
    Then group 1 is at revision 2 and delivers by "2026-10-26T16:00:00Z"
    When delivery-promise revises the promise of group 1 to revision 3 delivering 96 hours later
    Then group 1 is at revision 3 and delivers by "2026-10-28T16:00:00Z"
    And the order is "PLACED"

  Scenario: A revision with an inverted window is rejected so it is dead-lettered, not recorded
    When delivery-promise sends revision 1 of group 1 with an inverted window
    Then the consumed event is rejected with "validation-failed"
    And group 1 is at revision 0
    And the inbox recorded 0 events

  Scenario: A revision without a ship-by keeps the group's own, one with a ship-by replaces it
    When delivery-promise revises the promise of group 1 to revision 1 delivering 24 hours later without a ship-by
    Then group 1 ships by "2026-10-23T20:00:00Z"
    When delivery-promise revises the promise of group 1 to revision 2 delivering 48 hours later
    Then group 1 ships by "2026-10-25T20:00:00Z"

  # --- SharedTrackingNumbers: one package belongs to the groups of every order that named it (ADR 0012) ---

  Scenario: One delivery of a shared package closes the group of every order that named it
    Given the shopper places order "order-2" at minute 5
    And order-sourcing ships group 1 of order "order-1" with packages "TRK-S"
    And order-sourcing ships group 1 of order "order-2" with packages "TRK-S"
    When shipping-and-tracking delivers package "TRK-S" for order "order-1"
    Then group 1 of order "order-1" is "DELIVERED"
    And group 1 of order "order-2" is "DELIVERED"

  Scenario: A follower group that ships after the shared package was delivered is closed by its own shipment
    Given the shopper places order "order-2" at minute 5
    And order-sourcing ships group 1 of order "order-1" with packages "TRK-S"
    When shipping-and-tracking delivers package "TRK-S" for order "order-1"
    Then group 1 of order "order-1" is "DELIVERED"
    And group 1 of order "order-2" is "PROMISED"
    When order-sourcing ships group 1 of order "order-2" with packages "TRK-S"
    Then group 1 of order "order-2" is "DELIVERED"

  Scenario Outline: One lost or returned shared package closes every group that shares it
    Given the shopper places order "order-2" at minute 5
    And order-sourcing ships group 1 of order "order-1" with packages "TRK-S"
    And order-sourcing ships group 1 of order "order-2" with packages "TRK-S"
    When <outcome>
    Then group 1 of order "order-1" is "<state>"
    And group 1 of order "order-2" is "<state>"

    Examples:
      | outcome                                                                         | state    |
      | shipping-and-tracking declares package "TRK-S" lost for order "order-1"         | LOST     |
      | shipping-and-tracking returns package "TRK-S" to the sender for order "order-1" | RETURNED |
