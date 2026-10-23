Feature: The order reads of the REST API never leak the ship-to address
  GET /orders/{orderId} and its tracking return the order without the address;
  the restricted ship-to route is the only one that returns it (openapi
  getOrder, getOrderTracking, getShipTo). An id that names no order is
  not-found on every route.

  Background:
    Given a registered shopper
    And delivery-promise holds offer "offer-1" for group 1 and offer "offer-2" for group 2
    And the shopper has placed a cart of 3 lines in 2 groups

  Scenario: GET /orders/{orderId} returns the order without the address
    When the API requests "/orders/order-1"
    Then the API answers 200
    And the API response field "orderId" is "order-1"
    And the API response field "status" is "PLACED"
    And the API response contains no address

  Scenario: GET /orders/{orderId}/tracking returns the groups without the address
    When the API requests "/orders/order-1/tracking"
    Then the API answers 200
    And the API response field "status" is "PLACED"
    And the API response contains no address

  Scenario: GET /orders/{orderId}/ship-to returns the address for label printing
    When the API requests "/orders/order-1/ship-to"
    Then the API answers 200
    And the API response field "name" is "Ana Example"
    And the API response field "postalCode" is "78701"

  Scenario: An unknown order is not-found on every read route
    When the API requests "/orders/no-such-order"
    Then the API answers 404 with problem "not-found"
    When the API requests "/orders/no-such-order/tracking"
    Then the API answers 404 with problem "not-found"
    When the API requests "/orders/no-such-order/ship-to"
    Then the API answers 404 with problem "not-found"

  Scenario: The liveness probe answers ok without checking any dependency
    When the API requests "/healthz"
    Then the API answers 200
    And the API response field "status" is "ok"
