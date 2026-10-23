Feature: The order status follows the groups and only moves forward
  Status is derived from the group states; a late event that would move a status backwards is recorded and skipped (ErrStatusRegression).

  Background:
    Given a registered shopper
    And delivery-promise holds offer "offer-1" for group 1 and offer "offer-2" for group 2
    And the shopper has placed a cart of 3 lines in 2 groups

  Scenario: The status follows locks, shipments and deliveries
    When order-sourcing locks group 1
    Then the order is "IN_FULFILLMENT"
    When order-sourcing locks group 2
    And order-sourcing ships group 1 with packages "TRK-1,TRK-1b"
    Then the order is "PARTIALLY_SHIPPED"
    When order-sourcing ships group 2 with packages "TRK-2"
    Then the order is "SHIPPED"
    When shipping-and-tracking delivers package "TRK-2"
    Then the order is "PARTIALLY_DELIVERED"
    When shipping-and-tracking delivers package "TRK-1"
    Then group 1 is "SHIPPED"
    When shipping-and-tracking delivers package "TRK-1b"
    Then the order is "DELIVERED"

  Scenario: A delivery reported before the shipment settles the group on shipping
    Given order-sourcing locks group 2
    When shipping-and-tracking delivers package "TRK-2"
    And order-sourcing ships group 2 with packages "TRK-2"
    Then group 2 is "DELIVERED"

  Scenario: A late lock after shipment is skipped
    Given order-sourcing locks group 1
    And order-sourcing ships group 1 with packages "TRK-1"
    When order-sourcing locks group 1 again
    Then group 1 is "SHIPPED"

  Scenario: A revised promise moves the window but never the status
    When delivery-promise revises the promise of group 1 one day later
    Then group 1 has revision 1 and delivers one day later
    And the order is "PLACED"

  Scenario: Tracking shows the verdict of the promise
    Given order-sourcing locks group 1
    And order-sourcing ships group 1 with packages "TRK-1"
    When delivery-promise reports the promise of group 1 broken by 660 minutes
    Then the tracking of group 1 shows the verdict "BROKEN" late by 660 minutes
