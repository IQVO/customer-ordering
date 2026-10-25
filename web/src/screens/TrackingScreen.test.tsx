import { MemoryRouter, Route, Routes } from "react-router-dom";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { TrackingRoute } from "./TrackingScreen";
import { json, mockApi, problem } from "../test/fetchMock";
import { tracking } from "../test/fixtures";

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/tracking/:orderId" element={<TrackingRoute />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("TrackingScreen", () => {
  it("shows each group with its verdict, window and packages", async () => {
    mockApi({ "GET /orders/ord-1/tracking": json(tracking()) });
    renderAt("/tracking/ord-1");
    expect(await screen.findByRole("heading", { name: "Tracking ord-1" })).toBeInTheDocument();
    expect(screen.getByText("AT_RISK")).toBeInTheDocument();
    expect(screen.getByText("12 min")).toBeInTheDocument();
    expect(screen.getByText("no capacity at the node")).toBeInTheDocument();
    expect(screen.getByText("TRK-100")).toBeInTheDocument();
    expect(screen.getByText("CARRIER-1")).toBeInTheDocument();
    expect(screen.getByText("fc-aus-1")).toBeInTheDocument();
  });

  it("says so when a group has no package yet", async () => {
    mockApi({ "GET /orders/ord-1/tracking": json(tracking()) });
    renderAt("/tracking/ord-1");
    expect(await screen.findByText("No package has been labelled for this group yet.")).toBeInTheDocument();
  });

  it("renders the problem of a miss", async () => {
    mockApi({ "GET /orders/ord-nope/tracking": problem(404, "not-found", "Not found", "no order with this id") });
    renderAt("/tracking/ord-nope");
    expect(await screen.findByRole("alert")).toHaveTextContent("Not found");
  });
});
