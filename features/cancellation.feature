Feature: Cancellation is a request that order-sourcing decides
  A customer may cancel the groups that are not locked yet; order-sourcing accepts or refuses (Gap Register G12, G25).

  Background:
    Given a registered shopper
    And delivery-promise holds offer "offer-1" for group 1 and offer "offer-2" for group 2
    And the shopper has placed a cart of 3 lines in 2 groups

  Scenario: Cancelling while nothing is locked requests every group and publishes the request
    When the shopper cancels with "changed my mind"
    Then group 1 is "CANCELLATION_REQUESTED"
    And group 2 is "CANCELLATION_REQUESTED"
    And the order is "PLACED"
    And the event "CustomerOrderCancelled" was published once for groups 1,2

  Scenario: The order is cancelled once order-sourcing accepted every group
    Given the shopper cancels with "changed my mind"
    When order-sourcing accepts the cancellation of group 1
    And order-sourcing accepts the cancellation of group 2
    Then the order is "CANCELLED"

  Scenario: Cancelling applies to the unlocked groups only
    Given order-sourcing locks group 1
    When the shopper cancels with "changed my mind"
    Then group 1 is "LOCKED"
    And group 2 is "CANCELLATION_REQUESTED"
    And the event "CustomerOrderCancelled" was published once for groups 2

  Scenario: Cancelling when every group is locked is refused
    Given order-sourcing locks group 1
    And order-sourcing locks group 2
    When the shopper cancels with "too late"
    Then the cancellation is refused with "cancel-after-lock"

  Scenario: A refusal after the vendor accepted sends the group on its way
    Given the shopper cancels with "changed my mind"
    When order-sourcing refuses the cancellation of group 1 with "VENDOR_ACKNOWLEDGED"
    Then group 1 is "LOCKED"
    And the group 1 refusal reason is "VENDOR_ACKNOWLEDGED"

  Scenario: A repeated cancellation changes and publishes nothing
    Given the shopper cancels with "changed my mind"
    When the shopper cancels with "again"
    Then the event "CustomerOrderCancelled" was published once for groups 1,2

  Scenario: An unkeepable promise cancels its group
    When delivery-promise declares the promise of group 2 unkeepable
    Then group 2 is "CANCELLED"
    And the event "CustomerOrderCancelled" was published once for groups 2
