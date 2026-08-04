import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import { SelfEvolutionWorkbenchView } from "./WorkbenchView";
import type { SelfEvolutionWorkbenchViewProps } from "./workbench/types";
import type { EvoProcessDashboard, WorkflowStep } from "../shared";

function makeStep(overrides: Partial<WorkflowStep> = {}): WorkflowStep {
  return {
    id: "dataset",
    title: "Dataset",
    desc: "desc",
    status: "running",
    ...overrides,
  };
}

function makeDashboard(overrides: Partial<EvoProcessDashboard> = {}): EvoProcessDashboard {
  return {
    overview: [{ step: makeStep(), stage: "dataset", eventCount: 0 }],
    activeStage: "dataset",
    activeStep: makeStep(),
    recentActivities: [],
    recentActivityTotal: 0,
    cutoverActivities: [],
    cutoverCompleted: false,
    caseProgressGroups: [],
    ...overrides,
  };
}

function makeProps(overrides: Partial<SelfEvolutionWorkbenchViewProps> = {}): SelfEvolutionWorkbenchViewProps {
  return {
    processDashboard: makeDashboard(),
    abtestPreviewPanel: null,
    artifactNavigationPanel: null,
    artifactPanel: null,
    isArtifactPanelOpen: false,
    activeStepText: "Dataset",
    isRestoringThread: false,
    threadRestoreError: "",
    activeSession: { id: "session-1", title: "Session 1" },
    chatSessionsCount: 1,
    historySessionEntries: [],
    deletingHistoryKeys: [],
    displayedMessages: [],
    chatStreamRef: createRef<HTMLDivElement>(),
    isAutoMode: false,
    isAutoInteractionActive: false,
    isPlanningNextStep: false,
    isSendingMessage: false,
    prompt: "",
    isHistorySessionModalOpen: false,
    threadHistoryListError: "",
    isLoadingThreadHistoryList: false,
    isNewSessionConfigOpen: false,
    newSessionOptionCards: [],
    newSessionSummaryItems: [],
    isNewSessionStepOneDone: false,
    isNewSessionStepTwoDone: false,
    isNewSessionStepThreeDone: false,
    isNewSessionStepFourDone: false,
    isNewSessionConfirmDisabled: false,
    isConfirmingNewSession: false,
    getStepStatusLabel: (status) => status,
    renderKnowledgeAndModeTools: () => null,
    renderSendButton: () => null,
    onRetryRestoreThread: vi.fn(),
    onCloseSession: vi.fn(),
    onSelectHistorySession: vi.fn(),
    onEnterHistorySession: vi.fn(),
    onDeleteHistorySession: vi.fn(),
    onCreateSession: vi.fn(),
    onOpenHistorySessionModal: vi.fn(),
    onPromptChange: vi.fn(),
    onSend: vi.fn(),
    onConfirmIntentCheckpoint: vi.fn(),
    onContinueCheckpoint: vi.fn(),
    onOpenArtifact: vi.fn(),
    onOpenObservation: vi.fn(),
    onOpenCaseArtifact: vi.fn(),
    onWorkbenchTabChange: vi.fn(),
    onCloseArtifactPanel: vi.fn(),
    onCloseHistorySessionModal: vi.fn(),
    onRetryThreadHistoryList: vi.fn(),
    onCancelCreateSession: vi.fn(),
    onConfirmCreateSession: vi.fn(),
    ...overrides,
  };
}

describe("SelfEvolutionWorkbenchView", () => {
  it("renders the process activity section for the active stage by default", () => {
    renderWithProviders(<SelfEvolutionWorkbenchView {...makeProps()} />);
    expect(screen.getByText("selfEvolutionRun.currentStage")).toBeInTheDocument();
  });

  it("renders the dataset streaming table when viewing the dataset stage with rows", () => {
    renderWithProviders(
      <SelfEvolutionWorkbenchView
        {...makeProps({
          selectedViewStage: "dataset",
          streamingDatasetRows: [
            { key: "case-1", caseId: "case-1", question: "q", status: "done" } as never,
          ],
        })}
      />,
    );
    expect(screen.getByText("selfEvolutionRun.viewingStage")).toBeInTheDocument();
  });

  it("shows the thread restore notice when there is a restore error for a routed thread", () => {
    renderWithProviders(
      <SelfEvolutionWorkbenchView
        {...makeProps({ threadRestoreError: "thread not found", routeThreadId: "thread-1" })}
      />,
    );
    expect(screen.getAllByRole("alert").length).toBeGreaterThan(0);
    expect(screen.getByText("selfEvolutionRun.threadNotFoundTitle")).toBeInTheDocument();
  });

  it("calls onSend with the checkpoint command when the composer checkpoint button is clicked", () => {
    const onContinueCheckpoint = vi.fn();
    renderWithProviders(
      <SelfEvolutionWorkbenchView
        {...makeProps({
          onContinueCheckpoint,
          displayedCheckpointWaitPrompt: {
            message: "waiting",
            command: "continue",
            checkpointKind: "manual",
          } as never,
          processDashboard: makeDashboard({
            checkpoint: {
              message: "waiting",
              command: "continue",
              checkpointKind: "manual",
            } as never,
          }),
        })}
      />,
    );
    fireEvent.click(screen.getByText("continue"));
    expect(onContinueCheckpoint).toHaveBeenCalledTimes(1);
  });

  it("shows the final result card when the workflow is read-only ended", () => {
    renderWithProviders(
      <SelfEvolutionWorkbenchView
        {...makeProps({
          processDashboard: makeDashboard({
            overview: [{ step: makeStep({ status: "done" }), stage: "dataset", eventCount: 0 }],
          }),
          finalResultSummary: {
            verdict: "accept",
            title: "Accepted",
            desc: "desc",
            metrics: [],
            reasons: [],
          },
        })}
      />,
    );
    expect(screen.getByText("Accepted")).toBeInTheDocument();
  });
});
