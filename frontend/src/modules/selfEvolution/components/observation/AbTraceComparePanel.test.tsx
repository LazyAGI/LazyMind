import { describe, expect, it, vi } from "vitest";
import { renderWithProviders, screen } from "@/test/testUtils";
import { AbTraceComparePanel } from "./AbTraceComparePanel";
import type { AbCaseRow, AbCompareObservation } from "./types";
import type { TraceDetailObservation, TraceNode } from "../TraceObservationView";

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
    query: "",
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
    root: makeNode(),
    ...overrides,
  };
}

function makeObservation(overrides: Partial<AbCompareObservation> = {}): AbCompareObservation {
  return {
    kind: "compare",
    query: "what is rag",
    a: makeDetail(),
    b: makeDetail(),
    ...overrides,
  } as AbCompareObservation;
}

function makeCaseRow(overrides: Partial<AbCaseRow> = {}): AbCaseRow {
  return {
    caseId: "case-1",
    query: "what is rag",
    aScore: 0.5,
    bScore: 0.6,
    delta: 0.1,
    conclusion: "improve",
    tone: "up",
    ...overrides,
  };
}

describe("AbTraceComparePanel", () => {
  it("shows a loading spinner when loading is true", () => {
    renderWithProviders(<AbTraceComparePanel selectedCase={makeCaseRow()} loading />);
    expect(screen.getByText("selfEvolutionRun.observation.loadingAbTrace")).toBeInTheDocument();
  });

  it("shows an empty state with retry when there is an error and no observation", () => {
    const onRetry = vi.fn();
    renderWithProviders(
      <AbTraceComparePanel selectedCase={makeCaseRow()} error="failed to load" onRetry={onRetry} />,
    );
    expect(screen.getByText("failed to load")).toBeInTheDocument();
    expect(screen.getByText("selfEvolutionRun.observation.retry")).toBeInTheDocument();
  });

  it("renders the compare panel title with the case id and both trace columns", () => {
    renderWithProviders(
      <AbTraceComparePanel observation={makeObservation()} selectedCase={makeCaseRow({ caseId: "case-42" })} />,
    );
    expect(
      screen.getByText("selfEvolutionRun.observation.abComparePanelTitle"),
    ).toBeInTheDocument();
    expect(screen.getByText("selfEvolutionRun.observation.abBaselineTitle")).toBeInTheDocument();
    expect(screen.getByText("selfEvolutionRun.observation.abOptimizedTitle")).toBeInTheDocument();
  });

  it("renders the abtestId as a shortened report id tag when it is long", () => {
    renderWithProviders(
      <AbTraceComparePanel
        observation={makeObservation()}
        selectedCase={makeCaseRow()}
        abtestId="abtest-1234567890123456789"
      />,
    );
    expect(screen.getByText(/Report ID: abtest-1\.\.\./)).toBeInTheDocument();
  });
});
