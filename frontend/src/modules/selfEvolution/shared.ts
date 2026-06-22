import {
  AgentApi as CoreAgentApi,
  Configuration as CoreConfiguration,
  DefaultApi as CoreDefaultApi,
  type Dataset,
} from "@/api/generated/core-client";
import { BASE_URL, axiosInstance } from "@/components/request";

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

export const FIXED_EVAL_SET = "__none__";
export const FIXED_EXTRA_EVAL_STRATEGY: ExtraEvalStrategy = "generate";
export const DEFAULT_EVAL_CASE_COUNT = 10;
export const AGENT_API_BASE = `${BASE_URL}/api/core/agent`;
export const EVO_API_BASE = `${BASE_URL}/api/evo/v1/evo`;
export const SELF_EVOLUTION_LAST_THREAD_STORAGE_KEY = "lazymind:self-evolution:last-thread";
export const DEPRECATED_SELF_EVOLUTION_THREAD_HISTORY_STORAGE_KEY = "lazymind:self-evolution:thread-history";

export const workflowResultLabels: Record<WorkflowResultKind, string> = {
  datasets: "数据集结果",
  "eval-reports": "评测报告",
  "analysis-reports": "分析报告",
  diffs: "代码 diff 结果",
  abtests: "ABTest 详情",
};

export function getSelfEvolutionWorkflowImageSrc(language?: string) {
  return language?.startsWith("en") ? "/Lazy-e.png" : "/Lazy-c.png";
}

export function createCoreAgentApiClient() {
  const baseUrl = BASE_URL || window.location.origin;
  return new CoreDefaultApi(
    new CoreConfiguration({
      basePath: baseUrl,
      baseOptions: {
        headers: { "Content-Type": "application/json" },
      },
    }),
    baseUrl,
    axiosInstance,
  );
}

export function createCoreAgentGeneratedApiClient() {
  const baseUrl = BASE_URL || window.location.origin;
  return new CoreAgentApi(
    new CoreConfiguration({
      basePath: baseUrl,
      baseOptions: {
        headers: { "Content-Type": "application/json" },
      },
    }),
    baseUrl,
    axiosInstance,
  );
}

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

export const pxMetricMeta: Array<{ key: PxMetricKey; label: string; color: string }> = [
  { key: "answer_correctness", label: "答案正确性", color: "#1a73e8" },
  { key: "answer_score", label: "综合得分", color: "#22a06b" },
  { key: "chunk_recall", label: "Chunk 召回", color: "#f08c00" },
  { key: "doc_recall", label: "文档召回", color: "#7048e8" },
];

const pxMetricFieldAliases: Record<PxMetricKey, string[]> = {
  answer_correctness: ["answer_correctness", "answer_correctness_avg", "correct_rate"],
  answer_score: ["answer_score", "answer_score_avg"],
  chunk_recall: ["chunk_recall", "chunk_recall_avg"],
  doc_recall: ["doc_recall", "doc_recall_avg"],
};

function getMetricFieldNumber(payload: Record<string, unknown> | undefined, key: PxMetricKey, fallback = 0) {
  return clampScore(getNumberField(payload, pxMetricFieldAliases[key]) ?? fallback);
}

export const stageStepMap: Record<ThreadEventStage, WorkflowStepId> = {
  dataset: "dataset",
  eval: "px-report",
  analysis: "analysis",
  repair: "code-optimize",
  abtest: "ab-test",
};

export const stageLabels: Record<ThreadEventStage, string> = {
  dataset: "生成数据集",
  eval: "执行评测",
  analysis: "问题分析",
  repair: "代码修改",
  abtest: "ABTest 与切流",
};

const stageResultKindMap: Record<ThreadEventStage, WorkflowResultKind> = {
  dataset: "datasets",
  eval: "eval-reports",
  analysis: "analysis-reports",
  repair: "diffs",
  abtest: "abtests",
};

const stepStageMap: Record<WorkflowStepId, ThreadEventStage> = {
  dataset: "dataset",
  "px-report": "eval",
  analysis: "analysis",
  "code-optimize": "repair",
  "ab-test": "abtest",
};

export const checkpointCommandText = "继续执行";

export const terminalThreadEventTypes = new Set(["done", "thread.done", "thread.stop", "intent.done"]);
export const failedThreadEventTypes = new Set(["error", "thread.error", "intent.error", "USER_ACTIVE_THREAD_EXISTS"]);
const inactiveTerminalThreadStatuses = new Set(["cancelled", "canceled", "ended", "failed", "error"]);

export const eventActionLabels: Record<string, string> = {
  start: "开始",
  progress: "进度更新",
  finish: "完成",
  failed: "失败",
  cancel: "已取消",
  pause: "已暂停",
  resume: "已恢复",
  "indexer.result": "索引器结果",
  "conductor.result": "编排器结果",
  "researcher.result": "研究员结果",
  "tool.used": "工具调用",
  "round.diff": "代码变更",
};

export const analysisCategoryLabels: Record<string, string> = {
  retrieval_miss: "检索问题",
  generation_drift: "生成偏移",
  score_anomaly: "评分异常",
};

export const analysisVerdictLabels: Record<string, string> = {
  confirmed: "已确认",
  refuted: "已推翻",
  inconclusive: "待补证",
  partial: "部分成立",
};

export const workflowStepDefinitions: Omit<WorkflowStep, "status" | "runtimeText">[] = [
  {
    id: "dataset",
    title: "Step 1 · 生成数据集",
    desc: "将任务目标拆分为训练样本，生成数据集数据并写入自进化流水线。",
  },
  {
    id: "px-report",
    title: "Step 2 · 执行评测",
    desc: "基于数据集执行 RAG 评测和 judge 评分，建立效果基线。",
  },
  {
    id: "analysis",
    title: "Step 3 · 问题分析",
    desc: "自动分析误答样本，产出问题归因和优先级建议。",
  },
  {
    id: "code-optimize",
    title: "Step 4 · 代码修改",
    desc: "展示 repair/opencode 的补丁生成、验证和优化 diff。",
  },
  {
    id: "ab-test",
    title: "Step 5 · ABTest 与切流",
    desc: "执行对照实验并在用户确认后推进候选服务切流。",
  },
];

export const workflowStepOrder = workflowStepDefinitions.map((step) => step.id);

export const getKnowledgeBaseName = (dataset: Dataset) =>
  dataset.display_name || dataset.name || dataset.dataset_id || "未命名知识库";

export const isCanceledRequest = (error: unknown) => {
  const normalizedError = error as {
    code?: string;
    name?: string;
    config?: { signal?: AbortSignal };
    message?: string;
  };
  const messageText = normalizedError?.message?.toLowerCase() || "";

  return (
    normalizedError?.code === "ERR_CANCELED" ||
    normalizedError?.name === "CanceledError" ||
    normalizedError?.config?.signal?.aborted ||
    messageText.includes("canceled") ||
    messageText.includes("cancelled") ||
    messageText.includes("aborted")
  );
};

export const existingEvalSetOptions = [
  { label: "不使用已有评测集", value: "__none__" },
];

export const evalSetPreviewData: EvalDataset = {
  eval_set_id: "b2e1616d-3d60-4327-9995-3d700e0a6e81",
  eval_name: "string4",
  kb_id: "ds_e030b437e04837ef4dbb952d45e16902",
  task_id: "379cffde-e43b-4f61-8310-d578f3094f6c",
  create_time: "2026-04-18 18:42:46",
  total_nums: 6,
  cases: [
    {
      case_id: "55b6c4b2-0bf7-4abf-8445-7d0e9acc553d",
      reference_doc: ["20384-【沪派江南】乡土行纪  第十四辑：水美林幽·风物万象.pdf"],
      reference_context: [
        "随后，大家来到大石皮村乡村生活馆，领略徐行草编文化的独特魅力。年轻的非遗传承人陈姣为大家讲述了徐行草编的历史渊源，作为江南著名的草编之乡，徐行草编以精湛的工艺和深厚的文化底蕴，于2008年入选第二批国家级非物质文化遗产名录。",
      ],
      is_deleted: false,
      question: "徐行草编于何时入选国家级非物质文化遗产名录？",
      question_type: 1,
      key_point: ["答题关键点"],
      ground_truth: "2008年入选第二批国家级非物质文化遗产名录",
    },
    {
      case_id: "04c504d7-ba7c-4bfb-8b78-5f1b3ca2802b",
      reference_doc: ["20387-【沪派江南】从水库村之变，理解沪派江南.pdf"],
      reference_context: [
        "水庫村採用“三師聯創”機制，保留了水網、疏浚河道、搭建23座橋梁打通水系；引入數字遊民打造全域全場景示範區，利用閒置空間開展100多場活動；設計宅基地安置點時保留菜地尊重傳統生活方式。",
      ],
      is_deleted: false,
      question:
        "水庫村在鄉村振興過程中，如何通過“三師聯創”機制，既保護了江南水鄉的水網風貌，又引入數字遊民實現產業創新，同時保留村民傳統生活方式？",
      question_type: 2,
      key_point: ["答题关键点"],
      ground_truth:
        "水庫村採用“三師聯創”機制，由規劃師、建築師、景觀師聯合設計，首先保留了水網密布的地理特徵，將河道疏浚整治、搭建橋梁打通水系、恢復濕地生態，而非填河為路，既保護了江南田園風貌又兼顧交通。同時引入數字遊民社區，利用村內閒置空間打造工作場景，開展各類活動為鄉村注入青年活力和產業機會，並通過企業會議、項目落地帶動經濟發展。在村民安置方面，設計江南風貌的別墅區並特意保留菜地，讓農戶延續種菜生活方式，避免城市化帶來的“不適應”。這種模式體現了生態保護、產業創新與文化傳承的協調發展。",
    },
  ],
};

export const questionTypeLabelMap: Record<number, string> = {
  1: "单跳",
  2: "多跳",
  3: "公式",
  4: "表格",
  5: "代码",
};

export const formatQuestionType = (questionType: number) => {
  const label = questionTypeLabelMap[questionType];
  if (!label) {
    return String(questionType);
  }
  return label;
};

export const clampScore = (value: number) => {
  if (!Number.isFinite(value)) {
    return 0;
  }
  return Math.min(1, Math.max(0, value));
};

export const formatPercent = (value: number) => `${(value * 100).toFixed(1)}%`;

export const getQuestionTypeDisplayName = (item: EvalQuestionTypeSummary, index: number) => {
  if (item.question_type_name?.trim()) {
    return item.question_type_name.trim();
  }
  if (item.question_type_key?.trim()) {
    return item.question_type_key.trim();
  }
  if (typeof item.question_type === "number") {
    return formatQuestionType(item.question_type);
  }
  return `分类 ${index + 1}`;
};

export const buildPxCategoryMetricAveragesFromReport = (payload: unknown): PxCategoryMetricAverage[] => {
  const sourceRecord = Array.isArray(payload)
    ? (payload.find((item): item is Record<string, unknown> => isRecord(item)) ?? undefined)
    : isRecord(payload)
      ? payload
      : undefined;
  const reportRecord = getNestedRecordField(sourceRecord, ["data"]) || sourceRecord;

  const caseDetailSummary =
    getStructuredRecordField(reportRecord, ["case_details_summary"]) ||
    getNestedRecordField(reportRecord, ["case_details_summary"]);
  const questionTypes = (getStructuredArrayField(caseDetailSummary, ["question_types"]) || []).filter(
    (item): item is EvalQuestionTypeSummary => isRecord(item),
  );

  if (questionTypes.length > 0) {
    return questionTypes
      .map((item, index) => ({
        category: getQuestionTypeDisplayName(item, index),
        caseCount: typeof item.count === "number" ? item.count : 0,
        metrics: {
          answer_correctness: clampScore(Number(item.averages?.answer_correctness ?? 0)),
          answer_score: clampScore(Number(item.averages?.answer_score ?? 0)),
          chunk_recall: clampScore(Number(item.averages?.chunk_recall ?? 0)),
          doc_recall: clampScore(Number(item.averages?.doc_recall ?? 0)),
        },
      }))
      .sort((a, b) => a.category.localeCompare(b.category, "zh-CN", { numeric: true }));
  }

  const metricsRecord = getNestedRecordField(reportRecord, ["metrics"]);
  if (metricsRecord) {
    return [{
      category: "总体",
      caseCount: getNumberField(reportRecord, ["total", "total_cases", "case_count"]) || 0,
      metrics: {
        answer_correctness: getMetricFieldNumber(metricsRecord, "answer_correctness"),
        answer_score: getMetricFieldNumber(metricsRecord, "answer_score"),
        chunk_recall: getMetricFieldNumber(metricsRecord, "chunk_recall"),
        doc_recall: getMetricFieldNumber(metricsRecord, "doc_recall"),
      },
    }];
  }

  const byQuestionType = getNestedRecordField(reportRecord, ["by_question_type"]);
  return Object.entries(byQuestionType || {})
    .filter((entry): entry is [string, Record<string, unknown>] => isRecord(entry[1]))
    .map(([category, item]) => ({
      category,
      caseCount: getNumberField(item, ["total", "count", "case_count"]) || 0,
      metrics: {
        answer_correctness: getMetricFieldNumber(item, "answer_correctness"),
        answer_score: getMetricFieldNumber(item, "answer_score"),
        chunk_recall: getMetricFieldNumber(item, "chunk_recall"),
        doc_recall: getMetricFieldNumber(item, "doc_recall"),
      },
    }))
    .sort((a, b) => a.category.localeCompare(b.category, "zh-CN", { numeric: true }));
};

