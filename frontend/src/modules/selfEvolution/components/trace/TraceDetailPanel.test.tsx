import { describe, expect, it } from "vitest";
import { renderWithProviders, screen } from "@/test/testUtils";
import { TraceDetailPanel, TraceDetailWorkspace } from "./TraceDetailPanel";
import type { TraceDetailObservation, TraceNode } from "./types";

function makeNode(overrides: Partial<TraceNode> = {}): TraceNode {
  return {
    id: "root",
    name: "Root",
    type: "flow",
    status: "success",
    children: [],
    ...overrides,
  };
}

function makeDetail(overrides: Partial<TraceDetailObservation> = {}): TraceDetailObservation {
  return {
    traceId: "trace-abcdef1234567890",
    query: "how to build rag",
    status: "success",
    summary: {
      status: "success",
      latencyMs: 2500,
      roundCount: 3,
      toolCallCount: 2,
      retrievalCount: 1,
      rerankCount: 0,
      nodeCount: 2,
    },
    root: makeNode({
      children: [makeNode({ id: "child-1", name: "Tool call", type: "tool" })],
    }),
    ...overrides,
  };
}

describe("TraceDetailPanel", () => {
  it("renders the label, status tag and query", () => {
    renderWithProviders(<TraceDetailPanel detail={makeDetail()} label="Baseline" />);
    expect(screen.getByText("Baseline")).toBeInTheDocument();
    expect(screen.getByText("how to build rag")).toBeInTheDocument();
    expect(document.querySelector(".self-evolution-trace-detail-head .ant-tag")?.textContent).toBe("success");
  });

  it("renders per-type statistics for the flattened nodes", () => {
    renderWithProviders(<TraceDetailPanel detail={makeDetail()} label="Baseline" />);
    expect(screen.getByText("Flow 1")).toBeInTheDocument();
    expect(screen.getByText("Tool 1")).toBeInTheDocument();
  });

  it("applies the compact class when compact is true", () => {
    renderWithProviders(<TraceDetailPanel detail={makeDetail()} label="Baseline" compact />);
    expect(document.querySelector(".self-evolution-trace-detail-card.is-compact")).toBeInTheDocument();
  });
});

describe("TraceDetailWorkspace", () => {
  it("renders meta tiles, summary strip, flow panel and inspector panel", () => {
    renderWithProviders(<TraceDetailWorkspace detail={makeDetail()} title="Trace Detail" />);
    expect(screen.getByText("Trace Detail")).toBeInTheDocument();
    expect(document.querySelector(".self-evolution-trace-meta-grid")).toBeInTheDocument();
    expect(document.querySelector(".self-evolution-trace-summary-strip")).toBeInTheDocument();
    expect(document.querySelector(".self-evolution-trace-flow-panel")).toBeInTheDocument();
    expect(document.querySelector(".self-evolution-trace-inspector")).toBeInTheDocument();
  });
});
