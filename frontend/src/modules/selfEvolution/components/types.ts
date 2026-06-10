import { type ReactNode } from "react";

export type SelfEvolutionChatMessage = {
  id: string;
  role: "user" | "assistant";
  content: string;
  time: string;
  agentLabel?: string;
};

export type SelfEvolutionHistoryEntry = {
  key: string;
  sessionId?: string;
  threadId?: string;
  title: string;
  updatedAt: string;
  messageCount?: number;
  status?: string;
  source: "thread" | "local";
  isCurrent?: boolean;
  isPreviewing?: boolean;
};

export type SelfEvolutionCheckpointPrompt = {
  message: string;
  kind?: "checkpoint" | "failure";
  checkpointKind?: string;
  completedStageLabel?: string;
  nextOperationLabel?: string;
  command: string;
};

export type SelfEvolutionWorkbenchTab = "artifacts" | "messages" | "processes";

export type SelfEvolutionStepStatus = "running" | "pending" | "done" | "paused" | "canceled" | "failed";

export type SelfEvolutionWorkflowStep = {
  id: string;
  renderKey?: string;
  title: string;
  desc: string;
  status: SelfEvolutionStepStatus;
  runtimeText?: string;
  progress?: {
    statusText: string;
    percent: number;
  };
  progressPhases?: Array<{
    id: "rag" | "judge";
    title: string;
    desc: string;
    statusText: string;
    percent: number;
  }>;
};

export type SelfEvolutionLaunchOptionCard = {
  key: string;
  step: string;
  title: string;
  description: string;
  currentValue: string;
  toneClassName: string;
  icon: ReactNode;
  isHighlighted: boolean;
  isDescSingleLine: boolean;
  control: ReactNode;
};

export type SelfEvolutionSummaryItem = {
  label: string;
  value: string;
};
