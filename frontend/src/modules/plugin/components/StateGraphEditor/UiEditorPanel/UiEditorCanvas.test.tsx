import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../../../../../test/testUtils";
import UiEditorCanvas from "./UiEditorCanvas";
import type { PluginUiTab } from "../core/pluginModel";
import type { SlotDef } from "../core/model";

const slotMap: Record<string, SlotDef> = {
  outline: { id: "outline", type: "text", label: "Outline" },
  body: { id: "body", type: "text", label: "Body" },
};

function makeTab(overrides: Partial<PluginUiTab> = {}): PluginUiTab {
  return { id: "tab1", slots: [], ...overrides };
}

function makeDataTransfer(slotId: string) {
  return {
    types: ["application/x-slot-id"],
    dropEffect: "",
    getData: (type: string) => (type === "application/x-slot-id" ? slotId : ""),
  } as unknown as DataTransfer;
}

function baseProps() {
  return {
    slotMap,
    uiSlots: {},
    selectedSlotId: null,
    onSelectSlot: vi.fn(),
    onSlotsChange: vi.fn(),
    onCompositeLayoutChange: vi.fn(),
    onCompositeTabPositionChange: vi.fn(),
  };
}

describe("UiEditorCanvas", () => {
  it("renders an empty state when the tab has no slots", () => {
    renderWithProviders(<UiEditorCanvas tab={makeTab()} {...baseProps()} />);
    expect(screen.getByText("selfEvolutionRun.uiEditorCanvasEmptyDesc")).toBeInTheDocument();
  });

  it("renders a widget card for each slot in the tab", () => {
    const { container } = renderWithProviders(
      <UiEditorCanvas tab={makeTab({ slots: [{ id: "outline" }, { id: "body" }] })} {...baseProps()} />,
    );
    expect(container.querySelectorAll(".uep-widget-card")).toHaveLength(2);
  });

  it("adds a dropped slot to the tab when not already present", () => {
    const onSlotsChange = vi.fn();
    const { container } = renderWithProviders(
      <UiEditorCanvas tab={makeTab({ slots: [{ id: "outline" }] })} {...baseProps()} onSlotsChange={onSlotsChange} />,
    );
    const canvas = container.querySelector(".uep-canvas")!;
    fireEvent.drop(canvas, { dataTransfer: makeDataTransfer("body") });
    expect(onSlotsChange).toHaveBeenCalledWith([{ id: "outline" }, { id: "body" }]);
  });

  it("removes a slot and clears selection when its remove button is clicked", () => {
    const onSlotsChange = vi.fn();
    const onSelectSlot = vi.fn();
    renderWithProviders(
      <UiEditorCanvas
        tab={makeTab({ slots: [{ id: "outline" }, { id: "body" }] })}
        {...baseProps()}
        selectedSlotId="outline"
        onSlotsChange={onSlotsChange}
        onSelectSlot={onSelectSlot}
      />,
    );
    fireEvent.click(screen.getAllByLabelText(/uiWidgetRemoveAriaLabel/)[0]);
    expect(onSlotsChange).toHaveBeenCalledWith([{ id: "body" }]);
    expect(onSelectSlot).toHaveBeenCalledWith(null);
  });

  it("renders the CompositeLayoutEditor for composite-layout tabs", () => {
    const { container } = renderWithProviders(
      <UiEditorCanvas tab={makeTab({ layout: "composite" })} {...baseProps()} />,
    );
    expect(container.querySelector(".cle-root")).toBeInTheDocument();
  });

  it("applies the grid column count as a CSS variable for grid layout", () => {
    const { container } = renderWithProviders(
      <UiEditorCanvas
        tab={makeTab({ layout: "grid", slots: [{ id: "outline" }] })}
        {...baseProps()}
        gridCols={3}
      />,
    );
    const slotsEl = container.querySelector(".uep-canvas-slots--grid") as HTMLElement;
    expect(slotsEl.style.getPropertyValue("--uep-grid-cols")).toBe("repeat(3, 1fr)");
  });
});
