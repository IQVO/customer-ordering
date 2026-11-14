Feature: The tracking view shows the facts other contexts own and never moves the order
  Derived from the aggregate canvas (read model OrderTrackingView), ADR 0002 A6, ADR 0003 (consumed contracts),
  ADR 0014 (day-of window), ADR 0015 (delivery-point advisory) and ADR 0016 (pickup collection).

  Background:
    Given a registered shopper
    And delivery-promise holds offer "offer-1" for group 1 and offer "offer-2" for group 2
    And the shopper has placed a cart of 3 lines in 2 groups

  Scenario: Sourcing and promise signals show on the group that holds them while the statuses stay as they are
    When order-sourcing reports group 1 unsourceable with "NO_STOCK"
    And order-sourcing reports group 2 unsourceable with ""
    And delivery-promise reports the promise of group 1 at risk with "CARRIER_DELAY"
    And delivery-promise reports the promise of group 2 at risk with ""
    And delivery-promise reports the promise of group 2 kept
    And delivery-promise reports the unknown promise "p-404" at risk
    Then the tracking view of group 1 shows unsourceableReason "NO_STOCK"
    And the tracking view of group 2 shows unsourceableReason "UNSPECIFIED"
    And the tracking view of group 1 shows atRiskReason "CARRIER_DELAY"
    And the tracking view of group 1 shows no promiseVerdict
    And the tracking view of group 2 shows atRiskReason "UNSPECIFIED"
    And the tracking view of group 2 shows promiseVerdict "KEPT"
    And the tracking view of group 2 shows no lateByMinutes
    And group 1 is "PROMISED"
    And the order is "PLACED"

  Scenario: An event redelivered after a newer one is not applied again over it
    When delivery-promise reports the promise of group 1 at risk with "CARRIER_DELAY" as event "ev-risk-1"
    And delivery-promise reports the promise of group 1 at risk with "WEATHER" as event "ev-risk-2"
    And delivery-promise reports the promise of group 1 at risk with "CARRIER_DELAY" as event "ev-risk-1"
    Then the tracking view of group 1 shows atRiskReason "WEATHER"

  Scenario: A package shows its carrier, its newest scan and its exception without moving the order
    Given order-sourcing locks group 1
    And order-sourcing ships group 1 with packages "TRK-1"
    When shipping-and-tracking issues label "TRK-1" for assignment "as-1" carried by "ACME-EXPRESS"
    And shipping-and-tracking scans package "TRK-1" at node "hub-aus" in state "AT_STATION" at minute 200
    And shipping-and-tracking scans package "TRK-1" at node "hub-old" in state "IN_TRANSIT" at minute 100
    And shipping-and-tracking raises the exception "ADDRESS_NOT_FOUND" on package "TRK-1"
    Then the tracking package "TRK-1" shows carrier "ACME-EXPRESS"
    And the tracking package "TRK-1" shows state "AT_STATION"
    And the tracking package "TRK-1" shows lastScanNodeId "hub-aus"
    And the tracking package "TRK-1" shows exceptionReason "ADDRESS_NOT_FOUND"
    And the tracking package "TRK-1" shows destinationKind "SHIP_TO"
    And the order is "PARTIALLY_SHIPPED"

  Scenario: The day-of window shows only while the package is out for delivery and the window is current
    Given order-sourcing ships group 1 with packages "TRK-1"
    And shipping-and-tracking scans package "TRK-1" at node "station-aus" in state "OUT_FOR_DELIVERY" at minute 100
    When shipping-and-tracking assigns package "TRK-1" the window from minute 300 to minute 420 at minute 110
    Then the tracking package "TRK-1" shows etaStart at minute 300
    And the tracking package "TRK-1" shows etaEnd at minute 420
    When shipping-and-tracking scans package "TRK-1" at node "station-aus" in state "AT_STATION" at minute 200
    Then the tracking package "TRK-1" shows no etaStart
    When shipping-and-tracking assigns package "TRK-1" the window from minute 500 to minute 620 at minute 210
    Then the tracking package "TRK-1" shows no etaStart
    When shipping-and-tracking scans package "TRK-1" at node "station-aus" in state "OUT_FOR_DELIVERY" at minute 220
    Then the tracking package "TRK-1" shows no etaStart
    When shipping-and-tracking assigns package "TRK-1" the window from minute 640 to minute 760 at minute 230
    Then the tracking package "TRK-1" shows etaStart at minute 640
    When shipping-and-tracking delivers package "TRK-1"
    Then the tracking package "TRK-1" shows state "DELIVERED"
    And the tracking package "TRK-1" shows no etaStart

  Scenario: The newest window wins, is shown as received and leaves the promise alone
    Given order-sourcing ships group 1 with packages "TRK-1"
    And shipping-and-tracking scans package "TRK-1" at node "station-aus" in state "OUT_FOR_DELIVERY" at minute 100
    When shipping-and-tracking assigns package "TRK-1" the window from minute 1900 to minute 2000 at minute 120
    And shipping-and-tracking assigns package "TRK-1" the window from minute 100 to minute 200 at minute 110
    Then the tracking package "TRK-1" shows etaStart at minute 1900
    And the tracking package "TRK-1" shows etaEnd at minute 2000
    And the tracking view of group 1 shows deliverByLatest "2026-10-24T16:00:00Z"
    And group 1 is at revision 0

  Scenario: The delivery-point advisory is shown while the package is on its way and ends with the package
    Given order-sourcing ships group 1 with packages "TRK-1"
    When shipping-and-tracking flags the delivery point of package "TRK-1" as "NOT_HOME" at minute 100
    And shipping-and-tracking flags the delivery point of package "TRK-1" as "ACCESS_PROBLEM" at minute 90
    Then the tracking package "TRK-1" shows deliveryPointAdvisory.reason "NOT_HOME"
    And the tracking package "TRK-1" shows deliveryPointAdvisory.flaggedAt at minute 100
    And the tracking view of group 1 shows deliverByLatest "2026-10-24T16:00:00Z"
    And the order is "PARTIALLY_SHIPPED"
    When shipping-and-tracking delivers package "TRK-1"
    Then the tracking package "TRK-1" shows no deliveryPointAdvisory

  Scenario: A pickup package shows the point, then the deposit, then the collection, and never moves the order
    Given order-sourcing ships group 1 with packages "TRK-1"
    And shipping-and-tracking issues label "TRK-1" for assignment "as-1" to pickup point "pp-77"
    Then the tracking package "TRK-1" shows destinationKind "PICKUP_POINT"
    And the tracking package "TRK-1" shows pickupPointId "pp-77"
    And the tracking package "TRK-1" shows no pickupPoint
    When shipping-and-tracking reports package "TRK-1" ready for collection at point "pp-77" at minute 300 held until minute 4620
    Then the tracking package "TRK-1" shows state "READY_FOR_COLLECTION"
    And the tracking package "TRK-1" shows pickupPoint.readyForCollectionAt at minute 300
    And the tracking package "TRK-1" shows pickupPoint.holdUntil at minute 4620
    And the tracking package "TRK-1" shows no pickupPoint.collectedAt
    When shipping-and-tracking reports package "TRK-1" collected with proof "QR-4411" at minute 500
    Then the tracking package "TRK-1" shows state "COLLECTED"
    And the tracking package "TRK-1" shows pickupPoint.collectedAt at minute 500
    And the tracking package "TRK-1" shows pickupPoint.proofCode "QR-4411"
    And the order is "PARTIALLY_SHIPPED"
