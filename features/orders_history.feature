Feature: A shopper's order history is listed newest first, without the address
  The page is ordered placedAt descending with the orderId as the ascending
  tiebreak; a short page carries a nextCursor that walks to the older orders,
  like the catalog list (openapi listOrdersByShopper).

  Background:
    Given a registered shopper
    And delivery-promise holds offer "offer-1" for group 1 and offer "offer-2" for group 2

  Scenario: Orders are listed newest first and never carry the address
    When the shopper places order "order-old" at minute 0
    And the shopper places order "order-new" at minute 10
    And the API lists the orders of shopper "shopper-1"
    Then the API answers 200
    And the history lists the orders newest first:
      """
      order-new
      order-old
      """
    And the API response contains no address
    And the history page has no next cursor

  Scenario: A short page carries a cursor that walks to the older orders
    When the shopper places order "order-1" at minute 0
    And the shopper places order "order-2" at minute 10
    And the shopper places order "order-3" at minute 20
    And the API lists the orders of shopper "shopper-1" with limit 2
    Then the API answers 200
    And the history lists the orders newest first:
      """
      order-3
      order-2
      """
    And the history page has a next cursor
    When the API follows the history cursor
    Then the API answers 200
    And the history lists the orders newest first:
      """
      order-1
      """
    And the history page has no next cursor

  Scenario: Orders at the same instant tiebreak on the orderId
    When the shopper places order "order-b" at minute 0
    And the shopper places order "order-a" at minute 0
    And the API lists the orders of shopper "shopper-1"
    Then the history lists the orders newest first:
      """
      order-a
      order-b
      """

  Scenario: Only the shopper's own orders are listed
    Given a shopper registered with "bob@example.com"
    And the shopper places order "order-1" at minute 0
    When the shopper "shopper-2" places order "order-2" at minute 10
    And the API lists the orders of shopper "shopper-1"
    Then the history lists the orders newest first:
      """
      order-1
      """

  Scenario: An unknown shopper is not-found
    And the API lists the orders of shopper "shopper-nope"
    Then the API answers 404 with problem "not-found"

  Scenario: A cursor that is not one of ours is validation-failed
    And the API requests "/orders?shopperId=shopper-1&after=not-a-cursor"
    Then the API answers 422 with problem "validation-failed"

  Scenario: A limit over one hundred is validation-failed
    And the API requests "/orders?shopperId=shopper-1&limit=101"
    Then the API answers 422 with problem "validation-failed"
