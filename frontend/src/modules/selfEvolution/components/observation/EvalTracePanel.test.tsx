import { describe, expect, it } from "vitest";
import { renderWithProviders, screen, fireEvent } from "@/test/testUtils";
import { EvalTracePanel } from "./EvalTracePanel";
import type { TraceDetailObservation, TraceNode } from "../TraceObservationView";
import type { CsvBadcaseRow } from "./types";

function makeNode(overrides: Partial<TraceNode> = {}): TraceNode {
  return {
    id: "root",
    name: "Root",
    type: "flow",
    status: "success",
    latencyMs: 100,
    children: [],
    ...overrides,
  };
}

function makeDetail(overrides: Partial<TraceDetailObservation> = {}): TraceDetailObservation {
  return {
    traceId: "trace-1234567890abcdef",
    query: "what is rag",
    status: "success",
    summary: {
      status: "success",
      latencyMs: 500,
      roundCount: 1,
      toolCallCount: 1,
      retrievalCount: 1,
      rerankCount: 0,
      nodeCount: 1,
    },
    root: makeNode({
      children: [
        makeNode({
          id: "tool-1",
          name: "kb_search",
          type: "tool",
          status: "success",
          latencyMs: 50,
          output: {
            summary: "found docs",
            data: { items: [{ title: "Doc A", score: 0.9, text: "content", ref: "chunk-1" }] },
          },
        }),
      ],
    }),
    ...overrides,
  };
}

function makeRow(overrides: Partial<CsvBadcaseRow> = {}): CsvBadcaseRow {
  return {
    caseId: "case-1",
    query: "what is rag",
    reference: "ref",
    answer: "answer",
    score: 0.8,
    questionType: "single_hop",
    failureType: "none",
    failureTone: "blue",
    defect: "",
    reason: "",
    mode: "agentic_rag",
    traceId: "trace-1234567890abcdef",
    traceStatus: "success",
    failureReason: "no issue found",
    ...overrides,
  };
}

describe("EvalTracePanel", () => {
  it("renders the trace title with the case id and key meta cards", () => {
    renderWithProviders(<EvalTracePanel detail={makeDetail()} selectedRow={makeRow()} />);
    expect(
      screen.getByText("selfEvolutionRun.observation.agenticTraceCardTitle"),
    ).toBeInTheDocument();
  });

  it("shows the low-score label when the row score is below 0.5", () => {
    renderWithProviders(<EvalTracePanel detail={makeDetail()} selectedRow={makeRow({ score: 0.2 })} />);
    expect(screen.getByText("selfEvolutionRun.observation.lowScore")).toBeInTheDocument();
  });

  it("shows the empty inspector hint before a flow step is selected", () => {
    renderWithProviders(<EvalTracePanel detail={makeDetail()} selectedRow={makeRow()} />);
    expect(screen.getByText("selfEvolutionRun.observation.inspectorEmptyHint")).toBeInTheDocument();
  });

  it("selects a flow step and shows its inspector details, including retrieved docs", () => {
    renderWithProviders(<EvalTracePanel detail={makeDetail()} selectedRow={makeRow()} />);
    fireEvent.click(screen.getByText("Tool Call: kb_search"));
    expect(
      screen.getByText("selfEvolutionRun.observation.inspectorNodeTitle"),
    ).toBeInTheDocument();
    expect(screen.getByText(/Doc #1 Doc A/)).toBeInTheDocument();
  });

  it("shows the failure reason from the selected row in the observation section once a node is selected", () => {
    renderWithProviders(
      <EvalTracePanel detail={makeDetail()} selectedRow={makeRow({ failureReason: "retrieval missed key doc" })} />,
    );
    fireEvent.click(screen.getByText("Tool Call: kb_search"));
    expect(screen.getByText("retrieval missed key doc")).toBeInTheDocument();
  });
});
