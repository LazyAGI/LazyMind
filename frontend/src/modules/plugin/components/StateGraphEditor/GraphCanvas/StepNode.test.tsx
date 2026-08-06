import { screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { NodeProps } from "@xyflow/react";
import { ReactFlowProvider } from "@xyflow/react";
import { renderWithProviders } from "../../../../../test/testUtils";
import { StepNodeRenderer, TerminalNode, buildNodeErrorMap, type StepNodeData } from "./StepNode";
import type { ValidationError } from "../core/validator";

function baseData(overrides: Partial<StepNodeData> = {}): StepNodeData {
  return {
    id: "step1",
    label: "Write outline",
    mode: "auto",
    inputs: [],
    outputs: [],
    transitions: [],
    hasError: false,
    errorMessages: [],
    predecessorIds: [],
    outputLabels: {},
    nodeWidth: 148,
    visual: { x: 0, y: 0 },
    onResizeEnd: vi.fn(),
    onResizeDrag: vi.fn(() => ({ width: 148 })),
    getZoom: () => 1,
    ...overrides,
  };
}

function renderNode(data: StepNodeData, selected = false) {
  const props = { data, selected } as unknown as NodeProps;
  return renderWithProviders(
    <ReactFlowProvider>
      <StepNodeRenderer {...props} />
    </ReactFlowProvider>,
  );
}

describe("StepNodeRenderer", () => {
  it("renders the step id and label", () => {
    renderNode(baseData());
    expect(screen.getByText("step1")).toBeInTheDocument();
    expect(screen.getByText("Write outline")).toBeInTheDocument();
  });

  it("hides the step id when it is a hidden placeholder id", () => {
    renderNode(baseData({ id: ".hid-abc123" }));
    expect(screen.queryByText(".hid-abc123")).not.toBeInTheDocument();
  });

  it("shows the choice badge for choice-routed nodes with multiple transitions", () => {
    const { container } = renderNode(baseData({
      route: "choice",
      transitions: [{ to: "a" }, { to: "b" }],
    }));
    expect(container.querySelector(".step-node-badge--choice")).toBeInTheDocument();
    expect(container.querySelector(".step-node-badge--parallel")).not.toBeInTheDocument();
  });

  it("shows the parallel badge when route is 'all' with multiple transitions", () => {
    const { container } = renderNode(baseData({
      route: "all",
      transitions: [{ to: "a" }, { to: "b" }],
    }));
    expect(container.querySelector(".step-node-badge--parallel")).toBeInTheDocument();
  });

  it("shows the skip badge when skipIf is set", () => {
    const { container } = renderNode(baseData({
      skipIf: { material: "outline" },
    }));
    expect(container.querySelector(".step-node-badge--skip")).toBeInTheDocument();
  });

  it("applies the has-error class and shows the tooltip content when hasError is true", () => {
    const { container } = renderNode(baseData({
      hasError: true,
      errorMessages: ["Missing input"],
    }));
    expect(container.querySelector(".step-node.has-error")).toBeInTheDocument();
  });
});

describe("TerminalNode", () => {
  it("renders the start label with a source handle only", () => {
    const props = { data: { type: "start" } } as unknown as NodeProps;
    const { container } = renderWithProviders(
      <ReactFlowProvider><TerminalNode {...props} /></ReactFlowProvider>,
    );
    expect(container.querySelector(".terminal-node--start")).toBeInTheDocument();
    expect(screen.getByText("selfEvolutionRun.stepNodeStart")).toBeInTheDocument();
  });

  it("renders the end label with a target handle only", () => {
    const props = { data: { type: "end" } } as unknown as NodeProps;
    const { container } = renderWithProviders(
      <ReactFlowProvider><TerminalNode {...props} /></ReactFlowProvider>,
    );
    expect(container.querySelector(".terminal-node--end")).toBeInTheDocument();
    expect(screen.getByText("selfEvolutionRun.stepNodeEnd")).toBeInTheDocument();
  });
});

describe("buildNodeErrorMap", () => {
  it("groups error messages by nodeId and ignores global errors", () => {
    const errors: ValidationError[] = [
      { code: "missing_input", message: "A", nodeId: "step1" },
      { code: "missing_output", message: "B", nodeId: "step1" },
      { code: "cycle", message: "C", nodeId: "step2" },
      { code: "global_issue", message: "D" },
    ];
    const map = buildNodeErrorMap(errors);
    expect(map.get("step1")).toEqual(["A", "B"]);
    expect(map.get("step2")).toEqual(["C"]);
    expect(map.has(undefined as unknown as string)).toBe(false);
  });

  it("returns an empty map for no errors", () => {
    expect(buildNodeErrorMap([]).size).toBe(0);
  });
});
