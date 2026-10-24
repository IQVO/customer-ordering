Feature: A replayed request with the same Idempotency-Key is answered from the record
  The first response below 500 of a (route, key) is stored; the same key with
  the same body replays it byte for byte (marked Idempotent-Replayed) and
  nothing runs again — no second promise, no second event. The same key with
  another body is refused as a reuse.

  Background:
    Given a registered shopper
    And delivery-promise holds offer "offer-1" for group 1 and offer "offer-2" for group 2

  Scenario: A replayed checkout returns the stored answer and publishes one event
    When the API checks out the cart of 3 lines in 2 groups with Idempotency-Key "key-1"
    Then the API answers 201
    And the API response field "orderId" is "order-key-1"
    And 1 "CustomerOrderPlaced" events were published
    When the API checks out the same cart again with the same key
    Then the API answers 201
    And the response is marked replayed
    And the API response field "orderId" is "order-key-1"
    And 1 "CustomerOrderPlaced" events were published
    And delivery-promise made 2 promises

  Scenario: The same key with another body is refused as a reuse
    When the API checks out the cart of 3 lines in 2 groups with Idempotency-Key "key-2"
    Then the API answers 201
    When the API posts to "/checkout" with Idempotency-Key "key-2" the body:
      """
      {"shopperId": "shopper-1"}
      """
    Then the API answers 422 with problem "idempotency-key-reused"

  Scenario: A replayed cancellation publishes no second event
    Given the shopper has placed a cart of 3 lines in 2 groups
    When the API posts to "/orders/order-1:cancel" with Idempotency-Key "cancel-1" the body:
      """
      {"reason": "changed my mind"}
      """
    Then the API answers 200
    And 1 "CustomerOrderCancelled" events were published
    When the API posts to "/orders/order-1:cancel" with Idempotency-Key "cancel-1" the body:
      """
      {"reason": "changed my mind"}
      """
    Then the API answers 200
    And the response is marked replayed
    And 1 "CustomerOrderCancelled" events were published