export function getTimeLabel() {
  return new Date().toLocaleTimeString("zh-CN", {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}

export function createInitialWorkflowRuntimeState(): WorkflowRuntimeState {
  return {
    dataset: { status: "running" },
    "px-report": { status: "pending" },
    analysis: { status: "pending" },
    "code-optimize": { status: "pending" },
    "ab-test": { status: "pending" },
  };
}

export function createThreadRestoreWorkflowRuntimeState(): WorkflowRuntimeState {
  return {
    dataset: { status: "pending" },
    "px-report": { status: "pending" },
    analysis: { status: "pending" },
    "code-optimize": { status: "pending" },
    "ab-test": { status: "pending" },
  };
}

export function createCheckpointRestoreWorkflowRuntimeState(checkpoint: CheckpointWaitPrompt | undefined): WorkflowRuntimeState {
  const state = createThreadRestoreWorkflowRuntimeState();
  if (!checkpoint?.completedStage) {
    return state;
  }

  const currentStepId = stageStepMap[checkpoint.completedStage];
  const currentStepIndex = getWorkflowStepIndex(currentStepId);
  workflowStepOrder.forEach((stepId, index) => {
    if (index < currentStepIndex) {
      state[stepId] = { status: "done", progress: getCompletedProgressSnapshot() };
    }
  });

  state[currentStepId] = {
    status: "paused",
    runtimeText: checkpoint.message,
    progress: getCompletedProgressSnapshot(),
  };
  if (currentStepId === "px-report") {
    const progressPhases = getCompletedEvalProgressPhases();
    state[currentStepId] = {
      ...state[currentStepId],
      progress: getEvalOverallProgressSnapshot(progressPhases),
      progressPhases,
    };
  }
  return state;
}

export function createWorkflowRuntimeStateForMode(mode: EvolutionMode): WorkflowRuntimeState {
  return mode === "auto" ? createInitialWorkflowRuntimeState() : createThreadRestoreWorkflowRuntimeState();
}

export function createInitialWorkflowResultsState(): WorkflowResultsState {
  return {
    datasets: { loading: false, loaded: false },
    "eval-reports": { loading: false, loaded: false },
    "analysis-reports": { loading: false, loaded: false },
    diffs: { loading: false, loaded: false },
    abtests: { loading: false, loaded: false },
  };
}

export function getStepStatusLabel(status: StepStatus) {
  if (status === "running") {
    return "进行中";
  }
  if (status === "done") {
    return "已完成";
  }
  if (status === "paused") {
    return "已暂停";
  }
  if (status === "canceled") {
    return "已取消";
  }
  if (status === "failed") {
    return "已失败";
  }
  return "待执行";
}

export function getTerminalFlowStepStatus(status?: string): StepStatus | undefined {
  const normalizedStatus = status?.trim().toLowerCase();
  if (!normalizedStatus) {
    return undefined;
  }
  if (["cancel", "cancelled", "canceled"].includes(normalizedStatus)) {
    return "canceled";
  }
  if (["error", "failed"].includes(normalizedStatus)) {
    return "failed";
  }
  if (["completed", "done", "ended", "success", "succeeded"].includes(normalizedStatus)) {
    return "done";
  }
  return undefined;
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function getStringField(payload: Record<string, unknown> | undefined, keys: string[]) {
  if (!payload) {
    return undefined;
  }

  for (const key of keys) {
    const value = payload[key];
    if (typeof value === "string" && value.trim()) {
      return value.trim();
    }
  }

  return undefined;
}

export function getNumberField(payload: Record<string, unknown> | undefined, keys: string[]) {
  if (!payload) {
    return undefined;
  }

  for (const key of keys) {
    const value = payload[key];
    if (typeof value === "number" && Number.isFinite(value)) {
      return value;
    }
    if (typeof value === "string" && value.trim() && Number.isFinite(Number(value))) {
      return Number(value);
    }
  }

  return undefined;
}

export function getResultItems(value: unknown): unknown[] {
  if (Array.isArray(value)) {
    return value;
  }
  if (!isRecord(value)) {
    return [];
  }

  for (const key of ["data", "results", "items", "records", "reports", "datasets", "diffs", "abtests", "files"]) {
    const nestedValue = value[key];
    if (Array.isArray(nestedValue)) {
      return nestedValue;
    }
  }

  return [];
}

export function isEmptyResultPayload(value: unknown) {
  if (value === undefined || value === null) {
    return true;
  }
  if (typeof value === "string") {
    return value.trim().length === 0;
  }
  if (Array.isArray(value)) {
    return value.length === 0 || value.every(isEmptyResultPayload);
  }
  if (isRecord(value)) {
    const nestedItems = getResultItems(value);
    return nestedItems.length === 0 && Object.keys(value).length === 0;
  }
  return false;
}

export function stringifyResultPayload(value: unknown) {
  if (typeof value === "string") {
    return value;
  }
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

export function getResultStringField(value: unknown, keys: string[]): string | undefined {
  if (typeof value === "string" && value.trim()) {
    return value.trim();
  }
  if (Array.isArray(value)) {
    for (const item of value) {
      const result = getResultStringField(item, keys);
      if (result) {
        return result;
      }
    }
    return undefined;
  }
  if (!isRecord(value)) {
    return undefined;
  }

  const directValue = getStringField(value, keys);
  if (directValue) {
    return directValue;
  }

  for (const key of ["data", "result", "report", "content", "payload"]) {
    const nestedValue = value[key];
    const nestedString = getResultStringField(nestedValue, keys);
    if (nestedString) {
      return nestedString;
    }
  }

  const firstItem = getResultItems(value).find(Boolean);
  return getResultStringField(firstItem, keys);
}

export function buildCoreDownloadUrl(pathValue: string | undefined) {
  if (!pathValue) {
    return "";
  }

  const normalizedPath = pathValue.trim().replace(/^\/+/, "");
  if (!normalizedPath) {
    return "";
  }
  if (/^https?:\/\//i.test(normalizedPath)) {
    return normalizedPath;
  }

  const baseOrigin = BASE_URL || (typeof window !== "undefined" ? window.location.origin : "");
  if (!baseOrigin) {
    return "";
  }

  const corePath = normalizedPath.startsWith("api/core/")
    ? `/${normalizedPath}`
    : `/api/core/${normalizedPath}`;
  return new URL(corePath, baseOrigin).toString();
}

export function getResultDownloadPath(value: unknown) {
  if (Array.isArray(value)) {
    return getResultDownloadPath(value.find(Boolean));
  }

  return getResultStringField(value, [
    "file_url",
    "file_path",
    "relative_path",
    "stored_path",
    "artifact_path",
    "diff_artifact",
    "report_path",
  ]);
}

export function getDiffArtifactFiles(value: unknown): DiffArtifactFile[] {
  if (Array.isArray(value)) {
    return value.flatMap((item) => getDiffArtifactFiles(item));
  }
  if (!isRecord(value)) {
    return [];
  }

  const filesValue = value.files;
  if (Array.isArray(filesValue)) {
    return filesValue
      .filter((item): item is Record<string, unknown> => isRecord(item))
      .map((item, index) => {
        const path = getStringField(item, ["path", "file_path", "relative_path"]) || `unknown-file-${index + 1}`;
        const diffPath = getStringField(item, ["diff_path", "diff_artifact", "artifact_path", "stored_path"]) || "";
        return {
          path,
          diffPath,
          additions: getNumberField(item, ["additions"]),
          deletions: getNumberField(item, ["deletions"]),
          changeKind: getStringField(item, ["change_kind"]),
        };
      })
      .filter((item) => item.diffPath);
  }

  const directDiffPath = getStringField(value, ["diff_path", "diff_artifact", "artifact_path"]);
  if (directDiffPath) {
    return [
      {
        path: getStringField(value, ["path", "file_path", "relative_path"]) || "code-diff.diff",
        diffPath: directDiffPath,
        additions: getNumberField(value, ["additions"]),
        deletions: getNumberField(value, ["deletions"]),
        changeKind: getStringField(value, ["change_kind"]),
      },
    ];
  }

  for (const key of ["data", "result", "payload"]) {
    const nestedFiles = getDiffArtifactFiles(value[key]);
    if (nestedFiles.length > 0) {
      return nestedFiles;
    }
  }

  return [];
}

export function normalizeFetchedDiffArtifact(file: DiffArtifactFile, content: string) {
  const trimmedContent = content.trimEnd();
  if (!trimmedContent) {
    return "";
  }

  if (trimmedContent.includes("diff --git ")) {
    return trimmedContent;
  }

  const lines = trimmedContent.split("\n");
  const hasFileHeaders = lines.some((line) => line.startsWith("--- ")) && lines.some((line) => line.startsWith("+++ "));
  const diffHeader = `diff --git a/${file.path} b/${file.path}`;
  if (hasFileHeaders) {
    return [diffHeader, trimmedContent].join("\n");
  }

  return [diffHeader, `--- a/${file.path}`, `+++ b/${file.path}`, trimmedContent].join("\n");
}

export function getDownloadFileName(downloadUrl: string, fallbackFileName: string) {
  if (!downloadUrl) {
    return fallbackFileName;
  }

  const sanitizedUrl = downloadUrl.split("?")[0]?.split("#")[0] || "";
  const fileName = sanitizedUrl.split("/").filter(Boolean).pop();
  return fileName || fallbackFileName;
}

export function triggerBrowserDownload(downloadUrl: string, fileName: string) {
  const anchor = document.createElement("a");
  anchor.href = downloadUrl;
  anchor.download = fileName;
  anchor.target = "_blank";
  anchor.rel = "noopener noreferrer";
  document.body.appendChild(anchor);
  anchor.click();
  document.body.removeChild(anchor);
}

export function getNestedStringField(payload: Record<string, unknown> | undefined, keys: string[]) {
  const directValue = getStringField(payload, keys);
  if (directValue) {
    return directValue;
  }

  if (isRecord(payload?.data)) {
    return getStringField(payload.data, keys);
  }

  return undefined;
}

export function getNestedRecordField(payload: Record<string, unknown> | undefined, keys: string[]) {
  if (!payload) {
    return undefined;
  }

  for (const key of keys) {
    const value = payload[key];
    if (isRecord(value)) {
      return value;
    }
  }

  if (isRecord(payload.data)) {
    return getNestedRecordField(payload.data, keys);
  }

  return undefined;
}

export function getNestedArrayField(payload: ThreadRestorePayload, keys: string[]): unknown[] {
  if (Array.isArray(payload)) {
    return payload;
  }
  if (!isRecord(payload)) {
    return [];
  }

  for (const key of keys) {
    const value = payload[key];
    if (Array.isArray(value)) {
      return value;
    }
  }

  for (const key of ["data", "upstream", "result", "results", "thread"]) {
    const nestedValue = payload[key];
    if (isRecord(nestedValue) || Array.isArray(nestedValue)) {
      const nestedArray = getNestedArrayField(nestedValue, keys);
      if (nestedArray.length > 0) {
        return nestedArray;
      }
    }
  }

  return [];
}

export function formatThreadTime(value: unknown) {
  if (typeof value === "string" && value.trim()) {
    const date = new Date(value);
    if (!Number.isNaN(date.getTime())) {
      return date.toLocaleTimeString("zh-CN", {
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
      });
    }
    return value.trim();
  }

  if (typeof value === "number" && Number.isFinite(value)) {
    const date = new Date(value > 10_000_000_000 ? value : value * 1000);
    if (!Number.isNaN(date.getTime())) {
      return date.toLocaleTimeString("zh-CN", {
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
      });
    }
  }

  return getTimeLabel();
}

export function getThreadTimeSortValue(value: unknown) {
  if (typeof value === "string" && value.trim()) {
    const date = new Date(value);
    if (!Number.isNaN(date.getTime())) {
      return date.getTime();
    }
  }

  if (typeof value === "number" && Number.isFinite(value)) {
    const date = new Date(value > 10_000_000_000 ? value : value * 1000);
    if (!Number.isNaN(date.getTime())) {
      return date.getTime();
    }
  }

  return 0;
}

export function formatThreadListTime(value: unknown) {
  if (typeof value === "string" && value.trim()) {
    const date = new Date(value);
    if (!Number.isNaN(date.getTime())) {
      return date.toLocaleString("zh-CN", {
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
      });
    }
    return value.trim();
  }

  if (typeof value === "number" && Number.isFinite(value)) {
    const date = new Date(value > 10_000_000_000 ? value : value * 1000);
    if (!Number.isNaN(date.getTime())) {
      return date.toLocaleString("zh-CN", {
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
      });
    }
  }

  return "刚刚";
}

export function getThreadListItemTitle(item: Record<string, unknown>, threadId: string) {
  const payload = getNestedRecordField(item, ["thread_payload", "payload", "inputs", "input"]);
  return (
    getNestedStringField(item, ["title", "name", "thread_name", "display_name"]) ||
    getNestedStringField(payload, ["title", "name", "thread_name", "display_name", "kb_id", "dataset_id"]) ||
    `自进化会话 ${threadId.slice(0, 8)}`
  );
}

export function normalizeThreadListPayload(payload: unknown): ThreadHistoryEntry[] {
  const records = getNestedArrayField(payload as ThreadRestorePayload, ["threads", "items", "records", "data"]);

  return records
    .filter((item): item is Record<string, unknown> => isRecord(item))
    .reduce<ThreadHistoryEntry[]>((acc, item) => {
      const threadId = getNestedStringField(item, ["thread_id", "threadId", "id"]);
      if (!threadId) {
        return acc;
      }

      acc.push({
        threadId,
        title: getThreadListItemTitle(item, threadId),
        updatedAt: formatThreadListTime(
          item.updated_at || item.update_time || item.created_at || item.create_time || item.timestamp,
        ),
        status: getNestedStringField(item, ["status", "state"]),
      });
      return acc;
    }, []);
}

export function getDialogueEventAgentLabel(event: NormalizedThreadEvent) {
  if (event.type.startsWith("autooperator.")) {
    return "AutoOperator";
  }
  if (event.type === "message.user") {
    return "模拟用户";
  }
  if (event.type === "message.assistant") {
    return "回复 Agent";
  }
  return undefined;
}

export function buildAutoInteractionMessagesFromEvents(events: NormalizedThreadEvent[]): ChatMessage[] {
  return dedupeNormalizedEvents(events)
    .filter((event) => getDialogueEventAgentLabel(event) && (event.content || event.displayText))
    .map((event) => ({
      id: `event-chat-${event.key}`,
      role: event.role || "assistant",
      content: event.content || event.displayText || "",
      time: formatThreadTime(event.timestamp),
      sortTime:
        getThreadTimeSortValue(event.timestamp) ||
        (typeof event.sequence === "number" ? event.sequence : undefined),
      agentLabel: getDialogueEventAgentLabel(event),
    }))
    .sort((a, b) => {
      if (typeof a.sortTime === "number" && typeof b.sortTime === "number" && a.sortTime !== b.sortTime) {
        return a.sortTime - b.sortTime;
      }
      return a.id.localeCompare(b.id, "zh-CN", { numeric: true });
    });
}

function getHistoryMessageContent(value: unknown): string | undefined {
  if (typeof value === "string" && value.trim()) {
    return value.trim();
  }
  if (Array.isArray(value)) {
    const text = value
      .map((item) => {
        if (typeof item === "string") {
          return item;
        }
        if (isRecord(item)) {
          return getHistoryMessageContent(item.text) || getHistoryMessageContent(item.content);
        }
        return "";
      })
      .filter(Boolean)
      .join("");
    return text.trim() || undefined;
  }
  if (!isRecord(value)) {
    return undefined;
  }

  return (
    getStringField(value, ["content", "message", "text", "reply", "answer", "input", "output"]) ||
    getHistoryMessageContent(value.content) ||
    getHistoryMessageContent(value.message) ||
    getHistoryMessageContent(value.data) ||
    getHistoryMessageContent(value.payload)
  );
}

function getHistoryAssistantDeltaContent(value: unknown): string | undefined {
  if (Array.isArray(value)) {
    const text = value
      .map((item) => getHistoryAssistantDeltaContent(item))
      .filter(Boolean)
      .join("");
    return text.trim() || undefined;
  }

  if (isRecord(value)) {
    const type = getStringField(value, ["type", "event_name", "task_id"]);
    const delta = getStringField(value, ["delta", "content", "message"]);
    if ((type === "answer_delta" || type === "thinking_delta") && delta) {
      return delta;
    }
    return (
      getHistoryAssistantDeltaContent(value.records) ||
      getHistoryAssistantDeltaContent(value.events) ||
      getHistoryAssistantDeltaContent(value.data) ||
      getHistoryAssistantDeltaContent(value.payload) ||
      getHistoryAssistantDeltaContent(value.message)
    );
  }

  if (typeof value !== "string" || !value.trim()) {
    return undefined;
  }

  const deltas = value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line.startsWith("data:"))
    .map((line) => line.slice("data:".length).trim())
    .reduce<string[]>((acc, rawData) => {
      try {
        const payload = JSON.parse(rawData);
        if (!isRecord(payload)) {
          return acc;
        }
        const type = getStringField(payload, ["type"]);
        const delta = getStringField(payload, ["delta"]);
        if ((type === "answer_delta" || type === "thinking_delta") && delta) {
          acc.push(delta);
        }
      } catch {
        return acc;
      }
      return acc;
    }, []);

  return deltas.join("").trim() || undefined;
}

function normalizeHistoryEventMessages(payload: ThreadRestorePayload): ChatMessage[] {
  const rounds = getNestedArrayField(payload, ["rounds"]);
  const nestedRoundRecords = rounds.flatMap((item) =>
    isRecord(item)
      ? [
          ...getNestedArrayField(item, ["messages"]),
          ...getNestedArrayField(item, ["events"]),
          ...getNestedArrayField(item, ["records"]),
          ...getNestedArrayField(item, ["history"]),
        ]
      : [],
  );
  const records = [
    ...getNestedArrayField(payload, ["messages"]),
    ...getNestedArrayField(payload, ["events"]),
    ...getNestedArrayField(payload, ["records"]),
    ...getNestedArrayField(payload, ["history"]),
    ...nestedRoundRecords,
  ];

  return records
    .filter((item): item is Record<string, unknown> => isRecord(item))
    .flatMap<ChatMessage>((item, index) => {
      const event = normalizeThreadEvent({
        eventName: "message",
        data: JSON.stringify(item),
      });
      const directRole = getStringField(item, ["role"]);
      const role =
        event.role ||
        (directRole === "user" || directRole === "assistant" ? directRole : undefined);
      const content =
        event.content ||
        getNestedStringField(item, ["content", "message", "text", "reply", "answer"]);

      if (!role || !content) {
        return [];
      }

      const sortTime =
        getThreadTimeSortValue(event.timestamp) ||
        (typeof event.sequence === "number" ? event.sequence : undefined) ||
        index;

      return [
        {
          id: `thread-history-event-${event.key || index}`,
          role,
          content,
          time: formatThreadTime(event.timestamp),
          sortTime,
          agentLabel: getDialogueEventAgentLabel(event),
        },
      ];
    });
}

function dedupeAndSortChatMessages(messages: ChatMessage[]) {
  const seen = new Set<string>();
  return messages
    .filter((item) => {
      const key = `${item.role}:${item.content}:${item.sortTime ?? item.time}`;
      if (seen.has(key)) {
        return false;
      }
      seen.add(key);
      return true;
    })
    .sort((a, b) => {
      if (typeof a.sortTime === "number" && typeof b.sortTime === "number" && a.sortTime !== b.sortTime) {
        return a.sortTime - b.sortTime;
      }
      return a.id.localeCompare(b.id, "zh-CN", { numeric: true });
    });
}

export function normalizeThreadHistoryMessages(payload: ThreadRestorePayload): ChatMessage[] {
  const records = getNestedArrayField(payload, ["rounds"]);
  const roundMessages = records
    .filter((item): item is Record<string, unknown> => isRecord(item))
    .flatMap<ChatMessage>((item, index) => {
      const requestPayload = getNestedRecordField(item, ["request_payload"]);
      const userContent =
        getStringField(item, ["user_message", "userMessage"]) ||
        getHistoryMessageContent(requestPayload);
      const assistantContent =
        getStringField(item, ["assistant_message", "assistantMessage"]) ||
        getHistoryAssistantDeltaContent(item.records) ||
        getHistoryAssistantDeltaContent(item.assistant_message);
      const roundId = getStringField(item, ["round_id", "id"]) || `round-${index + 1}`;
      const createdAt = item.created_at || item.create_time || item.timestamp;
      const updatedAt = item.updated_at || item.update_time || createdAt;
      const baseSortTime =
        getThreadTimeSortValue(createdAt) ||
        getNumberField(item, ["sequence", "seq", "index"]) ||
        index * 2;
      const messages: ChatMessage[] = [];

      if (userContent) {
        messages.push({
          id: `thread-history-${roundId}-user-${index}`,
          role: "user",
          content: userContent,
          time: formatThreadTime(createdAt),
          sortTime: baseSortTime,
        });
      }

      if (assistantContent) {
        messages.push({
          id: `thread-history-${roundId}-assistant-${index}`,
          role: "assistant",
          content: assistantContent,
          time: formatThreadTime(updatedAt),
          sortTime: baseSortTime + 1,
        });
      }

      return messages;
    });

  return dedupeAndSortChatMessages([...normalizeHistoryEventMessages(payload), ...roundMessages]);
}

export function getEventPayloadData(payload: Record<string, unknown> | undefined) {
  if (isRecord(payload?.payload)) {
    return payload.payload;
  }
  if (isRecord(payload?.data)) {
    return payload.data;
  }
  return payload;
}

export function getThreadEventPayloadEnvelope(payload: Record<string, unknown> | undefined) {
  if (isRecord(payload?.payload)) {
    return payload.payload;
  }
  return undefined;
}

export function getThreadEventTypeFromPayload(payload: Record<string, unknown> | undefined) {
  const eventEnvelope = getThreadEventPayloadEnvelope(payload);
  const directTag = getStringField(payload, ["tag", "type"]);
  const nestedTag = getStringField(eventEnvelope, ["tag", "type"]);
  const eventName =
    getStringField(payload, ["event_name", "event", "kind", "name"]) ||
    getStringField(eventEnvelope, ["event_name", "event", "kind", "name"]);
  const stage =
    getStringField(payload, ["stage"]) ||
    getStringField(eventEnvelope, ["stage"]);

  if (!directTag && !nestedTag && stage === "message" && (eventName === "user" || eventName === "assistant")) {
    return `message.${eventName}`;
  }

  return directTag || nestedTag || eventName;
}

export function getThreadEventContentFromPayload(payload: Record<string, unknown> | undefined) {
  const eventEnvelope = getThreadEventPayloadEnvelope(payload);
  const eventPayload = getEventPayloadData(eventEnvelope) || getEventPayloadData(payload);

  return (
    getNestedStringField(payload, ["message", "content", "text", "reply", "thought", "delta"]) ||
    getNestedStringField(eventEnvelope, ["message", "content", "text", "reply", "thought", "delta"]) ||
    getNestedStringField(eventPayload, ["message", "content", "text", "reply", "thought", "delta"])
  );
}

export function clampPercent(value: number) {
  if (!Number.isFinite(value)) {
    return 0;
  }
  return Math.min(100, Math.max(0, Math.round(value)));
}

export function getRuntimeProgressStatusLabel(action: string | undefined) {
  if (action === "finish") {
    return "已完成";
  }
  if (action === "cancel") {
    return "已取消";
  }
  if (action === "pause") {
    return "已暂停";
  }
  return "进行中";
}

function getOperationRunId(payload: Record<string, unknown> | undefined) {
  const data = getEventPayloadData(payload);
  return getStringField(data, ["operation_run_id"]) || getStringField(getNestedRecordField(data, ["after"]) || getNestedRecordField(data, ["before"]), ["operation_run_id"]) ||
    getStringField(payload, ["operation_run_id"]);
}

function getEventFlowKind(payload: Record<string, unknown> | undefined) {
  const data = getEventPayloadData(payload);
  const value = getStringField(data, ["flow_kind"]) || getStringField(payload, ["flow_kind"]);
  return ({
    load_corpus: "dataset.load_corpus",
    build_corpus_snapshot: "dataset.build_corpus_snapshot",
    generate_case: "dataset.generate_case",
    assemble: "dataset.assemble",
  } as Record<string, string>)[value || ""] || value;
}

function getEventCaseId(payload: Record<string, unknown> | undefined) {
  const data = getEventPayloadData(payload);
  return getStringField(data, ["case_id"]) || getStringField(payload, ["case_id"]);
}

function getEventCaseProgress(payload: Record<string, unknown> | undefined): { current: number; total?: number } | undefined {
  const data = getEventPayloadData(payload);
  const current = getNumberField(data, ["case_index"]) ?? getNumberField(payload, ["case_index"]);
  return typeof current === "number" ? { current } : undefined;
}

function getEventArtifactId(payload: Record<string, unknown> | undefined) {
  const data = getEventPayloadData(payload);
  const detail = getNestedRecordField(data, ["detail"]) || getStructuredRecordField(data, ["detail"]);
  return getStringField(data, ["artifact_id", "writes_artifact_id"]) ||
    getStringField(detail, ["artifact_id", "writes_artifact_id"]) ||
    getStringField(payload, ["artifact_id", "writes_artifact_id"]);
}

function getEventDetailField(payload: Record<string, unknown> | undefined, keys: string[]) {
  const data = getEventPayloadData(payload);
  const detail = getNestedRecordField(data, ["detail"]) || getStructuredRecordField(data, ["detail"]);
  return getStringField(data, keys) || getStringField(detail, keys) || getStringField(payload, keys);
}

function getPayloadCaseTotal(eventData: Record<string, unknown> | undefined) {
  const detail = getNestedRecordField(eventData, ["detail"]) || getStructuredRecordField(eventData, ["detail"]);
  return getNumberField(eventData, ["total", "num_cases", "case_count", "count"]) ||
    getNumberField(detail, ["total", "num_cases", "case_count", "count"]);
}

function createSegmentProgressSnapshot(
  label: string,
  base: number,
  span: number,
  action: string | undefined,
  rank: number,
  current?: number,
  total?: number,
): WorkflowProgressSnapshot {
  const operationPercent =
    typeof current === "number" && typeof total === "number" && total > 0
      ? (current / total) * 100
      : typeof current === "number"
        ? 0
      : isActionKind(action, "finish")
        ? 100
        : 0;
  return {
    statusText: isActionKind(action, "finish") ? `${label}已完成` : `${label}中`,
    percent: clampPercent(base + (span * operationPercent) / 100),
    rank: rank + (current || 0),
  };
}

function getAbtestWorkflowProgressSnapshot(
  action: string | undefined,
  payload: Record<string, unknown> | undefined,
): WorkflowProgressSnapshot | undefined {
  const eventData = getEventPayloadData(payload);
  const flowKind = getEventFlowKind(payload);
  const operationProgress = getEventCaseProgress(payload);
  const caseTotal = getPayloadCaseTotal(eventData) || operationProgress?.total;
  const artifactId = getEventArtifactId(payload);
  const decision = getEventDetailField(payload, ["decision_status"]);

  if (flowKind === "abtest.candidate_rag_answer" && getEventCaseId(payload)) {
    return createSegmentProgressSnapshot("候选回答生成", 8, 40, action, 100, operationProgress?.current, caseTotal);
  }
  if (flowKind === "abtest.candidate_judge" && getEventCaseId(payload)) {
    return createSegmentProgressSnapshot("候选结果评测", 48, 40, action, 300, operationProgress?.current, caseTotal);
  }
  if (flowKind === "eval.aggregate" || artifactId === "candidate_eval_report") {
    return createSegmentProgressSnapshot("候选评测汇总", 88, 4, isActionKind(action, "finish") ? "progress" : action, 500);
  }
  if (flowKind === "abtest.candidate_service.start") {
    return createSegmentProgressSnapshot("候选服务启动", 0, 8, action, 50);
  }
  if (flowKind === "abtest.candidate_service.stop") {
    return {
      statusText: isActionKind(action, "finish") ? "候选服务回收已完成" : "候选服务回收中",
      percent: isActionKind(action, "finish") ? 100 : 98,
      rank: 750,
    };
  }
  if (decision) {
    return {
      statusText: decision.toLowerCase() === "accept" ? "候选通过切流门槛" : "候选未通过切流门槛",
      percent: 96,
      rank: 650,
    };
  }
  if (flowKind === "abtest.compare") {
    return createSegmentProgressSnapshot("A/B 对照决策", 92, 4, action, 600);
  }
  if (flowKind === "abtest.candidate_cutover" || artifactId === "candidate_algorithm_cutover") {
    return {
      statusText: isActionKind(action, "finish") ? "候选算法切流已完成" : "等待确认切流",
      percent: isActionKind(action, "finish") ? 100 : 96,
      rank: 700,
    };
  }

  return undefined;
}

const datasetOperationSegments = {
  "dataset.load_corpus": { label: "加载语料", base: 0, span: 18, rank: 10 },
  "dataset.build_corpus_snapshot": { label: "构建语料快照", base: 18, span: 17, rank: 20 },
  "dataset.generate_case": { label: "生成样本", base: 35, span: 45, rank: 30 },
  "dataset.assemble": { label: "组装数据集", base: 80, span: 20, rank: 50 },
} as const;

function getDatasetWorkflowProgressSnapshot(
  action: string | undefined,
  payload: Record<string, unknown> | undefined,
): WorkflowProgressSnapshot | undefined {
  const eventData = getEventPayloadData(payload);
  const segment = datasetOperationSegments[getEventFlowKind(payload) as keyof typeof datasetOperationSegments];
  if (!segment) {
    if (isActionKind(action, "finish")) {
      return getStringField(eventData, ["stage"]) === "dataset" ? getCompletedProgressSnapshot() : undefined;
    }
    return undefined;
  }

  const operationProgress = getEventCaseProgress(payload);
  const current =
    getNumberField(eventData, ["current", "completed", "done", "processed"]) ??
    operationProgress?.current;
  const total = getNumberField(eventData, ["total", "num_cases", "cases", "count"]);
  const explicitPercent = getNumberField(eventData, ["percent", "percentage", "progress"]);
  const operationPercent =
    typeof explicitPercent === "number"
      ? explicitPercent
      : typeof current === "number" && typeof total === "number" && total > 0
        ? (current / total) * 100
        : isActionKind(action, "finish")
          ? 100
          : isActionKind(action, "start")
            ? 0
            : undefined;

  if (typeof operationPercent !== "number") {
    return {
      statusText: `${segment.label}进行中`,
      percent: segment.base,
      rank: segment.rank,
    };
  }

  return {
    statusText: isActionKind(action, "finish") ? `${segment.label}已完成` : `${segment.label}进行中`,
    percent: clampPercent(segment.base + (segment.span * operationPercent) / 100),
    rank: segment.rank,
  };
}

type EvalPayloadPhase = WorkflowProgressPhaseId;

function normalizePhaseText(value: unknown) {
  return typeof value === "string" ? value.trim().toLowerCase() : "";
}

function isActionKind(action: string | undefined, kind: string) {
  const normalized = normalizePhaseText(action);
  return normalized === kind || normalized.endsWith(`.${kind}`);
}

function getEvalPayloadPhase(
  action: string | undefined,
  type: string | undefined,
  payload: Record<string, unknown> | undefined,
): EvalPayloadPhase | undefined {
  const eventData = getEventPayloadData(payload);
  const candidates = [
    action,
    type,
    getStringField(eventData, ["phase", "task", "task_type", "step", "name", "kind", "type", "event"]),
    getStringField(payload, ["phase", "task", "task_type", "step", "name", "kind", "type", "event"]),
  ]
    .map(normalizePhaseText)
    .filter(Boolean);

  if (candidates.some((item) => item.includes("judge"))) {
    return "judge";
  }
  if (candidates.some((item) => item.includes("rag"))) {
    return "rag";
  }
  if (isRecord(eventData?.judge) || eventData?.judge === true) {
    return "judge";
  }
  if (isRecord(eventData?.rag) || eventData?.rag === true) {
    return "rag";
  }
  return undefined;
}

function getEvalPhasePayloadData(payload: Record<string, unknown> | undefined, phase: EvalPayloadPhase | undefined) {
  const eventData = getEventPayloadData(payload);
  if (phase && isRecord(eventData?.[phase])) {
    return eventData[phase];
  }
  return eventData;
}

function getEvalPhaseLabel(phase: EvalPayloadPhase | undefined) {
  if (phase === "judge") {
    return "实际评测";
  }
  if (phase === "rag") {
    return "评测集问答生成";
  }
  return "执行评测";
}

function getEvalProgressStatusLabel(action: string | undefined, phase: EvalPayloadPhase | undefined) {
  if (isActionKind(action, "finish")) {
    return `${getEvalPhaseLabel(phase)}已完成`;
  }
  if (isActionKind(action, "cancel")) {
    return `${getEvalPhaseLabel(phase)}已取消`;
  }
  if (isActionKind(action, "pause")) {
    return `${getEvalPhaseLabel(phase)}已暂停`;
  }
  return `${getEvalPhaseLabel(phase)}中`;
}

export function getCompletedProgressSnapshot(): WorkflowProgressSnapshot {
  return {
    statusText: "已完成",
    percent: 100,
  };
}

function updateProgressStatusText(
  progress: WorkflowProgressSnapshot | undefined,
  statusText: string,
): WorkflowProgressSnapshot | undefined {
  return progress ? { ...progress, statusText } : progress;
}

function mergeProgressSnapshot(
  current: WorkflowProgressSnapshot | undefined,
  next: WorkflowProgressSnapshot | undefined,
): WorkflowProgressSnapshot | undefined {
  if (!next || !current) {
    return next || current;
  }
  if ((next.rank ?? -1) < (current.rank ?? -1)) {
    return current;
  }
  return next.percent < current.percent && next.statusText === current.statusText ? current : next;
}

const evalProgressPhaseDefinitions: Record<WorkflowProgressPhaseId, Omit<WorkflowProgressPhaseSnapshot, "statusText" | "percent">> = {
  rag: {
    id: "rag",
    title: "RAG 生成回答",
    desc: "取评测集样本，调用大模型生成回答",
  },
  judge: {
    id: "judge",
    title: "Judge 实际评测",
    desc: "对回答进行评分并汇总评测结果",
  },
};

function createEvalProgressPhaseSnapshot(
  phase: WorkflowProgressPhaseId,
  progress?: WorkflowProgressSnapshot,
): WorkflowProgressPhaseSnapshot {
  return {
    ...evalProgressPhaseDefinitions[phase],
    statusText: progress?.statusText || "等待开始",
    percent: progress?.percent ?? 0,
  };
}

function getDefaultEvalProgressPhases(): WorkflowProgressPhaseSnapshot[] {
  return [
    createEvalProgressPhaseSnapshot("rag"),
    createEvalProgressPhaseSnapshot("judge"),
  ];
}

function getCompletedEvalProgressPhases(): WorkflowProgressPhaseSnapshot[] {
  return [
    createEvalProgressPhaseSnapshot("rag", getCompletedProgressSnapshot()),
    createEvalProgressPhaseSnapshot("judge", getCompletedProgressSnapshot()),
  ];
}

function getEvalOverallProgressSnapshot(phases: WorkflowProgressPhaseSnapshot[] | undefined): WorkflowProgressSnapshot | undefined {
  if (!phases?.length) {
    return undefined;
  }
  const activePhase =
    phases.find((item) => item.statusText.endsWith("中")) ||
    phases.find((item) => item.percent > 0 && item.percent < 100) ||
    phases.find((item) => item.percent < 100);
  return {
    statusText: phases.every((item) => item.percent >= 100) ? "已完成" : activePhase?.statusText || "进行中",
    percent: clampPercent(phases.reduce((sum, item) => sum + item.percent, 0) / phases.length),
  };
}

function updateEvalProgressPhases(
  current: WorkflowProgressPhaseSnapshot[] | undefined,
  phase: WorkflowProgressPhaseId | undefined,
  progress: WorkflowProgressSnapshot | undefined,
  action: string | undefined,
  isOperationScoped = false,
): WorkflowProgressPhaseSnapshot[] {
  if (isActionKind(action, "finish") && !isOperationScoped && (!phase || phase === "judge")) {
    return getCompletedEvalProgressPhases();
  }

  const next = current?.length ? [...current] : getDefaultEvalProgressPhases();
  if (!phase) {
    return progress
      ? next.map((item) => ({
          ...item,
          statusText: progress.statusText,
          percent: progress.percent,
        }))
      : next;
  }

  const currentPhase = next.find((item) => item.id === phase);
  const progressSnapshot = progress || {
    statusText: isActionKind(action, "finish") && isOperationScoped
      ? `${getEvalPhaseLabel(phase)}中`
      : getEvalProgressStatusLabel(action, phase),
    percent: isActionKind(action, "finish") && !isOperationScoped ? 100 : currentPhase?.percent ?? 0,
  };

  return next.map((item) => {
    if (item.id === phase) {
      return createEvalProgressPhaseSnapshot(phase, progressSnapshot);
    }

    if (phase === "judge" && item.id === "rag" && item.percent < 100) {
      return createEvalProgressPhaseSnapshot("rag", getCompletedProgressSnapshot());
    }

    return item;
  });
}

export function parseStructuredRecord(value: unknown): Record<string, unknown> | undefined {
  if (isRecord(value)) {
    return value;
  }
  if (typeof value !== "string") {
    return undefined;
  }

  const candidates: string[] = [];
  const trimmed = value.trim();
  if (trimmed) {
    candidates.push(trimmed);
  }

  const fencedMatch = trimmed.match(/```(?:json)?\s*([\s\S]*?)```/i);
  if (fencedMatch?.[1]?.trim()) {
    candidates.unshift(fencedMatch[1].trim());
  }

  const firstBrace = trimmed.indexOf("{");
  const lastBrace = trimmed.lastIndexOf("}");
  if (firstBrace >= 0 && lastBrace > firstBrace) {
    candidates.push(trimmed.slice(firstBrace, lastBrace + 1));
  }

  for (const candidate of candidates) {
    try {
      const parsed = JSON.parse(candidate);
      if (isRecord(parsed)) {
        return parsed;
      }
    } catch {
      continue;
    }
  }

  return undefined;
}

export function parseStructuredArray(value: unknown): unknown[] | undefined {
  if (Array.isArray(value)) {
    return value;
  }
  if (typeof value !== "string") {
    return undefined;
  }

  try {
    const parsed = JSON.parse(value);
    return Array.isArray(parsed) ? parsed : undefined;
  } catch {
    return undefined;
  }
}

export function getStructuredRecordField(payload: Record<string, unknown> | undefined, keys: string[]) {
  if (!payload) {
    return undefined;
  }

  for (const key of keys) {
    const parsed = parseStructuredRecord(payload[key]);
    if (parsed) {
      return parsed;
    }
  }

  return undefined;
}

export function getStructuredArrayField(payload: Record<string, unknown> | undefined, keys: string[]) {
  if (!payload) {
    return undefined;
  }

  for (const key of keys) {
    const value = payload[key];
    if (Array.isArray(value)) {
      return value;
    }
    const parsed = parseStructuredArray(value);
    if (parsed) {
      return parsed;
    }
  }

  return undefined;
}

export function formatAnalysisVerdict(verdict: string | undefined) {
  if (!verdict) {
    return "调查中";
  }
  return analysisVerdictLabels[verdict] || verdict;
}

export function formatAnalysisCategory(category: string | undefined) {
  if (!category) {
    return "待归类";
  }
  return analysisCategoryLabels[category] || category;
}

export function formatConfidencePercent(value: number | undefined) {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return undefined;
  }

  const normalized = value <= 1 ? value * 100 : value;
  return `${Math.round(normalized)}%`;
}

export function formatAnalysisAgentName(agent: string | undefined) {
  if (!agent) {
    return "研究子代理";
  }
  if (agent.startsWith("researcher:")) {
    return `研究员 ${agent.slice("researcher:".length)}`;
  }
  return agent;
}

export function buildAnalysisEventDisplayText(
  action: string | undefined,
  type: string,
  payload: Record<string, unknown> | undefined,
) {
  const eventData = getEventPayloadData(payload);

  if (action === "start") {
    return "已启动问题归因分析，正在生成调查方向。";
  }

  if (type === "run.indexer.result") {
    const resultRecord = getNestedRecordField(eventData, ["result"]) || getStructuredRecordField(eventData, ["summary"]);
    const hypotheses = getStructuredArrayField(resultRecord, ["hypotheses"]) || [];
    return hypotheses.length > 0
      ? `已生成 ${hypotheses.length} 条调查假设，准备分配给研究子代理。`
      : "已完成首轮问题扫描，正在整理调查假设。";
  }

  if (type === "run.conductor.result") {
    const resultRecord = getNestedRecordField(eventData, ["result"]) || getStructuredRecordField(eventData, ["summary"]);
    const iteration = getNumberField(eventData, ["iteration"]) ?? getNumberField(resultRecord, ["iterations"]);
    const converged = resultRecord?.converged === true;
    const totalActions = getNumberField(resultRecord, ["total_actions"]);
    if (converged) {
      const actionText = typeof totalActions === "number" ? `，累计处理 ${totalActions} 项动作` : "";
      return `分析已收敛，共完成 ${iteration || 0} 轮编排${actionText}。`;
    }
    if (typeof iteration === "number" && iteration > 0) {
      return `已完成第 ${iteration} 轮编排，正在继续分配调查任务。`;
    }
    return "编排器正在整理研究子代理的调查任务。";
  }

  if (type === "run.tool.used") {
    const agent = formatAnalysisAgentName(getStringField(eventData, ["agent"]));
    const tool = getStringField(eventData, ["tool"]) || "工具";
    return `${agent} 正在使用 ${tool} 收集证据。`;
  }

  if (type === "run.researcher.result") {
    const agent = formatAnalysisAgentName(getStringField(eventData, ["agent"]));
    const resultRecord = getStructuredRecordField(eventData, ["result_summary"]);
    const hypothesisId = getStringField(resultRecord, ["hypothesis_id"]);
    const verdict = formatAnalysisVerdict(getStringField(resultRecord, ["verdict"]));
    return hypothesisId
      ? `${agent} 已提交 ${hypothesisId} 的调查结论：${verdict}。`
      : `${agent} 已提交一条调查结论。`;
  }

  if (action === "finish") {
    return "分析完成，已生成可查看的分析报告。";
  }

  if (action === "cancel") {
    return "分析已取消，当前结果未继续推进。";
  }

  if (action === "pause") {
    return "分析已暂停，等待继续执行。";
  }

  return undefined;
}

export function buildApplyEventDisplayText(
  action: string | undefined,
  type: string,
  payload: Record<string, unknown> | undefined,
) {
  const eventData = getEventPayloadData(payload);
  const phase = getStringField(eventData, ["phase", "task", "task_type", "step", "name", "kind", "type", "event"]);
  const detail = getNestedRecordField(eventData, ["detail"]) || getStructuredRecordField(eventData, ["detail"]);
  const attempt = getNumberField(eventData, ["attempt"]) ?? getNumberField(detail, ["attempt"]);
  const failure = getStringField(detail, ["failure", "failure_summary"]);

  if (phase === "repair_loop") {
    if (isActionKind(action, "finish")) {
      const decision = getStringField(detail, ["decision"]) || "完成";
      return `修复循环已结束，结果：${decision}。`;
    }
    return typeof attempt === "number" ? `修复循环正在执行第 ${attempt} 轮。` : "修复循环正在运行。";
  }

  if (phase === "opencode") {
    return typeof attempt === "number" ? `opencode 正在执行第 ${attempt} 轮代码修改。` : "opencode 正在生成候选改动。";
  }

  if (phase === "repair_patch") {
    const status = isActionKind(action, "failed") ? "未通过" : "已生成";
    return failure ? `候选补丁${status}：${failure}。` : `候选补丁${status}，正在等待验证。`;
  }

  if (phase === "repair_candidate_service" || phase === "candidate_service") {
    return isActionKind(action, "finish") ? "候选服务已启动并可用于验证。" : "候选服务正在启动，用于真实评测验证。";
  }

  if (action === "start") {
    return "已启动代码修改，正在生成候选改动。";
  }

  if (type === "apply.round.diff") {
    const round = getNumberField(eventData, ["round"]);
    const filesChanged = (getStructuredArrayField(eventData, ["files_changed"]) || []).filter(
      (item): item is string => typeof item === "string" && item.trim().length > 0,
    );
    const diffSummary = getStringField(eventData, ["diff_summary"]);
    const testsText = diffSummary?.includes("tests passed")
      ? "，测试已通过"
      : diffSummary?.includes("tests not run")
        ? "，尚未执行测试"
        : diffSummary?.includes("tests failed")
          ? "，测试未通过"
          : "";
    const fileText =
      filesChanged.length > 0 ? `涉及 ${filesChanged.length} 个文件` : "暂未产出文件改动";
    return typeof round === "number"
      ? `已产出第 ${round} 轮候选改动，${fileText}${testsText}。`
      : `已产出一轮候选改动，${fileText}${testsText}。`;
  }

  if (action === "finish") {
    return "候选修改版本已准备完成，可查看代码改动结果。";
  }

  if (action === "cancel") {
    return "代码修改已取消，当前候选版本未继续推进。";
  }

  return undefined;
}

export function buildDatasetEventDisplayText(
  action: string | undefined,
  payload: Record<string, unknown> | undefined,
) {
  const eventData = getEventPayloadData(payload);
  const operationSegment = datasetOperationSegments[getEventFlowKind(payload) as keyof typeof datasetOperationSegments];
  const current = getNumberField(eventData, ["current", "completed", "done", "processed"]);
  const total = getNumberField(eventData, ["total", "num_cases", "cases", "count"]);
  const countText =
    typeof current === "number" && typeof total === "number" && total > 0
      ? `，进度 ${current}/${total}`
      : typeof total === "number" && total > 0
        ? `，共 ${total} 条样本`
        : "";

  if (isActionKind(action, "start")) {
    return "已启动数据集生成，正在准备评测样本。";
  }
  if (isActionKind(action, "finish")) {
    if (operationSegment && operationSegment.base + operationSegment.span < 100) {
      return `${operationSegment.label}已完成，继续等待后续数据集操作。`;
    }
    return "数据集生成已完成，可下载查看结果。";
  }
  if (isActionKind(action, "cancel")) {
    return "数据集生成已取消。";
  }
  if (isActionKind(action, "pause")) {
    return "数据集生成已暂停，等待继续执行。";
  }
  return operationSegment
    ? `${operationSegment.label}正在执行${countText}。`
    : `数据集生成正在执行${countText}。`;
}

export function buildEvalEventDisplayText(
  action: string | undefined,
  type: string,
  payload: Record<string, unknown> | undefined,
) {
  const phase = getEvalPayloadPhase(action, type, payload);
  const eventData = getEvalPhasePayloadData(payload, phase);
  const phaseLabel = getEvalPhaseLabel(phase);
  const current = getNumberField(eventData, ["current", "completed", "done", "processed"]);
  const total = getNumberField(eventData, ["total", "num_cases", "cases", "count"]);
  const countText =
    typeof current === "number" && typeof total === "number" && total > 0
      ? `，进度 ${current}/${total}`
      : typeof total === "number" && total > 0
        ? `，共 ${total} 条样本`
        : "";

  if (isActionKind(action, "start")) {
    return phase === "rag"
      ? "已开始从评测集取样并调用大模型生成回答。"
      : phase === "judge"
        ? "已开始对模型回答进行实际评测。"
        : "已启动评测流程，正在准备评测任务。";
  }

  if (isActionKind(action, "finish")) {
    return phase === "rag"
      ? "评测集问答生成已完成，准备进入实际评测。"
      : phase === "judge"
        ? "实际评测已完成，可生成评测报告。"
        : "评测流程已完成，可查看评测报告。";
  }

  if (isActionKind(action, "cancel")) {
    return `${phaseLabel}已取消。`;
  }

  if (isActionKind(action, "pause")) {
    return `${phaseLabel}已暂停，等待继续执行。`;
  }

  if (phase) {
    return `${phaseLabel}正在执行${countText}。`;
  }

  return undefined;
}

export function buildAbtestEventDisplayText(action: string | undefined, payload?: Record<string, unknown>) {
  const eventData = getEventPayloadData(payload);
  const flowKind = getEventFlowKind(payload);
  const operationProgress = getEventCaseProgress(payload);
  const caseTotal = getPayloadCaseTotal(eventData) || operationProgress?.total;
  const status = getStringField(eventData, ["status"]);
  const decision = getEventDetailField(payload, ["decision_status"]);
  const caseText = operationProgress?.current
    ? `，case ${operationProgress.current}${caseTotal ? `/${caseTotal}` : ""}`
    : "";
  if (flowKind === "abtest.candidate_rag_answer" && getEventCaseId(payload)) {
    return `候选版本正在生成回答${caseText}。`;
  }
  if (flowKind === "abtest.candidate_judge" && getEventCaseId(payload)) {
    return `候选版本正在接受实际评测${caseText}。`;
  }
  if (flowKind === "eval.aggregate" || getEventArtifactId(payload) === "candidate_eval_report") {
    return isActionKind(action, "finish") ? "候选评测报告已汇总完成。" : "正在汇总候选评测报告。";
  }
  if (flowKind === "abtest.candidate_cutover") {
    return isActionKind(action, "finish") ? "候选算法切流已完成。" : "正在准备候选算法切流，需用户确认后推进。";
  }
  if (flowKind === "abtest.candidate_service.stop") {
    return status === "success" || isActionKind(action, "finish")
      ? "候选服务已停止，候选版本未切流。"
      : "正在停止候选服务，回收未通过的候选版本。";
  }
  if (flowKind === "abtest.candidate_service.start") {
    return isActionKind(action, "finish") ? "候选服务已就绪，可用于 A/B 对照。" : "正在启动候选服务。";
  }
  if (decision) {
    return decision === "accept"
      ? "A/B 对照决策完成：候选版本通过切流门槛。"
      : "A/B 对照决策完成：候选版本未通过切流门槛。";
  }
  if (flowKind === "abtest.compare") {
    return isActionKind(action, "finish") ? "A/B 对照评测已完成，正在整理决策结果。" : "正在执行 A/B 对照评测。";
  }
  if (action === "start") {
    return "已启动 A/B 测试，正在基于同一批样本执行对照评测。";
  }
  if (action === "finish") {
    return "A/B 测试已完成，可查看对比结果。";
  }
  if (action === "cancel") {
    return "A/B 测试已取消。";
  }
  return undefined;
}

export function getWorkflowProgressSnapshot(
  stage: ThreadEventStage | undefined,
  action: string | undefined,
  payload: Record<string, unknown> | undefined,
  type?: string,
): WorkflowProgressSnapshot | undefined {
  if (stage !== "dataset" && stage !== "eval" && stage !== "abtest") {
    return undefined;
  }

  if (stage === "dataset") {
    return getDatasetWorkflowProgressSnapshot(action, payload);
  }

  if (stage === "abtest") {
    return getAbtestWorkflowProgressSnapshot(action, payload);
  }

  const eventData = getEventPayloadData(payload);
  const evalPhase = stage === "eval" ? getEvalPayloadPhase(action, type, payload) : undefined;
  const progressData = stage === "eval" ? getEvalPhasePayloadData(payload, evalPhase) : eventData;
  const operationRunId = getOperationRunId(payload);
  const isEvalOperationScoped = stage === "eval" && Boolean(operationRunId);
  const operationProgress = getEventCaseProgress(payload);
  const current = getNumberField(progressData, ["current", "completed", "done", "processed"]) ?? operationProgress?.current;
  const total = getNumberField(progressData, ["total", "num_cases", "cases", "count"]) ?? operationProgress?.total;
  const explicitPercent = getNumberField(progressData, ["percent", "percentage", "progress"]);
  const hasProgressValue =
    typeof explicitPercent === "number" ||
    (typeof current === "number" && typeof total === "number" && total > 0);
  const percent =
    typeof explicitPercent === "number"
      ? explicitPercent
      : typeof current === "number" && typeof total === "number" && total > 0
        ? (current / total) * 100
        : isActionKind(action, "finish")
          ? isEvalOperationScoped
            ? undefined
            : 100
          : isActionKind(action, "start") && hasProgressValue
            ? 0
            : undefined;

  if (typeof percent !== "number") {
    return undefined;
  }

  const rank = operationProgress?.current ?? (getEventFlowKind(payload) === "dataset.assemble" ? current : undefined);
  return {
    statusText: rank ? "进行中" : stage === "eval" ? getEvalProgressStatusLabel(action, evalPhase) : getRuntimeProgressStatusLabel(action),
    percent: clampPercent(percent),
    rank,
  };
}

function isAbtestStageCompleteEvent(event: Pick<NormalizedThreadEvent, "action" | "progress" | "payload" | "stage">) {
  if (event.stage !== "abtest" || !isActionKind(event.action, "finish")) {
    return false;
  }
  return getEventArtifactId(event.payload) === "abtest_comparison" ||
    getEventArtifactId(event.payload) === "candidate_algorithm_cutover" ||
    getEventFlowKind(event.payload) === "abtest.candidate_cutover";
}

function isIntentSidecarOperation(event: Pick<NormalizedThreadEvent, "payload">) {
  const operationRunId = getOperationRunId(event.payload) || "";
  return (
    operationRunId.startsWith("intent.") ||
    operationRunId.startsWith("dataset.assemble.intervention.")
  );
}

function isStepFinishEvent(event: Pick<NormalizedThreadEvent, "action" | "progress" | "progressPhase" | "payload" | "stage">) {
  if (!isActionKind(event.action, "finish")) {
    return false;
  }
  if (isAbtestStageCompleteEvent(event)) {
    return true;
  }
  if (getOperationRunId(event.payload)) {
    return false;
  }
  if (event.stage === "dataset" && getStringField(getEventPayloadData(event.payload), ["stage"]) === "dataset_corpus") {
    return false;
  }
  return event.stage === "eval" ? !event.progressPhase || event.progressPhase === "judge" : true;
}

export function toThreadEventStage(value: unknown): ThreadEventStage | undefined {
  if (typeof value !== "string") {
    return undefined;
  }

  const normalized = value.trim();
  return {
    dataset: "dataset",
    eval: "eval",
    candidate_eval: "abtest",
    run: "analysis",
    analysis: "analysis",
    apply: "repair",
    repair: "repair",
    abtest: "abtest",
  }[normalized] as ThreadEventStage | undefined;
}

export function getStageLabel(value: unknown) {
  const stage = toThreadEventStage(value);
  if (stage) {
    return stageLabels[stage];
  }
  if (typeof value === "string" && value.trim()) {
    return value.trim();
  }
  return undefined;
}

export function getNextStageFromOperation(value: string | undefined): ThreadEventStage | undefined {
  if (!value) {
    return undefined;
  }

  const [operationStage] = value.split(".");
  return toThreadEventStage(operationStage);
}

export function formatCheckpointOperation(value: string | undefined) {
  if (!value) {
    return undefined;
  }

  const [operationStage, ...operationParts] = value.split(".");
  const stageLabel = getStageLabel(operationStage);
  const rawAction = operationParts.join(".");
  const actionLabel =
    {
      "": "",
      run: "",
      loop: "",
      candidate_cutover: "候选算法切流",
      "candidate_service.start": "候选服务启动",
      "candidate_service.stop": "候选服务停止",
    }[rawAction] ?? rawAction.replace(/_/g, " ");
  return [stageLabel, actionLabel].filter(Boolean).join(" · ");
}

function formatCheckpointCapability(value: string | undefined) {
  if (!value) {
    return undefined;
  }

  return {
    patch_dataset_case: "修改数据集样本",
    regenerate_dataset_case: "重写数据集样本",
    prepare_dataset_case: "准备评测样本",
    generate_dataset_case: "生成评测样本",
  }[value] ?? value.replace(/_/g, " ");
}

export function sanitizeCheckpointMessage(
  value: string,
  completedStageLabel: string | undefined,
  nextOperationLabel: string | undefined,
) {
  const cleaned = value
    .replace(/\([^)]*(?:task_id|abtest_id|summary_path|dataset_id|thread_id|\/var\/lib)[^)]*\)/gi, "")
    .replace(/\/var\/lib\/[^\s，。；、)）]+/g, "")
    .replace(/\s+/g, " ")
    .replace(/\s*([，。；、])\s*/g, "$1")
    .replace(/^[，。；、]+|[，。；、]+$/g, "")
    .trim();

  if (cleaned && cleaned.length <= 120) {
    return cleaned;
  }

  if (completedStageLabel && nextOperationLabel) {
    return `${completedStageLabel}已完成，请确认是否继续执行下一步。`;
  }
  if (completedStageLabel) {
    return `${completedStageLabel}已完成，请确认是否继续执行。`;
  }
  return "当前流程已暂停，请确认是否继续执行。";
}

