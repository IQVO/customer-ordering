Feature: Checkout turns a cart into an order with a confirmed promise per shipment group
  The order is placed only when every line belongs to exactly one promise that delivery-promise made and confirmed,
  and the promise destination matches the ship-to (ErrLineWithoutPromise, ErrPromiseDestinationMismatch).

  Background:
    Given a registered shopper
    And delivery-promise holds offer "offer-1" for group 1 and offer "offer-2" for group 2

  Scenario: A cart with two groups is placed and announced without the address
    When the shopper checks out a cart of 3 lines in 2 groups to postal code "78701"
    Then the order is "PLACED"
    And every group is "PROMISED"
    And delivery-promise confirmed 2 promises
    And the event "CustomerOrderPlaced" was published once with destination prefix "787"
    And no published event contains the address

  Scenario: A line that is in no group is refused before delivery-promise is called
    When the shopper checks out a cart whose line 3 is in no group
    Then the checkout is refused with "line-without-promise"
    And delivery-promise was not called
    And no order exists

  Scenario: A promise for another destination is refused and nothing is confirmed
    Given delivery-promise answers group 2 with destination "100"
    When the shopper checks out a cart of 3 lines in 2 groups to postal code "78701"
    Then the checkout is refused with "promise-destination-mismatch"
    And delivery-promise confirmed 0 promises
    And no order exists

  Scenario: An expired offer makes checkout re-quote and places nothing
    Given the offer "offer-2" has expired
    When the shopper checks out a cart of 3 lines in 2 groups to postal code "78701"
    Then the checkout is refused with "offer-not-quotable"
    And fresh offers are shown for groups 1 and 2
    And no order exists

  Scenario: A failed confirm makes checkout re-quote and places nothing
    Given delivery-promise cannot confirm the promise of group 2
    When the shopper checks out a cart of 3 lines in 2 groups to postal code "78701"
    Then the checkout is refused with "offer-not-quotable"
    And no order exists

  Scenario: A failed confirm cancels the promises already confirmed (A14)
    Given delivery-promise cannot confirm the promise of group 2
    When the shopper checks out a cart of 3 lines in 2 groups to postal code "78701"
    Then the checkout is refused with "offer-not-quotable"
    And delivery-promise cancelled 1 promises
    And delivery-promise cancelled the promise of group 1
    And no order exists

  Scenario: A cancel that fails does not change the answer of the checkout
    Given delivery-promise cannot confirm the promise of group 2
    And delivery-promise cannot cancel the promise of group 1
    When the shopper checks out a cart of 3 lines in 2 groups to postal code "78701"
    Then the checkout is refused with "offer-not-quotable"
    And delivery-promise cancelled the promise of group 1
    And no order exists

  Scenario: Repeating a checkout with the same Idempotency-Key returns the same order
    When the shopper checks out a cart of 3 lines in 2 groups to postal code "78701"
    And the same checkout is repeated
    Then the order is "PLACED"
    And delivery-promise made 2 promises
    And the event "CustomerOrderPlaced" was published once with destination prefix "787"
