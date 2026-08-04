import { describe, expect, it } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import { RepairTraceStreamPanel } from "./RepairTraceStreamPanel";
import type { RepairTraceRow } from "../../shared/repairTrace";

function makeRow(overrides: Partial<RepairTraceRow> = {}): RepairTraceRow {
  return {
    key: "row-1",
    eventType: "repair.attempt_started",
    category: "attempt",
    action: "running",
    statusLabel: "Running",
    attempt: 1,
    title: "Attempt started",
    chips: [],
    order: 0,
    ...overrides,
  };
}

describe("RepairTraceStreamPanel", () => {
  it("shows the waiting text when there are no rows", () => {
    renderWithProviders(<RepairTraceStreamPanel rows={[]} />);
    expect(screen.getByText("selfEvolutionRun.repairTraceWaiting")).toBeInTheDocument();
    expect(screen.getByText("selfEvolutionRun.repairTraceEmpty")).toBeInTheDocument();
  });

  it("groups rows by attempt and auto-expands the active attempt group", () => {
    const rows = [
      makeRow({ key: "r1", attempt: 1, action: "done", eventType: "repair.attempt_completed" }),
      makeRow({ key: "r2", attempt: 1 }),
    ];
    renderWithProviders(<RepairTraceStreamPanel rows={rows} />);
    // The attempt group label is built via the real (non-mocked) i18n instance
    // in shared/repairTrace.ts, so it renders actual zh-CN text, not the raw key.
    expect(screen.getByText("第 1 轮修复")).toBeInTheDocument();
    expect(screen.getAllByText("Attempt started").length).toBeGreaterThan(0);
  });

  it("toggles the attempt group when its header is clicked", () => {
    const rows = [makeRow()];
    renderWithProviders(<RepairTraceStreamPanel rows={rows} />);
    const header = screen.getByText("第 1 轮修复").closest("button")!;
    expect(header).toHaveAttribute("aria-expanded", "true");
    fireEvent.click(header);
    expect(header).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText("Attempt started")).not.toBeInTheDocument();
  });

  it("renders row chips and case id when present", () => {
    const rows = [makeRow({ caseId: "case-42", chips: ["tool: search"] })];
    renderWithProviders(<RepairTraceStreamPanel rows={rows} />);
    expect(screen.getByText("case-42")).toBeInTheDocument();
    expect(screen.getByText("tool: search")).toBeInTheDocument();
  });

  it("shows the progress summary reflecting running/done counts", () => {
    const rows = [
      makeRow({ key: "r1", action: "done" }),
      makeRow({ key: "r2", action: "running" }),
    ];
    renderWithProviders(<RepairTraceStreamPanel rows={rows} />);
    expect(screen.getByText("selfEvolutionRun.repairTraceProgress")).toBeInTheDocument();
  });
});
