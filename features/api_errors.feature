Feature: The REST API answers RFC 9457 problems for client mistakes
  Every client error is application/problem+json whose type is
  /problems/<slug> of the openapi error table. The main validation error of
  each route is covered: a malformed body, a missing Idempotency-Key, an
  unknown order and a conflict. (Slug names follow .claude/rules/rest-api.md,
  which is the operative table; see the PR body for the openapi drift.)

  Background:
    Given a registered shopper
    And delivery-promise holds offer "offer-1" for group 1 and offer "offer-2" for group 2

  Scenario: A checkout body that is not JSON is a malformed request
    When the API posts to "/checkout" with Idempotency-Key "bad-json" the body:
      """
      {"shopperId": "shopper-1",
      """
    Then the API answers 400 with problem "malformed-request"

  Scenario: An unknown field in a checkout body is a malformed request
    When the API posts to "/checkout" with Idempotency-Key "unknown-field" the body:
      """
      {"shopperId": "shopper-1", "giftWrap": true}
      """
    Then the API answers 400 with problem "malformed-request"

  Scenario: A checkout without an Idempotency-Key is refused before anything runs
    When the API posts to "/checkout" without an Idempotency-Key and body:
      """
      {"shopperId": "shopper-1"}
      """
    Then the API answers 400 with problem "idempotency-key-required"
    And delivery-promise was not called

  Scenario: A NUL escape in a body is a malformed request
    When the API posts to "/checkout" with Idempotency-Key "nul-body" the body:
      """
      {"shopperId": "shopper-1\u0000"}
      """
    Then the API answers 400 with problem "malformed-request"

  Scenario: A NUL in the path can never name an order
    When the API requests "/orders/order%00x"
    Then the API answers 404 with problem "resource-not-found"

  Scenario: An empty cancellation reason fails validation
    Given the shopper has placed a cart of 3 lines in 2 groups
    When the API posts to "/orders/order-1/cancellations" with Idempotency-Key "empty-reason" the body:
      """
      {"reason": ""}
      """
    Then the API answers 422 with problem "validation-failed"

  Scenario: Cancelling an unknown order is not-found
    When the API posts to "/orders/no-such-order/cancellations" with Idempotency-Key "no-such" the body:
      """
      {"reason": "changed my mind"}
      """
    Then the API answers 404 with problem "not-found"

  Scenario: A duplicate shopper registration is a conflict
    When the API posts to "/shoppers" with Idempotency-Key "dup" the body:
      """
      {"email": "ana@example.com"}
      """
    Then the API answers 409 with problem "shopper-exists"
