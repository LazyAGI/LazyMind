import { describe, expect, it } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import { DatasetStreamingTable } from "./DatasetStreamingTable";
import type { DatasetStreamingRow } from "../../hooks/controller/types";

function makeRows(count: number): DatasetStreamingRow[] {
  return Array.from({ length: count }, (_, i) => ({
    key: `row-${i}`,
    caseId: `case-${i}`,
  }));
}

describe("DatasetStreamingTable", () => {
  it("shows the waiting label when total is 0", () => {
    renderWithProviders(<DatasetStreamingTable rows={[]} current={0} total={0} />);
    expect(screen.getByText("selfEvolutionRun.datasetStreamingWaiting")).toBeInTheDocument();
  });

  it("renders the progress text using the raw current/total (unlike other streaming tables)", () => {
    renderWithProviders(<DatasetStreamingTable rows={makeRows(4)} current={2} total={4} />);
    expect(screen.getByText("selfEvolutionRun.datasetStreamingProgress")).toBeInTheDocument();
  });

  it("renders the empty locale text when there are no rows", () => {
    renderWithProviders(<DatasetStreamingTable rows={[]} current={0} total={0} />);
    expect(screen.getByText("selfEvolutionRun.datasetStreamingEmpty")).toBeInTheDocument();
  });

  it("paginates across multiple pages of rows", () => {
    renderWithProviders(<DatasetStreamingTable rows={makeRows(15)} current={0} total={15} />);
    expect(screen.getByText("case-0")).toBeInTheDocument();
    fireEvent.click(screen.getByText("selfEvolutionRun.datasetStreamingNextPage"));
    expect(screen.getByText("case-10")).toBeInTheDocument();
  });
});
