import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import StateGraphView, { type StateGraphData } from "./StateGraphView";

function buildData(overrides: Partial<StateGraphData> = {}): StateGraphData {
  return {
    nodes: [
      { id: "__start__", label: "__start__", step_index: 0, status: "succeeded", is_current: false },
      { id: "step-1", label: "Step One", step_index: 1, status: "succeeded", is_current: false },
      { id: "step-2", label: "Step Two", step_index: 2, status: "running", is_current: true },
      { id: "__end__", label: "__end__", step_index: 3, status: "pending", is_current: false },
    ],
    edges: [
      { from: "__start__", to: "step-1", condition: "", edge_type: "executed" },
      { from: "step-1", to: "step-2", condition: "", edge_type: "current_direct" },
      { from: "step-2", to: "__end__", condition: "", edge_type: "inactive" },
    ],
    initial: "__start__",
    ...overrides,
  };
}

describe("StateGraphView", () => {
  it("renders an svg graph with a legend and one node per data entry", () => {
    render(<StateGraphView data={buildData()} />);

    expect(screen.getByLabelText("Workflow graph")).toBeInTheDocument();
    expect(screen.getByText("Step One")).toBeInTheDocument();
    expect(screen.getByText("Step Two")).toBeInTheDocument();
  });

  it("renders without crashing for a single-node graph with no edges", () => {
    const data: StateGraphData = {
      nodes: [{ id: "__start__", label: "__start__", step_index: 0, status: "succeeded", is_current: false }],
      edges: [],
      initial: "__start__",
    };
    render(<StateGraphView data={data} />);
    expect(screen.getByLabelText("Workflow graph")).toBeInTheDocument();
  });

  it("drops self-loop and dangling edges without throwing", () => {
    const data = buildData({
      edges: [
        { from: "step-1", to: "step-1", condition: "", edge_type: "executed" },
        { from: "step-1", to: "unknown-node", condition: "", edge_type: "executed" },
      ],
    });
    render(<StateGraphView data={data} />);
    expect(screen.getByLabelText("Workflow graph")).toBeInTheDocument();
  });
});
