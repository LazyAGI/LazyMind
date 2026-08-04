import { fireEvent, render, waitFor } from "@testing-library/react";
import { beforeAll, describe, expect, it } from "vitest";
import { Position, ReactFlow, ReactFlowProvider } from "@xyflow/react";
import type { Edge, Node } from "@xyflow/react";
import { TransitionEdge } from "./TransitionEdge";
import type { TransitionEdgeData } from "./TransitionEdge";

// jsdom does not implement SVG geometry APIs; TransitionEdge samples the
// rendered path to place the arrowhead, so stub deterministic values.
beforeAll(() => {
  const proto = SVGElement.prototype as unknown as {
    getTotalLength?: () => number;
    getPointAtLength?: () => DOMPoint;
  };
  proto.getTotalLength = () => 100;
  proto.getPointAtLength = () => ({ x: 100, y: 0 } as DOMPoint);
});

// React Flow only computes an edge's source/target positions once both nodes
// report explicit handle bounds, which jsdom's ResizeObserver-driven
// measurement never produces. Supplying `handles` directly sidesteps that.
const nodes: Node[] = [
  {
    id: "step1", position: { x: 0, y: 0 }, data: {}, width: 100, height: 50,
    handles: [{ id: null, type: "source", position: Position.Right, x: 100, y: 25, width: 1, height: 1 }],
  } as unknown as Node,
  {
    id: "step2", position: { x: 200, y: 0 }, data: {}, width: 100, height: 50,
    handles: [{ id: null, type: "target", position: Position.Left, x: 0, y: 25, width: 1, height: 1 }],
  } as unknown as Node,
];

function makeEdge(data: Partial<TransitionEdgeData> = {}): Edge {
  return {
    id: "step1->step2",
    source: "step1",
    target: "step2",
    type: "transition",
    data: { condition: "", hasError: false, ...data } as TransitionEdgeData,
  };
}

const edgeTypes = { transition: TransitionEdge };

function renderEdge(edge: Edge) {
  return render(
    <ReactFlowProvider>
      <ReactFlow nodes={nodes} edges={[edge]} edgeTypes={edgeTypes} />
    </ReactFlowProvider>,
  );
}

describe("TransitionEdge", () => {
  it("renders a visible path and a wider invisible hit-area path", () => {
    const { container } = renderEdge(makeEdge());
    expect(container.querySelector(".react-flow__edge-path")).toBeInTheDocument();
    expect(container.querySelector(".react-flow__edge-interaction")).toBeInTheDocument();
  });

  it("does not show the condition popover before hovering", () => {
    renderEdge(makeEdge({ condition: "outline is ready" }));
    expect(document.querySelector(".transition-edge-popover")).toBeNull();
  });

  it("shows the condition popover after hovering the hit area", async () => {
    const { container } = renderEdge(makeEdge({ condition: "outline is ready" }));
    const hitArea = container.querySelector(".react-flow__edge-interaction")!;
    fireEvent.mouseEnter(hitArea);
    // EdgeLabelRenderer portals the popover into a sibling node, not container.
    await waitFor(() => {
      expect(document.querySelector(".transition-edge-popover-text")?.textContent).toBe(
        "outline is ready",
      );
    });
  });

  it("applies the has-error style to the popover when hasError is true", async () => {
    const { container } = renderEdge(makeEdge({ condition: "bad condition", hasError: true }));
    const hitArea = container.querySelector(".react-flow__edge-interaction")!;
    fireEvent.mouseEnter(hitArea);
    await waitFor(() => {
      expect(document.querySelector(".transition-edge-popover-inner.has-error")).toBeInTheDocument();
    });
  });

  it("does not render the popover when there is no condition text", () => {
    const { container } = renderEdge(makeEdge({ condition: "" }));
    const hitArea = container.querySelector(".react-flow__edge-interaction")!;
    fireEvent.mouseEnter(hitArea);
    expect(document.querySelector(".transition-edge-popover")).toBeNull();
  });
});