export function buildCheckpointWaitPrompt(payload: Record<string, unknown> | undefined): CheckpointWaitPrompt {
  const eventData = getEventPayloadData(payload);
  const nextOperation = getNestedRecordField(eventData, ["next_op", "nextOperation", "next"]);
  const nextOperationName = getStringField(nextOperation, ["op", "operation", "name"]);
  const checkpointKind = getStringField(eventData, ["checkpoint_kind", "checkpointKind"]) ||
    getStringField(payload, ["checkpoint_kind", "checkpointKind"]);
  const capabilityLabel = formatCheckpointCapability(
    getStringField(eventData, ["capability_id", "capabilityId"]) ||
      getStringField(payload, ["capability_id", "capabilityId"]),
  );
  const artifacts = getNestedRecordField(eventData, ["artifacts", "result", "data"]);
  const messageText =
    getStringField(eventData, ["message", "text", "content"]) ||
    getStringField(payload, ["message", "text", "content"]) ||
    "当前流程已暂停，等待确认是否继续下一步。";
  const completedStageLabel = getStageLabel(
    getStringField(eventData, ["completed_flow", "completed_stage", "stage"]) ||
      getStringField(artifacts, ["completed_flow", "stage"]),
  );
  const completedStage = toThreadEventStage(
    getStringField(eventData, ["completed_flow", "completed_stage", "stage"]) ||
      getStringField(artifacts, ["completed_flow", "stage"]),
  );
  const nextOperationLabel = formatCheckpointOperation(nextOperationName);
  const nextStage = toThreadEventStage(
    getStringField(eventData, ["next_stage", "nextStage"]) ||
      getStringField(artifacts, ["next_stage", "nextStage"]),
  ) || getNextStageFromOperation(nextOperationName);
  const command = checkpointKind === "manual_cutover"
    ? "确认切流"
    : checkpointKind === "intent_confirmation"
      ? "确认执行"
      : checkpointCommandText;
  const checkpointMessage = checkpointKind === "intent_confirmation"
    ? `已准备好执行${capabilityLabel ? `「${capabilityLabel}」` : "本次修改"}，确认后将应用这条干预。`
    : sanitizeCheckpointMessage(messageText, completedStageLabel, nextOperationLabel);

  return {
    kind: "checkpoint",
    checkpointKind,
    message: checkpointMessage,
    completedStage,
    completedStageLabel,
    nextOperationLabel,
    nextStage,
    command,
    taskId:
      getStringField(eventData, ["completed_task_id", "task_id"]) ||
      getStringField(artifacts, ["task_id"]),
  };
}

