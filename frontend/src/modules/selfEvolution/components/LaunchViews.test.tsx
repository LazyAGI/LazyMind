import { describe, expect, it, vi } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/testUtils";
import {
  LaunchOptionGrid,
  LaunchSummary,
  NewSessionConfigModal,
  SelfEvolutionHomeView,
} from "./LaunchViews";
import type { SelfEvolutionLaunchOptionCard, SelfEvolutionSummaryItem, SelfEvolutionWorkflowStep } from "./types";

function makeOptionCard(overrides: Partial<SelfEvolutionLaunchOptionCard> = {}): SelfEvolutionLaunchOptionCard {
  return {
    key: "kb",
    step: "1",
    title: "Knowledge base",
    description: "Pick a knowledge base",
    currentValue: "kb-1",
    toneClassName: "tone-1",
    icon: <span>icon</span>,
    isHighlighted: false,
    isDescSingleLine: false,
    control: <button type="button">select</button>,
    ...overrides,
  };
}

describe("LaunchOptionGrid", () => {
  it("renders one item per option card with title and current value", () => {
    renderWithProviders(<LaunchOptionGrid optionCards={[makeOptionCard()]} />);
    expect(screen.getByText("Knowledge base")).toBeInTheDocument();
    expect(
      screen.getByText((content) => content.startsWith("selfEvolutionRun.currentValue")),
    ).toBeInTheDocument();
  });

  it("applies the highlighted class when isHighlighted is true", () => {
    renderWithProviders(<LaunchOptionGrid optionCards={[makeOptionCard({ isHighlighted: true })]} />);
    expect(document.querySelector(".self-evolution-launch-compact-item.is-highlighted")).toBeInTheDocument();
  });

  it("renders an empty list when there are no option cards", () => {
    renderWithProviders(<LaunchOptionGrid optionCards={[]} />);
    expect(document.querySelectorAll(".self-evolution-launch-compact-item")).toHaveLength(0);
  });
});

describe("LaunchSummary", () => {
  it("renders a pill for every summary item", () => {
    const items: SelfEvolutionSummaryItem[] = [
      { label: "Mode", value: "auto" },
      { label: "Eval set", value: "default" },
    ];
    renderWithProviders(<LaunchSummary summaryItems={items} ariaLabel="summary" />);
    expect(screen.getByText("Mode")).toBeInTheDocument();
    expect(screen.getByText("auto")).toBeInTheDocument();
    expect(screen.getByText("Eval set")).toBeInTheDocument();
  });
});

describe("NewSessionConfigModal", () => {
  it("calls onConfirm when the start button is clicked and enabled", () => {
    const onConfirm = vi.fn();
    renderWithProviders(
      <NewSessionConfigModal
        open
        optionCards={[makeOptionCard()]}
        summaryItems={[{ label: "Mode", value: "auto" }]}
        isStepOneDone
        isStepTwoDone={false}
        isStepThreeDone={false}
        isStepFourDone={false}
        isConfirmDisabled={false}
        onCancel={vi.fn()}
        onConfirm={onConfirm}
      />,
    );
    fireEvent.click(screen.getByText("selfEvolutionRun.startNewSession"));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it("shows the starting label and disables confirm while confirming", () => {
    renderWithProviders(
      <NewSessionConfigModal
        open
        optionCards={[]}
        summaryItems={[]}
        isStepOneDone={false}
        isStepTwoDone={false}
        isStepThreeDone={false}
        isStepFourDone={false}
        isConfirmDisabled
        isConfirming
        onCancel={vi.fn()}
        onConfirm={vi.fn()}
      />,
    );
    const startButton = screen.getByText("selfEvolutionRun.starting");
    expect(startButton).toBeDisabled();
  });

  it("calls onCancel when the cancel button is clicked", () => {
    const onCancel = vi.fn();
    renderWithProviders(
      <NewSessionConfigModal
        open
        optionCards={[]}
        summaryItems={[]}
        isStepOneDone={false}
        isStepTwoDone={false}
        isStepThreeDone={false}
        isStepFourDone={false}
        isConfirmDisabled={false}
        onCancel={onCancel}
        onConfirm={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByText("common.cancel"));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });
});

describe("SelfEvolutionHomeView", () => {
  function makeStep(overrides: Partial<SelfEvolutionWorkflowStep> = {}): SelfEvolutionWorkflowStep {
    return { id: "dataset", title: "Dataset", desc: "", status: "pending", ...overrides };
  }

  it("renders workflow status badges and disables start when config is invalid", () => {
    renderWithProviders(
      <SelfEvolutionHomeView
        isLoadingThreadHistoryList={false}
        workflowSteps={[makeStep()]}
        launchOptionCards={[]}
        launchSummaryItems={[]}
        isLaunchConfigValid={false}
        isStartingSession={false}
        onOpenHistorySessionModal={vi.fn()}
        onStartSession={vi.fn()}
      />,
    );
    expect(screen.getByText("Dataset")).toBeInTheDocument();
    expect(screen.getByText("selfEvolutionRun.start")).toBeDisabled();
  });

  it("invokes onStartSession when the start button is enabled and clicked", () => {
    const onStartSession = vi.fn();
    renderWithProviders(
      <SelfEvolutionHomeView
        isLoadingThreadHistoryList={false}
        workflowSteps={[]}
        launchOptionCards={[]}
        launchSummaryItems={[]}
        isLaunchConfigValid
        isStartingSession={false}
        onOpenHistorySessionModal={vi.fn()}
        onStartSession={onStartSession}
      />,
    );
    fireEvent.click(screen.getByText("selfEvolutionRun.start"));
    expect(onStartSession).toHaveBeenCalledTimes(1);
  });

  it("invokes onOpenHistorySessionModal when the history button is clicked", () => {
    const onOpenHistorySessionModal = vi.fn();
    renderWithProviders(
      <SelfEvolutionHomeView
        isLoadingThreadHistoryList={false}
        workflowSteps={[]}
        launchOptionCards={[]}
        launchSummaryItems={[]}
        isLaunchConfigValid
        isStartingSession={false}
        onOpenHistorySessionModal={onOpenHistorySessionModal}
        onStartSession={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByText("selfEvolutionRun.historySessions"));
    expect(onOpenHistorySessionModal).toHaveBeenCalledTimes(1);
  });
});
