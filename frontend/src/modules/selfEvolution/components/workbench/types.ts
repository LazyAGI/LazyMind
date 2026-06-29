import type { MouseEvent, ReactNode, RefObject } from "react";
import type {
  SelfEvolutionChatMessage,
  SelfEvolutionHistoryEntry,
  SelfEvolutionLaunchOptionCard,
  SelfEvolutionSummaryItem,
  SelfEvolutionWorkbenchTab,
} from "../types";
import type {
  CheckpointWaitPrompt,
  EvoProcessDashboard,
  WorkflowResultKind,
  WorkflowStep as SelfEvolutionRuntimeWorkflowStep,
} from "../../shared";

export type SelfEvolutionSessionSummary = {
  id: string;
  title: string;
};

export type SelfEvolutionFinalResultSummary = {
  verdict: "accept" | "reject" | "done";
  title: string;
  desc: string;
  metrics: { label: string; value: string; tone: "good" | "bad" | "neutral" }[];
  reasons: string[];
};

export type SelfEvolutionObservationKind = "eval" | "abtest";

export type SelfEvolutionWorkbenchViewProps = {
  processDashboard: EvoProcessDashboard;
  finalResultSummary?: SelfEvolutionFinalResultSummary;
  abtestPreviewPanel: ReactNode;
  activeWorkbenchTab?: SelfEvolutionWorkbenchTab;
  artifactNavigationPanel: ReactNode;
  artifactPanel: ReactNode;
  isArtifactPanelOpen: boolean;
  activeStepText: string;
  routeThreadId?: string;
  isRestoringThread: boolean;
  threadRestoreError: string;
  activeSession: SelfEvolutionSessionSummary;
  chatSessionsCount: number;
  historySessionEntries: SelfEvolutionHistoryEntry[];
  deletingHistoryKeys: string[];
  displayedMessages: SelfEvolutionChatMessage[];
  chatStreamRef: RefObject<HTMLDivElement>;
  isAutoMode: boolean;
  isAutoInteractionActive: boolean;
  isPlanningNextStep: boolean;
  isSendingMessage: boolean;
  displayedCheckpointWaitPrompt?: CheckpointWaitPrompt;
  prompt: string;
  selectedViewStage?: string;
  isHistorySessionModalOpen: boolean;
  threadHistoryListError: string;
  isLoadingThreadHistoryList: boolean;
  isNewSessionConfigOpen: boolean;
  newSessionOptionCards: SelfEvolutionLaunchOptionCard[];
  newSessionSummaryItems: SelfEvolutionSummaryItem[];
  isNewSessionStepOneDone: boolean;
  isNewSessionStepTwoDone: boolean;
  isNewSessionStepThreeDone: boolean;
  isNewSessionStepFourDone: boolean;
  isNewSessionConfirmDisabled: boolean;
  isConfirmingNewSession: boolean;
  getStepStatusLabel: (status: SelfEvolutionRuntimeWorkflowStep["status"]) => string;
  renderKnowledgeAndModeTools: () => ReactNode;
  renderSendButton: () => ReactNode;
  onRetryRestoreThread: () => void;
  onCloseSession: (sessionId: string) => void;
  onSelectHistorySession: (entry: SelfEvolutionHistoryEntry) => void;
  onEnterHistorySession: (entry: SelfEvolutionHistoryEntry) => void;
  onDeleteHistorySession: (
    entry: SelfEvolutionHistoryEntry,
    event: MouseEvent<HTMLElement>,
  ) => void;
  onCreateSession: () => void;
  onOpenHistorySessionModal: () => void;
  onPromptChange: (value: string) => void;
  onSend: (command?: string) => void;
  onConfirmIntentCheckpoint: () => void;
  onContinueCheckpoint: (command?: string) => void;
  onOpenArtifact: (kind: WorkflowResultKind) => void;
  onOpenObservation: (kind: SelfEvolutionObservationKind) => void;
  onOpenCaseArtifact: (kind: WorkflowResultKind, artifactId: string, title: string, caseId?: string) => void;
  onWorkbenchTabChange: (tab?: SelfEvolutionWorkbenchTab) => void;
  onCloseArtifactPanel: () => void;
  onCloseHistorySessionModal: () => void;
  onRetryThreadHistoryList: () => void;
  onCancelCreateSession: () => void;
  onConfirmCreateSession: () => void;
};
