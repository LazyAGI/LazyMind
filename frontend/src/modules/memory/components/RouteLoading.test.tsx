import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import RouteLoading from "./RouteLoading";

describe("RouteLoading", () => {
  it("renders the provided title", () => {
    render(<RouteLoading title="Loading skills" />);
    expect(screen.getByText("Loading skills")).toBeInTheDocument();
  });

  it("renders a skeleton placeholder", () => {
    const { container } = render(<RouteLoading title="Loading" />);
    expect(container.querySelector(".ant-skeleton")).not.toBeNull();
  });

  it("renders the route loading container class", () => {
    const { container } = render(<RouteLoading title="Loading" />);
    expect(container.querySelector(".memory-review-page.is-route-loading")).not.toBeNull();
  });
});
