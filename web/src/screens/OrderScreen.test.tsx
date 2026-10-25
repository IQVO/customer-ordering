import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { OrderLookupScreen, OrderRoute } from "./OrderScreen";
import { json, mockApi, problem } from "../test/fetchMock";
import { order } from "../test/fixtures";

function Where() {
  return <div data-testid="where">{useLocation().pathname}</div>;
}

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/orders" element={<OrderLookupScreen />} />
        <Route path="/orders/:orderId" element={<OrderRoute />} />
      </Routes>
      <Where />
    </MemoryRouter>,
  );
}

describe("OrderLookupScreen", () => {
  it("opens the order, escaping the id", async () => {
    renderAt("/orders");
    const user = userEvent.setup();
    expect(screen.getByRole("button", { name: "View order" })).toBeDisabled();
    await user.type(screen.getByLabelText("Order"), "ord-1");
    await user.click(screen.getByRole("button", { name: "View order" }));
    expect(screen.getByTestId("where")).toHaveTextContent("/orders/ord-1");
  });

  it("refuses a malformed id before asking", async () => {
    renderAt("/orders");
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Order"), "a/b");
    expect(screen.getByRole("button", { name: "View order" })).toBeDisabled();
    expect(screen.getByText(/cannot contain/)).toBeInTheDocument();
  });
});

describe("OrderScreen", () => {
  it("shows the order, its lines and both shipment groups", async () => {
    mockApi({ "GET /orders/ord-1": json(order()) });
    renderAt("/orders/ord-1");
    expect(await screen.findByRole("heading", { name: "Order ord-1" })).toBeInTheDocument();
    expect(screen.getByText("shopper-1")).toBeInTheDocument();
    expect(screen.getByText("sku-100")).toBeInTheDocument();
    expect(screen.getByText("p-1")).toBeInTheDocument();
    expect(screen.getByText("p-2")).toBeInTheDocument();
    expect(screen.getByText("line 1")).toBeInTheDocument();
    expect(screen.getByText("line 2")).toBeInTheDocument();
    // The cancelledAt fact is absent on a live order.
    expect(screen.queryByText("Cancelled at")).not.toBeInTheDocument();
  });

  it("shows the cancellation facts of a cancelled order", async () => {
    mockApi({
      "GET /orders/ord-1": json(order({
        status: "CANCELLED",
        cancelledAt: "2026-10-24T11:00:00Z",
        groups: [{ ...order().groups[0], status: "CANCELLED", cancelReason: "changed my mind" }],
      })),
    });
    renderAt("/orders/ord-1");
    expect(await screen.findByText("Cancelled at")).toBeInTheDocument();
    expect(screen.getByText("changed my mind")).toBeInTheDocument();
  });

  it("renders the problem of a miss", async () => {
    mockApi({ "GET /orders/ord-nope": problem(404, "not-found", "Not found", "no order with this id") });
    renderAt("/orders/ord-nope");
    expect(await screen.findByRole("alert")).toHaveTextContent("Not found");
    expect(screen.getByText("no order with this id")).toBeInTheDocument();
  });
});
