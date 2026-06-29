import type { WorkflowResultKind } from "../../shared";
import type { TraceDetailObservation, TraceObservation } from "../TraceObservationView";

export type TFunction = (key: string, options?: Record<string, unknown>) => string;

export type ObservationResultKind = Extract<WorkflowResultKind, "eval-reports" | "abtests">;
export type TraceNode = TraceDetailObservation["root"];

export type ObservationPageLayoutContext = {
  isMenuCollapsed?: boolean;
  toggleMenu?: () => void;
};

export type ObservationHeaderControlsProps = {
  isMenuCollapsed?: boolean;
  toggleMenu?: () => void;
  onBack: () => void;
};

export type ObservationRouteParams = {
  threadId?: string;
  kind?: string;
};

export type ObservationPageState = {
  loading: boolean;
  loaded: boolean;
  data?: unknown;
  error?: string;
  notice?: string;
};

export type EvalBadcaseListState = {
  reportId?: string;
  loading: boolean;
  loaded: boolean;
  data?: unknown;
  error?: string;
};

export type AbCaseListState = {
  abtestId?: string;
  loading: boolean;
  loaded: boolean;
  data?: unknown;
  error?: string;
  totalSize?: number;
};

export type AbTraceCompareState = {
  caseId?: string;
  loading: boolean;
  loaded: boolean;
  data?: unknown;
  error?: string;
  aTraceId?: string;
  bTraceId?: string;
};

export type EvalReportsTraceState = {
  loading: boolean;
  loaded: boolean;
  data?: unknown;
};

export type CsvBadcaseRow = {
  caseId: string;
  query: string;
  reference: string;
  answer: string;
  score: number;
  failureType: string;
  failureTone: "red" | "orange" | "blue";
  defect: string;
  reason: string;
  mode: string;
  traceId: string;
  traceStatus: string;
  failureReason: string;
  tracePayload?: unknown;
};

export type EvalReportSummary = {
  reportId: string;
  dataset: string;
  correctRate?: number;
  badCaseCount?: number;
  traceCoverageRate?: number;
};

export type TraceDocRow = {
  key: string;
  title: string;
  ref: string;
  score?: number;
  text: string;
};

export type FlowRow = {
  key: string;
  round: number;
  title: string;
  desc: string;
  duration: string;
  tone: "normal" | "warning" | "success";
  node: TraceNode;
};

export type AbCompareObservation = Extract<TraceObservation, { kind: "compare" }>;

export type AbCaseRow = {
  caseId: string;
  query: string;
  aScore: number;
  bScore: number;
  delta: number;
  conclusion: string;
  tone: "up" | "down" | "flat";
};

export type AbMetricRow = {
  key: string;
  label: string;
  meanA: number;
  meanB: number;
  winRate: number;
  signP?: string;
};