export function isTerminalAbtestCheckpoint(prompt: CheckpointWaitPrompt | undefined) {
  return prompt?.completedStage === "abtest" && !prompt.nextStage;
}

export function buildFailureRetryPrompt(
  stage: ThreadEventStage | undefined,
  payload: Record<string, unknown> | undefined,
): CheckpointWaitPrompt {
  const eventData = getEventPayloadData(payload);
  const stageLabel = getStageLabel(stage) || "当前步骤";
  const rawMessage =
    getStringField(eventData, ["message", "error_message", "error", "detail"]) ||
    getStringField(payload, ["message", "error_message", "error", "detail"]);
  const errorCode =
    getStringField(eventData, ["error_code", "code"]) ||
    getStringField(payload, ["error_code", "code"]);
  const reason = getFriendlyFailureReason(errorCode, rawMessage);
  const taskId =
    getStringField(eventData, ["task_id", "apply_id", "run_id", "eval_id", "dataset_id"]) ||
    getStringField(payload, ["task_id"]);

  return {
    kind: "failure",
    message: `${stageLabel}执行失败：${reason} 你可以重试，或补充说明后再继续。`,
    completedStageLabel: stageLabel,
    nextStage: stage,
    command: "重试",
    taskId,
  };
}

export function getFriendlyFailureReason(errorCode: string | undefined, rawMessage: string | undefined) {
  if (errorCode === "REPORT_ACTIONS_NOT_READY" || rawMessage?.includes("below apply confidence/validity thresholds")) {
    return "分析报告里的自动修改建议把握不够，系统为了避免改错代码，暂时没有继续执行代码修改。";
  }
  if (errorCode === "RAG_CALL_FAILED" || rawMessage?.includes("chat service failed")) {
    return "调用问答服务时超时或失败，可能是模型服务暂时不稳定。";
  }
  if (rawMessage) {
    return rawMessage;
  }
  if (errorCode) {
    return `错误码：${errorCode}。`;
  }
  return "执行过程中出现错误。";
}

