import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import { AbReportPanel } from "./AbReportPanel";
import type { AbCaseRow } from "./types";
import type { AbtestComparisonArtifact } from "../../shared/abtestComparison";

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

function makeComparisonArtifact(overrides: Partial<AbtestComparisonArtifact> = {}): AbtestComparisonArtifact {
  return {
    runId: "run-1",
    algoId: "algo-a",
    candidateAlgoId: "algo-b",
    status: "done",
    verdict: "pass",
    reasons: [],
    metricRows: [{ key: "avg_overall", label: "overall", origin: 0.5, candidate: 0.6, delta: 0.1 }],
    caseRows: [],
    ...overrides,
  };
}

const baseProps = {
  rows: [makeCaseRow()],
  selectedCaseId: "case-1",
  onSelectCase: vi.fn(),
  onReloadRows: vi.fn(),
};

describe("AbReportPanel", () => {
  it("renders run id, algo ids and case count from the comparison artifact", () => {
    renderWithProviders(<AbReportPanel {...baseProps} comparisonArtifact={makeComparisonArtifact()} />);
    expect(screen.getByText("run-1")).toBeInTheDocument();
    expect(screen.getByText("algo-a")).toBeInTheDocument();
    expect(screen.getByText("algo-b")).toBeInTheDocument();
  });

  it("shows the highlight metric card for the overall metric", () => {
    renderWithProviders(<AbReportPanel {...baseProps} comparisonArtifact={makeComparisonArtifact()} />);
    expect(screen.getAllByText("50.0%").length).toBeGreaterThan(0);
    expect(screen.getAllByText("60.0%").length).toBeGreaterThan(0);
  });

  it("renders the reasons alert only when reasons are present", () => {
    const { rerender } = renderWithProviders(
      <AbReportPanel {...baseProps} comparisonArtifact={makeComparisonArtifact({ reasons: ["latency regressed"] })} />,
    );
    expect(screen.getByText("latency regressed")).toBeInTheDocument();

    rerender(<AbReportPanel {...baseProps} comparisonArtifact={makeComparisonArtifact({ reasons: [] })} />);
    expect(document.querySelector(".self-evolution-abtest-comparison-reasons")).not.toBeInTheDocument();
  });

  it("calls onSelectCase when clicking the view trace button for a case row", () => {
    const onSelectCase = vi.fn();
    renderWithProviders(<AbReportPanel {...baseProps} onSelectCase={onSelectCase} />);
    fireEvent.click(screen.getByText("selfEvolutionRun.observation.viewAbTrace"));
    expect(onSelectCase).toHaveBeenCalledWith("case-1");
  });

  it("shows an error alert with a retry action when rowsError is set", () => {
    const onReloadRows = vi.fn();
    renderWithProviders(<AbReportPanel {...baseProps} rowsError="failed to load" onReloadRows={onReloadRows} />);
    expect(screen.getByText("failed to load")).toBeInTheDocument();
    fireEvent.click(screen.getByText("selfEvolutionRun.observation.retry"));
    expect(onReloadRows).toHaveBeenCalledTimes(1);
  });

  it("falls back to summary metrics when no comparison artifact is provided", () => {
    renderWithProviders(
      <AbReportPanel
        {...baseProps}
        summary={{
          id: "run-2",
          verdict: "pass",
          reasons: [],
          metricRows: [{ metric: "avg_correctness", metricLabel: "correctness", meanA: 0.4, meanB: 0.5, winRateB: 0.5 }],
        } as never}
      />,
    );
    expect(screen.getByText("run-2")).toBeInTheDocument();
  });
});
