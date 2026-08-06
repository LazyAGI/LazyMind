import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../../../../../test/testUtils";
import WidgetConfigPanel from "./WidgetConfigPanel";

describe("WidgetConfigPanel", () => {
  it("renders only the base fields for text-single", () => {
    const { container } = renderWithProviders(
      <WidgetConfigPanel config={{ widgetType: "text-single" }} onChange={vi.fn()} />,
    );
    expect(container.querySelectorAll(".wcp-field")).toHaveLength(2);
    expect(screen.getByText("selfEvolutionRun.wcpReadOnly")).toBeInTheDocument();
    expect(screen.getByText("selfEvolutionRun.wcpMaxHeight")).toBeInTheDocument();
  });

  it("toggles readOnly through the checkbox", () => {
    const onChange = vi.fn();
    const { container } = renderWithProviders(
      <WidgetConfigPanel config={{ widgetType: "text-single" }} onChange={onChange} />,
    );
    fireEvent.click(container.querySelector('input[type="checkbox"]')!);
    expect(onChange).toHaveBeenCalledWith({ widgetType: "text-single", readOnly: true });
  });

  it("updates maxHeight through the number input", () => {
    const onChange = vi.fn();
    const { container } = renderWithProviders(
      <WidgetConfigPanel config={{ widgetType: "text-single" }} onChange={onChange} />,
    );
    const input = container.querySelector(".wcp-field-value .ant-input-number-input") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "200" } });
    expect(onChange).toHaveBeenCalledWith({ widgetType: "text-single", maxHeight: 200 });
  });

  it("shows the item-max-width field only when text-list layout is horizontal", () => {
    const { rerender, container } = renderWithProviders(
      <WidgetConfigPanel config={{ widgetType: "text-list", itemLayout: "vertical" }} onChange={vi.fn()} />,
    );
    expect(screen.queryByText("selfEvolutionRun.wcpItemMaxWidth")).not.toBeInTheDocument();

    rerender(
      <WidgetConfigPanel config={{ widgetType: "text-list", itemLayout: "horizontal" }} onChange={vi.fn()} />,
    );
    expect(screen.getByText("selfEvolutionRun.wcpItemMaxWidth")).toBeInTheDocument();
    void container;
  });

  it("shows the grid-max-cols field only when text-list layout is grid", () => {
    renderWithProviders(
      <WidgetConfigPanel config={{ widgetType: "text-list", itemLayout: "grid" }} onChange={vi.fn()} />,
    );
    expect(screen.getByText("selfEvolutionRun.wcpGridMaxCols")).toBeInTheDocument();
  });

  it("toggles showAddButton to false for text-list when unchecked", () => {
    const onChange = vi.fn();
    const { container } = renderWithProviders(
      <WidgetConfigPanel config={{ widgetType: "text-list" }} onChange={onChange} />,
    );
    const checkboxes = container.querySelectorAll('input[type="checkbox"]');
    fireEvent.click(checkboxes[checkboxes.length - 1]);
    expect(onChange).toHaveBeenCalledWith({ widgetType: "text-list", showAddButton: false });
  });

  it("renders image-gallery fields with defaults for card width/height", () => {
    renderWithProviders(
      <WidgetConfigPanel config={{ widgetType: "image-gallery" }} onChange={vi.fn()} />,
    );
    expect(screen.getByText("selfEvolutionRun.wcpCardWidth")).toBeInTheDocument();
    expect(screen.getByDisplayValue("180")).toBeInTheDocument();
    expect(screen.getByDisplayValue("140")).toBeInTheDocument();
  });

  it("toggles collapsed for json-block", () => {
    const onChange = vi.fn();
    const { container } = renderWithProviders(
      <WidgetConfigPanel config={{ widgetType: "json-block" }} onChange={onChange} />,
    );
    const checkboxes = container.querySelectorAll('input[type="checkbox"]');
    fireEvent.click(checkboxes[checkboxes.length - 1]);
    expect(onChange).toHaveBeenCalledWith({ widgetType: "json-block", collapsed: true });
  });

  it("returns null for an unrecognized widget type", () => {
    const { container } = renderWithProviders(
      <WidgetConfigPanel config={{ widgetType: "unknown" } as never} onChange={vi.fn()} />,
    );
    expect(container.querySelector(".wcp-root")).not.toBeInTheDocument();
  });
});
