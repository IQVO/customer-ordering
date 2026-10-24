import type { Order, ShipTo, Tracking } from "../types";

export function order(over: Partial<Order> = {}): Order {
  return {
    orderId: "ord-1",
    shopperId: "shopper-1",
    status: "IN_FULFILLMENT",
    destinationPostalPrefix: "787",
    placedAt: "2026-10-24T10:00:00Z",
    lines: [
      { lineNo: 1, catalogItemId: "sku-100", quantity: 2 },
      { lineNo: 2, catalogItemId: "sku-200", quantity: 1 },
    ],
    groups: [
      {
        groupNo: 1, promiseId: "p-1", tier: "NEXT_DAY",
        shipBy: "2026-10-24T10:10:00Z", deliverByEarliest: "2026-10-24T10:20:00Z", deliverByLatest: "2026-10-24T10:25:00Z",
        lineNos: [1], status: "SHIPPED", revisionNo: 3,
      },
      {
        groupNo: 2, promiseId: "p-2", tier: "STANDARD",
        shipBy: "2026-10-25T10:10:00Z", deliverByEarliest: "2026-10-26T10:20:00Z", deliverByLatest: "2026-10-26T20:00:00Z",
        lineNos: [2], status: "PROMISED", revisionNo: 1,
      },
    ],
    ...over,
  };
}

export function tracking(over: Partial<Tracking> = {}): Tracking {
  return {
    orderId: "ord-1",
    status: "PARTIALLY_SHIPPED",
    groups: [
      {
        groupNo: 1, promiseId: "p-1", status: "SHIPPED",
        deliverByEarliest: "2026-10-24T10:20:00Z", deliverByLatest: "2026-10-24T10:25:00Z",
        promiseVerdict: "AT_RISK", lateByMinutes: 12, shippedAt: "2026-10-24T10:11:00Z",
        packages: [
          {
            trackingNumber: "TRK-100", carrier: "CARRIER-1", state: "IN_TRANSIT",
            lastScanNodeId: "fc-aus-1", lastScanAt: "2026-10-24T10:13:20Z",
          },
        ],
      },
      {
        groupNo: 2, promiseId: "p-2", status: "PROMISED",
        deliverByEarliest: "2026-10-26T10:20:00Z", deliverByLatest: "2026-10-26T20:00:00Z",
        atRiskReason: "no capacity at the node",
        packages: [],
      },
    ],
    ...over,
  };
}

export const SHIP_TO: ShipTo = {
  name: "Ana Example",
  line1: "1 Sample Street",
  line2: "Apt 2",
  city: "Austin",
  region: "TX",
  postalCode: "78701",
  countryCode: "US",
  phone: "+1 555 0100",
};
