import { describe, expect, it, vi } from "vitest";

vi.mock("@/components/request", () => ({
  BASE_URL: "https://example.com",
  axiosInstance: { get: vi.fn(), post: vi.fn() },
  localizeErrorCode: (code?: string) => code || "",
}));

import { renderWithProviders, screen } from "@/test/testUtils";
import { AbtestObservationDashboard } from "./AbtestObservationDashboard";

function makeComparisonData(overrides: Record<string, unknown> = {}) {
  return {
    run_id: "run-1",
    algo_id: "algo-a",
    candidate_algo_id: "algo-b",
    status: "done",
    verdict: "pass",
    reasons: [],
    origin: {
      cases: [{ case_id: "case-1", overall: 0.5, correctness: 0.5 }],
    },
    candidate: {
      cases: [{ case_id: "case-1", overall: 0.6, correctness: 0.6 }],
    },
    ...overrides,
  };
}

describe("AbtestObservationDashboard", () => {
  it("shows a loading state when loading and no data yet", () => {
    renderWithProviders(
      <AbtestObservationDashboard data={undefined} loading onBack={vi.fn()} onReload={vi.fn()} />,
    );
    expect(screen.getByText("selfEvolutionRun.observation.loadingAbData")).toBeInTheDocument();
  });

  it("renders the ab report panel and trace compare panel when data has rows", () => {
    renderWithProviders(
      <AbtestObservationDashboard data={makeComparisonData()} loading={false} onBack={vi.fn()} onReload={vi.fn()} />,
    );
    expect(screen.getByText("run-1")).toBeInTheDocument();
    expect(screen.getByText("case-1")).toBeInTheDocument();
  });

  it("renders the raw data fallback when there are no rows", () => {
    renderWithProviders(
      <AbtestObservationDashboard data={{ foo: "bar" }} loading={false} onBack={vi.fn()} onReload={vi.fn()} />,
    );
    expect(screen.getByText("selfEvolutionRun.observation.rawData")).toBeInTheDocument();
  });

  it("calls onReload when the refresh button is clicked", () => {
    const onReload = vi.fn();
    renderWithProviders(
      <AbtestObservationDashboard data={makeComparisonData()} loading={false} onBack={vi.fn()} onReload={onReload} />,
    );
    screen.getByText("selfEvolutionRun.observation.refresh").click();
    expect(onReload).toHaveBeenCalledTimes(1);
  });

  it("renders the thread id tag and notice alert when provided", () => {
    renderWithProviders(
      <AbtestObservationDashboard
        data={makeComparisonData()}
        loading={false}
        threadId="thread-9"
        notice="stale data"
        onBack={vi.fn()}
        onReload={vi.fn()}
      />,
    );
    expect(screen.getByText("thread thread-9")).toBeInTheDocument();
    expect(screen.getByText("stale data")).toBeInTheDocument();
  });
});
