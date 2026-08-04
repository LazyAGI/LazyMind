import { describe, expect, it } from "vitest";
import { renderWithProviders, screen } from "@/test/testUtils";
import Rendering from "./index";

describe("Rendering", () => {
  it("shows the default loading translation key when no text is provided", () => {
    renderWithProviders(<Rendering />);
    expect(screen.getByText("knowledge.dataLoading")).toBeInTheDocument();
  });

  it("shows the custom text when provided", () => {
    renderWithProviders(<Rendering text="Loading segments..." />);
    expect(screen.getByText("Loading segments...")).toBeInTheDocument();
    expect(screen.queryByText("knowledge.dataLoading")).not.toBeInTheDocument();
  });
});
