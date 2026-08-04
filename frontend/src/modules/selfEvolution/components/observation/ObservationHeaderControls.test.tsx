import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import { ObservationHeaderControls } from "./ObservationHeaderControls";

describe("ObservationHeaderControls", () => {
  it("renders the back button and triggers onBack when clicked", () => {
    const onBack = vi.fn();
    renderWithProviders(<ObservationHeaderControls onBack={onBack} />);
    fireEvent.click(screen.getByText("selfEvolutionRun.observation.back"));
    expect(onBack).toHaveBeenCalledTimes(1);
  });

  it("does not render the expand-menu button when the menu is not collapsed", () => {
    renderWithProviders(<ObservationHeaderControls onBack={vi.fn()} isMenuCollapsed={false} toggleMenu={vi.fn()} />);
    expect(screen.queryByLabelText("selfEvolutionRun.observation.expandMenu")).not.toBeInTheDocument();
  });

  it("renders and triggers the expand-menu button when the menu is collapsed and toggleMenu is provided", () => {
    const toggleMenu = vi.fn();
    renderWithProviders(<ObservationHeaderControls onBack={vi.fn()} isMenuCollapsed toggleMenu={toggleMenu} />);
    fireEvent.click(screen.getByLabelText("selfEvolutionRun.observation.expandMenu"));
    expect(toggleMenu).toHaveBeenCalledTimes(1);
  });

  it("does not render the expand-menu button when toggleMenu is missing even if collapsed", () => {
    renderWithProviders(<ObservationHeaderControls onBack={vi.fn()} isMenuCollapsed />);
    expect(screen.queryByLabelText("selfEvolutionRun.observation.expandMenu")).not.toBeInTheDocument();
  });
});
