import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import { EvalReportPanel } from "./EvalReportPanel";
import type { CsvBadcaseRow, EvalReportSummary } from "./types";

function makeSummary(overrides: Partial<EvalReportSummary> = {}): EvalReportSummary {
  return {
    reportId: "report-1",
    dataset: "dataset-a",
    correctRate: 0.8,
    badCaseCount: 2,
    traceCoverageRate: 0.9,
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
    defect: "some defect",
    reason: "some reason",
    mode: "agentic_rag",
    traceId: "trace-1",
    traceStatus: "linked",
    failureReason: "no issue",
    ...overrides,
  };
}

const baseProps = {
  summary: makeSummary(),
  rows: [makeRow()],
  selectedCaseId: "case-1",
  onSelectCase: vi.fn(),
  onReloadRows: vi.fn(),
};

describe("EvalReportPanel", () => {
  it("renders the report title and metric cards", () => {
    renderWithProviders(<EvalReportPanel {...baseProps} />);
    expect(screen.getByText("selfEvolutionRun.observation.evalReportTitle")).toBeInTheDocument();
    expect(screen.getByText("80.0%")).toBeInTheDocument();
    expect(screen.getByText("2")).toBeInTheDocument();
  });

  it("renders the selected case result details", () => {
    renderWithProviders(<EvalReportPanel {...baseProps} />);
    expect(screen.getByText("no issue")).toBeInTheDocument();
    expect(screen.getAllByText("some defect").length).toBeGreaterThan(0);
  });

  it("filters rows by search keyword", () => {
    const rows = [makeRow({ caseId: "case-1", query: "alpha" }), makeRow({ caseId: "case-2", query: "beta" })];
    renderWithProviders(<EvalReportPanel {...baseProps} rows={rows} />);
    fireEvent.change(screen.getByPlaceholderText("selfEvolutionRun.observation.searchCasePlaceholder"), {
      target: { value: "beta" },
    });
    expect(screen.getByText("case-2")).toBeInTheDocument();
    expect(screen.queryByText("case-1")).not.toBeInTheDocument();
  });

  it("resets filters when the reset button is clicked", () => {
    const rows = [makeRow({ caseId: "case-1", query: "alpha" }), makeRow({ caseId: "case-2", query: "beta" })];
    renderWithProviders(<EvalReportPanel {...baseProps} rows={rows} />);
    fireEvent.change(screen.getByPlaceholderText("selfEvolutionRun.observation.searchCasePlaceholder"), {
      target: { value: "beta" },
    });
    fireEvent.click(screen.getByText("selfEvolutionRun.observation.reset"));
    expect(screen.getByText("case-1")).toBeInTheDocument();
    expect(screen.getByText("case-2")).toBeInTheDocument();
  });

  it("calls onSelectCase when clicking on a row", () => {
    const onSelectCase = vi.fn();
    renderWithProviders(<EvalReportPanel {...baseProps} onSelectCase={onSelectCase} />);
    fireEvent.click(screen.getByText("case-1"));
    expect(onSelectCase).toHaveBeenCalledWith("case-1");
  });

  it("shows an error alert with a retry button when rowsError is set", () => {
    const onReloadRows = vi.fn();
    renderWithProviders(
      <EvalReportPanel {...baseProps} rowsError="failed to load rows" onReloadRows={onReloadRows} />,
    );
    expect(screen.getByText("failed to load rows")).toBeInTheDocument();
    fireEvent.click(screen.getByText("selfEvolutionRun.observation.retry"));
    expect(onReloadRows).toHaveBeenCalledTimes(1);
  });
});
