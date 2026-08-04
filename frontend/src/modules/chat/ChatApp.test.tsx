import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import ChatApp from "./ChatApp";

vi.mock("./layout", () => ({
  default: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="layout-stub">{children}</div>
  ),
}));

describe("ChatApp", () => {
  it("renders the layout wrapper with the routed outlet content", () => {
    render(
      <MemoryRouter initialEntries={["/child"]}>
        <Routes>
          <Route path="/" element={<ChatApp />}>
            <Route path="child" element={<div data-testid="outlet-child" />} />
          </Route>
        </Routes>
      </MemoryRouter>,
    );
    expect(screen.getByTestId("layout-stub")).toBeInTheDocument();
    expect(screen.getByTestId("outlet-child")).toBeInTheDocument();
  });
});
