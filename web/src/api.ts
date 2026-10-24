import { ORDERING_API_BASE } from "./config";
import type { Order, ShipTo, Tracking } from "./types";

/** RFC 9457 problem+json body every error response from customer-ordering
 *  returns (`type` = /problems/<slug>, for example not-found). */
export interface ProblemDetails {
  type?: string;
  title?: string;
  status?: number;
  detail?: string;
  instance?: string;
}

/**
 * Every failed call -- an HTTP error with a problem+json body, an HTTP error
 * without one, or a network failure -- surfaces as one ApiError, so a screen
 * has a single shape to render: `title` (the problem's category) and `detail`
 * (what exactly was wrong), plus `slug` to branch on a specific problem
 * (e.g. `not-found`).
 */
export class ApiError extends Error {
  readonly status: number;
  readonly problem: ProblemDetails | null;
  readonly title: string;
  readonly detail: string;
  /** Last path segment of problem.type (e.g. "not-found"); "" when unknown. */
  readonly slug: string;

  constructor(status: number, problem: ProblemDetails | null, fallbackTitle: string) {
    const title = problem?.title || fallbackTitle;
    const detail = problem?.detail ?? "";
    super(detail || title);
    this.name = "ApiError";
    this.status = status;
    this.problem = problem;
    this.title = title;
    this.detail = detail;
    this.slug = problem?.type ? (problem.type.split("/").pop() ?? "") : "";
  }
}

/** Normalizes anything thrown by a request into an ApiError. */
export function toApiError(err: unknown): ApiError {
  if (err instanceof ApiError) return err;
  const message = err instanceof Error ? err.message : String(err);
  return new ApiError(0, { detail: message }, "Network error");
}

const seg = encodeURIComponent;

/**
 * The one door to the service. Only this context's own API is ever called
 * (`ORDERING_API_BASE` = <apiOrigin>/api/customer-ordering): no sibling
 * context of the retail network, no warehouse fleet service. This remote is
 * read-only: every call is a GET with no custom header, so they stay simple
 * CORS requests; placing, cancelling and registering a shopper are API
 * operations, not screen operations.
 */
async function request<T>(path: string): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`${ORDERING_API_BASE}${path}`, { method: "GET" });
  } catch (err) {
    throw toApiError(err);
  }
  if (!res.ok) {
    let problem: ProblemDetails | null = null;
    try {
      problem = (await res.json()) as ProblemDetails;
    } catch {
      // non-JSON error body -- fall through with problem = null
    }
    throw new ApiError(res.status, problem, `${res.status} ${res.statusText}`.trim());
  }
  return (await res.json()) as T;
}

/** GET /orders/{orderId}: the order with its lines and promise-carrying groups. */
export async function getOrder(orderId: string): Promise<Order> {
  const o = await request<Order>(`/orders/${seg(orderId)}`);
  return { ...o, lines: o.lines ?? [], groups: o.groups ?? [] };
}

/** GET /orders/{orderId}/tracking: the tracking view per group and package. */
export async function getOrderTracking(orderId: string): Promise<Tracking> {
  const t = await request<Tracking>(`/orders/${seg(orderId)}/tracking`);
  return {
    ...t,
    groups: (t.groups ?? []).map((g) => ({ ...g, packages: g.packages ?? [] })),
  };
}

/** GET /orders/{orderId}/ship-to: the synthetic delivery address (restricted read). */
export async function getShipTo(orderId: string): Promise<ShipTo> {
  return request<ShipTo>(`/orders/${seg(orderId)}/ship-to`);
}
