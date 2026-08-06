import { fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../../../../../test/testUtils";
import CompositeCanvas from "./CompositeCanvas";
import type { CompositePanelNode } from "../core/pluginModel";
import type { SlotDef } from "../core/model";

const slotMap: Record<string, SlotDef> = {
  outline: { id: "outline", type: "text", label: "Outline" },
  images: { id: "images", type: "image", cardinality: "list" },
};

function makeDataTransfer(slotId: string) {
  return {
    types: ["application/x-slot-id"],
    dropEffect: "",
    getData: (type: string) => (type === "application/x-slot-id" ? slotId : ""),
  } as unknown as DataTransfer;
}

describe("CompositeCanvas", () => {
  it("renders an empty drop placeholder for a leaf without a slot", () => {
    const node: CompositePanelNode = { slot: "" };
    renderWithProviders(<CompositeCanvas node={node} slotMap={slotMap} onChange={vi.fn()} />);
    expect(screen.getByText("selfEvolutionRun.ccDropSlotHere")).toBeInTheDocument();
  });

  it("renders a widget preview once the leaf has a bound slot", () => {
    const node: CompositePanelNode = { slot: "outline" };
    renderWithProviders(<CompositeCanvas node={node} slotMap={slotMap} onChange={vi.fn()} />);
    expect(screen.getByText("Outline")).toBeInTheDocument();
  });

  it("assigns the dropped slot directly when the leaf is empty", () => {
    const node: CompositePanelNode = { slot: "" };
    const onChange = vi.fn();
    const { container } = renderWithProviders(
      <CompositeCanvas node={node} slotMap={slotMap} onChange={onChange} />,
    );
    const leaf = container.querySelector(".cc-leaf")!;
    fireEvent.drop(leaf, { dataTransfer: makeDataTransfer("outline") });
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ slot: "outline" }));
  });

  it("asks for confirmation before overwriting an already-bound leaf", async () => {
    const node: CompositePanelNode = { slot: "outline" };
    const onChange = vi.fn();
    const { container } = renderWithProviders(
      <CompositeCanvas node={node} slotMap={slotMap} onChange={onChange} />,
    );
    const leaf = container.querySelector(".cc-leaf")!;
    fireEvent.drop(leaf, { dataTransfer: makeDataTransfer("images") });
    expect(onChange).not.toHaveBeenCalled();
    await waitFor(() => {
      expect(document.querySelector(".ant-modal")).toBeInTheDocument();
    });
  });

  it("splits a leaf into a row container with a new empty pane", () => {
    const node: CompositePanelNode = { slot: "outline" };
    const onChange = vi.fn();
    const { container } = renderWithProviders(
      <CompositeCanvas node={node} slotMap={slotMap} onChange={onChange} />,
    );
    const splitBtn = container.querySelector(".cc-leaf-action-btn:not(.cc-leaf-action-btn--tab)")!;
    fireEvent.click(splitBtn);
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        direction: "row",
        children: [
          expect.objectContaining({ slot: "outline" }),
          expect.objectContaining({ slot: "" }),
        ],
      }),
    );
  });

  it("converts a single-slot leaf into tabs mode when the tab button is clicked", () => {
    const node: CompositePanelNode = { slot: "outline" };
    const onChange = vi.fn();
    renderWithProviders(<CompositeCanvas node={node} slotMap={slotMap} onChange={onChange} />);
    fireEvent.click(screen.getByText("Tab"));
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        tabs: [
          { label: "Tab 1", slot: "outline" },
          { label: "Tab 2", slot: "" },
        ],
      }),
    );
  });

  it("renders the page bar and calls onPageBarPositionChange when a dock option is chosen", async () => {
    const node: CompositePanelNode = { slot: "outline" };
    const onPageBarPositionChange = vi.fn();
    renderWithProviders(
      <CompositeCanvas
        node={node}
        slotMap={slotMap}
        onChange={vi.fn()}
        pageBarPosition="bottom"
        onPageBarPositionChange={onPageBarPositionChange}
      />,
    );
    fireEvent.click(screen.getByLabelText("pagebar-position-setting"));
    await waitFor(() => {
      expect(screen.getByText("selfEvolutionRun.compositePageBarTop")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText("selfEvolutionRun.compositePageBarTop"));
    expect(onPageBarPositionChange).toHaveBeenCalledWith("top");
  });

  it("renders two leaf panes for a row container with two children", () => {
    const node: CompositePanelNode = {
      direction: "row",
      children: [{ slot: "outline", weight: 1 }, { slot: "images", weight: 1 }],
    };
    const { container } = renderWithProviders(
      <CompositeCanvas node={node} slotMap={slotMap} onChange={vi.fn()} />,
    );
    expect(container.querySelectorAll(".cc-leaf")).toHaveLength(2);
    expect(container.querySelector(".cc-container--row")).toBeInTheDocument();
  });
});
