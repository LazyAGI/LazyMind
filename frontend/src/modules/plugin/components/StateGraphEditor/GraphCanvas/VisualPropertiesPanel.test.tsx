import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "../../../../../test/testUtils";
import { EdgeVisualPanel, NodeVisualPanel } from "./VisualPropertiesPanel";
import type { EdgeVisual, NodeLayout } from "../core/model";

function baseLayout(overrides: Partial<NodeLayout> = {}): NodeLayout {
  return { x: 12, y: 24, ...overrides };
}

describe("NodeVisualPanel", () => {
  it("renders position/size fields with the current values", () => {
    renderWithProviders(
      <NodeVisualPanel value={baseLayout({ width: 200 })} onChange={vi.fn()} onReset={vi.fn()} />,
    );
    expect(screen.getByDisplayValue("12")).toBeInTheDocument();
    expect(screen.getByDisplayValue("24")).toBeInTheDocument();
    expect(screen.getByDisplayValue("200")).toBeInTheDocument();
  });

  it("hides the content-visibility section for terminal nodes", () => {
    renderWithProviders(
      <NodeVisualPanel value={baseLayout()} onChange={vi.fn()} onReset={vi.fn()} terminal />,
    );
    expect(screen.queryByText("内容显示")).not.toBeInTheDocument();
  });

  it("updates x/y through the position inputs", () => {
    const onChange = vi.fn();
    renderWithProviders(<NodeVisualPanel value={baseLayout()} onChange={onChange} onReset={vi.fn()} />);
    fireEvent.change(screen.getByDisplayValue("12"), { target: { value: "50" } });
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ x: 50 }));
  });

  it("toggles a visibility switch", () => {
    const onChange = vi.fn();
    const { container } = render(
      <NodeVisualPanel value={baseLayout()} onChange={onChange} onReset={vi.fn()} />,
    );
    const switches = container.querySelectorAll(".ant-switch");
    fireEvent.click(switches[0]);
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ visible: expect.objectContaining({ stepId: false }) }),
    );
  });

  it("sets a solid fill and shows the color/opacity controls when the fill type changes", () => {
    const onChange = vi.fn();
    const { container } = render(<NodeVisualPanel value={baseLayout()} onChange={onChange} onReset={vi.fn()} />);
    const fillSelect = container.querySelectorAll(".ant-select")[0];
    fireEvent.mouseDown(fillSelect.querySelector(".ant-select-selector")!);
    fireEvent.click(screen.getByText("纯色"));
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ fill: expect.objectContaining({ type: "solid" }) }),
    );
  });

  it("calls onReset when the reset button is clicked", () => {
    const onReset = vi.fn();
    render(<NodeVisualPanel value={baseLayout()} onChange={vi.fn()} onReset={onReset} />);
    fireEvent.click(screen.getByText("恢复默认样式"));
    expect(onReset).toHaveBeenCalled();
  });

  it("renders copy/paste buttons only when their handlers are provided", () => {
    const { rerender } = render(
      <NodeVisualPanel value={baseLayout()} onChange={vi.fn()} onReset={vi.fn()} />,
    );
    expect(screen.queryByText("复制样式")).not.toBeInTheDocument();

    rerender(
      <NodeVisualPanel
        value={baseLayout()}
        onChange={vi.fn()}
        onReset={vi.fn()}
        onCopy={vi.fn()}
        onPaste={vi.fn()}
      />,
    );
    expect(screen.getByText("复制样式")).toBeInTheDocument();
    expect(screen.getByText("粘贴样式")).toBeInTheDocument();
  });

  it("disables editing controls in readonly mode", () => {
    const { container } = render(
      <NodeVisualPanel value={baseLayout()} onChange={vi.fn()} onReset={vi.fn()} readonly />,
    );
    expect(container.querySelector(".ant-switch")).toBeDisabled();
    expect(screen.getByText("恢复默认样式").closest("button")).toBeDisabled();
  });
});

describe("EdgeVisualPanel", () => {
  function baseVisual(overrides: Partial<EdgeVisual> = {}): EdgeVisual {
    return { stroke: { color: "#8c8c8c", width: 1.5, style: "solid" }, ...overrides };
  }

  it("renders the stroke width and arrow controls with current values", () => {
    const { container } = render(
      <EdgeVisualPanel value={baseVisual()} onChange={vi.fn()} onReset={vi.fn()} />,
    );
    expect(screen.getByDisplayValue("1.5")).toBeInTheDocument();
    expect(container.querySelectorAll(".ant-switch")).toHaveLength(2);
  });

  it("updates stroke width through the input", () => {
    const onChange = vi.fn();
    render(<EdgeVisualPanel value={baseVisual()} onChange={onChange} onReset={vi.fn()} />);
    fireEvent.change(screen.getByDisplayValue("1.5"), { target: { value: "3" } });
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ stroke: expect.objectContaining({ width: 3 }) }),
    );
  });

  it("toggles showArrow off", () => {
    const onChange = vi.fn();
    const { container } = render(
      <EdgeVisualPanel value={baseVisual()} onChange={onChange} onReset={vi.fn()} />,
    );
    const switches = container.querySelectorAll(".ant-switch");
    fireEvent.click(switches[0]);
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ showArrow: false }));
  });

  it("calls onReset when the reset button is clicked", () => {
    const onReset = vi.fn();
    render(<EdgeVisualPanel value={baseVisual()} onChange={vi.fn()} onReset={onReset} />);
    fireEvent.click(screen.getByText("恢复默认样式"));
    expect(onReset).toHaveBeenCalled();
  });
});
