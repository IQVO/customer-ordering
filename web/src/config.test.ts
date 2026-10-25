import { describe, expect, it } from "vitest";
import { resolveOrderingApiBase } from "./config";

describe("resolveOrderingApiBase", () => {
  it("builds this context's base from the runtime apiOrigin", () => {
    expect(resolveOrderingApiBase({ apiOrigin: "http://localhost:8000" }, false)).toBe(
      "http://localhost:8000/api/customer-ordering",
    );
  });

  it("trims trailing slashes off the origin", () => {
    expect(resolveOrderingApiBase({ apiOrigin: "http://localhost:8000///" }, true)).toBe(
      "http://localhost:8000/api/customer-ordering",
    );
  });

  it("falls back to cmd/api's dev port outside production", () => {
    expect(resolveOrderingApiBase({}, false)).toBe("http://localhost:8080");
  });

  it("throws in production without an origin", () => {
    expect(() => resolveOrderingApiBase({}, true)).toThrow(/apiOrigin is required/);
    expect(() => resolveOrderingApiBase({ apiOrigin: "  " }, true)).toThrow(/apiOrigin is required/);
  });
});
