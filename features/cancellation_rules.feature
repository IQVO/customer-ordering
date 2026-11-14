Feature: Cancellation rules of the documentation: requests, races, the policy and the limits
  Derived from use-cases (CancelOrder shopper and CancelOnUnfulfillable), the aggregate canvas (state transitions,
  invariants, the Race note) and ADR 0002 A3-A5, A7, A23. Complements cancellation.feature.

  Background:
    Given a registered shopper
    And delivery-promise holds offer "offer-1" for group 1 and offer "offer-2" for group 2
    And the shopper has placed a cart of 3 lines in 2 groups

  Scenario: Once every request was refused there is nothing left to cancel
    Given the shopper cancels with "changed my mind"
    And order-sourcing refuses the cancellation of group 1 with "VENDOR_ACKNOWLEDGED"
    And order-sourcing refuses the cancellation of group 2 with "VENDOR_ACKNOWLEDGED"
    When the shopper cancels with "second try"
    Then the cancellation is refused with "cancel-after-lock"
    And group 1 is "LOCKED"
    And group 2 is "LOCKED"
    And the event "CustomerOrderCancelled" was published once for groups 1,2

  Scenario Outline: A cancel of an order whose every group has shipped is refused: a regression when it is closed, after the lock otherwise
    Given order-sourcing ships group 1 with packages "TRK-1"
    And order-sourcing ships group 2 with packages "TRK-2"
    And <first>
    And <second>
    When the shopper cancels with "too late"
    Then the order is "<status>"
    And the cancellation is refused with "<slug>"

    Examples:
      | first                                                | second                                               | status              | slug              |
      | shipping-and-tracking delivers package "TRK-1"       | shipping-and-tracking delivers package "TRK-2"       | DELIVERED           | status-regression |
      | shipping-and-tracking declares package "TRK-1" lost  | shipping-and-tracking declares package "TRK-2" lost  | LOST                | status-regression |
      | shipping-and-tracking returns package "TRK-1" to the sender | shipping-and-tracking returns package "TRK-2" to the sender | RETURNED | status-regression |
      | shipping-and-tracking delivers package "TRK-1"       | shipping-and-tracking declares package "TRK-2" lost  | PARTIALLY_DELIVERED | cancel-after-lock |
      | delivery-promise reports the promise of group 1 kept | delivery-promise reports the promise of group 2 kept | SHIPPED             | cancel-after-lock |

  Scenario: Cancelling an order that is already CANCELLED changes and publishes nothing
    Given the shopper cancels with "changed my mind"
    And order-sourcing accepts the cancellation of group 1
    And order-sourcing accepts the cancellation of group 2
    When the shopper cancels with "again"
    Then the cancellation is accepted
    And the order is "CANCELLED"
    And the event "CustomerOrderCancelled" was published once for groups 1,2

  Scenario: A lock that races the cancel request leaves the request standing until order-sourcing decides
    Given the shopper cancels with "changed my mind"
    When order-sourcing locks group 1
    Then group 1 is "CANCELLATION_REQUESTED"
    And the order is "PLACED"
    When order-sourcing refuses the cancellation of group 1 with "VENDOR_ACKNOWLEDGED"
    Then group 1 is "LOCKED"
    And the order is "IN_FULFILLMENT"

  Scenario: A vendor that ships wins over a cancel request, a late acceptance is skipped and the other group still cancels
    Given the shopper cancels with "changed my mind"
    When order-sourcing ships group 1 with packages "TRK-1"
    Then group 1 is "SHIPPED"
    And group 2 is "CANCELLATION_REQUESTED"
    And the order is "PARTIALLY_SHIPPED"
    When order-sourcing accepts the cancellation of group 2
    Then group 2 is "CANCELLED"
    And the order is "SHIPPED"
    When order-sourcing accepts the cancellation of group 1
    Then group 1 is "SHIPPED"

  Scenario: A cancelled group no longer counts, and a locked group can still be cancelled by order-sourcing
    Given order-sourcing locks group 1
    And the shopper cancels with "changed my mind"
    When order-sourcing accepts the cancellation of group 2
    Then group 2 is "CANCELLED"
    And the order is "IN_FULFILLMENT"
    When order-sourcing accepts the cancellation of group 1
    Then group 1 is "CANCELLED"
    And the order is "CANCELLED"

  Scenario: A refusal for a group nobody asked to cancel, or one already cancelled, is skipped
    When order-sourcing refuses the cancellation of group 1 with "VENDOR_ACKNOWLEDGED"
    Then group 1 is "PROMISED"
    And the group 1 refusal reason is ""
    When the shopper cancels with "changed my mind"
    And order-sourcing accepts the cancellation of group 1
    And order-sourcing refuses the cancellation of group 1 with "VENDOR_ACKNOWLEDGED"
    Then group 1 is "CANCELLED"
    And the group 1 refusal reason is ""

  # --- CancelOnUnfulfillable (A5) ---

  Scenario: An unkeepable promise cancels its group with delivery-promise's reason, and the last one cancels the order
    When delivery-promise declares the promise of group 2 unkeepable with "NO_FEASIBLE_PATH"
    Then group 2 is "CANCELLED"
    And the order is "PLACED"
    And the "CustomerOrderCancelled" event number 1 carries the reason "PROMISE_UNKEEPABLE: NO_FEASIBLE_PATH" for groups 2
    When delivery-promise declares the promise of group 1 unkeepable with "NO_STOCK"
    Then the order is "CANCELLED"
    And the "CustomerOrderCancelled" event number 2 carries the reason "PROMISE_UNKEEPABLE: NO_STOCK" for groups 1

  Scenario: An unkeepable promise of a group that is locked or already being cancelled is skipped
    Given order-sourcing locks group 1
    And the shopper cancels with "changed my mind"
    When delivery-promise declares the promise of group 1 unkeepable with "NO_STOCK"
    And delivery-promise declares the promise of group 2 unkeepable with "NO_FEASIBLE_PATH"
    Then group 1 is "LOCKED"
    And group 2 is "CANCELLATION_REQUESTED"
    And 1 "CustomerOrderCancelled" events were published

  # --- REST: the answer and the reason limit (A15) ---

  Scenario: The cancel answer lists the groups under request with the reason
    When the API posts to "/orders/order-1/cancellations" with Idempotency-Key "c-1" the body:
      """
      {"reason": "changed my mind"}
      """
    Then the API answers 200
    And the API response field "status" is "PLACED"
    And the API response path "groups.0.status" is "CANCELLATION_REQUESTED"
    And the API response path "groups.0.cancelReason" is "changed my mind"
    And the API response path "groups.1.status" is "CANCELLATION_REQUESTED"

  Scenario Outline: A cancel reason is accepted up to 200 characters and refused above
    When the API cancels order "order-1" with a reason of <length> characters
    Then the API answers <status>

    Examples:
      | length | status |
      | 200    | 200    |
      | 201    | 422    |
