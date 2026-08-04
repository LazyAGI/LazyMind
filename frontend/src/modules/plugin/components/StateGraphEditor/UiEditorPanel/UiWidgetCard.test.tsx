import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { DndContext } from "@dnd-kit/core";
import { SortableContext } from "@dnd-kit/sortable";
import { renderWithProviders } from "../../../../../test/testUtils";
import UiWidgetCard from "./UiWidgetCard";
import type { SlotDef } from "../core/model";

function renderCard(props: Partial<React.ComponentProps<typeof UiWidgetCard>> = {}) {
  const defaultProps: React.ComponentProps<typeof UiWidgetCard> = {
    slotId: "outline",
    onRemove: vi.fn(),
    ...props,
  };
  return renderWithProviders(
    <DndContext>
      <SortableContext items={["outline"]}>
        <UiWidgetCard {...defaultProps} />
      </SortableContext>
    </DndContext>,
  );
}

describe("UiWidgetCard", () => {
  it("renders the slot label, falling back to the slot id when no label is set", () => {
    const { container } = renderCard();
    expect(container.querySelector(".uep-widget-label")?.textContent).toBe("outline");
  });

  it("renders the slot's configured label when present", () => {
    const slotDef: SlotDef = { id: "outline", type: "text", label: "Outline" };
    const { container } = renderCard({ slotDef });
    expect(container.querySelector(".uep-widget-label")?.textContent).toBe("Outline");
    expect(container.querySelector(".uep-widget-card")?.textContent).not.toContain("outline");
  });

  it("applies the selected class when isSelected is true", () => {
    const { container } = renderCard({ isSelected: true });
    expect(container.querySelector(".uep-widget-card--selected")).toBeInTheDocument();
  });

  it("calls onSelect with the slot id when the card is clicked", () => {
    const onSelect = vi.fn();
    const { container } = renderCard({ onSelect });
    fireEvent.click(container.querySelector(".uep-widget-card")!);
    expect(onSelect).toHaveBeenCalledWith("outline");
  });

  it("calls onRemove and stops propagation when the remove button is clicked", () => {
    const onRemove = vi.fn();
    const onSelect = vi.fn();
    renderCard({ onRemove, onSelect });
    fireEvent.click(screen.getByLabelText("selfEvolutionRun.uiWidgetRemoveAriaLabel"));
    expect(onRemove).toHaveBeenCalledWith("outline");
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("renders a widget placeholder preview using the default widget for the slot type", () => {
    const slotDef: SlotDef = { id: "outline", type: "image", cardinality: "list" };
    const { container } = renderCard({ slotDef });
    expect(container.querySelector(".wp-image-gallery")).toBeInTheDocument();
  });
});
