import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../../../../../test/testUtils";
import UiWysiwygPreview from "./UiWysiwygPreview";
import { createEmptyPluginModel } from "../core/pluginModel";
import type { PluginModel, PluginUiTab } from "../core/pluginModel";
import type { SlotDef } from "../core/model";

const slotMap: Record<string, SlotDef> = {
  outline: { id: "outline", type: "text", label: "Outline" },
};

function makeModel(tabs: PluginUiTab[], overrides: Partial<PluginModel> = {}): PluginModel {
  return { ...createEmptyPluginModel(), ui: { tabs }, ...overrides };
}

describe("UiWysiwygPreview", () => {
  it("shows the empty hint and an add-tab button when there are no tabs", () => {
    renderWithProviders(
      <UiWysiwygPreview pluginModel={makeModel([])} slotMap={slotMap} onAddTab={vi.fn()} />,
    );
    expect(screen.getByText("selfEvolutionRun.uiWysiwygEmptyHint")).toBeInTheDocument();
    expect(screen.getByText("selfEvolutionRun.uiWysiwygAddTab")).toBeInTheDocument();
  });

  it("renders a step for each tab and marks the active one", () => {
    const tabs: PluginUiTab[] = [
      { id: "tab1", label: "Tab One", slots: [] },
      { id: "tab2", label: "Tab Two", slots: [] },
    ];
    const { container } = renderWithProviders(
      <UiWysiwygPreview pluginModel={makeModel(tabs)} slotMap={slotMap} activeTabId="tab2" />,
    );
    expect(screen.getByText("Tab One")).toBeInTheDocument();
    expect(screen.getByText("Tab Two")).toBeInTheDocument();
    const steps = container.querySelectorAll(".wywp-step");
    expect(steps[1].className).toContain("wywp-step--active");
    expect(steps[0].className).toContain("wywp-step--done");
  });

  it("shows the no-slots message for a vertical tab without slots", () => {
    const tabs: PluginUiTab[] = [{ id: "tab1", label: "Tab One", slots: [] }];
    renderWithProviders(
      <UiWysiwygPreview pluginModel={makeModel(tabs)} slotMap={slotMap} activeTabId="tab1" />,
    );
    expect(screen.getByText("selfEvolutionRun.uiWysiwygNoSlots")).toBeInTheDocument();
  });

  it("renders a widget placeholder for each slot in the active tab", () => {
    const tabs: PluginUiTab[] = [{ id: "tab1", label: "Tab One", slots: [{ id: "outline" }] }];
    renderWithProviders(
      <UiWysiwygPreview pluginModel={makeModel(tabs)} slotMap={slotMap} activeTabId="tab1" />,
    );
    expect(screen.getByText("Outline")).toBeInTheDocument();
  });

  it("calls onTabSelect when a step is clicked", () => {
    const onTabSelect = vi.fn();
    const tabs: PluginUiTab[] = [
      { id: "tab1", label: "Tab One", slots: [] },
      { id: "tab2", label: "Tab Two", slots: [] },
    ];
    renderWithProviders(
      <UiWysiwygPreview
        pluginModel={makeModel(tabs)}
        slotMap={slotMap}
        activeTabId="tab1"
        onTabSelect={onTabSelect}
      />,
    );
    fireEvent.click(screen.getByText("Tab Two"));
    expect(onTabSelect).toHaveBeenCalledWith("tab2");
  });

  it("renders the editable UiEditorCanvas when all edit handlers are provided", () => {
    const tabs: PluginUiTab[] = [{ id: "tab1", label: "Tab One", slots: [{ id: "outline" }] }];
    const { container } = renderWithProviders(
      <UiWysiwygPreview
        pluginModel={makeModel(tabs)}
        slotMap={slotMap}
        activeTabId="tab1"
        onSlotsChange={vi.fn()}
        onCompositeLayoutChange={vi.fn()}
        onCompositeTabPositionChange={vi.fn()}
      />,
    );
    expect(container.querySelector(".uep-canvas")).toBeInTheDocument();
  });

  it("shows footer action buttons only when not in editor mode", () => {
    const tabs: PluginUiTab[] = [{ id: "tab1", label: "Tab One", slots: [] }];
    const { rerender } = renderWithProviders(
      <UiWysiwygPreview pluginModel={makeModel(tabs)} slotMap={slotMap} activeTabId="tab1" editorMode />,
    );
    expect(screen.queryByText("selfEvolutionRun.uiWysiwygFooterContinue")).not.toBeInTheDocument();

    rerender(
      <UiWysiwygPreview
        pluginModel={makeModel(tabs)}
        slotMap={slotMap}
        activeTabId="tab1"
        editorMode={false}
      />,
    );
    expect(screen.getByText("selfEvolutionRun.uiWysiwygFooterContinue")).toBeInTheDocument();
  });
});
