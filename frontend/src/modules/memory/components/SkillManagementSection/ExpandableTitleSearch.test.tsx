import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import ExpandableTitleSearch from "./ExpandableTitleSearch";

describe("ExpandableTitleSearch", () => {
  it("keeps the collapsed field out of the accessibility tree", () => {
    render(
      <ExpandableTitleSearch
        value=""
        placeholder="Search skills"
        onChange={vi.fn()}
        onSearch={vi.fn()}
      />,
    );

    const toggle = screen.getByRole("button", { name: "Search skills" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    const field = document.getElementById(toggle.getAttribute("aria-controls") || "");
    expect(field).toHaveAttribute("aria-hidden", "true");
    expect(field).toHaveAttribute("inert");
    expect(field?.querySelector("input")).toHaveAttribute("tabindex", "-1");

    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(toggle).toHaveAttribute("aria-hidden", "true");
    expect(field).toHaveAttribute("aria-hidden", "false");
    expect(field).not.toHaveAttribute("inert");
  });

  it("collapses when clicking outside", () => {
    render(
      <div>
        <ExpandableTitleSearch
          value=""
          placeholder="Search skills"
          onChange={vi.fn()}
          onSearch={vi.fn()}
        />
        <button type="button">outside</button>
      </div>,
    );

    const toggle = screen.getByRole("button", { name: "Search skills" });
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");

    fireEvent.pointerDown(screen.getByRole("button", { name: "outside" }));
    expect(toggle).toHaveAttribute("aria-expanded", "false");
  });
});
