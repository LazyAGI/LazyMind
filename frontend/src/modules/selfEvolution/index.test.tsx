import { describe, expect, it, vi } from "vitest";

vi.mock("./components/HistorySessions", () => ({
  HistorySessionModal: () => <div data-testid="history-modal" />,
}));
vi.mock("./components/AlgorithmVersionManagementPage", () => ({
  AlgorithmVersionManagementPage: () => <div data-testid="algorithm-page" />,
}));
vi.mock("./components/RoutingStrategyManagementPage", () => ({
  RoutingStrategyManagementPage: () => <div data-testid="routing-page" />,
}));
vi.mock("./components/LaunchViews", () => ({
  SelfEvolutionHomeView: () => <div data-testid="home-view" />,
}));
vi.mock("./components/ObservationPage", () => ({
  SelfEvolutionObservationPage: () => <div data-testid="observation-page" />,
}));
vi.mock("./components/WorkbenchView", () => ({
  SelfEvolutionWorkbenchView: () => <div data-testid="workbench-view" />,
}));

const { SelfEvolutionPageController } = vi.hoisted(() => ({
  SelfEvolutionPageController: vi.fn(),
}));
vi.mock("./components/SelfEvolutionPage", () => ({
  SelfEvolutionPageController,
}));

import { renderWithProviders, screen } from "@/test/testUtils";
import {
  SelfEvolutionAlgorithmManagementPage,
  SelfEvolutionDetailPage,
  SelfEvolutionHomePage,
  SelfEvolutionObservationPage,
  SelfEvolutionRoutingStrategyPage,
} from "./index";

describe("selfEvolution module page entrypoints", () => {
  it("renders the home view when the workbench is not visible", () => {
    SelfEvolutionPageController.mockImplementation(({ children }: { children: (props: unknown) => unknown }) =>
      children({
        isWorkbenchVisible: false,
        homeViewProps: {},
        homeHistoryModalProps: {},
        workbenchViewProps: {},
      }) as never,
    );
    renderWithProviders(<SelfEvolutionHomePage />);
    expect(screen.getByTestId("home-view")).toBeInTheDocument();
    expect(screen.getByTestId("history-modal")).toBeInTheDocument();
  });

  it("renders the workbench view when the workbench is visible", () => {
    SelfEvolutionPageController.mockImplementation(({ children }: { children: (props: unknown) => unknown }) =>
      children({
        isWorkbenchVisible: true,
        homeViewProps: {},
        homeHistoryModalProps: {},
        workbenchViewProps: {},
      }) as never,
    );
    renderWithProviders(<SelfEvolutionDetailPage />);
    expect(screen.getByTestId("workbench-view")).toBeInTheDocument();
  });

  it("renders the observation page", () => {
    renderWithProviders(<SelfEvolutionObservationPage />);
    expect(screen.getByTestId("observation-page")).toBeInTheDocument();
  });

  it("renders the algorithm management page", () => {
    renderWithProviders(<SelfEvolutionAlgorithmManagementPage />);
    expect(screen.getByTestId("algorithm-page")).toBeInTheDocument();
  });

  it("renders the routing strategy page", () => {
    renderWithProviders(<SelfEvolutionRoutingStrategyPage />);
    expect(screen.getByTestId("routing-page")).toBeInTheDocument();
  });
});
