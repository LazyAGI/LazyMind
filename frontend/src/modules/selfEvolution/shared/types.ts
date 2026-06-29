export type EvolutionMode = "auto" | "interactive";
export type ExtraEvalStrategy = "skip" | "generate";
export type WorkflowStepId = "dataset" | "px-report" | "analysis" | "code-optimize" | "ab-test";
export type StepStatus = "running" | "pending" | "done" | "paused" | "canceled" | "failed";
export type ChatRole = "user" | "assistant";
export type ThreadEventStage = "dataset" | "eval" | "analysis" | "repair" | "abtest";

export type WorkflowProgressSnapshot = {
  statusText: string;
  percent: number;
  rank?: number;
};

export type WorkflowProgressPhaseId = "rag" | "judge";
export type EvoStageActivity = {
  key: string;
  stage?: ThreadEventStage;
  title: string;
  detail: string;
  time: string;
  tone: "normal" | "progress" | "checkpoint" | "auto" | "message" | "error";
  flowKind?: string;
  artifactKind?: WorkflowResultKind;
  artifactId?: string;
  artifactLabel?: string;
};

export type EvoCaseProgressStep = {
  key: string;
  label: string;
  status: StepStatus;
};

export type EvoCaseProgressItem = {
  caseId: string;
  title: string;
  completed: number;
  total: number;
  status: StepStatus;
  steps: EvoCaseProgressStep[];
  artifactKind: WorkflowResultKind;
  artifactId?: string;
  artifactLabel: string;
  updatedAt?: string;
};

export type EvoCaseProgressGroup = {
  stage: Extract<ThreadEventStage, "dataset" | "eval" | "analysis" | "abtest">;
  title: string;
  pageSize: number;
  cases: EvoCaseProgressItem[];
};

export type EvoStageOverviewItem = {
  step: WorkflowStep;
  stage: ThreadEventStage;
  eventCount: number;
  latestActivity?: EvoStageActivity;
};

export type EvoProcessDashboard = {
  overview: EvoStageOverviewItem[];
  activeStage?: ThreadEventStage;
  activeStep?: WorkflowStep;
  activeProgress?: WorkflowProgressSnapshot;
  activeProgressPhases?: WorkflowProgressPhaseSnapshot[];
  recentActivities: EvoStageActivity[];
  recentActivityTotal: number;
  checkpoint?: CheckpointWaitPrompt;
  cutoverActivities: EvoStageActivity[];
  cutoverCompleted: boolean;
  caseProgressGroups: EvoCaseProgressGroup[];
};

export type WorkflowProgressPhaseSnapshot = WorkflowProgressSnapshot & {
  id: WorkflowProgressPhaseId;
  title: string;
  desc: string;
};

export type WorkflowStep = {
  id: WorkflowStepId;
  renderKey?: string;
  title: string;
  desc: string;
  status: StepStatus;
  runtimeText?: string;
  progress?: WorkflowProgressSnapshot;
  progressPhases?: WorkflowProgressPhaseSnapshot[];
};

export type EvalCaseItem = {
  case_id: string;
  reference_doc: string[];
  reference_context: string[];
  is_deleted: boolean;
  question: string;
  question_type: number;
  key_point: string[];
  ground_truth: string;
};

export type EvalDataset = {
  eval_set_id: string;
  eval_name: string;
  kb_id: string;
  task_id: string;
  create_time: string;
  total_nums: number;
  cases: EvalCaseItem[];
};

export type ChatMessage = {
  id: string;
  role: ChatRole;
  content: string;
  time: string;
  sortTime?: number;
  agentLabel?: string;
  streamAnswerStarted?: boolean;
};

export type ChatSession = {
  id: string;
  title: string;
  updatedAt: string;
  threadId?: string;
  messages: ChatMessage[];
};

export type ThreadHistoryEntry = {
  threadId: string;
  title: string;
  updatedAt: string;
  status?: string;
};

