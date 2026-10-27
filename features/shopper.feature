Feature: Shoppers register with a unique e-mail and save addresses
  A saved address is a ShipTo under an addressId (openapi addSavedAddress,
  getShopper, findShopperByEmail); the id derives from the add's
  Idempotency-Key, so a replay keeps one address. A shopper holds at most 20.

  Scenario: A shopper registers
    When a shopper registers with "Ana@Example.com"
    Then the shopper is ACTIVE with e-mail "ana@example.com"

  Scenario: A second registration with the same e-mail is refused
    Given a shopper registered with "ana@example.com"
    When a shopper registers with "ANA@example.com"
    Then the registration is refused with "shopper-exists"

  Scenario: A saved address appears on the profile
    Given a registered shopper
    When the shopper saves an address with Idempotency-Key "home"
    Then the API answers 201
    And the saved address has an addressId
    And the shopper profile lists 1 saved address

  Scenario: Saving again under the same key keeps one address
    Given a registered shopper
    When the shopper saves an address with Idempotency-Key "home"
    And the shopper saves an address with Idempotency-Key "home"
    Then the shopper profile lists 1 saved address

  Scenario: The twenty-first address is refused
    Given a registered shopper
    And the shopper saved 20 addresses
    When the shopper saves an address with Idempotency-Key "one-too-many"
    Then the API answers 422 with problem "too-many-addresses"

  Scenario: An unknown shopper cannot save an address
    When the shopper "shopper-nope" saves an address with Idempotency-Key "k"
    Then the API answers 404 with problem "not-found"

  Scenario: A shopper is found by e-mail, trimmed and lower-cased
    Given a shopper registered with "ana@example.com"
    When the API looks up the shopper with e-mail "  ANA@Example.COM "
    Then the API answers 200
    And the API response field "shopperId" is "shopper-1"

  Scenario: An unknown e-mail is not-found
    When the API looks up the shopper with e-mail "bob@example.com"
    Then the API answers 404 with problem "not-found"
