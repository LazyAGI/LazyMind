import { describe, expect, it } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import { AnalysisStreamingTable } from "./AnalysisStreamingTable";
import type { AnalysisStreamingRow } from "../../hooks/controller/types";

function makeRows(count: number): AnalysisStreamingRow[] {
  return Array.from({ length: count }, (_, i) => ({
    key: `row-${i}`,
    caseId: `case-${i}`,
  }));
}

describe("AnalysisStreamingTable", () => {
  it("shows the waiting label when there are no rows and total is 0", () => {
    renderWithProviders(<AnalysisStreamingTable rows={[]} current={0} total={0} />);
    expect(screen.getByText("selfEvolutionRun.analysisStreamingWaiting")).toBeInTheDocument();
  });

  it("renders the progress text with current/total counts", () => {
    renderWithProviders(<AnalysisStreamingTable rows={makeRows(4)} current={2} total={4} />);
    expect(screen.getByText("selfEvolutionRun.analysisStreamingProgress")).toBeInTheDocument();
  });

  it("paginates rows and updates the page indicator", () => {
    // current=0 keeps the initial page at 1 (the auto-advance effect only fires
    // when current increases past its previous value).
    renderWithProviders(<AnalysisStreamingTable rows={makeRows(15)} current={0} total={15} />);
    expect(screen.getByText("selfEvolutionRun.analysisStreamingPageIndicator")).toBeInTheDocument();
    expect(screen.getByText("case-0")).toBeInTheDocument();
    fireEvent.click(screen.getByText("selfEvolutionRun.analysisStreamingNextPage"));
    expect(screen.getByText("case-10")).toBeInTheDocument();
  });

  it("disables the next page button on the last page", () => {
    renderWithProviders(<AnalysisStreamingTable rows={makeRows(15)} current={0} total={15} />);
    fireEvent.click(screen.getByText("selfEvolutionRun.analysisStreamingNextPage"));
    expect(screen.getByText("selfEvolutionRun.analysisStreamingNextPage")).toBeDisabled();
  });
});
