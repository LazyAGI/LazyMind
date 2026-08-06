import { describe, expect, it, vi } from "vitest";

const { fetchRouterABStrategy, fetchRouterAlgorithms, putRouterABStrategy } = vi.hoisted(() => ({
  fetchRouterABStrategy: vi.fn(),
  fetchRouterAlgorithms: vi.fn(),
  putRouterABStrategy: vi.fn(),
}));

vi.mock("../shared/routerApi", () => ({
  fetchRouterABStrategy,
  fetchRouterAlgorithms,
  putRouterABStrategy,
  getRouterApiErrorMessage: (_error: unknown, fallback: string) => fallback,
}));

import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import { RoutingStrategyManagementPage } from "./RoutingStrategyManagementPage";
import type { RouterABStrategy, RouterAlgorithm } from "../shared/routerApi";

function makeAlgorithm(overrides: Partial<RouterAlgorithm> = {}): RouterAlgorithm {
  return {
    algorithm_id: "evo_algo_1",
    status: "active",
    expected_state: "active",
    healthy_instances: 1,
    instance_count: 1,
    owner: { thread_id: "thread-1" },
    router_chat_url: "",
    router_admin_url: "",
    ...overrides,
  };
}

function makeStrategy(overrides: Partial<RouterABStrategy> = {}): RouterABStrategy {
  return {
    active: true,
    id: 1,
    weights: { evo_algo_1: 100 },
    updated_by: {},
    ...overrides,
  };
}

describe("RoutingStrategyManagementPage", () => {
  it("shows a loading skeleton before the strategy loads", () => {
    fetchRouterAlgorithms.mockReset().mockReturnValue(new Promise(() => {}));
    fetchRouterABStrategy.mockReset().mockReturnValue(new Promise(() => {}));
    renderWithProviders(<RoutingStrategyManagementPage />);
    expect(document.querySelector(".self-evolution-routing-loading")).toBeInTheDocument();
  });

  it("renders the live strategy weights and the algorithm catalog once loaded", async () => {
    fetchRouterAlgorithms.mockReset().mockResolvedValue([makeAlgorithm()]);
    fetchRouterABStrategy.mockReset().mockResolvedValue(makeStrategy());
    renderWithProviders(<RoutingStrategyManagementPage />);
    await waitFor(() => {
      expect(screen.getAllByText("evo_algo_1").length).toBeGreaterThan(0);
    });
    expect(screen.getByText("100")).toBeInTheDocument();
  });

  it("shows a load error with a retry button when the API call fails", async () => {
    fetchRouterAlgorithms.mockReset().mockResolvedValue([]);
    fetchRouterABStrategy.mockReset().mockRejectedValue(new Error("boom"));
    renderWithProviders(<RoutingStrategyManagementPage />);
    await waitFor(() => {
      expect(screen.getByRole("alert")).toBeInTheDocument();
    });
    expect(screen.getByText("selfEvolutionRun.retry")).toBeInTheDocument();
  });

  it("adds a second algorithm to the draft when clicking an unselected catalog entry", async () => {
    fetchRouterAlgorithms.mockReset().mockResolvedValue([
      makeAlgorithm({ algorithm_id: "evo_algo_1" }),
      makeAlgorithm({ algorithm_id: "evo_algo_2" }),
    ]);
    fetchRouterABStrategy.mockReset().mockResolvedValue(makeStrategy());
    renderWithProviders(<RoutingStrategyManagementPage />);
    await waitFor(() => {
      expect(screen.getByText("selfEvolutionRun.algorithmRoutingJoin")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText("selfEvolutionRun.algorithmRoutingJoin"));
    await waitFor(() => {
      expect(screen.getAllByText("50").length).toBeGreaterThan(0);
    });
  });

  it("saves the strategy with the current draft weights when clicking save", async () => {
    fetchRouterAlgorithms.mockReset().mockResolvedValue([makeAlgorithm()]);
    fetchRouterABStrategy.mockReset().mockResolvedValue(
      makeStrategy({ updated_by: { reason: "" } }),
    );
    putRouterABStrategy.mockReset().mockResolvedValue(makeStrategy({ updated_by: { reason: "changed" } }));
    renderWithProviders(<RoutingStrategyManagementPage />);
    await waitFor(() => {
      expect(screen.getByLabelText("selfEvolutionRun.algorithmRoutingReasonLabel")).toBeInTheDocument();
    });
    fireEvent.change(screen.getByLabelText("selfEvolutionRun.algorithmRoutingReasonLabel"), {
      target: { value: "changed" },
    });
    fireEvent.click(screen.getByText("selfEvolutionRun.algorithmRoutingSave"));
    await waitFor(() => {
      expect(putRouterABStrategy).toHaveBeenCalledWith(
        expect.objectContaining({ weights: { evo_algo_1: 100 }, reason: "changed" }),
      );
    });
  });
});
