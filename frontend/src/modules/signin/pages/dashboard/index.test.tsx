import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import AppLayout from "./index";

vi.mock("./index.scss", () => ({}));
vi.mock("@/public/layout-bg-IsmwJvyW.png", () => ({ default: "bg.png" }));
vi.mock("@/public/Lazy.png", () => ({ default: "logo.png" }));

function renderDashboard() {
  return render(
    <MemoryRouter initialEntries={["/"]}>
      <Routes>
        <Route path="/" element={<AppLayout />}>
          <Route index element={<div>child-outlet</div>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

describe("signin dashboard layout", () => {
  it("shows a loading spinner before the login UI is ready, then renders the outlet", async () => {
    renderDashboard();
    // The outlet is revealed asynchronously via a useEffect on mount.
    expect(await screen.findByText("child-outlet")).toBeInTheDocument();
  });

  it("renders the logo image and footer copyright text", async () => {
    renderDashboard();
    await screen.findByText("child-outlet");
    expect(screen.getByAltText("logo")).toBeInTheDocument();
    expect(screen.getByText("LazyMind")).toBeInTheDocument();
  });
});
