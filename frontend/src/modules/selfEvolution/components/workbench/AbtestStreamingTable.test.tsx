import { describe, expect, it } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import { AbtestStreamingTable } from "./AbtestStreamingTable";
import type { AbtestStreamingRow } from "../../hooks/controller/types";

function makeRows(count: number): AbtestStreamingRow[] {
  return Array.from({ length: count }, (_, i) => ({
    key: `row-${i}`,
    caseId: `case-${i}`,
  }));
}

describe("AbtestStreamingTable", () => {
  it("shows the waiting label when there are no rows and total is 0", () => {
    renderWithProviders(<AbtestStreamingTable rows={[]} current={0} total={0} />);
    expect(screen.getByText("selfEvolutionRun.abtestStreamingWaiting")).toBeInTheDocument();
  });

  it("renders the progress text using the done count when higher than current", () => {
    const rows: AbtestStreamingRow[] = [
      { key: "r1", caseId: "c1", judgeStatus: "done" },
      { key: "r2", caseId: "c2" },
    ];
    renderWithProviders(<AbtestStreamingTable rows={rows} current={0} total={2} />);
    expect(screen.getByText("selfEvolutionRun.abtestStreamingProgress")).toBeInTheDocument();
  });

  it("paginates rows across multiple pages", () => {
    // current=0 avoids the auto-scroll-to-active-page effect jumping past page 1.
    renderWithProviders(<AbtestStreamingTable rows={makeRows(15)} current={0} total={15} />);
    expect(screen.getByText("case-0")).toBeInTheDocument();
    fireEvent.click(screen.getByText("selfEvolutionRun.abtestStreamingNextPage"));
    expect(screen.getByText("case-10")).toBeInTheDocument();
    expect(screen.queryByText("case-0")).not.toBeInTheDocument();
  });
});
