import { describe, expect, it, vi } from "vitest";

const {
  fetchRouterAlgorithms,
  deleteRouterAlgorithm,
  registerRouterAlgorithm,
  runRouterAlgorithmAction,
} = vi.hoisted(() => ({
  fetchRouterAlgorithms: vi.fn(),
  deleteRouterAlgorithm: vi.fn(),
  registerRouterAlgorithm: vi.fn(),
  runRouterAlgorithmAction: vi.fn(),
}));

vi.mock("../shared/routerApi", () => ({
  fetchRouterAlgorithms,
  deleteRouterAlgorithm,
  registerRouterAlgorithm,
  runRouterAlgorithmAction,
  getRouterApiErrorMessage: (_error: unknown, fallback: string) => fallback,
}));

import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import { AlgorithmVersionManagementPage } from "./AlgorithmVersionManagementPage";
import type { RouterAlgorithm } from "../shared/routerApi";

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

describe("AlgorithmVersionManagementPage", () => {
  it("loads and renders algorithm cards on mount", async () => {
    fetchRouterAlgorithms.mockReset().mockResolvedValue([makeAlgorithm()]);
    renderWithProviders(<AlgorithmVersionManagementPage />);
    await waitFor(() => {
      expect(screen.getByText("evo_algo_1")).toBeInTheDocument();
    });
  });

  it("shows an empty state when there are no algorithms", async () => {
    fetchRouterAlgorithms.mockReset().mockResolvedValue([]);
    renderWithProviders(<AlgorithmVersionManagementPage />);
    await waitFor(() => {
      expect(screen.getByText("selfEvolutionRun.algorithmManagementNoAlgorithms")).toBeInTheDocument();
    });
  });

  it("switches to table view and shows the algorithm row", async () => {
    fetchRouterAlgorithms.mockReset().mockResolvedValue([makeAlgorithm({ algorithm_id: "evo_table_algo" })]);
    renderWithProviders(<AlgorithmVersionManagementPage />);
    await waitFor(() => {
      expect(screen.getByText("evo_table_algo")).toBeInTheDocument();
    });
    fireEvent.click(document.querySelector(".ant-segmented-item:first-child") as Element);
    await waitFor(() => {
      expect(document.querySelector(".self-evolution-algorithm-table")).toBeInTheDocument();
    });
  });

  it("reloads the list after applying filters", async () => {
    fetchRouterAlgorithms.mockReset().mockResolvedValue([makeAlgorithm()]);
    renderWithProviders(<AlgorithmVersionManagementPage />);
    await waitFor(() => {
      expect(fetchRouterAlgorithms).toHaveBeenCalledTimes(1);
    });
    fireEvent.change(
      screen.getByPlaceholderText("selfEvolutionRun.algorithmManagementThreadFilter"),
      { target: { value: "thread-42" } },
    );
    fireEvent.click(screen.getByText("common.search"));
    await waitFor(() => {
      expect(fetchRouterAlgorithms).toHaveBeenLastCalledWith(
        expect.objectContaining({ threadId: "thread-42" }),
      );
    });
  });
});