export type HistorySessionEntry = {
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

export type NewSessionDraft = {
  selectedKb?: string;
  selectedEvalSet?: string;
  extraEvalStrategy?: ExtraEvalStrategy;
  mode?: EvolutionMode;
};

export type SelfEvolutionPageView = "home" | "detail";

export type SelfEvolutionRouteState = {
  openWorkbench?: boolean;
};

export type KnowledgeBaseOption = {
  label: string;
  value: string;
};

export type AgentThreadCreateResponse = {
  id?: string;
  thread_id?: string;
  data?: {
    upstream?: {
      id?: string;
      thread_id?: string;
    };
    thread?: {
      id?: string;
      thread_id?: string;
    };
  };
};

export type ThreadEventFrame = {
  id?: string;
  eventName: string;
  data: string;
};

export type ThreadRestorePayload = Record<string, unknown> | unknown[] | undefined;

export type WorkflowRuntimeState = Record<
  WorkflowStepId,
  {
    status: StepStatus;
    runtimeText?: string;
    progress?: WorkflowProgressSnapshot;
    progressPhases?: WorkflowProgressPhaseSnapshot[];
  }
>;

export type NormalizedThreadEvent = {
  key: string;
  timestamp?: string;
  sequence?: number;
  taskId?: string;
  type: string;
  stage?: ThreadEventStage;
  action?: string;
  role?: ChatRole;
  content?: string;
  payload?: Record<string, unknown>;
  displayText?: string;
  progress?: WorkflowProgressSnapshot;
  progressPhase?: WorkflowProgressPhaseId;
  checkpointWait?: CheckpointWaitPrompt;
};

export type ChatStreamDeltaKind = "thinking" | "answer";

export type CheckpointWaitPrompt = {
  message: string;
  kind?: "checkpoint" | "failure";
  checkpointKind?: string;
  completedStage?: ThreadEventStage;
  completedStageLabel?: string;
  nextOperationLabel?: string;
  nextStage?: ThreadEventStage;
  command: string;
  taskId?: string;
  datasetId?: string;
};

export type WorkflowResultKind = "datasets" | "eval-reports" | "analysis-reports" | "diffs" | "abtests";

export type WorkflowResultState = {
  loading: boolean;
  loaded: boolean;
  error?: string;
  data?: unknown;
};

export type WorkflowResultsState = Record<WorkflowResultKind, WorkflowResultState>;

export type DiffArtifactContentState = {
  loading: boolean;
  key: string;
  content: string;
  error?: string;
};

export type DiffArtifactFile = {
  path: string;
  diffPath: string;
  additions?: number;
  deletions?: number;
  changeKind?: string;
};

export type AbComparisonRow = {
  key: string;
  category: string;
  baselineSummary: string;
  experimentSummary: string;
  deltaSummary: string;
};

export type ParsedDiffFile = {
  id: string;
  fromPath: string;
  toPath: string;
  displayPath: string;
  lines: string[];
  additions: number;
  deletions: number;
};

export type DiffFileTreeNode = {
  name: string;
  path: string;
  nodeType: "dir" | "file";
  fileId?: string;
  children: DiffFileTreeNode[];
};

export type PxMetricKey = "answer_correctness" | "answer_score" | "chunk_recall" | "doc_recall";

export type PxCategoryMetricAverage = {
  category: string;
  caseCount: number;
  metrics: Record<PxMetricKey, number>;
};

export type EvalQuestionTypeSummary = {
  question_type?: number;
  question_type_key?: string;
  question_type_name?: string;
  count?: number;
  averages?: Partial<Record<PxMetricKey, number>>;
};

export type AbCategoryComparison = {
  category: string;
  baseline: Record<PxMetricKey, number>;
  experiment: Record<PxMetricKey, number>;
  delta: Record<PxMetricKey, number>;
};

export type AbSummaryMetricRow = {
  key: string;
  metric: string;
  metricLabel: string;
  meanA: number;
  meanB: number;
  deltaMean: number;
  winRateB: number;
  signP?: number | null;
  n?: number;
};

export type AbTopDiffRow = {
  key: string;
  caseKey: string;
  a: number;
  b: number;
  delta: number;
};

export type AbSummaryReport = {
  id: string;
  markdown?: string;
  verdict?: string;
  alignedCases?: number;
  reasons: string[];
  metricRows: AbSummaryMetricRow[];
  topDiffRows: AbTopDiffRow[];
  missingMetrics: string[];
  primaryMetric?: string;
  guardMetrics: string[];
};

export type EvalPayloadPhase = WorkflowProgressPhaseId;
