import type { ReactNode } from "react";
import type { WorkflowResultKind, WorkflowStep } from "../../shared";

export type DatasetCasePreviewRow = {
  key: string;
  caseId: string;
  question: string;
  answer: string;
  questionType: string;
  difficulty: string;
  references: string;
};

export type AnalysisCasePreviewRow = {
  key: string;
  caseId: string;
  coarseCategory: string;
  fineCategory: string;
  confidence: string;
  lossScore: string;
  quality: string;
};

export type AnalysisCategorySummaryRow = {
  key: string;
  category: string;
  count: number;
  ratio: string;
  ratioValue: number;
  color: string;
};

export type PxCaseDetailRow = {
  key: string;
  caseId: string;
  question: string;
  score: string;
  failureType: string;
  defect: string;
  reason: string;
  traceId: string;
};

export type ArtifactPanelItem = {
  kind: WorkflowResultKind;
  stepId: WorkflowStep["id"];
  sectionTitle: string;
  sectionDesc: string;
  title: string;
  desc: string;
  fallbackUrl: string;
  fileName: string;
  preview: ReactNode;
};

export type CaseArtifactState = {
  kind: WorkflowResultKind;
  artifactId: string;
  caseId?: string;
  title: string;
  loading: boolean;
  data?: unknown;
  error?: string;
};

export type EvalReportBadCasesState = {
  reportId?: string;
  loading: boolean;
  loaded: boolean;
  data?: unknown;
  error?: string;
  totalSize?: number;
  page?: number;
  pageSize?: number;
  pageToken?: string;
  nextPageToken?: string;
};

export type ThreadStepSummary = {
  stepId: string;
  title?: string;
  status?: string;
  active: boolean;
  orderIndex?: number;
  eventCount?: number;
  currentTaskId?: string;
  nextStepRunId?: string;
  startedAt?: string;
  endedAt?: string;
};

export type ThreadStepListState = {
  steps: ThreadStepSummary[];
  activeStepId?: string;
};

export type TFunction = (key: string, options?: Record<string, unknown>) => string;
