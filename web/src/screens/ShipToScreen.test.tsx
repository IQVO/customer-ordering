import { MemoryRouter, Route, Routes } from "react-router-dom";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ShipToRoute } from "./ShipToScreen";
import { json, mockApi, problem } from "../test/fetchMock";
import { SHIP_TO } from "../test/fixtures";

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/ship-to/:orderId" element={<ShipToRoute />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("ShipToScreen", () => {
  it("shows the synthetic address of the order", async () => {
    mockApi({ "GET /orders/ord-1/ship-to": json(SHIP_TO) });
    renderAt("/ship-to/ord-1");
    expect(await screen.findByRole("heading", { name: "Ship-to of ord-1" })).toBeInTheDocument();
    expect(screen.getByText("Ana Example")).toBeInTheDocument();
    expect(screen.getByText("1 Sample Street, Apt 2")).toBeInTheDocument();
    expect(screen.getByText("78701")).toBeInTheDocument();
    expect(screen.getByText("+1 555 0100")).toBeInTheDocument();
  });

  it("omits the phone fact when the order carries none", async () => {
    mockApi({ "GET /orders/ord-1/ship-to": json({ ...SHIP_TO, phone: undefined }) });
    renderAt("/ship-to/ord-1");
    await screen.findByText("Ana Example");
    expect(screen.queryByText("+1 555 0100")).not.toBeInTheDocument();
  });

  it("renders the problem of a miss", async () => {
    mockApi({ "GET /orders/ord-nope/ship-to": problem(404, "not-found", "Not found", "no order with this id") });
    renderAt("/ship-to/ord-nope");
    expect(await screen.findByRole("alert")).toHaveTextContent("Not found");
  });
});