export function compactPayloadForDisplay(payload: Record<string, unknown> | undefined) {
  if (!payload) {
    return "";
  }

  const eventData = getEventPayloadData(payload);
  const status = getStringField(eventData, ["status"]);
  const phase = getStringField(eventData, ["phase", "stage", "task", "task_type", "step", "name", "kind", "type", "event"]);
  const operationRunId = getOperationRunId(payload);
  const currentItem = getStringField(eventData, ["current_item", "item_ref", "case_id", "artifact_id"]);
  const detailRecord = getNestedRecordField(eventData, ["detail"]) || getStructuredRecordField(eventData, ["detail"]);
  const metrics = [
    getNumberField(eventData, ["current", "completed", "done", "processed"]) !== undefined &&
    getNumberField(eventData, ["total", "num_cases", "cases", "count"]) !== undefined
      ? `进度 ${getNumberField(eventData, ["current", "completed", "done", "processed"])}/${getNumberField(eventData, ["total", "num_cases", "cases", "count"])}`
      : "",
    getStringField(detailRecord, ["artifact_id"]) ? `产物 ${getStringField(detailRecord, ["artifact_id"])}` : "",
    currentItem ? `当前 ${currentItem}` : "",
  ].filter(Boolean);
  const structured = [
    operationRunId ? formatOperationRunId(operationRunId) : phase,
    status,
    ...metrics,
  ].filter(Boolean);
  if (structured.length > 0) {
    return structured.join(" · ");
  }

  const entries = Object.entries(payload).filter(
    ([key, value]) =>
      ![
        "type",
        "event",
        "event_name",
        "kind",
        "stage",
        "action",
        "message",
        "content",
        "text",
        "reply",
        "thought",
        "seq",
        "event_id",
        "created_at",
        "checkpoint_id",
        "payload",
      ].includes(key) &&
      value !== undefined &&
      value !== null &&
      value !== "",
  );
  if (entries.length === 0) {
    return "";
  }

  return entries.slice(0, 4).map(([key, value]) => {
    if (Array.isArray(value)) {
      return `${key} ${value.length} 项`;
    }
    if (isRecord(value)) {
      return `${key} 已更新`;
    }
    return `${key} ${String(value).slice(0, 80)}`;
  }).join(" · ");
}

export function getDiffLineType(line: string) {
  if (line.startsWith("+++ ") || line.startsWith("--- ") || line.startsWith("diff --git") || line.startsWith("index ")) {
    return "meta";
  }
  if (line.startsWith("@@")) {
    return "hunk";
  }
  if (line.startsWith("+")) {
    return "add";
  }
  if (line.startsWith("-")) {
    return "remove";
  }
  return "context";
}

export function getShortLabel(text: string, maxLength = 6) {
  if (text.length <= maxLength) {
    return text;
  }
  return `${text.slice(0, maxLength)}…`;
}

export function normalizeDiffPath(path: string) {
  const cleaned = path.replace(/^([ab])\//, "");
  const lazyMindIndex = cleaned.indexOf("LazyMind/");
  if (lazyMindIndex >= 0) {
    return cleaned.slice(lazyMindIndex + "LazyMind/".length);
  }
  return cleaned;
}

export function parseUnifiedDiff(diffText: string): ParsedDiffFile[] {
  const lines = diffText.split("\n");
  const files: ParsedDiffFile[] = [];
  let currentFile: ParsedDiffFile | null = null;
  let fileIndex = 0;

  const pushCurrent = () => {
    if (currentFile) {
      files.push(currentFile);
      currentFile = null;
    }
  };

  for (const line of lines) {
    if (line.startsWith("diff --git ")) {
      pushCurrent();
      fileIndex += 1;
      const match = line.match(/^diff --git a\/(.+?) b\/(.+)$/);
      const fromPath = match?.[1] || "";
      const toPath = match?.[2] || fromPath || "unknown-file";
      currentFile = {
        id: `diff-file-${fileIndex}`,
        fromPath,
        toPath,
        displayPath: normalizeDiffPath(toPath),
        lines: [line],
        additions: 0,
        deletions: 0,
      };
      continue;
    }

    if (!currentFile) {
      currentFile = {
        id: "diff-file-fallback",
        fromPath: "unknown-file",
        toPath: "unknown-file",
        displayPath: "unknown-file",
        lines: [],
        additions: 0,
        deletions: 0,
      };
    }

    currentFile.lines.push(line);
    if (line.startsWith("+") && !line.startsWith("+++")) {
      currentFile.additions += 1;
    }
    if (line.startsWith("-") && !line.startsWith("---")) {
      currentFile.deletions += 1;
    }
  }

  pushCurrent();
  return files;
}

export function buildDiffFileTree(files: ParsedDiffFile[]): DiffFileTreeNode[] {
  const tree: DiffFileTreeNode[] = [];

  const ensureDirNode = (nodes: DiffFileTreeNode[], name: string, path: string) => {
    let dirNode = nodes.find((node) => node.nodeType === "dir" && node.path === path);
    if (!dirNode) {
      dirNode = {
        name,
        path,
        nodeType: "dir",
        children: [],
      };
      nodes.push(dirNode);
    }
    return dirNode;
  };

  for (const file of files) {
    const segments = file.displayPath.split("/").filter(Boolean);
    let currentNodes = tree;
    let currentPath = "";

    segments.forEach((segment, index) => {
      currentPath = currentPath ? `${currentPath}/${segment}` : segment;
      const isLeafFile = index === segments.length - 1;

      if (isLeafFile) {
        const exists = currentNodes.some(
          (node) => node.nodeType === "file" && node.path === currentPath && node.fileId === file.id,
        );
        if (!exists) {
          currentNodes.push({
            name: segment,
            path: currentPath,
            nodeType: "file",
            fileId: file.id,
            children: [],
          });
        }
      } else {
        const dirNode = ensureDirNode(currentNodes, segment, currentPath);
        currentNodes = dirNode.children;
      }
    });
  }

  const sortNodes = (nodes: DiffFileTreeNode[]) => {
    nodes.sort((a, b) => {
      if (a.nodeType !== b.nodeType) {
        return a.nodeType === "dir" ? -1 : 1;
      }
      return a.name.localeCompare(b.name, "zh-CN", { numeric: true });
    });
    nodes.forEach((node) => sortNodes(node.children));
  };
  sortNodes(tree);
  return tree;
}

export function buildAbCategoryComparisons(reports: AbSummaryReport[]): AbCategoryComparison[] {
  return reports
    .filter((report) => report.metricRows.length > 0)
    .map((report, index) => {
      const metricMap = new Map(report.metricRows.map((row) => [row.metric, row]));
      const baseline = {} as Record<PxMetricKey, number>;
      const experiment = {} as Record<PxMetricKey, number>;
      const delta = {} as Record<PxMetricKey, number>;

      pxMetricMeta.forEach((metric) => {
        const row = metricMap.get(metric.key);
        baseline[metric.key] = clampScore(row?.meanA ?? 0);
        experiment[metric.key] = clampScore(row?.meanB ?? 0);
        delta[metric.key] = row?.deltaMean ?? experiment[metric.key] - baseline[metric.key];
      });

      return {
        category: reports.length === 1 ? "总体" : report.id || `报告 ${index + 1}`,
        baseline,
        experiment,
        delta,
      };
    });
}

export function formatMetricPercent(value: number) {
  return `${Math.round(value * 100)}%`;
}

export function formatMetricDelta(value: number) {
  const percent = Math.round(value * 100);
  return `${percent > 0 ? "+" : ""}${percent}%`;
}

export function formatMetricSummary(metrics: Record<PxMetricKey, number>) {
  return [
    `正确性 ${formatMetricPercent(metrics.answer_correctness)}`,
    `综合得分 ${formatMetricPercent(metrics.answer_score)}`,
    `Chunk 召回 ${formatMetricPercent(metrics.chunk_recall)}`,
    `文档召回 ${formatMetricPercent(metrics.doc_recall)}`,
  ].join(" / ");
}

export function toFiniteNumber(value: unknown, fallback = 0) {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string" && value.trim() && Number.isFinite(Number(value))) {
    return Number(value);
  }
  return fallback;
}

