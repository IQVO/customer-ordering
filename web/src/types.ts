/** Wire types of apis/openapi.yaml (camelCase, as the REST contract names them). */

/** The nine order states (internal/domain/customerorder). */
export const ORDER_STATUSES = [
  "PLACED", "IN_FULFILLMENT", "PARTIALLY_SHIPPED", "SHIPPED",
  "PARTIALLY_DELIVERED", "DELIVERED", "LOST", "RETURNED", "CANCELLED",
] as const;
export type OrderStatus = (typeof ORDER_STATUSES)[number];

/** The eight shipment-group states. */
export const GROUP_STATUSES = [
  "PROMISED", "LOCKED", "SHIPPED", "DELIVERED",
  "LOST", "RETURNED", "CANCELLATION_REQUESTED", "CANCELLED",
] as const;
export type GroupStatus = (typeof GROUP_STATUSES)[number];

/** One cart line of the order. */
export interface OrderLine {
  lineNo: number;
  catalogItemId: string;
  quantity: number;
}

/** One shipment group with its confirmed promise (GET /orders/{orderId}). */
export interface OrderGroup {
  groupNo: number;
  promiseId: string;
  tier: string;
  shipBy: string;
  deliverByEarliest: string;
  deliverByLatest: string;
  lineNos: number[];
  status: GroupStatus;
  revisionNo?: number;
  cancelReason?: string;
  cancelRequestedAt?: string;
  cancelRefusedReason?: string;
}

/** GET /orders/{orderId} -- the order without its ship-to address. */
export interface Order {
  orderId: string;
  shopperId: string;
  status: OrderStatus;
  destinationPostalPrefix: string;
  placedAt: string;
  cancelledAt?: string;
  lines: OrderLine[];
  groups: OrderGroup[];
}

/** One package of a group (GET /orders/{orderId}/tracking). */
export interface TrackingPackage {
  trackingNumber: string;
  carrier?: string;
  state?: string;
  lastScanNodeId?: string;
  lastScanAt?: string;
  deliveredAt?: string;
  exceptionReason?: string;
}

/** The tracking of one shipment group. */
export interface TrackingGroup {
  groupNo: number;
  promiseId: string;
  status: GroupStatus;
  deliverByEarliest: string;
  deliverByLatest: string;
  atRiskReason?: string;
  unsourceableReason?: string;
  promiseVerdict?: string;
  lateByMinutes?: number;
  lockedAt?: string;
  shippedAt?: string;
  cancelRefusedReason?: string;
  lostAt?: string;
  returnedAt?: string;
  outcomePackage?: string;
  packages: TrackingPackage[];
}

/** GET /orders/{orderId}/tracking -- the OrderTrackingView. */
export interface Tracking {
  orderId: string;
  status: OrderStatus;
  groups: TrackingGroup[];
}

/** GET /orders/{orderId}/ship-to -- the synthetic delivery address. */
export interface ShipTo {
  name: string;
  line1: string;
  line2?: string;
  city: string;
  region: string;
  postalCode: string;
  countryCode: string;
  phone?: string;
}
