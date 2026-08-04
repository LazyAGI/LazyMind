import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../../../../../test/testUtils";
import WidgetSelector from "./WidgetSelector";

describe("WidgetSelector", () => {
  it("defaults to the slot type's default widget when no value is set", () => {
    renderWithProviders(<WidgetSelector slotType="text" onChange={vi.fn()} />);
    expect(screen.getByText("selfEvolutionRun.widgetTextSingle")).toBeInTheDocument();
  });

  it("only offers compatible widget options for the slot type", () => {
    const { container } = renderWithProviders(
      <WidgetSelector slotType="image" cardinality="single" onChange={vi.fn()} />,
    );
    fireEvent.mouseDown(container.querySelector(".ant-select-selector")!);
    const options = document.querySelectorAll(".ant-select-item-option");
    expect(options).toHaveLength(1);
    expect(options[0].textContent).toBe("selfEvolutionRun.widgetImageSingle");
  });

  it("shows the provided value instead of the default when set", () => {
    renderWithProviders(
      <WidgetSelector slotType="text" cardinality="single" value="text-markdown" onChange={vi.fn()} />,
    );
    expect(screen.getByText("selfEvolutionRun.widgetTextMarkdown")).toBeInTheDocument();
  });

  it("calls onChange with the selected widget type", () => {
    const onChange = vi.fn();
    const { container } = renderWithProviders(
      <WidgetSelector slotType="text" cardinality="single" onChange={onChange} />,
    );
    fireEvent.mouseDown(container.querySelector(".ant-select-selector")!);
    fireEvent.click(screen.getByText("selfEvolutionRun.widgetTextMarkdown"));
    expect(onChange).toHaveBeenCalledWith("text-markdown");
  });

  it("falls back to text-single compatible widgets for an unknown slot type", () => {
    const { container } = renderWithProviders(
      <WidgetSelector slotType="unknown-type" onChange={vi.fn()} />,
    );
    fireEvent.mouseDown(container.querySelector(".ant-select-selector")!);
    const options = document.querySelectorAll(".ant-select-item-option");
    expect(options).toHaveLength(1);
    expect(options[0].textContent).toBe("selfEvolutionRun.widgetTextSingle");
  });
});
