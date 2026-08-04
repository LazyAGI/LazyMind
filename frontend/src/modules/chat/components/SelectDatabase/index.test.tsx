import { describe, expect, it } from "vitest";
import { renderWithProviders, screen } from "@/test/testUtils";
import SelectDatabase from "./index";

describe("SelectDatabase", () => {
  it("renders the trigger with the database label", () => {
    renderWithProviders(<SelectDatabase />);
    expect(screen.getByText("chat.configDatabase")).toBeInTheDocument();
  });

  it("does not mark the trigger as selected when there is no current database", () => {
    renderWithProviders(<SelectDatabase />);
    const trigger = screen.getByText("chat.configDatabase").closest("div");
    expect(trigger).not.toHaveClass("selected");
  });

  it("marks the trigger as selected when a currentDatabase is provided", () => {
    renderWithProviders(<SelectDatabase currentDatabase="db-1" />);
    const trigger = screen.getByText("chat.configDatabase").closest("div");
    expect(trigger).toHaveClass("selected");
  });
});
