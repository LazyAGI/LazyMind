import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import App from "./App";

vi.mock("./router", () => ({
  default: () => <div data-testid="app-router" />,
}));

vi.mock("./globalState", () => ({
  BASENAME: "",
}));

describe("App", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders the router inside a BrowserRouter without crashing", () => {
    render(<App />);
    expect(screen.getByTestId("app-router")).toBeInTheDocument();
  });
});
