import { describe, expect, it } from "vitest";
import { renderWithProviders, screen } from "@/test/testUtils";
import { TraceComparePanel } from "./TraceComparePanel";
import type { TraceDetailObservation, TraceObservation } from "./types";

function makeDetail(overrides: Partial<TraceDetailObservation> = {}): TraceDetailObservation {
  return {
    traceId: "trace-baseline-1234567890",
    query: "compare query",
    status: "success",
    summary: {
      status: "success",
      latencyMs: 1000,
      roundCount: 1,
      toolCallCount: 1,
      retrievalCount: 1,
      rerankCount: 0,
      nodeCount: 1,
    },
    root: { id: "root", name: "Root", type: "flow", status: "success", children: [] },
    ...overrides,
  };
}

describe("TraceComparePanel", () => {
  it("renders the title, main query and both trace ids", () => {
    const observation: Extract<TraceObservation, { kind: "compare" }> = {
      kind: "compare",
      query: "compare query",
      a: makeDetail({ traceId: "trace-a-1234567890", query: "" }),
      b: makeDetail({
        traceId: "trace-b-1234567890",
        query: "",
        summary: { status: "success", latencyMs: 2000, nodeCount: 1 },
      }),
    };
    renderWithProviders(<TraceComparePanel observation={observation} title="Compare Panel" />);
    expect(screen.getByText("Compare Panel")).toBeInTheDocument();
    expect(screen.getByText("compare query")).toBeInTheDocument();
  });

  it("renders a delta row reflecting the latency difference between a and b", () => {
    const observation: Extract<TraceObservation, { kind: "compare" }> = {
      kind: "compare",
      query: "q",
      a: makeDetail({ summary: { status: "success", latencyMs: 1000, nodeCount: 1 } }),
      b: makeDetail({ summary: { status: "success", latencyMs: 1500, nodeCount: 1 } }),
    };
    renderWithProviders(<TraceComparePanel observation={observation} title="Compare" />);
    expect(document.querySelector(".self-evolution-trace-compare-grid")?.textContent).toContain("+500ms");
  });

  it("renders both compact detail panels side by side", () => {
    const observation: Extract<TraceObservation, { kind: "compare" }> = {
      kind: "compare",
      query: "q",
      a: makeDetail(),
      b: makeDetail(),
    };
    renderWithProviders(<TraceComparePanel observation={observation} title="Compare" />);
    expect(document.querySelectorAll(".self-evolution-trace-detail-card.is-compact")).toHaveLength(2);
  });
});
