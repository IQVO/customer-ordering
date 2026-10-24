import { describe, expect, it } from "vitest";
import { ApiError, getOrder, getOrderTracking, getShipTo } from "./api";
import { ORDERING_API_BASE } from "./config";
import { json, mockApi, problem } from "./test/fetchMock";
import { order, SHIP_TO, tracking } from "./test/fixtures";

describe("getOrder", () => {
  it("GETs /orders/{orderId} and normalises the arrays", async () => {
    const api = mockApi({ "GET /orders/ord-1": json(order()) });
    const got = await getOrder("ord-1");
    expect(api.to("GET /orders/ord-1")[0].url).toBe(`${ORDERING_API_BASE}/orders/ord-1`);
    expect(got.groups[0].promiseId).toBe("p-1");
    expect(got.lines).toHaveLength(2);
  });

  it("keeps empty arrays when the body omits them", async () => {
    mockApi({ "GET /orders/ord-1": json({ ...order(), lines: undefined, groups: undefined }) });
    const got = await getOrder("ord-1");
    expect(got.lines).toEqual([]);
    expect(got.groups).toEqual([]);
  });

  it("escapes the order id into the path", async () => {
    const api = mockApi({ "GET /orders/a%2Fb": json(order()) });
    await getOrder("a/b");
    expect(api.to("GET /orders/a%2Fb")).toHaveLength(1);
  });

  it("surfaces the RFC 9457 problem of a 404", async () => {
    mockApi({ "GET /orders/ord-nope": problem(404, "not-found", "Not found", "no order with this id") });
    const err = await getOrder("ord-nope").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    const apiErr = err as ApiError;
    expect(apiErr.status).toBe(404);
    expect(apiErr.slug).toBe("not-found");
    expect(apiErr.title).toBe("Not found");
    expect(apiErr.detail).toBe("no order with this id");
  });

  it("wraps a network failure as a status-0 ApiError", async () => {
    mockApi({});
    await expect(getOrder("ord-1")).rejects.toThrow(/unmocked request/);
  });
});

describe("getOrderTracking", () => {
  it("GETs /orders/{orderId}/tracking and normalises every group's packages", async () => {
    const api = mockApi({ "GET /orders/ord-1/tracking": json(tracking()) });
    const got = await getOrderTracking("ord-1");
    expect(api.to("GET /orders/ord-1/tracking")).toHaveLength(1);
    expect(got.groups[0].packages[0].trackingNumber).toBe("TRK-100");
  });

  it("keeps an empty package list a list", async () => {
    mockApi({
      "GET /orders/ord-1/tracking": json({
        ...tracking(),
        groups: [{ ...tracking().groups[1], packages: undefined }],
      }),
    });
    const got = await getOrderTracking("ord-1");
    expect(got.groups[0].packages).toEqual([]);
  });
});

describe("getShipTo", () => {
  it("GETs the restricted address read", async () => {
    const api = mockApi({ "GET /orders/ord-1/ship-to": json(SHIP_TO) });
    const got = await getShipTo("ord-1");
    expect(api.to("GET /orders/ord-1/ship-to")).toHaveLength(1);
    expect(got.postalCode).toBe("78701");
  });

  it("reports a miss as not-found", async () => {
    mockApi({ "GET /orders/ord-nope/ship-to": problem(404, "not-found", "Not found", "no order with this id") });
    const err = await getShipTo("ord-nope").catch((e: unknown) => e);
    expect((err as ApiError).slug).toBe("not-found");
  });
});