export function formatAbMetricLabel(metric: string) {
  return pxMetricMeta.find((item) => item.key === metric)?.label || metric;
}

export function getAbtestResultRecords(value: unknown): Record<string, unknown>[] {
  if (Array.isArray(value)) {
    return value.filter((item): item is Record<string, unknown> => isRecord(item) && Object.keys(item).length > 0);
  }
  if (!isRecord(value)) {
    return [];
  }

  const nestedItems = getResultItems(value).filter((item): item is Record<string, unknown> => isRecord(item));
  return nestedItems.length > 0 ? nestedItems : [value];
}

export function buildAbSummaryReports(payload: unknown): AbSummaryReport[] {
  return getAbtestResultRecords(payload)
    .reduce<AbSummaryReport[]>((reports, record, index) => {
      const dataRecord = getNestedRecordField(record, ["data"]) || record;
      const summary =
        getStructuredRecordField(dataRecord, ["summary"]) ||
        getNestedRecordField(dataRecord, ["summary"]) ||
        (getNestedRecordField(dataRecord, ["metrics"]) ? dataRecord : undefined);
      if (!summary) {
        return reports;
      }

      const metricsRecord =
        getStructuredRecordField(summary, ["metrics"]) || getNestedRecordField(summary, ["metrics"]);
      const baselineMetrics = getNestedRecordField(metricsRecord, ["baseline"]);
      const candidateMetrics = getNestedRecordField(metricsRecord, ["candidate"]);
      const deltaMetrics = getNestedRecordField(metricsRecord, ["delta"]);
      const caseDeltas = (getStructuredArrayField(summary, ["case_deltas"]) || []).filter(
        (item): item is Record<string, unknown> => isRecord(item),
      );
      const improvedCount = caseDeltas.filter((item) => getStringField(item, ["outcome"]) === "improved").length;
      const metricRows = baselineMetrics && candidateMetrics
        ? pxMetricMeta.map((metric) => ({
          key: metric.key,
          metric: metric.key,
          metricLabel: metric.label,
          meanA: getMetricFieldNumber(baselineMetrics, metric.key),
          meanB: getMetricFieldNumber(candidateMetrics, metric.key),
          deltaMean: getNumberField(deltaMetrics, pxMetricFieldAliases[metric.key]) ?? getMetricFieldNumber(candidateMetrics, metric.key) - getMetricFieldNumber(baselineMetrics, metric.key),
          winRateB: caseDeltas.length ? improvedCount / caseDeltas.length : 0,
          signP: null,
          n: caseDeltas.length || getNumberField(summary, ["case_count"]),
        }))
        : metricsRecord
          ? Object.entries(metricsRecord)
            .filter((entry): entry is [string, Record<string, unknown>] => isRecord(entry[1]))
            .map(([metric, item]) => ({
              key: metric,
              metric,
              metricLabel: formatAbMetricLabel(metric),
              meanA: clampScore(toFiniteNumber(item.mean_a)),
              meanB: clampScore(toFiniteNumber(item.mean_b)),
              deltaMean: toFiniteNumber(item.delta_mean),
              winRateB: clampScore(toFiniteNumber(item.win_rate_b)),
              signP: item.sign_p === null || item.sign_p === undefined ? null : toFiniteNumber(item.sign_p),
              n: getNumberField(item, ["n"]),
            }))
          : [];

      const topDiffRows = (getStructuredArrayField(summary, ["top_diff_cases"]) || caseDeltas)
        .filter((item): item is Record<string, unknown> => isRecord(item))
        .map((item, rowIndex) => ({
          key: getStringField(item, ["case_key", "case_id", "id"]) || `case-${rowIndex + 1}`,
          caseKey: getStringField(item, ["case_key", "case_id", "id"]) || `case-${rowIndex + 1}`,
          a: getMetricFieldNumber(getNestedRecordField(item, ["before"]) || item, "answer_correctness"),
          b: getMetricFieldNumber(getNestedRecordField(item, ["after"]) || item, "answer_correctness"),
          delta: getNumberField(getNestedRecordField(item, ["delta"]) || item, pxMetricFieldAliases.answer_correctness) ?? 0,
        }));

      const policy = getStructuredRecordField(summary, ["policy"]) || getNestedRecordField(summary, ["policy"]);
      const decision = getNestedRecordField(summary, ["decision"]);
      const reasons = (getStructuredArrayField(summary, ["reasons"]) || getStructuredArrayField(decision, ["reasons"]) || []).filter(
        (item): item is string => typeof item === "string" && item.trim().length > 0,
      );
      const missingMetrics = (getStructuredArrayField(summary, ["missing_metrics"]) || []).filter(
        (item): item is string => typeof item === "string" && item.trim().length > 0,
      );
      const guardMetrics = (getStructuredArrayField(policy, ["guard_metrics"]) || []).filter(
        (item): item is string => typeof item === "string" && item.trim().length > 0,
      );
      const reportId =
        getStringField(dataRecord, ["abtest_id", "id", "task_id"]) ||
        getStringField(record, ["abtest_id", "id", "task_id"]) ||
        `abtest-${index + 1}`;
      const markdown =
        getResultStringField(dataRecord, ["markdown", "report", "content", "text"]) ||
        getResultStringField(record, ["markdown", "report", "content", "text"]);
      const verdict =
        getStringField(summary, ["verdict"]) ||
        getStringField(decision, ["status"]) ||
        getResultStringField(dataRecord, ["verdict"]) ||
        getResultStringField(record, ["verdict"]);

      reports.push({
        id: reportId,
        markdown,
        verdict,
        alignedCases: getNumberField(summary, ["aligned_cases", "case_count"]) || caseDeltas.length || undefined,
        reasons,
        metricRows,
        topDiffRows,
        missingMetrics,
        primaryMetric: getStringField(policy, ["primary_metric"]) || getStringField(decision, ["primary_metric"]),
        guardMetrics,
      });
      return reports;
    }, []);
}

export function formatMaybePValue(value: number | null | undefined) {
  if (value === null || value === undefined || !Number.isFinite(value)) {
    return "-";
  }
  return value < 0.001 ? "<0.001" : value.toFixed(3);
}

export function parseSSEFrame(rawFrame: string): ThreadEventFrame | undefined {
  const lines = rawFrame.split(/\r?\n/);
  const dataLines: string[] = [];
  let eventName = "message";
  let id: string | undefined;

  lines.forEach((line) => {
    if (line.startsWith("id:")) {
      id = line.slice("id:".length).trim() || undefined;
    }
    if (line.startsWith("event:")) {
      eventName = line.slice("event:".length).trim() || "message";
    }
    if (line.startsWith("data:")) {
      dataLines.push(line.slice("data:".length).trimStart());
    }
  });

  if (dataLines.length === 0) {
    return undefined;
  }

  return {
    id,
    eventName,
    data: dataLines.join("\n"),
  };
}

export function parseThreadEventPayload(data: string): Record<string, unknown> | undefined {
  try {
    const value = JSON.parse(data);
    return isRecord(value) ? value : { value };
  } catch {
    return undefined;
  }
}

export function getChatStreamDeltaKind(type: string): ChatStreamDeltaKind | undefined {
  if (type === "thinking_delta" || type === "intent.thinking_delta") {
    return "thinking";
  }
  if (type === "answer_delta" || type === "intent.answer_delta") {
    return "answer";
  }
  return undefined;
}

export function isTerminalThreadEvent(type: string) {
  return terminalThreadEventTypes.has(type);
}

export function isFailedThreadEvent(type: string) {
  return failedThreadEventTypes.has(type);
}

export function isInactiveTerminalThreadEvent(event: NormalizedThreadEvent) {
  if (!isTerminalThreadEvent(event.type)) {
    return false;
  }
  const status = getStringField(event.payload, ["status"]);
  return Boolean(status && inactiveTerminalThreadStatuses.has(status.toLowerCase()));
}

export function normalizeThreadEvent(frame: ThreadEventFrame): NormalizedThreadEvent {
  const payload = parseThreadEventPayload(frame.data);
  const eventEnvelope = getThreadEventPayloadEnvelope(payload);
  const payloadType = getThreadEventTypeFromPayload(payload);
  const eventType = payloadType || (frame.eventName !== "message" ? frame.eventName : "");
  const [typeStage, ...actionParts] = eventType.split(".");
  const isCheckpointEvent = eventType.startsWith("checkpoint.");
  const isAutoOperatorEvent = eventType.startsWith("autooperator.");
  const operationRunId = getOperationRunId(payload);
  const stageFromPayload =
    (operationRunId?.startsWith("candidate_eval.") ? "abtest" : undefined) ||
    toThreadEventStage(payload?.stage) ||
    toThreadEventStage(eventEnvelope?.stage);
  const stage = isCheckpointEvent ? undefined : stageFromPayload || (isAutoOperatorEvent ? undefined : toThreadEventStage(typeStage));
  const action = isCheckpointEvent
    ? actionParts.join(".") || undefined
    : isAutoOperatorEvent
      ? actionParts.join(".") || undefined
    : getStringField(payload, ["action"]) ||
      getStringField(eventEnvelope, ["action"]) ||
      (stage && actionParts.length > 0
        ? actionParts.join(".")
        : stage && eventType && !toThreadEventStage(eventType) && eventType !== "message"
          ? eventType
          : undefined);
  const type = isCheckpointEvent || isAutoOperatorEvent ? eventType : stage && action ? `${stage}.${action}` : eventType || "message";
  const role = type === "message.user" ? "user" : type === "message.assistant" ? "assistant" : undefined;
  const content = getThreadEventContentFromPayload(payload) || (!payload ? frame.data.trim() : undefined);
  const timestamp =
    getStringField(payload, ["ts", "timestamp", "created_at", "create_time", "updated_at", "update_time"]) ||
    getStringField(eventEnvelope, ["ts", "timestamp", "created_at", "create_time", "updated_at", "update_time"]) ||
    undefined;
  const sequence = getNumberField(payload, ["seq"]) ?? getNumberField(eventEnvelope, ["seq"]);
  const taskId =
    getStringField(payload, ["task_id"]) ||
    getStringField(eventEnvelope, ["task_id"]) ||
    getStringField(getEventPayloadData(payload), ["task_id", "run_id"]) ||
    undefined;
  const messageEventId =
    getStringField(payload, ["message_id", "messageId", "intent_id", "intentId"]) ||
    getStringField(eventEnvelope, ["message_id", "messageId", "intent_id", "intentId"]) ||
    undefined;
  const key =
    frame.id ||
    [
      getStringField(payload, ["thread_id"]) || getStringField(eventEnvelope, ["thread_id"]),
      typeof sequence === "number" ? String(sequence) : "",
      taskId || messageEventId,
      type,
      timestamp,
    ]
      .filter(Boolean)
      .join(":") ||
    `${type}:${frame.data}`;

  if (isTerminalThreadEvent(frame.eventName) || isTerminalThreadEvent(type)) {
    return {
      key,
      timestamp,
      sequence,
      taskId,
      type,
      payload,
      displayText: "事件流已结束，线程停止信号已收到。",
    };
  }

  if (isFailedThreadEvent(frame.eventName) || isFailedThreadEvent(type)) {
    const errorText = content || "消息处理失败，请稍后重试。";
    return {
      key,
      timestamp,
      sequence,
      taskId,
      type,
      role: "assistant",
      content: errorText,
      payload,
      displayText: errorText,
    };
  }

  const chatStreamDeltaKind = getChatStreamDeltaKind(type);
  if (chatStreamDeltaKind) {
    return {
      key,
      timestamp,
      sequence,
      taskId,
      type,
      role: "assistant",
      content,
      payload,
      displayText: content,
    };
  }

  if (role) {
    return {
      key,
      timestamp,
      sequence,
      taskId,
      type,
      role,
      content,
      payload,
      displayText: content,
    };
  }

  if (type === "intent.thought" || type === "intent.reply") {
    return {
      key,
      timestamp,
      sequence,
      taskId,
      type,
      role: "assistant",
      content: type === "intent.thought" && content ? `意图分析：${content}` : content,
      payload,
      displayText: content,
    };
  }

  if (type === "checkpoint.wait") {
    const checkpointWait = buildCheckpointWaitPrompt(payload);
    return {
      key,
      timestamp,
      sequence,
      taskId: checkpointWait.taskId || taskId,
      type,
      payload,
      content: checkpointWait.message,
      displayText: checkpointWait.message,
      checkpointWait,
    };
  }

  if (type === "checkpoint.created" || type === "checkpoint.continue" || type === "checkpoint.cancel") {
    const checkpointId = getStringField(payload, ["checkpoint_id"]);
    const displayText =
      type === "checkpoint.created"
        ? checkpointId
          ? `已保存流程 checkpoint：${checkpointId}。`
          : "已保存流程 checkpoint。"
        : type === "checkpoint.cancel"
          ? "用户已终止当前 checkpoint。"
          : "用户已确认继续执行。";
    return {
      key,
      timestamp,
      sequence,
      taskId,
      type,
      payload,
      content: displayText,
      displayText,
    };
  }

  if (action === "failed") {
    const checkpointWait = buildFailureRetryPrompt(stage, payload);
    return {
      key,
      timestamp,
      sequence,
      taskId: checkpointWait.taskId || taskId,
      type,
      stage,
      action,
      payload,
      content: checkpointWait.message,
      displayText: checkpointWait.message,
      checkpointWait,
    };
  }

  if (!stage) {
    const fallbackText = content || compactPayloadForDisplay(payload);
    return {
      key,
      timestamp,
      sequence,
      taskId,
      type,
      payload,
      content: fallbackText,
      displayText: fallbackText || (type === "message" ? "" : type),
    };
  }

  const actionLabel = action ? eventActionLabels[action] || action : "事件更新";
  const detail = content || compactPayloadForDisplay(payload);
  const displayText =
    (stage === "dataset" && buildDatasetEventDisplayText(action, payload)) ||
    (stage === "analysis" && buildAnalysisEventDisplayText(action, type, payload)) ||
    (stage === "repair" && buildApplyEventDisplayText(action, type, payload)) ||
    (stage === "eval" && buildEvalEventDisplayText(action, type, payload)) ||
    (stage === "abtest" && buildAbtestEventDisplayText(action, payload)) ||
    (stage === "dataset" && "数据集生成正在执行。") ||
    (detail ? `${stageLabels[stage]}：${actionLabel}，${detail}` : `${stageLabels[stage]}：${actionLabel}`);
  const progress = getWorkflowProgressSnapshot(stage, action, payload, type);
  const progressPhase = stage === "eval" ? getEvalPayloadPhase(action, type, payload) : undefined;

  return {
    key,
    timestamp,
    sequence,
    taskId,
    type,
    stage,
    action,
    payload,
    content: detail,
    displayText: progress ? undefined : displayText,
    progress,
    progressPhase,
  };
}

export function compareNormalizedThreadEvents(a: NormalizedThreadEvent, b: NormalizedThreadEvent) {
  if (typeof a.sequence === "number" && typeof b.sequence === "number" && a.sequence !== b.sequence) {
    return a.sequence - b.sequence;
  }

  if (a.timestamp && b.timestamp) {
    const aTime = new Date(a.timestamp).getTime();
    const bTime = new Date(b.timestamp).getTime();
    if (!Number.isNaN(aTime) && !Number.isNaN(bTime) && aTime !== bTime) {
      return aTime - bTime;
    }
  }

  return a.key.localeCompare(b.key, "zh-CN", { numeric: true });
}

export function getNormalizedEventDedupeKey(event: NormalizedThreadEvent) {
  return [
    getStringField(event.payload, ["thread_id"]) || "",
    typeof event.sequence === "number" ? String(event.sequence) : "",
    event.taskId || "",
    event.type,
    event.timestamp || "",
  ]
    .filter(Boolean)
    .join(":") || event.key;
}

export function dedupeNormalizedEvents(events: NormalizedThreadEvent[]) {
  return Array.from(new Map(events.map((item) => [getNormalizedEventDedupeKey(item), item])).values()).sort(compareNormalizedThreadEvents);
}

