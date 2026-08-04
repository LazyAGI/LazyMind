import { describe, expect, it } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import { AbtestComparisonPanel } from "./AbtestComparisonPanel";
import type { AbtestComparisonArtifact, AbtestComparisonCaseRow } from "../../shared/abtestComparison";

function makeCaseRow(overrides: Partial<AbtestComparisonCaseRow> = {}): AbtestComparisonCaseRow {
  return {
    key: `case-${Math.random()}`,
    caseId: "case-1",
    originOverall: 0.5,
    candidateOverall: 0.6,
    deltaOverall: 0.1,
    originCorrectness: 0.5,
    candidateCorrectness: 0.6,
    ...overrides,
  };
}

function makeArtifact(overrides: Partial<AbtestComparisonArtifact> = {}): AbtestComparisonArtifact {
  return {
    runId: "run-1",
    algoId: "algo-a",
    candidateAlgoId: "algo-b",
    status: "done",
    verdict: "pass",
    reasons: [],
    metricRows: [{ key: "avg_correctness", label: "correctness", origin: 0.5, candidate: 0.6, delta: 0.1 }],
    caseRows: [makeCaseRow()],
    ...overrides,
  };
}

describe("AbtestComparisonPanel", () => {
  it("renders the run id, algo ids and case count", () => {
    renderWithProviders(<AbtestComparisonPanel artifact={makeArtifact()} />);
    expect(screen.getByText("run-1")).toBeInTheDocument();
    expect(screen.getByText("algo-a")).toBeInTheDocument();
    expect(screen.getByText("algo-b")).toBeInTheDocument();
  });

  it("renders the reasons alert only when reasons are present", () => {
    const { rerender } = renderWithProviders(
      <AbtestComparisonPanel artifact={makeArtifact({ reasons: ["latency regressed"] })} />,
    );
    expect(screen.getByText("latency regressed")).toBeInTheDocument();

    rerender(<AbtestComparisonPanel artifact={makeArtifact({ reasons: [] })} />);
    expect(document.querySelector(".self-evolution-abtest-comparison-reasons")).not.toBeInTheDocument();
  });

  it("renders case rows and a dash when the reason is missing", () => {
    renderWithProviders(
      <AbtestComparisonPanel artifact={makeArtifact({ caseRows: [makeCaseRow({ caseId: "case-77", reason: undefined })] })} />,
    );
    expect(screen.getByText("case-77")).toBeInTheDocument();
  });

  it("shows pagination controls only when there are more than one page of cases", () => {
    const manyCases = Array.from({ length: 12 }, (_, i) => makeCaseRow({ key: `c${i}`, caseId: `case-${i}` }));
    renderWithProviders(<AbtestComparisonPanel artifact={makeArtifact({ caseRows: manyCases })} />);
    expect(screen.getByText("case-0")).toBeInTheDocument();
    fireEvent.click(screen.getByText("selfEvolutionRun.abtestStreamingNextPage"));
    expect(screen.getByText("case-10")).toBeInTheDocument();
    expect(screen.queryByText("case-0")).not.toBeInTheDocument();
  });

  it("does not show pagination for a single page of cases", () => {
    renderWithProviders(<AbtestComparisonPanel artifact={makeArtifact()} />);
    expect(screen.queryByText("selfEvolutionRun.abtestStreamingNextPage")).not.toBeInTheDocument();
  });
});
