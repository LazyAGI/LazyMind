import { describe, expect, it } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import { EvalStreamingTable } from "./EvalStreamingTable";
import type { EvalStreamingRow } from "../../hooks/controller/types";

function makeRows(count: number): EvalStreamingRow[] {
  return Array.from({ length: count }, (_, i) => ({
    key: `row-${i}`,
    caseId: `case-${i}`,
    answerStatus: i === 0 ? "done" : undefined,
  }));
}

describe("EvalStreamingTable", () => {
  it("shows the waiting label when total is 0 and no rows exist", () => {
    renderWithProviders(<EvalStreamingTable rows={[]} current={0} total={0} />);
    expect(screen.getByText("selfEvolutionRun.evalStreamingWaiting")).toBeInTheDocument();
  });

  it("renders the progress text with current/total counts", () => {
    renderWithProviders(<EvalStreamingTable rows={makeRows(3)} current={1} total={5} />);
    expect(
      screen.getByText("selfEvolutionRun.evalStreamingProgress"),
    ).toBeInTheDocument();
  });

  it("renders the empty locale text when there are no rows", () => {
    renderWithProviders(<EvalStreamingTable rows={[]} current={0} total={0} />);
    expect(screen.getByText("selfEvolutionRun.evalStreamingEmpty")).toBeInTheDocument();
  });

  it("disables the next page button when there is only a single page of rows", () => {
    renderWithProviders(<EvalStreamingTable rows={makeRows(3)} current={0} total={3} />);
    expect(screen.getByText("selfEvolutionRun.evalStreamingNextPage")).toBeDisabled();
  });

  it("renders pagination controls and navigates to the next page", () => {
    // current=0 keeps the auto-scroll-to-active-page effect on page 1 initially
    // (only the doneCount-derived progress, which is 1 here, would push it forward).
    renderWithProviders(<EvalStreamingTable rows={makeRows(15)} current={0} total={15} />);
    const nextButton = screen.getByText("selfEvolutionRun.evalStreamingNextPage");
    expect(nextButton).toBeInTheDocument();
    expect(screen.getByText("case-0")).toBeInTheDocument();
    fireEvent.click(nextButton);
    expect(screen.getByText("case-10")).toBeInTheDocument();
  });

  it("disables the prev button on the first page", () => {
    renderWithProviders(<EvalStreamingTable rows={makeRows(15)} current={0} total={15} />);
    expect(screen.getByText("selfEvolutionRun.evalStreamingPrevPage")).toBeDisabled();
  });
});