function getLastItem<T>(items: T[]): T | undefined {
  return items.length ? items[items.length - 1] : undefined;
}

export function getWorkflowStepIndex(stepId: WorkflowStepId | undefined) {
  if (!stepId) {
    return -1;
  }
  return workflowStepOrder.indexOf(stepId);
}

export function createWorkflowStepFromRuntime(
  stepId: WorkflowStepId,
  runtimeState: WorkflowRuntimeState,
  renderKey = stepId,
): WorkflowStep {
  const definition = workflowStepDefinitions.find((step) => step.id === stepId) || workflowStepDefinitions[0];
  const runtime = runtimeState[stepId];
  return {
    ...definition,
    renderKey,
    status: runtime.status,
    runtimeText: runtime.runtimeText,
    progress: runtime.progress || (stepId === "px-report" ? getEvalOverallProgressSnapshot(runtime.progressPhases) : undefined),
    progressPhases: runtime.progressPhases,
  };
}

const terminalFlowRuntimeText: Partial<Record<StepStatus, string>> = {
  canceled: "流程已取消。",
  done: "流程已结束。",
  failed: "流程已失败。",
};

function getTerminalOverrideStepIndex(steps: WorkflowStep[]) {
  for (let index = steps.length - 1; index >= 0; index -= 1) {
    if (["running", "paused", "failed", "canceled"].includes(steps[index].status)) {
      return index;
    }
  }
  for (let index = 0; index < steps.length; index += 1) {
    if (steps[index].status === "pending") {
      return index;
    }
  }
  return steps.length > 0 ? steps.length - 1 : -1;
}

function applyTerminalFlowStepStatus(
  steps: WorkflowStep[],
  terminalStepStatus?: StepStatus,
) {
  if (!terminalStepStatus || steps.length === 0) {
    return steps;
  }
  const terminalStepIndex = getTerminalOverrideStepIndex(steps);
  if (terminalStepIndex < 0) {
    return steps;
  }
  return steps.map((step, index) =>
    index === terminalStepIndex
      ? {
          ...step,
          status: terminalStepStatus,
          runtimeText: terminalFlowRuntimeText[terminalStepStatus] || step.runtimeText,
          progress: terminalStepStatus === "done"
            ? step.progress || getCompletedProgressSnapshot()
            : step.progress,
        }
      : step,
  );
}

export function buildWorkflowStepRuntimeFromEvents(events: NormalizedThreadEvent[], isSuperseded: boolean) {
  const snapshot: {
    status: StepStatus;
    runtimeText?: string;
    progress?: WorkflowProgressSnapshot;
    progressPhases?: WorkflowProgressPhaseSnapshot[];
  } = {
    status: "running",
  };

  events.forEach((event) => {
    if (snapshot.status === "done" && isIntentSidecarOperation(event)) {
      return;
    }

    if (event.stage === "eval") {
      snapshot.progressPhases = updateEvalProgressPhases(
        snapshot.progressPhases,
        event.progressPhase,
        event.progress,
        event.action,
        Boolean(getOperationRunId(event.payload)),
      );
      snapshot.progress = getEvalOverallProgressSnapshot(snapshot.progressPhases);
    }

    const isFinished = isStepFinishEvent(event);

    if (isFinished) {
      snapshot.status = "done";
      if (event.stage === "eval") {
        snapshot.progressPhases = getCompletedEvalProgressPhases();
        snapshot.progress = getEvalOverallProgressSnapshot(snapshot.progressPhases);
      } else {
        snapshot.progress = event.progress || getCompletedProgressSnapshot();
      }
    } else if (event.action === "cancel") {
      snapshot.status = "canceled";
    } else if (event.action === "failed") {
      snapshot.status = "failed";
    } else if (event.action === "pause") {
      snapshot.status = "paused";
      if (event.stage !== "eval") {
        snapshot.progress = mergeProgressSnapshot(
          snapshot.progress,
          event.progress || updateProgressStatusText(snapshot.progress, getRuntimeProgressStatusLabel(event.action)),
        );
      }
    } else {
      snapshot.status = "running";
      if (event.stage !== "eval") {
        snapshot.progress = mergeProgressSnapshot(
          snapshot.progress,
          event.progress || updateProgressStatusText(snapshot.progress, getRuntimeProgressStatusLabel(event.action)),
        );
      }
    }
    snapshot.runtimeText = event.progress ? undefined : event.displayText;
  });

  if (isSuperseded && snapshot.status === "running") {
    snapshot.status = "done";
    if (snapshot.progressPhases) {
      snapshot.progressPhases = getCompletedEvalProgressPhases();
      snapshot.progress = getEvalOverallProgressSnapshot(snapshot.progressPhases);
    } else {
      snapshot.progress = getCompletedProgressSnapshot();
    }
  }

  if (snapshot.status === "done") {
    if (snapshot.progressPhases) {
      snapshot.progressPhases = getCompletedEvalProgressPhases();
      snapshot.progress = getEvalOverallProgressSnapshot(snapshot.progressPhases);
    } else {
      snapshot.progress = getCompletedProgressSnapshot();
    }
  }

  return snapshot;
}

export function buildVisibleWorkflowSteps(
  events: NormalizedThreadEvent[],
  runtimeState: WorkflowRuntimeState,
  includeFirstStep: boolean,
  terminalStepStatus?: StepStatus,
): WorkflowStep[] {
  const stageEvents = dedupeNormalizedEvents(events).filter((event) => event.stage);
  if (stageEvents.length === 0) {
    return applyTerminalFlowStepStatus(
      includeFirstStep ? [createWorkflowStepFromRuntime("dataset", runtimeState)] : [],
      terminalStepStatus,
    );
  }

  const groups: Array<{ stepId: WorkflowStepId; events: NormalizedThreadEvent[] }> = [];
  stageEvents.forEach((event) => {
    if (!event.stage) {
      return;
    }
    const stepId = stageStepMap[event.stage];
    const latestGroup = getLastItem(groups);
    if (latestGroup?.stepId === stepId) {
      latestGroup.events.push(event);
      return;
    }
    groups.push({ stepId, events: [event] });
  });

  const steps = groups.map((group, index) => {
    const definition = workflowStepDefinitions.find((step) => step.id === group.stepId) || workflowStepDefinitions[0];
    return {
      ...definition,
      renderKey: `${group.stepId}-${index}`,
      ...buildWorkflowStepRuntimeFromEvents(group.events, index < groups.length - 1),
    };
  });
  return applyTerminalFlowStepStatus(steps, terminalStepStatus);
}

function eventActivityTone(event: NormalizedThreadEvent): EvoStageActivity["tone"] {
  if (event.type.startsWith("autooperator.")) {
    return "auto";
  }
  if (event.type.startsWith("checkpoint.")) {
    return "checkpoint";
  }
  if (event.type.startsWith("message.") || event.type.startsWith("intent.")) {
    return "message";
  }
  if (event.action === "failed") {
    return "error";
  }
  return event.progress ? "progress" : "normal";
}

function eventActivityTitle(event: NormalizedThreadEvent) {
  if (event.type.startsWith("autooperator.")) {
    return "自动处理记录";
  }
  if (event.type === "checkpoint.wait") {
    return "等待确认";
  }
  if (event.type === "checkpoint.continue") {
    return "继续执行";
  }
  if (event.type === "checkpoint.cancel") {
    return "终止流程";
  }
  if (event.type === "message.user") {
    return "前端干预";
  }
  if (event.type === "message.assistant" || event.type.startsWith("intent.")) {
    return "意图处理";
  }
  const operationRunId = getOperationRunId(event.payload);
  if (operationRunId) {
    return formatOperationRunId(operationRunId);
  }
  return event.stage ? stageLabels[event.stage] : event.type;
}

function formatOperationRunId(operationRunId: string) {
  const name = operationRunId
    .replace(/^dataset\./, "dataset · ")
    .replace(/^eval\./, "eval · ")
    .replace(/^analysis\./, "analysis · ")
    .replace(/^repair\./, "repair · ")
    .replace(/^abtest\./, "abtest · ")
    .replace(/_/g, " ");
  return name.replace(/\bcase\.(\d+)/, "case $1");
}

const repairAnalysisArtifactPrefixes = [
  "repair_loop_plan",
  "repair_evidence_packet",
  "fault_localization",
  "diagnostic_probe_plan",
  "diagnostic_probe_result",
  "repair_diagnosis",
  "opencode_instruction",
  "opencode_explore_instruction",
  "opencode_patch_instruction",
  "opencode_no_patch_instruction",
];

const repairExecutionArtifactPrefixes = [
  "opencode_probe_trace",
  "opencode_patch_trace",
  "opencode_worker_report",
  "opencode_patch_worker_report",
  "opencode_probe_worker_report",
  "opencode_no_patch_worker_report",
  "repair_hypothesis",
  "repair_plan",
  "opencode_run_trace",
  "code_patch_candidate",
  "candidate_service",
  "candidate_service_run",
  "repair_evaluation",
  "patch_correctness_assessment",
  "patch_critique",
  "branch_decision",
  "repair_branch_state_before",
  "repair_branch_state_after",
  "repair_state_transition",
  "candidate_classification_report",
  "repair_loop_decision",
  "repair_loop_memory",
  "repair_loop_state",
  "verified_repair",
];

function getActivityArtifactKind(event: NormalizedThreadEvent): WorkflowResultKind | undefined {
  if (!event.stage || event.type === "checkpoint.created") {
    return undefined;
  }
  if (event.checkpointWait) {
    return stageResultKindMap[event.stage];
  }
  const eventData = getEventPayloadData(event.payload);
  const detail = getNestedRecordField(eventData, ["detail"]) || getStructuredRecordField(eventData, ["detail"]);
  const artifactId =
    getStringField(detail, ["artifact_id", "writes_artifact_id"]) ||
    getStringField(eventData, ["artifact_id", "writes_artifact_id", "current_item"]) ||
    getOperationRunId(event.payload);
  const finalArtifactIds: Record<ThreadEventStage, string[]> = {
    dataset: ["eval_dataset"],
    eval: ["eval_report", "candidate_eval_report"],
    analysis: ["classification_report", "repair_loop_plan"],
    repair: ["verified_repair", "repair_loop_agent", "candidate_workspace"],
    abtest: ["abtest_comparison", "candidate_algorithm_cutover"],
  };
  const repairArtifactId = artifactId || "";
  const isRepairAnalysisArtifact = event.stage === "repair" && repairArtifactId.length > 0 &&
    repairAnalysisArtifactPrefixes.some((prefix) => repairArtifactId === prefix || repairArtifactId.startsWith(`${prefix}_`));
  if (isRepairAnalysisArtifact) {
    return "analysis-reports";
  }
  const isRepairExecutionArtifact = event.stage === "repair" && repairArtifactId.length > 0 &&
    repairExecutionArtifactPrefixes.some((prefix) => repairArtifactId === prefix || repairArtifactId.startsWith(`${prefix}_`));
  return artifactId && (finalArtifactIds[event.stage].includes(artifactId) || isRepairExecutionArtifact)
    ? stageResultKindMap[event.stage]
    : undefined;
}

function getActivityArtifactLabel(artifactKind: WorkflowResultKind | undefined) {
  if (!artifactKind) {
    return undefined;
  }
  return `查看${workflowResultLabels[artifactKind]}`;
}

function buildEventActivity(event: NormalizedThreadEvent): EvoStageActivity {
  const progressText = event.progress ? `${event.progress.statusText} ${event.progress.percent}%` : "";
  const stageProgressText = event.stage === "abtest" ? progressText : "";
  const detail = event.displayText || stageProgressText || event.content || progressText || compactPayloadForDisplay(event.payload) || event.type;
  const artifactKind = getActivityArtifactKind(event);
  const artifactId = getEventArtifactId(event.payload);
  const flowKind = getEventFlowKind(event.payload);
  return {
    key: event.key,
    stage: event.stage,
    title: eventActivityTitle(event),
    detail,
    time: formatThreadTime(event.timestamp),
    tone: eventActivityTone(event),
    flowKind,
    artifactKind,
    artifactId,
    artifactLabel: getActivityArtifactLabel(artifactKind),
  };
}

function stageProgressFromEvents(events: NormalizedThreadEvent[], stage: ThreadEventStage) {
  return getLastItem(
    events.filter((event) => event.stage === stage && event.progress &&
      !(stage === "eval" && getEventFlowKind(event.payload) === "eval.answer_and_judge")),
  )?.progress;
}

type CaseProgressState = {
  caseId: string;
  steps: Record<string, StepStatus>;
  artifactId?: string;
  updatedAt?: string;
};

const datasetCaseSteps = ["load_corpus", "build_snapshot", "generate", "assemble"] as const;
const evalCaseSteps = ["rag", "judge"] as const;
const analysisCaseSteps = ["coarse", "fine"] as const;
const caseStepLabels: Record<string, string> = {
  load_corpus: "load_corpus",
  build_snapshot: "build_snapshot",
  generate: "generate",
  assemble: "assemble",
  rag: "RAG",
  judge: "judge",
  coarse: "coarse",
  fine: "fine",
};

function getCaseProgressActionStatus(event: NormalizedThreadEvent): StepStatus | undefined {
  const eventData = getEventPayloadData(event.payload);
  const after = getNestedRecordField(eventData, ["after"]);
  const status = getStringField(eventData, ["status"]) || getStringField(after, ["status"]);
  if (event.action === "finish" || status === "success" || status === "ended" || status === "skipped") {
    return "done";
  }
  if (event.action === "failed" || status === "failed") {
    return "failed";
  }
  if (event.action === "pause" || status === "checkpointed") {
    return "paused";
  }
  if (event.action === "cancel" || status === "cancelled") {
    return "canceled";
  }
  if (event.action === "progress" || status === "running") {
    return "running";
  }
  return undefined;
}

function updateCaseStep(
  cases: Map<string, CaseProgressState>, caseId: string, step: string,
  status: StepStatus | undefined, updatedAt?: string, artifactId?: string,
) {
  if (!status) {
    return;
  }
  const item = cases.get(caseId) || { caseId, steps: {} };
  const previous = item.steps[step];
  if (previous !== "done" || status === "done") {
    item.steps[step] = status;
  }
  item.artifactId = artifactId || item.artifactId;
  item.updatedAt = updatedAt || item.updatedAt;
  cases.set(caseId, item);
}

function getOperationCaseId(payload: Record<string, unknown> | undefined) {
  return getEventCaseId(payload) || getStringField(getEventPayloadData(payload), ["current_item"]);
}

function applyGlobalDatasetStep(cases: Map<string, CaseProgressState>, step: string, status: StepStatus | undefined, updatedAt?: string, artifactId?: string) {
  cases.forEach((item) => updateCaseStep(cases, item.caseId, step, status, updatedAt, artifactId));
}

function buildCaseItem(item: CaseProgressState, steps: readonly string[], artifactKind: WorkflowResultKind, artifactId: string | undefined, artifactLabel: string): EvoCaseProgressItem {
  const builtSteps = steps.map((key) => ({ key, label: caseStepLabels[key] || key, status: item.steps[key] || "pending" }));
  const completed = builtSteps.filter((step) => step.status === "done").length;
  const status: StepStatus = completed === builtSteps.length ? "done" :
    builtSteps.some((step) => step.status === "failed") ? "failed" :
      builtSteps.some((step) => step.status === "canceled") ? "canceled" :
        builtSteps.some((step) => step.status === "paused") ? "paused" :
          builtSteps.some((step) => step.status === "running" || step.status === "done") ? "running" : "pending";
  return { caseId: item.caseId, title: item.caseId.replace(/^case_0*/, "Case "), completed, total: builtSteps.length, status, steps: builtSteps, artifactKind, artifactId, artifactLabel, updatedAt: item.updatedAt };
}

const areCaseStepsDone = (item: CaseProgressState, steps: readonly string[]) => steps.every((step) => item.steps[step] === "done");

function sortCaseItems(a: EvoCaseProgressItem, b: EvoCaseProgressItem) {
  const left = Number(a.caseId.match(/\d+/)?.[0] || 0);
  const right = Number(b.caseId.match(/\d+/)?.[0] || 0);
  return left - right || a.caseId.localeCompare(b.caseId);
}

