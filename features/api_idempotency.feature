Feature: A replayed request with the same Idempotency-Key is answered from the record
  The first answer of a key (the key is global) is stored when it is final for
  the request: a success, or a refusal whose problem type is FINAL or OUTCOME
  (ADR 0019). The same key with the same method, path and body then replays it
  byte for byte (marked Idempotency-Replayed) and nothing runs again — no second
  promise, no second event. The same key with another request is refused as a
  reuse (409). A refusal that depends on state that can change (RETRYABLE: a
  concurrent update, an order not known yet) and a 5xx are not stored: the key is
  released and a retry runs the work again.

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
    Then the API answers 409 with problem "idempotency-key-reused"

  Scenario: The same key on another route is refused as a reuse
    When the API checks out the cart of 3 lines in 2 groups with Idempotency-Key "key-3"
    Then the API answers 201
    When the API posts to "/shoppers" with Idempotency-Key "key-3" the body:
      """
      {"email": "reuse@example.com"}
      """
    Then the API answers 409 with problem "idempotency-key-reused"

  Scenario: A malformed Idempotency-Key is refused
    When the API checks out the cart of 3 lines in 2 groups with Idempotency-Key "chave-é"
    Then the API answers 400 with problem "idempotency-key-invalid"

  Scenario: A replayed cancellation publishes no second event
    Given the shopper has placed a cart of 3 lines in 2 groups
    When the API posts to "/orders/order-1/cancellations" with Idempotency-Key "cancel-1" the body:
      """
      {"reason": "changed my mind"}
      """
    Then the API answers 200
    And 1 "CustomerOrderCancelled" events were published
    When the API posts to "/orders/order-1/cancellations" with Idempotency-Key "cancel-1" the body:
      """
      {"reason": "changed my mind"}
      """
    Then the API answers 200
    And the response is marked replayed
    And 1 "CustomerOrderCancelled" events were published

  Scenario: A refusal that depends on state that can change is not stored, so the key retries
    When the API posts to "/orders/order-key-4/cancellations" with Idempotency-Key "cancel-4" the body:
      """
      {"reason": "changed my mind"}
      """
    Then the API answers 404 with problem "not-found"
    When the API checks out the cart of 3 lines in 2 groups with Idempotency-Key "key-4"
    Then the API answers 201
    When the API posts to "/orders/order-key-4/cancellations" with Idempotency-Key "cancel-4" the body:
      """
      {"reason": "changed my mind"}
      """
    Then the API answers 200
    And the response is not marked replayed
    And 1 "CustomerOrderCancelled" events were published

  Scenario: A final refusal is stored and replayed
    When the API posts to "/shoppers" with Idempotency-Key "reg-1" the body:
      """
      {"email": "twice@example.com"}
      """
    Then the API answers 201
    When the API posts to "/shoppers" with Idempotency-Key "reg-2" the body:
      """
      {"email": "twice@example.com"}
      """
    Then the API answers 409 with problem "shopper-exists"
    And the response is not marked replayed
    When the API posts to "/shoppers" with Idempotency-Key "reg-2" the body:
      """
      {"email": "twice@example.com"}
      """
    Then the API answers 409 with problem "shopper-exists"
    And the response is marked replayed
