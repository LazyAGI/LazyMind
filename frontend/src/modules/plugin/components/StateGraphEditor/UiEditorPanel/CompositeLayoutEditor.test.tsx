import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../../../../../test/testUtils";
import CompositeLayoutEditor from "./CompositeLayoutEditor";
import type { PluginUiTab } from "../core/pluginModel";
import type { SlotDef } from "../core/model";

const slotMap: Record<string, SlotDef> = {
  outline: { id: "outline", type: "text", label: "Outline" },
};

function makeTab(overrides: Partial<PluginUiTab> = {}): PluginUiTab {
  return { id: "tab1", slots: [], ...overrides };
}

describe("CompositeLayoutEditor", () => {
  it("shows the template picker when the tab has no composite layout yet", () => {
    renderWithProviders(
      <CompositeLayoutEditor
        tab={makeTab()}
        slotMap={slotMap}
        uiSlots={{}}
        onChange={vi.fn()}
        onPageBarPositionChange={vi.fn()}
      />,
    );
    expect(screen.getByText("selfEvolutionRun.cleSelectTemplate")).toBeInTheDocument();
  });

  it("calls onChange with the selected template node when a template is clicked", () => {
    const onChange = vi.fn();
    const { container } = renderWithProviders(
      <CompositeLayoutEditor
        tab={makeTab()}
        slotMap={slotMap}
        uiSlots={{}}
        onChange={onChange}
        onPageBarPositionChange={vi.fn()}
      />,
    );
    fireEvent.click(container.querySelectorAll(".cle-template-btn")[1]);
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        direction: "row",
        children: [expect.objectContaining({ slot: "" }), expect.objectContaining({ slot: "" })],
      }),
    );
  });

  it("shows the canvas and hides the template picker once a layout exists", () => {
    const tab = makeTab({
      composite_layout: { direction: "row", children: [{ slot: "outline", weight: 1 }] },
    });
    const { container } = renderWithProviders(
      <CompositeLayoutEditor
        tab={tab}
        slotMap={slotMap}
        uiSlots={{}}
        onChange={vi.fn()}
        onPageBarPositionChange={vi.fn()}
      />,
    );
    expect(screen.queryByText("selfEvolutionRun.cleSelectTemplate")).not.toBeInTheDocument();
    expect(container.querySelector(".cle-canvas-wrap")).toBeInTheDocument();
  });

  it("resets the layout back to empty when the reset button is clicked", () => {
    const onChange = vi.fn();
    const tab = makeTab({
      composite_layout: { direction: "row", children: [{ slot: "outline", weight: 1 }] },
    });
    renderWithProviders(
      <CompositeLayoutEditor
        tab={tab}
        slotMap={slotMap}
        uiSlots={{}}
        onChange={onChange}
        onPageBarPositionChange={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByText("selfEvolutionRun.cleResetLayout"));
    expect(onChange).toHaveBeenCalledWith({ direction: "row", children: [] });
  });
});
