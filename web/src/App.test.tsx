import { MemoryRouter, Route, Routes } from "react-router-dom";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import App from "./App";
import { json, mockApi } from "./test/fetchMock";
import { order, SHIP_TO, tracking } from "./test/fixtures";

// The console mounts the remote under its own splat route; the remote's
// relative links must resolve against the mount point, not the full URL.
function renderUnderHost(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/customer-ordering/*" element={<App />} />
      </Routes>
    </MemoryRouter>,
  );
}

const ROUTES = {
  "GET /orders/ord-1": json(order()),
  "GET /orders/ord-1/tracking": json(tracking()),
  "GET /orders/ord-1/ship-to": json(SHIP_TO),
};

describe("App", () => {
  it("redirects the mount point to the order lookup", async () => {
    mockApi(ROUTES);
    renderUnderHost("/customer-ordering");
    expect(await screen.findByRole("heading", { name: "Orders" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Orders" })).toHaveAttribute(
      "href",
      "/customer-ordering/orders",
    );
  });

  it("keeps the sub-nav at the mount point from a deep route", async () => {
    mockApi(ROUTES);
    renderUnderHost("/customer-ordering/tracking/ord-1");
    await screen.findByRole("heading", { name: "Tracking ord-1" });
    expect(screen.getByRole("link", { name: "Ship-to" })).toHaveAttribute(
      "href",
      "/customer-ordering/ship-to",
    );
  });

  it("navigates between the three read-only sections of one order", async () => {
    mockApi(ROUTES);
    renderUnderHost("/customer-ordering/orders");
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText("Order"), "ord-1");
    await user.click(screen.getByRole("button", { name: "View order" }));
    expect(await screen.findByRole("heading", { name: "Order ord-1" })).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "Tracking" }));
    await user.type(await screen.findByLabelText("Order"), "ord-1");
    await user.click(screen.getByRole("button", { name: "View tracking" }));
    expect(await screen.findByRole("heading", { name: "Tracking ord-1" })).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "Ship-to" }));
    await user.type(await screen.findByLabelText("Order"), "ord-1");
    await user.click(screen.getByRole("button", { name: "View ship-to" }));
    expect(await screen.findByRole("heading", { name: "Ship-to of ord-1" })).toBeInTheDocument();
  });
});
