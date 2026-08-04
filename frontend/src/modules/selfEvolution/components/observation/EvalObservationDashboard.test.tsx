import { describe, expect, it, vi } from "vitest";

vi.mock("@/components/request", () => ({
  BASE_URL: "https://example.com",
  axiosInstance: { get: vi.fn(), post: vi.fn() },
  localizeErrorCode: (code?: string) => code || "",
}));

import { renderWithProviders, screen, waitFor } from "@/test/testUtils";
import { EvalObservationDashboard } from "./EvalObservationDashboard";

function makeGateEvalData(overrides: Record<string, unknown> = {}) {
  return {
    run_id: "run-1",
    algo_id: "algo-a",
    avg_correctness: 0.9,
    correct_rate: 0.9,
    case_num: 1,
    cases: [
      {
        case_id: "case-1",
        query: "what is rag",
        score: 0.8,
        trace_id: "-",
      },
    ],
    ...overrides,
  };
}

describe("EvalObservationDashboard", () => {
  it("renders the report summary and an empty trace state when there is no threadId", async () => {
    const onBack = vi.fn();
    renderWithProviders(<EvalObservationDashboard data={makeGateEvalData()} onBack={onBack} />);
    expect(screen.getByText("selfEvolutionRun.observation.evalReportTitle")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByText("case-1")).toBeInTheDocument();
    });
  });

  it("shows the notice alert when a notice is provided", () => {
    renderWithProviders(
      <EvalObservationDashboard data={makeGateEvalData()} notice="stale data" onBack={vi.fn()} />,
    );
    expect(screen.getByText("stale data")).toBeInTheDocument();
  });

  it("renders the thread id tag when threadId is provided", () => {
    renderWithProviders(
      <EvalObservationDashboard data={makeGateEvalData()} threadId="thread-42" onBack={vi.fn()} />,
    );
    expect(screen.getByText("thread thread-42")).toBeInTheDocument();
  });

  it("shows an empty observation message when there are no rows at all", () => {
    renderWithProviders(
      <EvalObservationDashboard data={makeGateEvalData({ cases: [] })} onBack={vi.fn()} />,
    );
    expect(screen.getAllByText("selfEvolutionRun.observation.emptyObservation").length).toBeGreaterThan(0);
  });
});
