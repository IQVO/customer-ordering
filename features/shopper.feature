Feature: Shoppers register with a unique e-mail

  Scenario: A shopper registers
    When a shopper registers with "Ana@Example.com"
    Then the shopper is ACTIVE with e-mail "ana@example.com"

  Scenario: A second registration with the same e-mail is refused
    Given a shopper registered with "ana@example.com"
    When a shopper registers with "ANA@example.com"
    Then the registration is refused with "shopper-exists"
