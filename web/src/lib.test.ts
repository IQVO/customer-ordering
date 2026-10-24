import { describe, expect, it } from "vitest";
import { lineNosLabel, orderIdProblem, utcLabel, windowLabel } from "./lib";

describe("utcLabel", () => {
  it("renders an RFC 3339 instant as a compact UTC stamp", () => {
    expect(utcLabel("2026-10-24T10:00:00Z")).toBe("2026-10-24 10:00:00Z");
  });

  it("renders the missing-value marker for an absent instant", () => {
    expect(utcLabel(undefined)).toBe("—");
  });

  it("passes unparseable text through", () => {
    expect(utcLabel("not a date")).toBe("not a date");
  });
});

describe("windowLabel", () => {
  it("joins the promise window with an en dash", () => {
    expect(windowLabel("2026-10-24T10:20:00Z", "2026-10-24T10:25:00Z")).toBe(
      "2026-10-24 10:20:00Z – 2026-10-24 10:25:00Z",
    );
  });
});

describe("orderIdProblem", () => {
  it("accepts a plain id", () => {
    expect(orderIdProblem("ord-3f9c1a2b")).toBeNull();
  });

  it("flags the empty id, an over-long id and forbidden characters", () => {
    expect(orderIdProblem("")).toMatch(/Enter an order id/);
    expect(orderIdProblem("x".repeat(65))).toMatch(/at most 64 characters/);
    expect(orderIdProblem("a/b")).toMatch(/cannot contain spaces/);
    expect(orderIdProblem("a b")).toMatch(/cannot contain spaces/);
  });
});

describe("lineNosLabel", () => {
  it("pluralises and lists the line numbers", () => {
    expect(lineNosLabel([1])).toBe("line 1");
    expect(lineNosLabel([1, 2])).toBe("lines 1, 2");
    expect(lineNosLabel(undefined)).toBe("—");
  });
});
