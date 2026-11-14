Feature: Checkout rules of the documentation: limits, destinations, delivery-promise failures and the checkout choices
  Derived from the business context, use-cases (PlaceOrder), the aggregate canvas (invariants, policies
  CheckoutPromising, ConsolidationKeyDerivation, ConsolidatedChoiceStored) and ADR 0002 (A9-A15, A25), 0009, 0012,
  0013 and 0016. Complements checkout.feature, which pins the happy path and the first refusals.

  Background:
    Given a registered shopper
    And delivery-promise holds offer "offer-1" for group 1 and offer "offer-2" for group 2

  # --- every line in exactly one group, nothing reaches delivery-promise before the order is sound ---

  Scenario: A line named by two groups is refused before delivery-promise is called
    When the shopper checks out a cart whose line 2 is in both group 1 and group 2
    Then the checkout is refused with "line-without-promise"
    And delivery-promise was not called
    And no order exists

  Scenario: A checkout by a shopper that never registered is refused before delivery-promise is called (A9)
    When the API posts to "/checkout" with Idempotency-Key "ghost" the body:
      """
      {"shopperId":"shopper-9",
       "shipTo":{"name":"Ana Example","line1":"1 Sample Street","city":"Austin","region":"TX","postalCode":"78701","countryCode":"US"},
       "lines":[{"lineNo":1,"catalogItemId":"sku-100","quantity":2}],
       "groups":[{"groupNo":1,"offerId":"offer-1","lineNos":[1]}]}
      """
    Then the API answers 422 with problem "validation-failed"
    And delivery-promise was not called
    And 0 "CustomerOrderPlaced" events were published

  Scenario: A saved address of another shopper cannot be used at checkout (A25)
    Given a shopper registered with "bob@example.com"
    And the shopper "shopper-2" saves an address with Idempotency-Key "bobs-home"
    When the API posts to "/checkout" with Idempotency-Key "not-mine" the body:
      """
      {"shopperId":"shopper-1","shipToAddressId":"address-shopper-2:bobs-home",
       "lines":[{"lineNo":1,"catalogItemId":"sku-100","quantity":2}],
       "groups":[{"groupNo":1,"offerId":"offer-1","lineNos":[1]}]}
      """
    Then the API answers 422 with problem "validation-failed"
    And delivery-promise was not called
    And 0 "CustomerOrderPlaced" events were published

  # --- the destination rule: the promise serves the ship-to's normalised postal prefix (A10, A11) ---

  Scenario Outline: The postal code and the promise's destination decide the checkout
    Given delivery-promise promises every group for destination "<destination>"
    When the shopper checks out a cart of 3 lines in 2 groups to postal code "<postal>"
    Then the checkout ends as "<outcome>" with destination prefix "<prefix>"

    Examples:
      | postal                | destination | outcome                      | prefix |
      | 78701                 | 787         | PLACED                       | 787    |
      | 78701                 | 78701       | PLACED                       | 787    |
      | 78701                 | 78          | PLACED                       | 787    |
      | 787                   | 787         | PLACED                       | 787    |
      | 787-01                | 787         | PLACED                       | 787    |
      | k1a 0b1               | K1A         | PLACED                       | K1A    |
      | ABCDEFGHIJKLMNOPQRST  | ABC         | PLACED                       | ABC    |
      | 78701                 | 788         | promise-destination-mismatch |        |
      | 78701                 | 787011      | promise-destination-mismatch |        |
      | 78701                 |             | promise-destination-mismatch |        |
      | 78                    | 787         | validation-failed            |        |
      | 7-8                   | 787         | validation-failed            |        |
      | --                    | 787         | validation-failed            |        |
      | ABCDEFGHIJKLMNOPQRSTU | ABC         | validation-failed            |        |

  # --- documented caps at the boundary (A15) ---

  Scenario Outline: An order holds up to 100 lines in up to 50 groups, one more of either is refused
    When a generated cart of <lines> lines in <groups> groups is checked out
    Then the checkout ends as "<outcome>"
    And delivery-promise made <promises> promises

    Examples:
      | lines | groups | outcome           | promises |
      | 100   | 1      | PLACED            | 1        |
      | 50    | 50     | PLACED            | 50       |
      | 100   | 50     | PLACED            | 50       |
      | 101   | 1      | validation-failed | 0        |
      | 51    | 51     | validation-failed | 0        |

  Scenario Outline: A quantity runs from 1 to the Postgres integer ceiling
    When the shopper checks out one line of quantity <quantity>
    Then the checkout ends as "<outcome>"

    Examples:
      | quantity   | outcome           |
      | 1          | PLACED            |
      | 2147483647 | PLACED            |
      | 0          | validation-failed |
      | 2147483648 | validation-failed |

  Scenario Outline: A ship-to field is accepted at its cap and refused one character over
    When the shopper saves an address whose "<field>" has <length> characters
    Then the API answers <status>

    Examples:
      | field      | length | status |
      | name       | 100    | 201    |
      | name       | 101    | 422    |
      | line1      | 100    | 201    |
      | line1      | 101    | 422    |
      | city       | 100    | 201    |
      | city       | 101    | 422    |
      | region     | 100    | 201    |
      | region     | 101    | 422    |
      | postalCode | 20     | 201    |
      | postalCode | 21     | 422    |
      | phone      | 40     | 201    |
      | phone      | 41     | 422    |

  # --- delivery-promise failures: documented problem, nothing placed, nothing left confirmed (A12, A14) ---

  Scenario Outline: A refusal of delivery-promise is answered with its documented problem and places nothing
    Given delivery-promise refuses offer "offer-2" with "<failure>"
    When the API checks out the cart of 3 lines in 2 groups with Idempotency-Key "refused"
    Then the API answers <status> with problem "<slug>"
    And delivery-promise confirmed 0 promises
    And 0 "CustomerOrderPlaced" events were published

    Examples:
      | failure            | status | slug                         |
      | capacity-exhausted | 409    | promise-capacity-exhausted   |
      | book-closed        | 409    | book-closed                  |
      | group-spans-nodes  | 422    | group-spans-nodes            |
      | unavailable        | 503    | delivery-promise-unavailable |

  Scenario Outline: A failed confirm cancels what was confirmed, and an unknown outcome cancels its own group too
    Given delivery-promise refuses to confirm the promise of group 2 with "<failure>"
    When the API checks out the cart of 3 lines in 2 groups with Idempotency-Key "confirm-<failure>"
    Then the API answers <status> with problem "<slug>"
    And delivery-promise cancelled <cancelled> promises
    And delivery-promise cancelled the promise of group 1
    And 0 "CustomerOrderPlaced" events were published

    Examples:
      | failure            | status | slug                         | cancelled |
      | capacity-exhausted | 409    | promise-capacity-exhausted   | 1         |
      | unavailable        | 503    | delivery-promise-unavailable | 2         |

  Scenario: A retry with the same Idempotency-Key after an outage places the order on the same promises
    Given delivery-promise refuses offer "offer-2" with "unavailable"
    When the API checks out the cart of 3 lines in 2 groups with Idempotency-Key "retry-1"
    Then the API answers 503 with problem "delivery-promise-unavailable"
    And no order exists
    Given delivery-promise recovers
    When the API checks out the same cart again with the same key
    Then the API answers 201
    And the API response field "orderId" is "order-retry-1"
    And every promise request named order "order-retry-1"
    And 1 "CustomerOrderPlaced" events were published

  Scenario Outline: A cancel of a confirmed promise that fails is queued for the worker, one that is refused is not
    Given delivery-promise cannot confirm the promise of group 2
    And delivery-promise refuses to cancel the promise of group 1 with "<failure>"
    When the shopper checks out a cart of 3 lines in 2 groups to postal code "78701"
    Then the checkout is refused with "offer-not-quotable"
    And delivery-promise cancelled the promise of group 1
    And the compensation queue holds <queued> cancels

    Examples:
      | failure         | queued |
      | unavailable     | 1      |
      | not-cancellable | 0      |

  # --- CheckoutPromising: a re-quote asks for every group at the ship-to's destination ---

  Scenario: An expired offer re-quotes every group's lines at the ship-to prefix and shows the fresh offers
    Given the offer "offer-2" has expired
    When the API checks out the cart of 3 lines in 2 groups with Idempotency-Key "expired"
    Then the API answers 409 with problem "offer-not-quotable"
    And the API response path "requotedOffers.0.groupNo" is "1"
    And the API response path "requotedOffers.0.offerId" is "fresh-1"
    And the API response path "requotedOffers.1.groupNo" is "2"
    And delivery-promise was asked to quote these destinations:
      | prefix | pickupPoint | lines               |
      | 787    |             | sku-100x2,sku-200x1 |
      | 787    |             | sku-300x5           |
    And 0 "CustomerOrderPlaced" events were published

  # --- pickup point checkout (ADR 0016) ---

  Scenario: A checkout to a pickup point stores no address and the ship-to route answers the point
    Given delivery-promise answers offer "offer-1" with the pickup point "pp-77"
    When the API posts to "/checkout" with Idempotency-Key "pickup-1" the body:
      """
      {"shopperId":"shopper-1","pickupPointId":"pp-77",
       "lines":[{"lineNo":1,"catalogItemId":"sku-100","quantity":2},{"lineNo":2,"catalogItemId":"sku-200","quantity":1},{"lineNo":3,"catalogItemId":"sku-300","quantity":5}],
       "groups":[{"groupNo":1,"offerId":"offer-1","lineNos":[1,2]},{"groupNo":2,"offerId":"offer-2","lineNos":[3]}]}
      """
    Then the API answers 201
    And the placed event of order "order-pickup-1" names pickup point "pp-77" and destination prefix "787"
    When the API requests "/orders/order-pickup-1/ship-to"
    Then the API answers 200
    And the API response field "destinationKind" is "PICKUP_POINT"
    And the API response field "pickupPointId" is "pp-77"
    And the API response has no path "line1"
    And the API response has no path "postalCode"

  Scenario Outline: Exactly one of shipTo, shipToAddressId and pickupPointId is required
    When the API posts to "/checkout" with Idempotency-Key "<key>" the body:
      """
      {"shopperId":"shopper-1"<destination>,
       "lines":[{"lineNo":1,"catalogItemId":"sku-100","quantity":2}],
       "groups":[{"groupNo":1,"offerId":"offer-1","lineNos":[1]}]}
      """
    Then the API answers 422 with problem "validation-failed"
    And the API problem detail names "shipToAddressId"
    And delivery-promise was not called

    Examples:
      | key          | destination                                                                                                                                                  |
      | point-and-to | ,"pickupPointId":"pp-77","shipTo":{"name":"Ana Example","line1":"1 Sample Street","city":"Austin","region":"TX","postalCode":"78701","countryCode":"US"} |
      | point-and-id | ,"pickupPointId":"pp-77","shipToAddressId":"address-shopper-1:home"                                                                                          |
      | no-place     |                                                                                                                                                              |

  Scenario Outline: A pickup order refuses a promise for another point, another destination or none
    Given <promise>
    When the shopper "shopper-1" places order "order-pickup" for pickup point "pp-77"
    Then the checkout is refused
    And delivery-promise confirmed 0 promises
    And no order exists

    Examples:
      | promise                                                       |
      | delivery-promise answers offer "offer-1" with the pickup point "pp-99" |
      | delivery-promise promises offer "offer-2" for destination "100"        |
      | delivery-promise promises offer "offer-1" for destination ""           |

  Scenario: A ship-to order refuses a promise that names a pickup point
    Given delivery-promise answers offer "offer-1" with the pickup point "pp-77"
    When the shopper checks out a cart of 3 lines in 2 groups to postal code "78701"
    Then the checkout is refused with "promise-destination-mismatch"
    And no order exists

  Scenario: An expired offer of a pickup checkout re-quotes to the point, not to a postal prefix
    Given the offer "offer-2" has expired
    When the shopper "shopper-1" places order "order-pickup" for pickup point "pp-77"
    Then the checkout is refused with "offer-not-quotable"
    And delivery-promise was asked to quote these destinations:
      | prefix | pickupPoint | lines               |
      |        | pp-77       | sku-100x2,sku-200x1 |
      |        | pp-77       | sku-300x5           |

  # --- ConsolidationKeyDerivation (ADR 0012): one opaque key per shopper and place ---

  Scenario: The key follows the shopper and the place, however the address is written
    Given the consolidation key secret is configured
    And a shopper registered with "bob@example.com"
    When the shopper "shopper-1" places order "order-a" shipping to:
      | name        | line1       | city   | region | postalCode | countryCode | phone       |
      | Ana Example | 12 Main St. | Austin | TX     | 78701      | US          | +1 555 0100 |
    And the shopper "shopper-1" places order "order-b" shipping to:
      | name         | line1      | city   | region | postalCode | countryCode | phone |
      | Ana's Mother | 12 main st | AUSTIN | tx     | 78701      | US          |       |
    And the shopper "shopper-1" places order "order-c" shipping to:
      | name        | line1       | city   | region | postalCode | countryCode | phone |
      | Ana Example | 99 Other Rd | Austin | TX     | 78701      | US          |       |
    And the shopper "shopper-2" places order "order-d" shipping to:
      | name        | line1       | city   | region | postalCode | countryCode | phone |
      | Ana Example | 12 Main St. | Austin | TX     | 78701      | US          |       |
    Then orders "order-a" and "order-b" carry the same consolidation keys
    And orders "order-a" and "order-c" carry different consolidation keys
    And orders "order-a" and "order-d" carry different consolidation keys
    And the placed event of order "order-a" carries a 64-character lower-case hex consolidation key

  Scenario: A pickup order never shares a key with an address order, and two orders to one point do
    Given the consolidation key secret is configured
    When the shopper "shopper-1" places order "order-a" shipping to:
      | name        | line1       | city   | region | postalCode | countryCode | phone |
      | Ana Example | 12 Main St. | Austin | TX     | 78701      | US          |       |
    And the shopper "shopper-1" places order "order-p1" for pickup point "pp-77"
    And the shopper "shopper-1" places order "order-p2" for pickup point "pp-77"
    And the shopper "shopper-1" places order "order-p3" for pickup point "pp-88"
    Then orders "order-a" and "order-p1" carry different consolidation keys
    And orders "order-p1" and "order-p2" carry the same consolidation keys
    And orders "order-p1" and "order-p3" carry different consolidation keys

  Scenario: Without a secret no key is derived and the order never merges
    When the shopper "shopper-1" places order "order-a" shipping to:
      | name        | line1       | city   | region | postalCode | countryCode | phone |
      | Ana Example | 12 Main St. | Austin | TX     | 78701      | US          |       |
    Then the placed event of order "order-a" carries no consolidation key

  # --- ConsolidatedChoiceStored (ADR 0013): the tier, the chosen local day and the eligibility ---

  Scenario: A CONSOLIDATED group keeps its tier, its local delivery day and its eligibility
    Given delivery-promise promises offer "offer-1" as CONSOLIDATED in zone "Pacific/Auckland" delivering by "2026-10-24T16:00:00Z" and eligible
    When the API checks out the cart of 3 lines in 2 groups with Idempotency-Key "consolidated"
    Then the API answers 201
    And the API response path "groups.0.tier" is "CONSOLIDATED"
    And the API response path "groups.0.chosenDeliveryDay" is "2026-10-25"
    And the API response path "groups.0.consolidationEligible" is "true"
    And the API response has no path "groups.1.chosenDeliveryDay"
    And the placed event of group 1 carries tier "CONSOLIDATED" and chosen day "2026-10-25"

  Scenario: An eligibility the promise did not state stays unknown, an explicit no stays no
    Given delivery-promise promises offer "offer-1" as CONSOLIDATED in zone "America/Sao_Paulo" delivering by "2026-10-24T16:00:00Z"
    And delivery-promise promises offer "offer-2" as CONSOLIDATED in zone "America/Sao_Paulo" delivering by "2026-10-24T16:00:00Z" and not eligible
    When the API checks out the cart of 3 lines in 2 groups with Idempotency-Key "unknown-eligibility"
    Then the API answers 201
    And the API response path "groups.0.chosenDeliveryDay" is "2026-10-24"
    And the API response has no path "groups.0.consolidationEligible"
    And the API response path "groups.1.consolidationEligible" is "false"

  Scenario: A day and an eligibility on any other tier are dropped without failing the checkout
    Given delivery-promise promises offer "offer-1" as NEXT_DAY in zone "America/Chicago" delivering by "2026-10-24T16:00:00Z" and eligible
    When the API checks out the cart of 3 lines in 2 groups with Idempotency-Key "other-tier"
    Then the API answers 201
    And the API response path "groups.0.tier" is "NEXT_DAY"
    And the API response has no path "groups.0.chosenDeliveryDay"
    And the API response has no path "groups.0.consolidationEligible"

  Scenario: A revised window moves the window but keeps the zone and the day the shopper chose
    Given delivery-promise promises offer "offer-1" as CONSOLIDATED in zone "Pacific/Auckland" delivering by "2026-10-24T16:00:00Z" and eligible
    When the API checks out the cart of 3 lines in 2 groups with Idempotency-Key "revised-choice"
    And delivery-promise revises the promise of group 1 to revision 1 delivering 24 hours later
    Then group 1 is at revision 1 and delivers by "2026-10-25T16:00:00Z"
    And group 1 keeps the time zone "Pacific/Auckland"
    When the API requests "/orders/order-revised-choice"
    Then the API response path "groups.0.chosenDeliveryDay" is "2026-10-25"
    And the API response path "groups.0.timeZone" is "Pacific/Auckland"