function buildCaseProgressGroups(events: NormalizedThreadEvent[]): EvoCaseProgressGroup[] {
  const datasetCases = new Map<string, CaseProgressState>();
  const evalCases = new Map<string, CaseProgressState>();
  const analysisCases = new Map<string, CaseProgressState>();
  const abtestCases = new Map<string, CaseProgressState>();
  const datasetGlobal: Record<string, StepStatus | undefined> = {};
  events.forEach((event) => {
    const operationRunId = getOperationRunId(event.payload);
    const flowKind = getEventFlowKind(event.payload);
    const artifactId = getEventArtifactId(event.payload);
    const status = getCaseProgressActionStatus(event);
    if (!operationRunId || !status) {
      return;
    }
    const caseId = getOperationCaseId(event.payload);
    if (flowKind === "dataset.load_corpus") {
      datasetGlobal.load_corpus = status;
      applyGlobalDatasetStep(datasetCases, "load_corpus", status, event.timestamp);
    } else if (flowKind === "dataset.build_corpus_snapshot") {
      datasetGlobal.build_snapshot = status;
      applyGlobalDatasetStep(datasetCases, "build_snapshot", status, event.timestamp);
    } else if (caseId && flowKind === "dataset.assemble" && status === "running") {
      updateCaseStep(datasetCases, caseId, "assemble", "done", event.timestamp);
    } else if (flowKind === "dataset.assemble") {
      datasetGlobal.assemble = status;
      applyGlobalDatasetStep(datasetCases, "assemble", status, event.timestamp);
    } else if (caseId && flowKind === "dataset.generate_case") {
      Object.entries(datasetGlobal).forEach(([step, value]) => updateCaseStep(datasetCases, caseId, step, value, event.timestamp));
      updateCaseStep(datasetCases, caseId, "generate", status, event.timestamp, artifactId);
    } else if (caseId && event.stage === "eval" && flowKind === "eval.answer_and_judge") {
      updateCaseStep(evalCases, caseId, "rag", status, event.timestamp, artifactId);
      updateCaseStep(evalCases, caseId, "judge", status, event.timestamp, artifactId);
    } else if (caseId && flowKind === "abtest.candidate_rag_answer") {
      updateCaseStep(abtestCases, caseId, "rag", status, event.timestamp, artifactId);
    } else if (caseId && flowKind === "abtest.candidate_judge") {
      updateCaseStep(abtestCases, caseId, "judge", status, event.timestamp, artifactId);
    } else if (caseId && flowKind === "analysis.coarse_classify") {
      updateCaseStep(analysisCases, caseId, "coarse", status, event.timestamp, artifactId);
    } else if (caseId && flowKind === "analysis.fine_classify") {
      updateCaseStep(analysisCases, caseId, "fine", status, event.timestamp, artifactId);
    }
  });
  const groups: EvoCaseProgressGroup[] = [
    { stage: "dataset", title: "Step 1 · 数据集 case", pageSize: 10, cases: Array.from(datasetCases.values()).map((item) => buildCaseItem(item, datasetCaseSteps, "datasets", areCaseStepsDone(item, datasetCaseSteps) ? item.artifactId : undefined, "查看该 case 详情")).sort(sortCaseItems) },
    { stage: "eval", title: "Step 2 · 评测 case", pageSize: 10, cases: Array.from(evalCases.values()).map((item) => buildCaseItem(item, evalCaseSteps, "eval-reports", areCaseStepsDone(item, evalCaseSteps) ? item.artifactId : undefined, "查看该 case 结果")).sort(sortCaseItems) },
    { stage: "analysis", title: "Step 3 · 分析 case", pageSize: 10, cases: Array.from(analysisCases.values()).map((item) => buildCaseItem(item, analysisCaseSteps, "analysis-reports", areCaseStepsDone(item, analysisCaseSteps) ? item.artifactId : undefined, "查看该 case 分类")).sort(sortCaseItems) },
    { stage: "abtest", title: "Step 5 · ABTest case", pageSize: 10, cases: Array.from(abtestCases.values()).map((item) => buildCaseItem(item, evalCaseSteps, "abtests", areCaseStepsDone(item, evalCaseSteps) ? item.artifactId : undefined, "查看该 case 对照")).sort(sortCaseItems) },
  ];
  return groups.filter((group) => group.cases.length > 0);
}

function shouldShowProcessActivity(event: NormalizedThreadEvent) {
  if (event.type === "checkpoint.created" || isTerminalThreadEvent(event.type)) {
    return false;
  }
  return Boolean(event.displayText || event.content || event.progress || event.checkpointWait || event.type.startsWith("autooperator."));
}

function isCutoverActivity(item: EvoStageActivity) {
  return item.stage === "abtest" && item.artifactId === "candidate_algorithm_cutover";
}

function isCutoverCompletedEvent(event: NormalizedThreadEvent) {
  return event.stage === "abtest" &&
    (getEventFlowKind(event.payload) === "abtest.candidate_cutover" ||
      getEventArtifactId(event.payload) === "candidate_algorithm_cutover") &&
    (isActionKind(event.action, "finish") || event.progress?.percent === 100);
}

function getStageLogicalTaskCount(events: NormalizedThreadEvent[], stage: ThreadEventStage) {
  const keys = new Set<string>();
  events.forEach((event) => {
    const payload = event.payload;
    const operationRefs = getStructuredArrayField(payload, ["operation_refs"]);
    operationRefs?.forEach((item) => {
      if (typeof item !== "string") {
        return;
      }
      const flowKind = operationFlowKindFromRef(item);
      if (stage === "eval" && flowKind !== "eval.rag_answer" && flowKind !== "eval.judge_answer") {
        return;
      }
      keys.add(item);
    });
    const operationRunId = getOperationRunId(payload);
    if (!operationRunId) {
      return;
    }
    const flowKind = getEventFlowKind(payload) || operationFlowKindFromRef(operationRunId);
    if (stage === "eval" && flowKind !== "eval.rag_answer" && flowKind !== "eval.judge_answer") {
      return;
    }
    keys.add(operationRunId);
  });
  return keys.size || events.length;
}

function operationFlowKindFromRef(ref: string) {
  if (/^(?:eval|eval_retry_\d+)\.rag\./.test(ref)) {
    return "eval.rag_answer";
  }
  if (/^(?:eval|eval_retry_\d+)\.judge\./.test(ref)) {
    return "eval.judge_answer";
  }
  if (/^(?:eval|eval_retry_\d+)\.aggregate$/.test(ref)) {
    return "eval.aggregate";
  }
  return "";
}

export function buildEvoProcessDashboard(
  events: NormalizedThreadEvent[],
  runtimeState: WorkflowRuntimeState,
  includeFirstStep: boolean,
  terminalStepStatus?: StepStatus,
): EvoProcessDashboard {
  const sortedEvents = dedupeNormalizedEvents(events);
  const cutoverCompleted = sortedEvents.some(isCutoverCompletedEvent);
  const hasInactiveTerminalEvent = sortedEvents.some(isInactiveTerminalThreadEvent);
  const checkpoint = cutoverCompleted || hasInactiveTerminalEvent || terminalStepStatus
    ? undefined
    : getPendingCheckpointWaitPrompt(sortedEvents);
  const visibleStepsById = new Map(
    buildVisibleWorkflowSteps(sortedEvents, runtimeState, includeFirstStep, terminalStepStatus)
      .map((step) => [step.id, step]),
  );
  const runtimeSteps = workflowStepDefinitions.map((definition) =>
    visibleStepsById.get(definition.id) || createWorkflowStepFromRuntime(definition.id, runtimeState),
  );
  const hasStageEvents = sortedEvents.some((event) => event.stage);
  const overview = runtimeSteps.map((step) => {
    const stage = stepStageMap[step.id];
    const stageEvents = sortedEvents.filter((event) => event.stage === stage);
    const status: StepStatus = cutoverCompleted
      ? "done"
      : checkpoint?.completedStage === stage
      ? "paused"
      : includeFirstStep && !hasStageEvents && step.id === "dataset"
        ? "running"
        : step.status;
    return {
      step: {
        ...step,
        status,
        progress: cutoverCompleted
          ? { ...getCompletedProgressSnapshot(), statusText: stage === "abtest" ? "候选算法切流已完成" : "已完成" }
          : step.progress || stageProgressFromEvents(sortedEvents, stage),
      },
      stage,
      eventCount: getStageLogicalTaskCount(stageEvents, stage),
      latestActivity: stageEvents.length ? buildEventActivity(stageEvents[stageEvents.length - 1]) : undefined,
    };
  });
  const visibleActivityEvents = sortedEvents.filter(shouldShowProcessActivity);
  const activities = visibleActivityEvents.map(buildEventActivity);
  const caseProgressGroups = buildCaseProgressGroups(sortedEvents);
  const latestStage = cutoverCompleted ? "abtest" : checkpoint?.completedStage || getLastItem(visibleActivityEvents.filter((event) => event.stage))?.stage;
  const activeOverview =
    (latestStage ? overview.find((item) => item.stage === latestStage) : undefined) ||
    overview.find((item) => ["running", "paused", "failed", "canceled"].includes(item.step.status)) ||
    overview.find((item) => item.step.status === "pending") ||
    getLastItem(overview);
  const recentActivities = activities.slice().reverse();
  const cutoverActivities = activities.filter(isCutoverActivity).slice(-3).reverse();
  return {
    overview,
    activeStage: activeOverview?.stage,
    activeStep: activeOverview?.step,
    activeProgress: activeOverview?.step.progress,
    activeProgressPhases: activeOverview?.step.progressPhases,
    recentActivities,
    recentActivityTotal: visibleActivityEvents.length,
    checkpoint,
    cutoverActivities,
    cutoverCompleted,
    caseProgressGroups,
  };
}

export function getPendingCheckpointWaitPrompt(events: NormalizedThreadEvent[]) {
  const hasInactiveTerminalEvent = events.some(isInactiveTerminalThreadEvent);
  if (hasInactiveTerminalEvent) {
    return undefined;
  }

  const checkpointEvents = events
    .filter((event) => event.type === "checkpoint.wait" && event.checkpointWait)
    .sort(compareNormalizedThreadEvents);
  const latestCheckpointEvent = getLastItem(checkpointEvents);

  if (!latestCheckpointEvent?.checkpointWait) {
    return undefined;
  }

  const nextStage = latestCheckpointEvent.checkpointWait.nextStage;
  const hasContinued = events.some((event) => {
    const isLaterEvent = isThreadEventAfter(latestCheckpointEvent, event);
    if (!isLaterEvent) {
      return false;
    }
    if (
      event.type === "checkpoint.continue" ||
      event.type === "checkpoint.rewind" ||
      event.type === "checkpoint.cancel"
    ) {
      return true;
    }
    if (event.type.startsWith("autooperator.")) {
      return false;
    }
    if (nextStage && event.stage === nextStage) {
      return true;
    }
    return Boolean(event.stage);
  });

  return hasContinued ? undefined : latestCheckpointEvent.checkpointWait;
}

export function isThreadEventAfter(
  baseEvent: Pick<NormalizedThreadEvent, "sequence" | "timestamp" | "key">,
  candidateEvent: Pick<NormalizedThreadEvent, "sequence" | "timestamp" | "key">,
) {
  if (
    typeof baseEvent.sequence === "number" &&
    typeof candidateEvent.sequence === "number" &&
    baseEvent.sequence !== candidateEvent.sequence
  ) {
    return candidateEvent.sequence > baseEvent.sequence;
  }
  if (baseEvent.timestamp && candidateEvent.timestamp) {
    const baseTime = new Date(baseEvent.timestamp).getTime();
    const candidateTime = new Date(candidateEvent.timestamp).getTime();
    if (!Number.isNaN(baseTime) && !Number.isNaN(candidateTime) && baseTime !== candidateTime) {
      return candidateTime > baseTime;
    }
  }
  return compareNormalizedThreadEvents(baseEvent as NormalizedThreadEvent, candidateEvent as NormalizedThreadEvent) < 0;
}

export function reduceWorkflowRuntimeState(
  prev: WorkflowRuntimeState,
  event: NormalizedThreadEvent,
): WorkflowRuntimeState {
  if (!event.stage) {
    return prev;
  }

  const stepId = stageStepMap[event.stage];
  const stepIndex = getWorkflowStepIndex(stepId);
  const action = event.action;
  const next: WorkflowRuntimeState = { ...prev };
  workflowStepDefinitions.forEach((step, index) => {
    next[step.id] = { ...prev[step.id] };
    if (index < stepIndex && next[step.id].status === "pending") {
      next[step.id].status = "done";
    }
  });

  const current = next[stepId];
  if (current.status === "done" && isIntentSidecarOperation(event)) {
    return next;
  }

  if (event.stage === "eval") {
    current.progressPhases = updateEvalProgressPhases(
      current.progressPhases,
      event.progressPhase,
      event.progress,
      action,
      Boolean(getOperationRunId(event.payload)),
    );
    current.progress = getEvalOverallProgressSnapshot(current.progressPhases);
  }

  const isFinished = isStepFinishEvent(event);

  if (isFinished) {
    current.status = "done";
    if (event.stage === "eval") {
      current.progressPhases = getCompletedEvalProgressPhases();
      current.progress = getEvalOverallProgressSnapshot(current.progressPhases);
    } else {
      current.progress = event.progress || getCompletedProgressSnapshot();
    }
  } else if (action === "cancel") {
    current.status = "canceled";
  } else if (action === "failed") {
    current.status = "failed";
  } else if (action === "pause") {
    current.status = "paused";
    if (event.stage !== "eval") {
      current.progress = mergeProgressSnapshot(
        current.progress,
        event.progress || updateProgressStatusText(current.progress, getRuntimeProgressStatusLabel(action)),
      );
    }
  } else {
    current.status = "running";
    if (event.stage !== "eval") {
      current.progress = mergeProgressSnapshot(
        current.progress,
        event.progress || updateProgressStatusText(current.progress, getRuntimeProgressStatusLabel(action)),
      );
    }
  }
  current.runtimeText = event.progress ? undefined : event.displayText;
  return next;
}

export function reduceWorkflowRuntimeStateFromEvents(
  prev: WorkflowRuntimeState,
  events: NormalizedThreadEvent[],
): WorkflowRuntimeState {
  return dedupeNormalizedEvents(events).reduce(reduceWorkflowRuntimeState, prev);
}

export function getThreadTitleFromHistoryPayload(payload: ThreadRestorePayload) {
  if (!isRecord(payload)) {
    return undefined;
  }

  return getNestedStringField(payload, ["title"]);
}

export function getThreadTitleFromPayload(payload: ThreadRestorePayload) {
  if (!isRecord(payload)) {
    return undefined;
  }

  return (
    getNestedStringField(payload, ["title", "name", "thread_name"]) ||
    getNestedStringField(getNestedRecordField(payload, ["thread", "upstream", "data"]), [
      "title",
      "name",
      "thread_name",
    ])
  );
}

export function getThreadKnowledgeBaseId(payload: ThreadRestorePayload) {
  if (!isRecord(payload)) {
    return undefined;
  }

  const threadPayload = getThreadPayloadFromRestorePayload(payload);
  const inputs =
    getNestedRecordField(threadPayload, ["inputs", "input", "config"]) ||
    getNestedRecordField(payload, ["inputs", "input", "config"]);
  return (
    getNestedStringField(threadPayload, ["kb_id", "knowledge_base_id", "dataset_id"]) ||
    getNestedStringField(payload, ["kb_id", "knowledge_base_id", "dataset_id"]) ||
    getNestedStringField(inputs, ["kb_id", "knowledge_base_id", "dataset_id"])
  );
}

export function getThreadPayloadFromRestorePayload(payload: ThreadRestorePayload) {
  if (!isRecord(payload)) {
    return undefined;
  }

  const threadRecord = getNestedRecordField(payload, ["thread"]);
  return (
    getNestedRecordField(threadRecord, ["thread_payload", "threadPayload", "payload"]) ||
    getNestedRecordField(payload, ["thread_payload", "threadPayload", "payload"])
  );
}

export function getThreadModeFromPayload(payload: ThreadRestorePayload): EvolutionMode | undefined {
  if (!isRecord(payload)) {
    return undefined;
  }

  const threadPayload = getThreadPayloadFromRestorePayload(payload);
  const inputs =
    getNestedRecordField(threadPayload, ["inputs", "input", "config"]) ||
    getNestedRecordField(payload, ["inputs", "input", "config"]);
  const modeValue =
    getNestedStringField(threadPayload, ["mode", "evolution_mode", "interaction_mode"]) ||
    getNestedStringField(payload, ["mode", "evolution_mode", "interaction_mode"]) ||
    getNestedStringField(inputs, ["mode", "evolution_mode", "interaction_mode"]);

  return modeValue === "auto" || modeValue === "interactive" ? modeValue : undefined;
}
