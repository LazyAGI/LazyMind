import { useCallback, useEffect, useMemo, useRef, useState, type MouseEvent, type ReactNode } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  Collapse,
  Dropdown,
  Modal,
  Table,
  Tag,
  Typography,
  type MenuProps,
  message,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  CheckCircleFilled,
  CloseOutlined,
  ClockCircleFilled,
  FileTextOutlined,
  DownOutlined,
  ExperimentOutlined,
  LoadingOutlined,
  ReloadOutlined,
  DatabaseOutlined,
  HistoryOutlined,
  MessageOutlined,
  PlusOutlined,
} from "@ant-design/icons";
import SendIcon from "@/modules/chat/assets/icons/send_icon.svg?react";
import {
  Configuration as CoreConfiguration,
  DefaultApi as CoreDefaultApi,
  type Dataset,
} from "@/api/generated/core-client";
import type { AxiosError } from "axios";
import { AgentAppsAuth } from "@/components/auth";
import MarkdownViewer from "@/modules/knowledge/components/MarkdownViewer";
import { KnowledgeBaseServiceApi } from "@/modules/knowledge/utils/request";
import { BASE_URL, axiosInstance, getLocalizedErrorMessage } from "@/components/request";
import { getSelfEvolutionWorkflowImageSrc } from "@/modules/selfEvolution/shared";
import {
  ChatComposer,
  ChatMessageStream,
  HistorySessionItem,
  HistorySessionTab,
} from "./components";
import "./index.scss";

const { Paragraph, Text, Title } = Typography;

type EvolutionMode = "auto" | "interactive";
type ExtraEvalStrategy = "skip" | "generate";
type WorkflowStepId = "dataset" | "px-report" | "analysis" | "code-optimize" | "ab-test";
type StepStatus = "running" | "pending" | "done" | "paused" | "canceled";
type ChatRole = "user" | "assistant";
type ThreadEventStage = "dataset_gen" | "eval" | "run" | "apply" | "abtest";

type WorkflowProgressSnapshot = {
  statusText: string;
  percent: number;
};

type WorkflowStep = {
  id: WorkflowStepId;
  renderKey?: string;
  title: string;
  desc: string;
  status: StepStatus;
  runtimeText?: string;
  progress?: WorkflowProgressSnapshot;
};

type EvalCaseItem = {
  case_id: string;
  reference_doc: string[];
  reference_context: string[];
  is_deleted: boolean;
  question: string;
  question_type: number;
  key_point: string[];
  ground_truth: string;
};

type EvalDataset = {
  eval_set_id: string;
  eval_name: string;
  kb_id: string;
  task_id: string;
  create_time: string;
  total_nums: number;
  cases: EvalCaseItem[];
};

type ChatMessage = {
  id: string;
  role: ChatRole;
  content: string;
  time: string;
  sortTime?: number;
  agentLabel?: string;
};

type ChatSession = {
  id: string;
  title: string;
  updatedAt: string;
  threadId?: string;
  messages: ChatMessage[];
};

type ThreadHistoryEntry = {
  threadId: string;
  title: string;
  updatedAt: string;
  status?: string;
};

type HistorySessionEntry = {
  key: string;
  sessionId?: string;
  threadId?: string;
  title: string;
  updatedAt: string;
  messageCount?: number;
  status?: string;
  source: "thread" | "local";
};

type NewSessionDraft = {
  selectedKb?: string;
  selectedEvalSet?: string;
  extraEvalStrategy?: ExtraEvalStrategy;
  mode?: EvolutionMode;
};

type KnowledgeBaseOption = {
  label: string;
  value: string;
};

type AgentThreadCreateResponse = {
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

type ThreadEventFrame = {
  id?: string;
  eventName: string;
  data: string;
};

type ThreadRestorePayload = Record<string, unknown> | unknown[] | undefined;

type WorkflowRuntimeState = Record<
  WorkflowStepId,
  { status: StepStatus; runtimeText?: string; progress?: WorkflowProgressSnapshot }
>;

type NormalizedThreadEvent = {
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
  checkpointWait?: CheckpointWaitPrompt;
};

type ChatStreamDeltaKind = "thinking" | "answer";

type CheckpointWaitPrompt = {
  message: string;
  completedStageLabel?: string;
  nextOperationLabel?: string;
  nextStage?: ThreadEventStage;
  command: string;
  taskId?: string;
  datasetId?: string;
};

type AnalysisHypothesisItem = {
  id: string;
  claim: string;
  category?: string;
  confidence?: number;
  investigationPaths: string[];
  verdict?: string;
  refinedClaim?: string;
  suggestedAction?: string;
  agent?: string;
};

type AnalysisAgentItem = {
  agent: string;
  rounds?: number;
  toolCallCount: number;
  tools: Array<{ name: string; count: number }>;
  verdict?: string;
  confidence?: number;
  hypothesisId?: string;
};

type AnalysisTimelineItem = {
  key: string;
  title: string;
  detail: string;
  time?: string;
};

type AnalysisRunSummary = {
  status: StepStatus;
  hypothesisCount: number;
  agentCount: number;
  completedAgentCount: number;
  toolCallCount: number;
  iterationCount?: number;
  converged?: boolean;
  crossStepNarrative?: string;
  hypotheses: AnalysisHypothesisItem[];
  agents: AnalysisAgentItem[];
  timeline: AnalysisTimelineItem[];
};

type ApplyRunSummary = {
  status: StepStatus;
  roundCount?: number;
  changedFileCount: number;
  changedFiles: string[];
  testStatusText?: string;
  commitSha?: string;
  timeline: AnalysisTimelineItem[];
};

type WorkflowResultKind = "datasets" | "eval-reports" | "analysis-reports" | "diffs" | "abtests";

type WorkflowResultState = {
  loading: boolean;
  loaded: boolean;
  error?: string;
  data?: unknown;
};

type WorkflowResultsState = Record<WorkflowResultKind, WorkflowResultState>;

type DiffArtifactContentState = {
  loading: boolean;
  key: string;
  content: string;
  error?: string;
};

type DiffArtifactFile = {
  path: string;
  diffPath: string;
  additions?: number;
  deletions?: number;
  changeKind?: string;
};

type AbComparisonRow = {
  key: string;
  category: string;
  baselineSummary: string;
  experimentSummary: string;
  deltaSummary: string;
};

const FIXED_EVAL_SET = "__none__";
const FIXED_EXTRA_EVAL_STRATEGY: ExtraEvalStrategy = "generate";
const DEFAULT_EVAL_CASE_COUNT = 100;
const AGENT_API_BASE = `${BASE_URL}/api/core/agent`;
const SELF_EVOLUTION_LAST_THREAD_STORAGE_KEY = "lazymind:self-evolution:last-thread";
const DEPRECATED_SELF_EVOLUTION_THREAD_HISTORY_STORAGE_KEY = "lazymind:self-evolution:thread-history";

const workflowResultLabels: Record<WorkflowResultKind, string> = {
  datasets: "数据集结果",
  "eval-reports": "评测报告",
  "analysis-reports": "分析报告",
  diffs: "代码 diff 结果",
  abtests: "A/B 测试结果",
};

function createCoreAgentApiClient() {
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

type ParsedDiffFile = {
  id: string;
  fromPath: string;
  toPath: string;
  displayPath: string;
  lines: string[];
  additions: number;
  deletions: number;
};

type DiffFileTreeNode = {
  name: string;
  path: string;
  nodeType: "dir" | "file";
  fileId?: string;
  children: DiffFileTreeNode[];
};

type PxMetricKey = "answer_correctness" | "faithfulness" | "context_recall" | "doc_recall";

type PxCategoryMetricAverage = {
  category: string;
  caseCount: number;
  metrics: Record<PxMetricKey, number>;
};

type EvalQuestionTypeSummary = {
  question_type?: number;
  question_type_key?: string;
  question_type_name?: string;
  count?: number;
  averages?: Partial<Record<PxMetricKey, number>>;
};

type AbCategoryComparison = {
  category: string;
  baseline: Record<PxMetricKey, number>;
  experiment: Record<PxMetricKey, number>;
  delta: Record<PxMetricKey, number>;
};

type AbSummaryMetricRow = {
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

type AbTopDiffRow = {
  key: string;
  caseKey: string;
  a: number;
  b: number;
  delta: number;
};

type AbSummaryReport = {
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

const pxMetricMeta: Array<{ key: PxMetricKey; label: string; color: string }> = [
  { key: "answer_correctness", label: "答案正确性", color: "#1a73e8" },
  { key: "faithfulness", label: "忠实性", color: "#22a06b" },
  { key: "context_recall", label: "上下文召回", color: "#f08c00" },
  { key: "doc_recall", label: "文档召回", color: "#7048e8" },
];

const stageStepMap: Record<ThreadEventStage, WorkflowStepId> = {
  dataset_gen: "dataset",
  eval: "px-report",
  run: "analysis",
  apply: "code-optimize",
  abtest: "ab-test",
};

const stageLabels: Record<ThreadEventStage, string> = {
  dataset_gen: "生成评测集",
  eval: "执行评测",
  run: "执行分析",
  apply: "代码修改",
  abtest: "ABTest",
};

const checkpointCommandText = "继续执行";

const terminalThreadEventTypes = new Set(["done", "thread.done", "thread.stop", "intent.done"]);

const eventActionLabels: Record<string, string> = {
  start: "开始",
  progress: "进度更新",
  finish: "完成",
  cancel: "已取消",
  pause: "已暂停",
  resume: "已恢复",
  "indexer.result": "索引器结果",
  "conductor.result": "编排器结果",
  "researcher.result": "研究员结果",
  "tool.used": "工具调用",
  "round.diff": "代码变更",
};

const analysisCategoryLabels: Record<string, string> = {
  retrieval_miss: "检索问题",
  generation_drift: "生成偏移",
  score_anomaly: "评分异常",
};

const analysisVerdictLabels: Record<string, string> = {
  confirmed: "已确认",
  refuted: "已推翻",
  inconclusive: "待补证",
  partial: "部分成立",
};

const workflowStepDefinitions: Omit<WorkflowStep, "status" | "runtimeText">[] = [
  {
    id: "dataset",
    title: "Step 1 · 生成数据集",
    desc: "将任务目标拆分为训练样本，生成数据集数据并写入自进化流水线。",
  },
  {
    id: "px-report",
    title: "Step 2 · 评测报告",
    desc: "基于数据集生成首轮评测报告，建立效果基线。",
  },
  {
    id: "analysis",
    title: "Step 3 · 分析报告",
    desc: "自动分析误答样本，产出问题归因和优先级建议。",
  },
  {
    id: "code-optimize",
    title: "Step 4 · 代码优化",
    desc: "根据分析结论给出可执行改造项，形成优化清单。",
  },
  {
    id: "ab-test",
    title: "Step 5 · A/B 测试",
    desc: "执行对照实验并上传新评测报告，确认优化收益。",
  },
];

const workflowStepOrder = workflowStepDefinitions.map((step) => step.id);

const getKnowledgeBaseName = (dataset: Dataset) =>
  dataset.display_name || dataset.name || dataset.dataset_id || "未命名知识库";

const isCanceledRequest = (error: unknown) => {
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

const existingEvalSetOptions = [
  { label: "不使用已有评测集", value: "__none__" },
];

const evalSetPreviewData: EvalDataset = {
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

const questionTypeLabelMap: Record<number, string> = {
  1: "单跳",
  2: "多跳",
  3: "公式",
  4: "表格",
  5: "代码",
};

const formatQuestionType = (questionType: number) => {
  const label = questionTypeLabelMap[questionType];
  if (!label) {
    return String(questionType);
  }
  return label;
};

const clampScore = (value: number) => {
  if (!Number.isFinite(value)) {
    return 0;
  }
  return Math.min(1, Math.max(0, value));
};

const formatPercent = (value: number) => `${(value * 100).toFixed(1)}%`;

const getQuestionTypeDisplayName = (item: EvalQuestionTypeSummary, index: number) => {
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

const buildPxCategoryMetricAveragesFromReport = (payload: unknown): PxCategoryMetricAverage[] => {
  const sourceRecord = Array.isArray(payload)
    ? (payload.find((item): item is Record<string, unknown> => isRecord(item)) ?? undefined)
    : isRecord(payload)
      ? payload
      : undefined;

  const caseDetailSummary =
    getStructuredRecordField(sourceRecord, ["case_details_summary"]) ||
    getNestedRecordField(sourceRecord, ["case_details_summary"]);
  const questionTypes = (getStructuredArrayField(caseDetailSummary, ["question_types"]) || []).filter(
    (item): item is EvalQuestionTypeSummary => isRecord(item),
  );

  return questionTypes
    .map((item, index) => ({
      category: getQuestionTypeDisplayName(item, index),
      caseCount: typeof item.count === "number" ? item.count : 0,
      metrics: {
        answer_correctness: clampScore(Number(item.averages?.answer_correctness ?? 0)),
        faithfulness: clampScore(Number(item.averages?.faithfulness ?? 0)),
        context_recall: clampScore(Number(item.averages?.context_recall ?? 0)),
        doc_recall: clampScore(Number(item.averages?.doc_recall ?? 0)),
      },
    }))
    .sort((a, b) => a.category.localeCompare(b.category, "zh-CN", { numeric: true }));
};

function getTimeLabel() {
  return new Date().toLocaleTimeString("zh-CN", {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}

function createInitialWorkflowRuntimeState(): WorkflowRuntimeState {
  return {
    dataset: { status: "running" },
    "px-report": { status: "pending" },
    analysis: { status: "pending" },
    "code-optimize": { status: "pending" },
    "ab-test": { status: "pending" },
  };
}

function createThreadRestoreWorkflowRuntimeState(): WorkflowRuntimeState {
  return {
    dataset: { status: "pending" },
    "px-report": { status: "pending" },
    analysis: { status: "pending" },
    "code-optimize": { status: "pending" },
    "ab-test": { status: "pending" },
  };
}

function createInitialWorkflowResultsState(): WorkflowResultsState {
  return {
    datasets: { loading: false, loaded: false },
    "eval-reports": { loading: false, loaded: false },
    "analysis-reports": { loading: false, loaded: false },
    diffs: { loading: false, loaded: false },
    abtests: { loading: false, loaded: false },
  };
}

function getStepStatusLabel(status: StepStatus) {
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
  return "待执行";
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function getStringField(payload: Record<string, unknown> | undefined, keys: string[]) {
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

function getNumberField(payload: Record<string, unknown> | undefined, keys: string[]) {
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

function getResultItems(value: unknown): unknown[] {
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

function isEmptyResultPayload(value: unknown) {
  if (value === undefined || value === null) {
    return true;
  }
  if (typeof value === "string") {
    return value.trim().length === 0;
  }
  if (Array.isArray(value)) {
    return value.length === 0;
  }
  if (isRecord(value)) {
    const nestedItems = getResultItems(value);
    return nestedItems.length === 0 && Object.keys(value).length === 0;
  }
  return false;
}

function stringifyResultPayload(value: unknown) {
  if (typeof value === "string") {
    return value;
  }
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function getResultStringField(value: unknown, keys: string[]): string | undefined {
  if (typeof value === "string" && value.trim()) {
    return value.trim();
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

function buildCoreDownloadUrl(pathValue: string | undefined) {
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

function getResultDownloadPath(value: unknown) {
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

function getDiffArtifactFiles(value: unknown): DiffArtifactFile[] {
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

function normalizeFetchedDiffArtifact(file: DiffArtifactFile, content: string) {
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

function getDownloadFileName(downloadUrl: string, fallbackFileName: string) {
  if (!downloadUrl) {
    return fallbackFileName;
  }

  const sanitizedUrl = downloadUrl.split("?")[0]?.split("#")[0] || "";
  const fileName = sanitizedUrl.split("/").filter(Boolean).pop();
  return fileName || fallbackFileName;
}

function triggerBrowserDownload(downloadUrl: string, fileName: string) {
  const anchor = document.createElement("a");
  anchor.href = downloadUrl;
  anchor.download = fileName;
  anchor.target = "_blank";
  anchor.rel = "noopener noreferrer";
  document.body.appendChild(anchor);
  anchor.click();
  document.body.removeChild(anchor);
}

function getNestedStringField(payload: Record<string, unknown> | undefined, keys: string[]) {
  const directValue = getStringField(payload, keys);
  if (directValue) {
    return directValue;
  }

  if (isRecord(payload?.data)) {
    return getStringField(payload.data, keys);
  }

  return undefined;
}

function getNestedRecordField(payload: Record<string, unknown> | undefined, keys: string[]) {
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

function getNestedArrayField(payload: ThreadRestorePayload, keys: string[]): unknown[] {
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

function formatThreadTime(value: unknown) {
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

function getThreadTimeSortValue(value: unknown) {
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

function formatThreadListTime(value: unknown) {
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

function getThreadListItemTitle(item: Record<string, unknown>, threadId: string) {
  const payload = getNestedRecordField(item, ["thread_payload", "payload", "inputs", "input"]);
  return (
    getNestedStringField(item, ["title", "name", "thread_name", "display_name"]) ||
    getNestedStringField(payload, ["title", "name", "thread_name", "display_name", "kb_id", "dataset_id"]) ||
    `自进化会话 ${threadId.slice(0, 8)}`
  );
}

function normalizeThreadListPayload(payload: unknown): ThreadHistoryEntry[] {
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

function getDialogueEventAgentLabel(event: NormalizedThreadEvent) {
  if (event.type === "message.user") {
    return "模拟用户";
  }
  if (event.type === "message.assistant") {
    return "回复 Agent";
  }
  return undefined;
}

function buildAutoInteractionMessagesFromEvents(events: NormalizedThreadEvent[]): ChatMessage[] {
  return dedupeNormalizedEvents(events)
    .filter((event) => event.role && event.content && getDialogueEventAgentLabel(event))
    .map((event) => ({
      id: `event-chat-${event.key}`,
      role: event.role as ChatRole,
      content: event.content || "",
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

function getEventPayloadData(payload: Record<string, unknown> | undefined) {
  if (isRecord(payload?.payload)) {
    return payload.payload;
  }
  if (isRecord(payload?.data)) {
    return payload.data;
  }
  return payload;
}

function getThreadEventPayloadEnvelope(payload: Record<string, unknown> | undefined) {
  if (isRecord(payload?.payload)) {
    return payload.payload;
  }
  return undefined;
}

function getThreadEventTypeFromPayload(payload: Record<string, unknown> | undefined) {
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

function getThreadEventContentFromPayload(payload: Record<string, unknown> | undefined) {
  const eventEnvelope = getThreadEventPayloadEnvelope(payload);
  const eventPayload = getEventPayloadData(eventEnvelope) || getEventPayloadData(payload);

  return (
    getNestedStringField(payload, ["message", "content", "text", "reply", "thought", "delta"]) ||
    getNestedStringField(eventEnvelope, ["message", "content", "text", "reply", "thought", "delta"]) ||
    getNestedStringField(eventPayload, ["message", "content", "text", "reply", "thought", "delta"])
  );
}

function clampPercent(value: number) {
  if (!Number.isFinite(value)) {
    return 0;
  }
  return Math.min(100, Math.max(0, Math.round(value)));
}

function getRuntimeProgressStatusLabel(action: string | undefined) {
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

function getCompletedProgressSnapshot(): WorkflowProgressSnapshot {
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

function parseStructuredRecord(value: unknown): Record<string, unknown> | undefined {
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

function parseStructuredArray(value: unknown): unknown[] | undefined {
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

function getStructuredRecordField(payload: Record<string, unknown> | undefined, keys: string[]) {
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

function getStructuredArrayField(payload: Record<string, unknown> | undefined, keys: string[]) {
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

function formatAnalysisVerdict(verdict: string | undefined) {
  if (!verdict) {
    return "调查中";
  }
  return analysisVerdictLabels[verdict] || verdict;
}

function formatAnalysisCategory(category: string | undefined) {
  if (!category) {
    return "待归类";
  }
  return analysisCategoryLabels[category] || category;
}

function formatConfidencePercent(value: number | undefined) {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return undefined;
  }

  const normalized = value <= 1 ? value * 100 : value;
  return `${Math.round(normalized)}%`;
}

function formatAnalysisAgentName(agent: string | undefined) {
  if (!agent) {
    return "研究子代理";
  }
  if (agent.startsWith("researcher:")) {
    return `研究员 ${agent.slice("researcher:".length)}`;
  }
  return agent;
}

function buildAnalysisEventDisplayText(
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

function buildApplyEventDisplayText(
  action: string | undefined,
  type: string,
  payload: Record<string, unknown> | undefined,
) {
  const eventData = getEventPayloadData(payload);

  if (action === "start") {
    return "已启动代码优化，正在生成候选改动。";
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
    return "候选优化版本已准备完成，可查看代码改动结果。";
  }

  if (action === "cancel") {
    return "代码优化已取消，当前候选版本未继续推进。";
  }

  return undefined;
}

function buildAbtestEventDisplayText(action: string | undefined) {
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

function getWorkflowProgressSnapshot(
  stage: ThreadEventStage | undefined,
  action: string | undefined,
  payload: Record<string, unknown> | undefined,
): WorkflowProgressSnapshot | undefined {
  if (stage !== "dataset_gen" && stage !== "eval" && stage !== "abtest") {
    return undefined;
  }

  const eventData = getEventPayloadData(payload);
  const current = getNumberField(eventData, ["current", "completed", "done", "processed"]);
  const total = getNumberField(eventData, ["total", "num_cases", "cases", "count"]);
  const explicitPercent = getNumberField(eventData, ["percent", "percentage", "progress"]);
  const hasProgressValue =
    typeof explicitPercent === "number" ||
    (typeof current === "number" && typeof total === "number" && total > 0);
  const percent =
    action === "finish"
      ? 100
      : action === "start"
        ? typeof explicitPercent === "number"
          ? explicitPercent
          : typeof current === "number" && typeof total === "number" && total > 0
            ? (current / total) * 100
            : hasProgressValue
              ? 0
              : undefined
        : typeof explicitPercent === "number"
          ? explicitPercent
          : typeof current === "number" && typeof total === "number" && total > 0
            ? (current / total) * 100
            : undefined;

  if (typeof percent !== "number") {
    return undefined;
  }

  return {
    statusText: getRuntimeProgressStatusLabel(action),
    percent: clampPercent(percent),
  };
}

function toThreadEventStage(value: unknown): ThreadEventStage | undefined {
  if (typeof value !== "string") {
    return undefined;
  }

  const normalized = value.trim();
  if (normalized === "dataset_gen" || normalized === "eval" || normalized === "run" || normalized === "apply" || normalized === "abtest") {
    return normalized;
  }

  return undefined;
}

function getStageLabel(value: unknown) {
  const stage = toThreadEventStage(value);
  if (stage) {
    return stageLabels[stage];
  }
  if (typeof value === "string" && value.trim()) {
    return value.trim();
  }
  return undefined;
}

function getNextStageFromOperation(value: string | undefined): ThreadEventStage | undefined {
  if (!value) {
    return undefined;
  }

  const [operationStage] = value.split(".");
  return toThreadEventStage(operationStage);
}

function formatCheckpointOperation(value: string | undefined) {
  if (!value) {
    return undefined;
  }

  const [operationStage, ...operationParts] = value.split(".");
  const stageLabel = getStageLabel(operationStage);
  const actionLabel = operationParts.length > 0 ? operationParts.join(".") : "";
  return [stageLabel, actionLabel].filter(Boolean).join(" · ");
}

function sanitizeCheckpointMessage(
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

function buildCheckpointWaitPrompt(payload: Record<string, unknown> | undefined): CheckpointWaitPrompt {
  const eventData = getEventPayloadData(payload);
  const nextOperation = getNestedRecordField(eventData, ["next_op", "nextOperation", "next"]);
  const nextOperationName = getStringField(nextOperation, ["op", "operation", "name"]);
  const artifacts = getNestedRecordField(eventData, ["artifacts", "result", "data"]);
  const messageText =
    getStringField(eventData, ["message", "text", "content"]) ||
    getStringField(payload, ["message", "text", "content"]) ||
    "当前流程已暂停，等待确认是否继续下一步。";
  const completedStageLabel = getStageLabel(
    getStringField(eventData, ["completed_flow", "completed_stage", "stage"]) ||
      getStringField(artifacts, ["completed_flow", "stage"]),
  );
  const nextOperationLabel = formatCheckpointOperation(nextOperationName);

  return {
    message: sanitizeCheckpointMessage(messageText, completedStageLabel, nextOperationLabel),
    completedStageLabel,
    nextOperationLabel,
    nextStage: getNextStageFromOperation(nextOperationName),
    command: checkpointCommandText,
    taskId:
      getStringField(eventData, ["completed_task_id", "task_id"]) ||
      getStringField(artifacts, ["task_id"]),
  };
}

function isTerminalAbtestCheckpoint(prompt: CheckpointWaitPrompt | undefined) {
  return prompt?.completedStageLabel === stageLabels.abtest && !prompt.nextStage;
}

function compactPayloadForDisplay(payload: Record<string, unknown> | undefined) {
  if (!payload) {
    return "";
  }

  const entries = Object.entries(payload).filter(
    ([key, value]) =>
      !["type", "event", "event_name", "kind", "stage", "action", "message", "content", "text", "reply", "thought"].includes(key) &&
      value !== undefined &&
      value !== null &&
      value !== "",
  );
  if (entries.length === 0) {
    return "";
  }

  const compactPayload = Object.fromEntries(entries);
  try {
    return JSON.stringify(compactPayload);
  } catch {
    return "";
  }
}

function getDiffLineType(line: string) {
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

function getShortLabel(text: string, maxLength = 6) {
  if (text.length <= maxLength) {
    return text;
  }
  return `${text.slice(0, maxLength)}…`;
}

function normalizeDiffPath(path: string) {
  const cleaned = path.replace(/^([ab])\//, "");
  const lazyMindIndex = cleaned.indexOf("LazyMind/");
  if (lazyMindIndex >= 0) {
    return cleaned.slice(lazyMindIndex + "LazyMind/".length);
  }
  return cleaned;
}

function parseUnifiedDiff(diffText: string): ParsedDiffFile[] {
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

function buildDiffFileTree(files: ParsedDiffFile[]): DiffFileTreeNode[] {
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

function buildAbCategoryComparisons(reports: AbSummaryReport[]): AbCategoryComparison[] {
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

function formatMetricPercent(value: number) {
  return `${Math.round(value * 100)}%`;
}

function formatMetricDelta(value: number) {
  const percent = Math.round(value * 100);
  return `${percent > 0 ? "+" : ""}${percent}%`;
}

function formatMetricSummary(metrics: Record<PxMetricKey, number>) {
  return [
    `正确性 ${formatMetricPercent(metrics.answer_correctness)}`,
    `忠实性 ${formatMetricPercent(metrics.faithfulness)}`,
    `上下文召回 ${formatMetricPercent(metrics.context_recall)}`,
    `文档召回 ${formatMetricPercent(metrics.doc_recall)}`,
  ].join(" / ");
}

function toFiniteNumber(value: unknown, fallback = 0) {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string" && value.trim() && Number.isFinite(Number(value))) {
    return Number(value);
  }
  return fallback;
}

function formatAbMetricLabel(metric: string) {
  return pxMetricMeta.find((item) => item.key === metric)?.label || metric;
}

function getAbtestResultRecords(value: unknown): Record<string, unknown>[] {
  if (Array.isArray(value)) {
    return value.filter((item): item is Record<string, unknown> => isRecord(item));
  }
  if (!isRecord(value)) {
    return [];
  }

  const nestedItems = getResultItems(value).filter((item): item is Record<string, unknown> => isRecord(item));
  return nestedItems.length > 0 ? nestedItems : [value];
}

function buildAbSummaryReports(payload: unknown): AbSummaryReport[] {
  return getAbtestResultRecords(payload)
    .reduce<AbSummaryReport[]>((reports, record, index) => {
      const summary =
        getStructuredRecordField(record, ["summary"]) ||
        getNestedRecordField(record, ["summary"]) ||
        (isRecord(record.metrics) ? record : undefined);
      if (!summary) {
        return reports;
      }

      const metricsRecord =
        getStructuredRecordField(summary, ["metrics"]) || getNestedRecordField(summary, ["metrics"]);
      const metricRows = metricsRecord
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

      const topDiffRows = (getStructuredArrayField(summary, ["top_diff_cases"]) || [])
        .filter((item): item is Record<string, unknown> => isRecord(item))
        .map((item, rowIndex) => ({
          key: getStringField(item, ["case_key", "case_id", "id"]) || `case-${rowIndex + 1}`,
          caseKey: getStringField(item, ["case_key", "case_id", "id"]) || `case-${rowIndex + 1}`,
          a: toFiniteNumber(item.a),
          b: toFiniteNumber(item.b),
          delta: toFiniteNumber(item.delta),
        }));

      const policy = getStructuredRecordField(summary, ["policy"]) || getNestedRecordField(summary, ["policy"]);
      const reasons = (getStructuredArrayField(summary, ["reasons"]) || []).filter(
        (item): item is string => typeof item === "string" && item.trim().length > 0,
      );
      const missingMetrics = (getStructuredArrayField(summary, ["missing_metrics"]) || []).filter(
        (item): item is string => typeof item === "string" && item.trim().length > 0,
      );
      const guardMetrics = (getStructuredArrayField(policy, ["guard_metrics"]) || []).filter(
        (item): item is string => typeof item === "string" && item.trim().length > 0,
      );

      reports.push({
        id: getStringField(record, ["abtest_id", "id", "task_id"]) || `abtest-${index + 1}`,
        markdown: getResultStringField(record, ["markdown", "report", "content", "text"]),
        verdict: getStringField(summary, ["verdict"]) || getResultStringField(record, ["verdict"]),
        alignedCases: getNumberField(summary, ["aligned_cases"]),
        reasons,
        metricRows,
        topDiffRows,
        missingMetrics,
        primaryMetric: getStringField(policy, ["primary_metric"]),
        guardMetrics,
      });
      return reports;
    }, []);
}

function formatMaybePValue(value: number | null | undefined) {
  if (value === null || value === undefined || !Number.isFinite(value)) {
    return "-";
  }
  return value < 0.001 ? "<0.001" : value.toFixed(3);
}

function parseSSEFrame(rawFrame: string): ThreadEventFrame | undefined {
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

function parseThreadEventPayload(data: string): Record<string, unknown> | undefined {
  try {
    const value = JSON.parse(data);
    return isRecord(value) ? value : { value };
  } catch {
    return undefined;
  }
}

function getChatStreamDeltaKind(type: string): ChatStreamDeltaKind | undefined {
  if (type === "thinking_delta" || type === "intent.thinking_delta") {
    return "thinking";
  }
  if (type === "answer_delta" || type === "intent.answer_delta") {
    return "answer";
  }
  return undefined;
}

function isTerminalThreadEvent(type: string) {
  return terminalThreadEventTypes.has(type);
}

function normalizeThreadEvent(frame: ThreadEventFrame): NormalizedThreadEvent {
  const payload = parseThreadEventPayload(frame.data);
  const eventEnvelope = getThreadEventPayloadEnvelope(payload);
  const payloadType = getThreadEventTypeFromPayload(payload);
  const eventType = payloadType || (frame.eventName !== "message" ? frame.eventName : "");
  const [typeStage, ...actionParts] = eventType.split(".");
  const isCheckpointEvent = eventType.startsWith("checkpoint.");
  const stageFromPayload =
    toThreadEventStage(payload?.stage) ||
    toThreadEventStage(eventEnvelope?.stage);
  const stage = isCheckpointEvent ? undefined : stageFromPayload || toThreadEventStage(typeStage);
  const action = isCheckpointEvent
    ? actionParts.join(".") || undefined
    : getStringField(payload, ["action"]) ||
      getStringField(eventEnvelope, ["action"]) ||
      (stage && actionParts.length > 0
        ? actionParts.join(".")
        : stage && eventType && !toThreadEventStage(eventType) && eventType !== "message"
          ? eventType
          : undefined);
  const type = isCheckpointEvent ? eventType : stage && action ? `${stage}.${action}` : eventType || "message";
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
  const key =
    frame.id ||
    [
      getStringField(payload, ["thread_id"]) || getStringField(eventEnvelope, ["thread_id"]),
      typeof sequence === "number" ? String(sequence) : "",
      taskId,
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
    (stage === "run" && buildAnalysisEventDisplayText(action, type, payload)) ||
    (stage === "apply" && buildApplyEventDisplayText(action, type, payload)) ||
    (stage === "abtest" && buildAbtestEventDisplayText(action)) ||
    (detail ? `${stageLabels[stage]}：${actionLabel}，${detail}` : `${stageLabels[stage]}：${actionLabel}`);
  const progress = getWorkflowProgressSnapshot(stage, action, payload);

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
  };
}

function compareNormalizedThreadEvents(a: NormalizedThreadEvent, b: NormalizedThreadEvent) {
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

function getNormalizedEventDedupeKey(event: NormalizedThreadEvent) {
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

function dedupeNormalizedEvents(events: NormalizedThreadEvent[]) {
  return Array.from(new Map(events.map((item) => [getNormalizedEventDedupeKey(item), item])).values()).sort(compareNormalizedThreadEvents);
}

function getWorkflowStepIndex(stepId: WorkflowStepId | undefined) {
  if (!stepId) {
    return -1;
  }
  return workflowStepOrder.indexOf(stepId);
}

function createWorkflowStepFromRuntime(
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
    progress: runtime.progress,
  };
}

function buildWorkflowStepRuntimeFromEvents(events: NormalizedThreadEvent[], isSuperseded: boolean) {
  const snapshot: { status: StepStatus; runtimeText?: string; progress?: WorkflowProgressSnapshot } = {
    status: "running",
  };

  events.forEach((event) => {
    if (event.action === "finish") {
      snapshot.status = "done";
      snapshot.progress = event.progress || getCompletedProgressSnapshot();
    } else if (event.action === "cancel") {
      snapshot.status = "canceled";
    } else if (event.action === "pause") {
      snapshot.status = "paused";
      snapshot.progress =
        event.progress ||
        updateProgressStatusText(snapshot.progress, getRuntimeProgressStatusLabel(event.action));
    } else {
      snapshot.status = "running";
      snapshot.progress =
        event.progress ||
        updateProgressStatusText(snapshot.progress, getRuntimeProgressStatusLabel(event.action));
    }
    snapshot.runtimeText = event.progress ? undefined : event.displayText;
  });

  if (isSuperseded && snapshot.status === "running") {
    snapshot.status = "done";
    snapshot.progress = getCompletedProgressSnapshot();
  }

  if (snapshot.status === "done") {
    snapshot.progress = getCompletedProgressSnapshot();
  }

  return snapshot;
}

function buildVisibleWorkflowSteps(
  events: NormalizedThreadEvent[],
  runtimeState: WorkflowRuntimeState,
  includeFirstStep: boolean,
): WorkflowStep[] {
  const stageEvents = dedupeNormalizedEvents(events).filter((event) => event.stage);
  if (stageEvents.length === 0) {
    return includeFirstStep ? [createWorkflowStepFromRuntime("dataset", runtimeState)] : [];
  }

  const groups: Array<{ stepId: WorkflowStepId; events: NormalizedThreadEvent[] }> = [];
  stageEvents.forEach((event) => {
    if (!event.stage) {
      return;
    }
    const stepId = stageStepMap[event.stage];
    const latestGroup = groups.at(-1);
    if (latestGroup?.stepId === stepId) {
      latestGroup.events.push(event);
      return;
    }
    groups.push({ stepId, events: [event] });
  });

  return groups.map((group, index) => {
    const definition = workflowStepDefinitions.find((step) => step.id === group.stepId) || workflowStepDefinitions[0];
    return {
      ...definition,
      renderKey: `${group.stepId}-${index}`,
      ...buildWorkflowStepRuntimeFromEvents(group.events, index < groups.length - 1),
    };
  });
}

function buildAnalysisRunSummary(events: NormalizedThreadEvent[]): AnalysisRunSummary | undefined {
  const runEvents = events.filter((item) => item.stage === "run");
  if (runEvents.length === 0) {
    return undefined;
  }

  const groupedByTask = new Map<string, NormalizedThreadEvent[]>();
  runEvents.forEach((event, index) => {
    const groupKey = event.taskId || `run-${index}`;
    const current = groupedByTask.get(groupKey) || [];
    current.push(event);
    groupedByTask.set(groupKey, current);
  });

  const latestRunEvents =
    Array.from(groupedByTask.values())
      .map((group) => group.sort(compareNormalizedThreadEvents))
      .sort((a, b) => compareNormalizedThreadEvents(a[a.length - 1], b[b.length - 1]))
      .at(-1) || [];

  if (latestRunEvents.length === 0) {
    return undefined;
  }

  let status: StepStatus = "running";
  let iterationCount: number | undefined;
  let converged: boolean | undefined;
  let crossStepNarrative: string | undefined;

  const timeline: AnalysisTimelineItem[] = [];
  const hypothesesMap = new Map<string, AnalysisHypothesisItem>();
  const agentsMap = new Map<
    string,
    {
      rounds?: number;
      toolCounts: Map<string, number>;
      verdict?: string;
      confidence?: number;
      hypothesisId?: string;
    }
  >();

  const appendTimeline = (key: string, title: string, detail: string, time?: string) => {
    if (!detail) {
      return;
    }
    const alreadyExists = timeline.some((item) => item.key === key);
    if (!alreadyExists) {
      timeline.push({ key, title, detail, time });
    }
  };

  latestRunEvents.forEach((event) => {
    const eventData = getEventPayloadData(event.payload);

    if (event.action === "start") {
      status = "running";
      appendTimeline("start", "启动分析", "系统已创建分析任务，开始生成调查方向。", event.timestamp);
    }

    if (event.action === "pause") {
      status = "paused";
    }

    if (event.action === "cancel") {
      status = "canceled";
      appendTimeline("cancel", "分析中断", "本轮分析已取消，未继续推进后续动作。", event.timestamp);
    }

    if (event.action === "finish") {
      status = "done";
      appendTimeline("finish", "生成报告", "问题归因已完成，分析报告可以展开查看。", event.timestamp);
    }

    if (event.type === "run.indexer.result") {
      const resultRecord =
        getNestedRecordField(eventData, ["result"]) || getStructuredRecordField(eventData, ["summary"]);
      const hypotheses = getStructuredArrayField(resultRecord, ["hypotheses"]) || [];
      hypotheses.forEach((item) => {
        if (!isRecord(item)) {
          return;
        }
        const id = getStringField(item, ["id"]) || `H${hypothesesMap.size + 1}`;
        const claim = getStringField(item, ["claim"]) || "待补充调查说明";
        const category = getStringField(item, ["category"]);
        const confidence = getNumberField(item, ["confidence"]);
        const investigationPaths =
          (getStructuredArrayField(item, ["investigation_paths"]) || [])
            .filter((path): path is string => typeof path === "string" && path.trim().length > 0)
            .map((path) => path.trim()) || [];

        hypothesesMap.set(id, {
          id,
          claim,
          category,
          confidence,
          investigationPaths,
        });
      });

      crossStepNarrative = getStringField(resultRecord, ["cross_step_narrative"]) || crossStepNarrative;
      appendTimeline(
        "indexer",
        "生成调查方向",
        hypotheses.length > 0
          ? `已整理出 ${hypotheses.length} 条优先调查项，进入子代理取证阶段。`
          : "已完成首轮扫描，正在准备调查项。",
        event.timestamp,
      );
    }

    if (event.type === "run.conductor.result") {
      const resultRecord =
        getNestedRecordField(eventData, ["result"]) || getStructuredRecordField(eventData, ["summary"]);
      const nextIteration = getNumberField(eventData, ["iteration"]) ?? getNumberField(resultRecord, ["iterations"]);
      if (typeof nextIteration === "number") {
        iterationCount = nextIteration;
      }
      if (resultRecord?.converged === true) {
        converged = true;
        appendTimeline(
          "conductor-final",
          "完成编排",
          typeof iterationCount === "number"
            ? `分析在第 ${iterationCount} 轮后收敛，等待输出最终报告。`
            : "分析已收敛，等待输出最终报告。",
          event.timestamp,
        );
      } else if (typeof nextIteration === "number") {
        appendTimeline(
          "conductor-iteration",
          "分配调查任务",
          `已完成第 ${nextIteration} 轮任务编排，持续派发子代理调查。`,
          event.timestamp,
        );
      }
    }

    if (event.type === "run.tool.used") {
      const agent = getStringField(eventData, ["agent"]);
      const tool = getStringField(eventData, ["tool"]) || "tool";
      if (!agent) {
        return;
      }
      const agentSummary =
        agentsMap.get(agent) ||
        {
          toolCounts: new Map<string, number>(),
        };
      agentSummary.toolCounts.set(tool, (agentSummary.toolCounts.get(tool) || 0) + 1);
      agentsMap.set(agent, agentSummary);
    }

    if (event.type === "run.researcher.result") {
      const agent = getStringField(eventData, ["agent"]);
      if (!agent) {
        return;
      }

      const agentSummary =
        agentsMap.get(agent) ||
        {
          toolCounts: new Map<string, number>(),
        };
      agentSummary.rounds = getNumberField(eventData, ["rounds"]) || agentSummary.rounds;

      const resultRecord = getStructuredRecordField(eventData, ["result_summary"]);
      const hypothesisId = getStringField(resultRecord, ["hypothesis_id"]);
      const verdict = getStringField(resultRecord, ["verdict"]);
      const confidence = getNumberField(resultRecord, ["confidence"]);
      const refinedClaim = getStringField(resultRecord, ["refined_claim"]);
      const suggestedAction = getStringField(resultRecord, ["suggested_action"]);

      agentSummary.verdict = verdict || agentSummary.verdict;
      agentSummary.confidence = confidence ?? agentSummary.confidence;
      agentSummary.hypothesisId = hypothesisId || agentSummary.hypothesisId;
      agentsMap.set(agent, agentSummary);

      if (hypothesisId) {
        const existingHypothesis = hypothesesMap.get(hypothesisId);
        const fallbackClaim = getStringField(resultRecord, ["refined_claim"]) || "已返回调查结论";
        hypothesesMap.set(hypothesisId, {
          id: hypothesisId,
          claim: existingHypothesis?.claim || fallbackClaim,
          category: existingHypothesis?.category,
          confidence: confidence ?? existingHypothesis?.confidence,
          investigationPaths: existingHypothesis?.investigationPaths || [],
          verdict: verdict || existingHypothesis?.verdict,
          refinedClaim: refinedClaim || existingHypothesis?.refinedClaim,
          suggestedAction: suggestedAction || existingHypothesis?.suggestedAction,
          agent,
        });
      }

      appendTimeline(
        "researcher-result",
        "回收调查结论",
        hypothesisId
          ? `${formatAnalysisAgentName(agent)} 已完成 ${hypothesisId} 的调查并返回结论。`
          : `${formatAnalysisAgentName(agent)} 已返回一条调查结论。`,
        event.timestamp,
      );
    }
  });

  const agents = Array.from(agentsMap.entries())
    .map(([agent, item]) => ({
      agent,
      rounds: item.rounds,
      toolCallCount: Array.from(item.toolCounts.values()).reduce((sum, count) => sum + count, 0),
      tools: Array.from(item.toolCounts.entries())
        .map(([name, count]) => ({ name, count }))
        .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name, "zh-CN")),
      verdict: item.verdict,
      confidence: item.confidence,
      hypothesisId: item.hypothesisId,
    }))
    .sort((a, b) => {
      if (Boolean(a.verdict) !== Boolean(b.verdict)) {
        return a.verdict ? -1 : 1;
      }
      return a.agent.localeCompare(b.agent, "zh-CN", { numeric: true });
    });

  const hypotheses = Array.from(hypothesesMap.values()).sort((a, b) =>
    a.id.localeCompare(b.id, "zh-CN", { numeric: true }),
  );

  return {
    status,
    hypothesisCount: hypotheses.length,
    agentCount: agents.length,
    completedAgentCount: agents.filter((item) => Boolean(item.verdict)).length,
    toolCallCount: agents.reduce((sum, item) => sum + item.toolCallCount, 0),
    iterationCount,
    converged,
    crossStepNarrative,
    hypotheses,
    agents,
    timeline: timeline.sort((a, b) => {
      if (a.time && b.time) {
        return new Date(a.time).getTime() - new Date(b.time).getTime();
      }
      return a.key.localeCompare(b.key, "zh-CN", { numeric: true });
    }),
  };
}

function buildApplyRunSummary(events: NormalizedThreadEvent[]): ApplyRunSummary | undefined {
  const applyEvents = events.filter((item) => item.stage === "apply");
  if (applyEvents.length === 0) {
    return undefined;
  }

  const groupedByTask = new Map<string, NormalizedThreadEvent[]>();
  applyEvents.forEach((event, index) => {
    const groupKey = event.taskId || `apply-${index}`;
    const current = groupedByTask.get(groupKey) || [];
    current.push(event);
    groupedByTask.set(groupKey, current);
  });

  const latestApplyEvents =
    Array.from(groupedByTask.values())
      .map((group) => group.sort(compareNormalizedThreadEvents))
      .sort((a, b) => compareNormalizedThreadEvents(a[a.length - 1], b[b.length - 1]))
      .at(-1) || [];

  if (latestApplyEvents.length === 0) {
    return undefined;
  }

  let status: StepStatus = "running";
  let roundCount: number | undefined;
  let testStatusText: string | undefined;
  let commitSha: string | undefined;
  const changedFiles = new Set<string>();
  const timeline: AnalysisTimelineItem[] = [];

  const appendTimeline = (key: string, title: string, detail: string, time?: string) => {
    if (!detail) {
      return;
    }
    const timelineKey = `${key}-${time || "no-time"}-${title}`;
    const alreadyExists = timeline.some((item) => item.key === timelineKey);
    if (!alreadyExists) {
      timeline.push({ key: timelineKey, title, detail, time });
    }
  };

  latestApplyEvents.forEach((event) => {
    const eventData = getEventPayloadData(event.payload);

    if (event.action === "start") {
      status = "running";
      appendTimeline("apply-start", "启动优化", "系统已根据分析结论开始生成候选改动。", event.timestamp);
    }

    if (event.type === "apply.round.diff") {
      const round = getNumberField(eventData, ["round"]);
      if (typeof round === "number") {
        roundCount = round;
      }
      const files = (getStructuredArrayField(eventData, ["files_changed"]) || []).filter(
        (item): item is string => typeof item === "string" && item.trim().length > 0,
      );
      files.forEach((file) => changedFiles.add(file));

      const diffSummary = getStringField(eventData, ["diff_summary"]);
      if (diffSummary?.includes("tests passed")) {
        testStatusText = "测试已通过";
      } else if (diffSummary?.includes("tests not run")) {
        testStatusText = "尚未执行测试";
      } else if (diffSummary?.includes("tests failed")) {
        testStatusText = "测试未通过";
      }

      commitSha = getStringField(eventData, ["commit_sha"]) || commitSha;
      appendTimeline(
        typeof round === "number" ? `apply-diff-round-${round}` : `apply-diff-${event.key}`,
        "生成候选改动",
        typeof round === "number"
          ? `已完成第 ${round} 轮改动草案，当前涉及 ${files.length} 个文件。`
          : `已生成一轮改动草案，当前涉及 ${files.length} 个文件。`,
        event.timestamp,
      );
    }

    if (event.action === "finish") {
      status = "done";
      appendTimeline("apply-finish", "完成候选版本", "候选优化版本已准备完成，可继续查看代码差异。", event.timestamp);
    }

    if (event.action === "cancel") {
      status = "canceled";
    }
  });

  return {
    status,
    roundCount,
    changedFileCount: changedFiles.size,
    changedFiles: Array.from(changedFiles).sort((a, b) => a.localeCompare(b, "zh-CN", { numeric: true })),
    testStatusText,
    commitSha,
    timeline: timeline.sort((a, b) => {
      if (a.time && b.time) {
        return new Date(a.time).getTime() - new Date(b.time).getTime();
      }
      return a.key.localeCompare(b.key, "zh-CN", { numeric: true });
    }),
  };
}

function getPendingCheckpointWaitPrompt(events: NormalizedThreadEvent[]) {
  const latestCheckpointEvent = events
    .filter((event) => event.type === "checkpoint.wait" && event.checkpointWait)
    .sort(compareNormalizedThreadEvents)
    .at(-1);

  if (!latestCheckpointEvent?.checkpointWait) {
    return undefined;
  }

  const nextStage = latestCheckpointEvent.checkpointWait.nextStage;
  const hasContinued =
    Boolean(nextStage) &&
    events.some((event) => {
      if (event.stage !== nextStage) {
        return false;
      }
      if (
        typeof latestCheckpointEvent.sequence === "number" &&
        typeof event.sequence === "number"
      ) {
        return event.sequence > latestCheckpointEvent.sequence;
      }
      if (latestCheckpointEvent.timestamp && event.timestamp) {
        const checkpointTime = new Date(latestCheckpointEvent.timestamp).getTime();
        const eventTime = new Date(event.timestamp).getTime();
        return !Number.isNaN(checkpointTime) && !Number.isNaN(eventTime) && eventTime > checkpointTime;
      }
      return compareNormalizedThreadEvents(latestCheckpointEvent, event) < 0;
    });

  return hasContinued ? undefined : latestCheckpointEvent.checkpointWait;
}

function reduceWorkflowRuntimeState(
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
  if (action === "finish") {
    current.status = "done";
    current.progress = event.progress || getCompletedProgressSnapshot();
  } else if (action === "cancel") {
    current.status = "canceled";
  } else if (action === "pause") {
    current.status = "paused";
    current.progress =
      event.progress ||
      updateProgressStatusText(current.progress, getRuntimeProgressStatusLabel(action));
  } else {
    current.status = "running";
    current.progress =
      event.progress ||
      updateProgressStatusText(current.progress, getRuntimeProgressStatusLabel(action));
  }
  current.runtimeText = event.progress ? undefined : event.displayText;
  return next;
}

function getThreadTitleFromPayload(payload: ThreadRestorePayload) {
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

function getThreadKnowledgeBaseId(payload: ThreadRestorePayload) {
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

function getThreadPayloadFromRestorePayload(payload: ThreadRestorePayload) {
  if (!isRecord(payload)) {
    return undefined;
  }

  const threadRecord = getNestedRecordField(payload, ["thread"]);
  return (
    getNestedRecordField(threadRecord, ["thread_payload", "threadPayload", "payload"]) ||
    getNestedRecordField(payload, ["thread_payload", "threadPayload", "payload"])
  );
}

function getThreadModeFromPayload(payload: ThreadRestorePayload): EvolutionMode | undefined {
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

export default function SelfEvolutionPage() {
  const { t, i18n } = useTranslation();
  const workflowImageSrc = getSelfEvolutionWorkflowImageSrc(i18n.resolvedLanguage || i18n.language);
  const navigate = useNavigate();
  const { threadId: routeThreadId } = useParams<{ threadId?: string }>();
  const [mode, setMode] = useState<EvolutionMode>("interactive");
  const [selectedEvalSet, setSelectedEvalSet] = useState<string>(FIXED_EVAL_SET);
  const [extraEvalStrategy, setExtraEvalStrategy] = useState<ExtraEvalStrategy>(FIXED_EXTRA_EVAL_STRATEGY);
  const [selectedKb, setSelectedKb] = useState<string>();
  const [knowledgeBaseOptions, setKnowledgeBaseOptions] = useState<KnowledgeBaseOption[]>([]);
  const [isKnowledgeBaseLoading, setIsKnowledgeBaseLoading] = useState(true);
  const [knowledgeBaseError, setKnowledgeBaseError] = useState("");
  const [hasLaunchValidationTriggered, setHasLaunchValidationTriggered] = useState(false);
  const [prompt, setPrompt] = useState("");
  const [isWorkbenchVisible, setIsWorkbenchVisible] = useState(false);
  const [isStartingSession, setIsStartingSession] = useState(false);
  const [isSendingMessage, setIsSendingMessage] = useState(false);
  const [isRestoringThread, setIsRestoringThread] = useState(false);
  const [isHistorySessionModalOpen, setIsHistorySessionModalOpen] = useState(false);
  const [isLoadingThreadHistoryList, setIsLoadingThreadHistoryList] = useState(false);
  const [deletingHistoryKeys, setDeletingHistoryKeys] = useState<string[]>([]);
  const [threadHistoryListError, setThreadHistoryListError] = useState("");
  const [threadRestoreError, setThreadRestoreError] = useState("");
  const [isNewSessionConfigOpen, setIsNewSessionConfigOpen] = useState(false);
  const [hasNewSessionValidationTriggered, setHasNewSessionValidationTriggered] = useState(false);
  const [newSessionDraft, setNewSessionDraft] = useState<NewSessionDraft>({});
  const [workflowRuntimeState, setWorkflowRuntimeState] = useState<WorkflowRuntimeState>(
    createInitialWorkflowRuntimeState,
  );
  const [workflowResults, setWorkflowResults] = useState<WorkflowResultsState>(
    createInitialWorkflowResultsState,
  );
  const [liveCheckpointWaitPrompt, setLiveCheckpointWaitPrompt] = useState<CheckpointWaitPrompt>();
  const [diffArtifactContent, setDiffArtifactContent] = useState<DiffArtifactContentState>({
    loading: false,
    key: "",
    content: "",
  });
  const [threadEvents, setThreadEvents] = useState<NormalizedThreadEvent[]>([]);
  const threadEventsRef = useRef<NormalizedThreadEvent[]>([]);
  const [remoteThreadHistory, setRemoteThreadHistory] = useState<ThreadHistoryEntry[]>([]);
  const [chatSessions, setChatSessions] = useState<ChatSession[]>([
    {
      id: "session-1",
      title: "当前会话",
      updatedAt: "刚刚",
      messages: [],
    },
  ]);
  const [activeSessionId, setActiveSessionId] = useState("session-1");
  const chatStreamRef = useRef<HTMLDivElement | null>(null);
  const threadEventsAbortRef = useRef<{ threadId: string; controller: AbortController } | null>(null);
  const processedThreadEventIdsRef = useRef<Set<string>>(new Set());
  const processedWorkflowEventKeysRef = useRef<Set<string>>(new Set());
  const restoreRequestIdRef = useRef(0);
  const [activeDiffFileId, setActiveDiffFileId] = useState("");
  const [collapsedDiffDirs, setCollapsedDiffDirs] = useState<Record<string, boolean>>({});

  useEffect(() => {
    window.localStorage.removeItem(DEPRECATED_SELF_EVOLUTION_THREAD_HISTORY_STORAGE_KEY);
  }, []);

  const fetchKnowledgeBaseOptions = useCallback((signal?: AbortSignal) => {
    setIsKnowledgeBaseLoading(true);
    setKnowledgeBaseError("");

    KnowledgeBaseServiceApi()
      .datasetServiceListDatasets({ pageSize: 1000 }, { signal })
      .then((res) => {
        const nextOptions = (res.data.datasets || [])
          .filter((dataset): dataset is Dataset => Boolean(dataset.dataset_id))
          .map((dataset) => ({
            label: getKnowledgeBaseName(dataset),
            value: dataset.dataset_id,
          }));

        setKnowledgeBaseOptions(nextOptions);
        setSelectedKb((prev) =>
          prev && nextOptions.some((item) => item.value === prev) ? prev : undefined,
        );
        setNewSessionDraft((prev) =>
          prev.selectedKb && !nextOptions.some((item) => item.value === prev.selectedKb)
            ? { ...prev, selectedKb: undefined }
            : prev,
        );
      })
      .catch((error) => {
        if (isCanceledRequest(error)) {
          return;
        }

        setKnowledgeBaseError(
          getLocalizedErrorMessage(error, "知识库列表加载失败，请稍后重试。") ||
            "知识库列表加载失败，请稍后重试。",
        );
      })
      .finally(() => {
        if (!signal?.aborted) {
          setIsKnowledgeBaseLoading(false);
        }
      });
  }, []);
  const selectedKnowledgeBaseLabel = knowledgeBaseOptions.find((item) => item.value === selectedKb)?.label;
  const knowledgeBasePlaceholder = knowledgeBaseError
    ? "知识库加载失败"
    : isKnowledgeBaseLoading
      ? "正在加载知识库"
      : knowledgeBaseOptions.length === 0
        ? "暂无可用知识库"
        : "知识库";
  const selectedKnowledgeBase = selectedKnowledgeBaseLabel || knowledgeBasePlaceholder;
  const knowledgeBaseLaunchLabel =
    selectedKnowledgeBaseLabel ||
    (knowledgeBaseError || isKnowledgeBaseLoading || knowledgeBaseOptions.length === 0
      ? knowledgeBasePlaceholder
      : "尚未选择知识库");
  const getExistingEvalSetLabel = useCallback(
    (value?: string) => {
      const option = existingEvalSetOptions.find((item) => item.value === value);
      if (option?.value === FIXED_EVAL_SET) {
        return t("selfEvolutionRun.noExistingEvalSet");
      }
      return option?.label || t("selfEvolutionRun.noExistingEvalSet");
    },
    [t],
  );
  const selectedEvalSetLabel = getExistingEvalSetLabel(selectedEvalSet);
  const isExtraEvalRequired = selectedEvalSet === "__none__";
  const extraEvalLabel = extraEvalStrategy === "generate" ? "是，补充评测集" : "否，不补充";
  const interventionLabel = mode === "interactive" ? "是，人工干预" : "否，自动处理";
  const modeLabel = mode === "auto" ? "自动处理" : "交互处理";
  const isKnowledgeBaseRequired = !selectedKb;
  const isLaunchConfigComplete = Boolean(selectedKb && selectedEvalSet && extraEvalStrategy && mode);
  const isLaunchConfigValid =
    isLaunchConfigComplete && (!isExtraEvalRequired || extraEvalStrategy === "generate");
  const isSendDisabled = !prompt.trim() || isSendingMessage;
  const draftSelectedKnowledgeBaseLabel = knowledgeBaseOptions.find(
    (item) => item.value === newSessionDraft.selectedKb,
  )?.label;
  const draftKnowledgeBaseLaunchLabel =
    draftSelectedKnowledgeBaseLabel ||
    (knowledgeBaseError || isKnowledgeBaseLoading || knowledgeBaseOptions.length === 0
      ? knowledgeBasePlaceholder
      : "请选择知识库");
  const draftSelectedEvalSetLabel = newSessionDraft.selectedEvalSet
    ? getExistingEvalSetLabel(newSessionDraft.selectedEvalSet)
    : undefined;
  const draftEvalSetLabel = draftSelectedEvalSetLabel || "请选择评测集";
  const isDraftExtraEvalRequired = newSessionDraft.selectedEvalSet === "__none__";
  const draftExtraEvalLabel =
    newSessionDraft.extraEvalStrategy === "generate"
      ? "是，补充评测集"
      : newSessionDraft.extraEvalStrategy === "skip"
        ? "否，不补充"
        : "请选择补充策略";
  const draftInterventionLabel =
    newSessionDraft.mode === "interactive"
      ? "是，人工干预"
      : newSessionDraft.mode === "auto"
        ? "否，自动处理"
        : "请选择干预方式";
  const isNewSessionDraftComplete = Boolean(
    newSessionDraft.selectedKb &&
      newSessionDraft.selectedEvalSet &&
      newSessionDraft.extraEvalStrategy &&
      newSessionDraft.mode,
  );
  const isNewSessionDraftValid =
    isNewSessionDraftComplete &&
    (!isDraftExtraEvalRequired || newSessionDraft.extraEvalStrategy === "generate");
  const isNewSessionStepOneDone = Boolean(newSessionDraft.selectedKb);
  const isNewSessionStepTwoDone = Boolean(newSessionDraft.selectedEvalSet);
  const isNewSessionStepThreeDone = Boolean(newSessionDraft.extraEvalStrategy);
  const isNewSessionStepFourDone = Boolean(newSessionDraft.mode);
  const workflowSteps = useMemo<WorkflowStep[]>(
    () => buildVisibleWorkflowSteps(threadEvents, workflowRuntimeState, isWorkbenchVisible),
    [isWorkbenchVisible, threadEvents, workflowRuntimeState],
  );
  const analysisRunSummary = useMemo(() => buildAnalysisRunSummary(threadEvents), [threadEvents]);
  const applyRunSummary = useMemo(() => buildApplyRunSummary(threadEvents), [threadEvents]);
  const pendingCheckpointWaitPrompt = useMemo(
    () => liveCheckpointWaitPrompt || getPendingCheckpointWaitPrompt(threadEvents),
    [liveCheckpointWaitPrompt, threadEvents],
  );
  const activeStepText = useMemo(() => {
    const activeStep =
      workflowSteps.find((item) => item.status === "running") ||
      workflowSteps.find((item) => item.status === "paused") ||
      workflowSteps.find((item) => item.status === "pending");
    return activeStep?.title || "流程已完成";
  }, [workflowSteps]);
  const datasetDownloadFileName = useMemo(() => {
    const normalizedEvalName =
      evalSetPreviewData.eval_name.replace(/[\\/:*?"<>|]+/g, "_").trim() || "eval-dataset";
    return `${normalizedEvalName}-${evalSetPreviewData.eval_set_id}.json`;
  }, []);
  const datasetDownloadUrl = useMemo(() => {
    if (typeof window === "undefined") {
      return "";
    }
    const datasetBlob = new Blob([JSON.stringify(evalSetPreviewData, null, 2)], {
      type: "application/json;charset=utf-8",
    });
    return URL.createObjectURL(datasetBlob);
  }, []);
  const fetchedPxCategoryMetricAverages = useMemo<PxCategoryMetricAverage[]>(
    () => buildPxCategoryMetricAveragesFromReport(workflowResults["eval-reports"].data),
    [workflowResults["eval-reports"].data],
  );
  const pxReportCategoryMetrics = fetchedPxCategoryMetricAverages;
  const isSinglePxCategory = pxReportCategoryMetrics.length === 1;
  const pxReportTotalCases = useMemo(() => {
    const sourceRecord = Array.isArray(workflowResults["eval-reports"].data)
      ? (workflowResults["eval-reports"].data.find((item): item is Record<string, unknown> => isRecord(item)) ??
        undefined)
      : isRecord(workflowResults["eval-reports"].data)
        ? workflowResults["eval-reports"].data
        : undefined;
    const caseDetailSummary =
      getStructuredRecordField(sourceRecord, ["case_details_summary"]) ||
      getNestedRecordField(sourceRecord, ["case_details_summary"]);

    return (
      getNumberField(caseDetailSummary, ["total_count"]) ||
      getNumberField(sourceRecord, ["total_cases"]) ||
      pxReportCategoryMetrics.reduce((total, item) => total + item.caseCount, 0)
    );
  }, [pxReportCategoryMetrics, workflowResults]);
  const abSummaryReports = useMemo<AbSummaryReport[]>(
    () => buildAbSummaryReports(workflowResults.abtests.data),
    [workflowResults.abtests.data],
  );
  const abCategoryComparisons = useMemo<AbCategoryComparison[]>(
    () => buildAbCategoryComparisons(abSummaryReports),
    [abSummaryReports],
  );
  const isSingleAbCategory = abCategoryComparisons.length <= 1;
  const abComparisonRows = useMemo<AbComparisonRow[]>(
    () =>
      abCategoryComparisons.map((item) => ({
        key: item.category,
        category: item.category,
        baselineSummary: formatMetricSummary(item.baseline),
        experimentSummary: formatMetricSummary(item.experiment),
        deltaSummary: [
          `正确性 ${formatMetricDelta(item.delta.answer_correctness)}`,
          `忠实性 ${formatMetricDelta(item.delta.faithfulness)}`,
          `上下文召回 ${formatMetricDelta(item.delta.context_recall)}`,
          `文档召回 ${formatMetricDelta(item.delta.doc_recall)}`,
        ].join(" / "),
      })),
    [abCategoryComparisons],
  );
  const abComparisonColumns = useMemo<ColumnsType<AbComparisonRow>>(
    () => [
      { title: "评测分类", dataIndex: "category", key: "category", width: 140 },
      {
        title: "基线结果",
        dataIndex: "baselineSummary",
        key: "baselineSummary",
        width: 320,
        render: (value: string) => (
          <span className="self-evolution-table-ellipsis" title={value}>
            {value}
          </span>
        ),
      },
      {
        title: "优化结果",
        dataIndex: "experimentSummary",
        key: "experimentSummary",
        width: 320,
        render: (value: string) => (
          <span className="self-evolution-table-ellipsis" title={value}>
            {value}
          </span>
        ),
      },
      {
        title: "变化摘要",
        dataIndex: "deltaSummary",
        key: "deltaSummary",
        width: 320,
        render: (value: string) => (
          <span className="self-evolution-table-ellipsis" title={value}>
            {value}
          </span>
        ),
      },
    ],
    [],
  );
  const abComparisonDownloadUrl = useMemo(() => {
    if (typeof window === "undefined") {
      return "";
    }
    const abBlob = new Blob([JSON.stringify(abCategoryComparisons, null, 2)], {
      type: "application/json;charset=utf-8",
    });
    return URL.createObjectURL(abBlob);
  }, [abCategoryComparisons]);
  const directFetchedDiffText = useMemo(
    () => getResultStringField(workflowResults.diffs.data, ["diff", "patch", "content", "text"]),
    [workflowResults.diffs.data],
  );
  const diffArtifactFiles = useMemo(
    () => getDiffArtifactFiles(workflowResults.diffs.data),
    [workflowResults.diffs.data],
  );
  const diffArtifactKey = useMemo(
    () => diffArtifactFiles.map((file) => `${file.path}:${file.diffPath}`).join("|"),
    [diffArtifactFiles],
  );
  const fetchedDiffText = directFetchedDiffText || diffArtifactContent.content;
  const fetchedAnalysisReportMarkdown = useMemo(
    () =>
      getResultStringField(workflowResults["analysis-reports"].data, [
        "markdown",
        "report",
        "content",
        "text",
        "summary",
      ]),
    [workflowResults],
  );
  const parsedDiffFiles = useMemo(
    () => parseUnifiedDiff(fetchedDiffText),
    [fetchedDiffText],
  );
  const diffFileTree = useMemo(() => buildDiffFileTree(parsedDiffFiles), [parsedDiffFiles]);
  const activeDiffFile = parsedDiffFiles.find((item) => item.id === activeDiffFileId) || parsedDiffFiles[0];
  const activeSession = chatSessions.find((item) => item.id === activeSessionId) || chatSessions[0];
  const activeMessages = activeSession?.messages ?? [];
  const activeThreadId = activeSession?.threadId || routeThreadId;
  const isAutoInteractionActive = mode === "auto" && Boolean(activeThreadId);
  const autoInteractionMessages = useMemo(
    () => buildAutoInteractionMessagesFromEvents(threadEvents),
    [threadEvents],
  );
  const displayedMessages = isAutoInteractionActive ? autoInteractionMessages : activeMessages;
  const displayedCheckpointWaitPrompt =
    isAutoInteractionActive || isTerminalAbtestCheckpoint(pendingCheckpointWaitPrompt)
      ? undefined
      : pendingCheckpointWaitPrompt;
  const datasetResultDownloadUrl = useMemo(
    () => buildCoreDownloadUrl(getResultDownloadPath(workflowResults.datasets.data)),
    [workflowResults.datasets.data],
  );
  const evalReportDownloadUrl = useMemo(
    () => buildCoreDownloadUrl(getResultDownloadPath(workflowResults["eval-reports"].data)),
    [workflowResults["eval-reports"].data],
  );
  const diffResultDownloadUrl = useMemo(() => {
    if (typeof window === "undefined" || !fetchedDiffText) {
      return "";
    }
    const diffBlob = new Blob([fetchedDiffText], {
      type: "text/x-diff;charset=utf-8",
    });
    return URL.createObjectURL(diffBlob);
  }, [fetchedDiffText]);
  const abtestResultDownloadUrl = useMemo(
    () => buildCoreDownloadUrl(getResultDownloadPath(workflowResults.abtests.data)),
    [workflowResults.abtests.data],
  );
  const fetchDiffDownloadText = useCallback(
    async (resultData: unknown, signal?: AbortSignal) => {
      const directDiffText = getResultStringField(resultData, ["diff", "patch", "content", "text"]);
      if (directDiffText) {
        return directDiffText;
      }

      const diffFiles = getDiffArtifactFiles(resultData);
      if (diffFiles.length === 0) {
        return "";
      }

      const contents = await Promise.all(
        diffFiles.map(async (file) => {
          const response = await axiosInstance.post(
            `${AGENT_API_BASE}/files:content`,
            { path: file.diffPath },
            { signal },
          );
          const responseData = response.data;
          const content =
            typeof responseData === "string"
              ? responseData
              : getResultStringField(responseData, ["content", "diff", "patch", "text"]) ||
                stringifyResultPayload(responseData);
          return normalizeFetchedDiffArtifact(file, content);
        }),
      );

      return contents.filter(Boolean).join("\n\n");
    },
    [],
  );

  useEffect(() => {
    if (directFetchedDiffText) {
      setDiffArtifactContent({ loading: false, key: "", content: "" });
      return;
    }

    if (diffArtifactFiles.length === 0) {
      setDiffArtifactContent({ loading: false, key: "", content: "" });
      return;
    }

    const controller = new AbortController();
    setDiffArtifactContent((prev) => ({
      loading: true,
      key: diffArtifactKey,
      content: prev.key === diffArtifactKey ? prev.content : "",
      error: undefined,
    }));

    fetchDiffDownloadText(workflowResults.diffs.data, controller.signal)
      .then((content) => {
        if (controller.signal.aborted) {
          return;
        }

        setDiffArtifactContent({
          loading: false,
          key: diffArtifactKey,
          content,
        });
      })
      .catch((error) => {
        if (isCanceledRequest(error) || controller.signal.aborted) {
          return;
        }

        setDiffArtifactContent({
          loading: false,
          key: diffArtifactKey,
          content: "",
          error: getLocalizedErrorMessage(error, "代码文件内容加载失败，请稍后重试。"),
        });
      });

    return () => {
      controller.abort();
    };
  }, [diffArtifactFiles, diffArtifactKey, directFetchedDiffText, fetchDiffDownloadText, workflowResults.diffs.data]);

  const historySessionEntries = useMemo<HistorySessionEntry[]>(() => {
    const sessionEntries = chatSessions
      .filter((session) => session.id !== activeSessionId)
      .map<HistorySessionEntry>((session) => ({
        key: session.threadId || session.id,
        sessionId: session.id,
        threadId: session.threadId,
        title: session.title,
        updatedAt: session.updatedAt,
        messageCount: session.messages.length,
        source: session.threadId ? "thread" : "local",
      }));
    const mergedEntries = [
      ...sessionEntries,
      ...remoteThreadHistory
        .filter((item) => item.threadId !== activeThreadId)
        .filter((item) => !sessionEntries.some((session) => session.threadId === item.threadId))
        .map<HistorySessionEntry>((item) => ({
          key: item.threadId,
          sessionId: undefined,
          threadId: item.threadId,
          title: item.title,
          updatedAt: item.updatedAt,
          messageCount: undefined,
          status: item.status,
          source: "thread" as const,
        })),
    ];

    return mergedEntries.sort((a, b) =>
      b.updatedAt.localeCompare(a.updatedAt, "zh-CN", { numeric: true }),
    );
  }, [activeSessionId, activeThreadId, chatSessions, remoteThreadHistory]);
  const isRuntimeConfigLocked = isWorkbenchVisible || Boolean(activeSession?.threadId);
  const getWorkflowResultUrl = useCallback(
    (kind: WorkflowResultKind) => {
      if (!activeThreadId) {
        return "";
      }
      return `${AGENT_API_BASE}/threads/${encodeURIComponent(activeThreadId)}/results/${kind}`;
    },
    [activeThreadId],
  );
  const fetchWorkflowResult = useCallback(
    async (kind: WorkflowResultKind, options?: { force?: boolean }) => {
      if (!activeThreadId) {
        message.warning("当前没有可用线程 ID，无法请求结果接口。", 2);
        return undefined;
      }

      const currentState = workflowResults[kind];
      if (!options?.force && (currentState.loading || currentState.loaded)) {
        return currentState.data;
      }

      setWorkflowResults((prev) => ({
        ...prev,
        [kind]: { ...prev[kind], loading: true, error: undefined },
      }));

      try {
        const response = await axiosInstance.get(getWorkflowResultUrl(kind));
        setWorkflowResults((prev) => ({
          ...prev,
          [kind]: {
            loading: false,
            loaded: true,
            data: response.data,
          },
        }));
        return response.data;
      } catch (error) {
        setWorkflowResults((prev) => ({
          ...prev,
          [kind]: {
            ...prev[kind],
            loading: false,
            loaded: true,
            error: getLocalizedErrorMessage(error, `${workflowResultLabels[kind]}加载失败，请稍后重试。`),
          },
        }));
        return undefined;
      }
    },
    [activeThreadId, getWorkflowResultUrl, workflowResults],
  );
  const handleWorkflowDownload = useCallback(
    async (
      kind: WorkflowResultKind,
      fallbackUrl: string,
      fallbackFileName: string,
      event?: MouseEvent<HTMLElement>,
    ) => {
      event?.preventDefault();
      event?.stopPropagation();

      const currentData = workflowResults[kind].data;
      const nextData = currentData ?? (await fetchWorkflowResult(kind));
      let downloadUrl = buildCoreDownloadUrl(getResultDownloadPath(nextData)) || fallbackUrl;
      let temporaryDownloadUrl = "";

      if (kind === "diffs" && !downloadUrl) {
        const diffText = await fetchDiffDownloadText(nextData);
        if (diffText && typeof window !== "undefined") {
          temporaryDownloadUrl = URL.createObjectURL(
            new Blob([diffText], {
              type: "text/x-diff;charset=utf-8",
            }),
          );
          downloadUrl = temporaryDownloadUrl;
        }
      }

      if (!downloadUrl) {
        message.warning(`${workflowResultLabels[kind]}暂无可下载文件。`, 1.5);
        return;
      }

      triggerBrowserDownload(downloadUrl, getDownloadFileName(downloadUrl, fallbackFileName));

      if (temporaryDownloadUrl) {
        window.setTimeout(() => {
          URL.revokeObjectURL(temporaryDownloadUrl);
        }, 0);
      }
    },
    [fetchDiffDownloadText, fetchWorkflowResult, workflowResults],
  );
  const handleWorkflowResultCollapseChange = useCallback(
    (kind: WorkflowResultKind) => (activeKeys: string | string[]) => {
      const isOpen = Array.isArray(activeKeys) ? activeKeys.length > 0 : Boolean(activeKeys);
      if (isOpen) {
        void fetchWorkflowResult(kind);
      }
    },
    [fetchWorkflowResult],
  );

  useEffect(() => {
    const controller = new AbortController();
    fetchKnowledgeBaseOptions(controller.signal);

    return () => {
      controller.abort();
    };
  }, [fetchKnowledgeBaseOptions]);

  useEffect(() => {
    setWorkflowResults(createInitialWorkflowResultsState());
  }, [activeThreadId]);

  useEffect(() => {
    const chatStream = chatStreamRef.current;
    if (!chatStream) {
      return;
    }
    chatStream.scrollTo({
      top: chatStream.scrollHeight,
      behavior: "auto",
    });
  }, [activeSessionId, displayedMessages.length]);

  useEffect(
    () => () => {
      if (datasetDownloadUrl) {
        URL.revokeObjectURL(datasetDownloadUrl);
      }
    },
    [datasetDownloadUrl],
  );

  useEffect(
    () => () => {
      threadEventsAbortRef.current?.controller.abort();
      threadEventsAbortRef.current = null;
    },
    [],
  );

  const knowledgeBaseMenuItems = useMemo<MenuProps["items"]>(() => {
    if (isKnowledgeBaseLoading) {
      return [
        {
          key: "__loading__",
          label: "正在加载知识库...",
          disabled: true,
          icon: <LoadingOutlined spin />,
        },
      ];
    }

    if (knowledgeBaseError) {
      return [
        {
          key: "__retry__",
          label: `${knowledgeBaseError} 点击重试`,
          icon: <ReloadOutlined />,
        },
      ];
    }

    if (knowledgeBaseOptions.length === 0) {
      return [
        {
          key: "__empty__",
          label: "暂无可用知识库",
          disabled: true,
        },
      ];
    }

    return knowledgeBaseOptions.map((item) => ({
      key: item.value,
      label: (
        <span className="self-evolution-knowledge-option" title={item.label}>
          {item.label}
        </span>
      ),
    }));
  }, [isKnowledgeBaseLoading, knowledgeBaseError, knowledgeBaseOptions]);

  const onKnowledgeBaseMenuClick = (
    key: string,
    onSelect: (nextKnowledgeBase: string) => void,
  ) => {
    if (key === "__retry__") {
      fetchKnowledgeBaseOptions();
      return;
    }
    if (key.startsWith("__")) {
      return;
    }

    onSelect(key);
  };

  const modeMenuItems: MenuProps["items"] = [
    { key: "auto", label: "自动处理" },
    { key: "interactive", label: "交互处理" },
  ];

  const existingEvalSetMenuItems: MenuProps["items"] = [
    ...existingEvalSetOptions.map((item) => ({
      key: item.value,
      label: getExistingEvalSetLabel(item.value),
    })),
  ];
  const extraEvalStrategyMenuItems: MenuProps["items"] = [
    { key: FIXED_EXTRA_EVAL_STRATEGY, label: "是，借助大模型补充生成" },
  ];
  const newSessionExtraEvalStrategyMenuItems: MenuProps["items"] = [
    { key: FIXED_EXTRA_EVAL_STRATEGY, label: "是，借助大模型补充生成" },
  ];

  const buildSessionIntroContent = (
    targetKnowledgeBase: string,
    targetEvalSetLabel: string,
    targetExtraEvalLabel: string,
    targetInterventionLabel: string,
  ) =>
    `已启动流程：知识库「${targetKnowledgeBase}」、评测集「${targetEvalSetLabel}」、补充评测集「${targetExtraEvalLabel}」、干预模式「${targetInterventionLabel}」。当前进入第一步：生成数据集。`;

  const extractThreadId = (response: AgentThreadCreateResponse) =>
    response.data?.upstream?.id ||
    response.data?.upstream?.thread_id ||
    response.data?.thread?.thread_id ||
    response.data?.thread?.id;

  const createAndStartThread = async () => {
    const evalName =
      selectedEvalSet && selectedEvalSet !== FIXED_EVAL_SET
        ? selectedEvalSet
        : `eval_${new Date().toISOString().replace(/[-:.TZ]/g, "").slice(0, 14)}`;

    const createResponse = await axiosInstance.post<AgentThreadCreateResponse>(`${AGENT_API_BASE}/threads`, {
      mode,
      title: selectedKnowledgeBaseLabel || selectedKnowledgeBase || "self evolution test",
      inputs: {
        kb_id: selectedKb,
        algo_id: "general_algo",
        eval_name: evalName,
        num_cases: DEFAULT_EVAL_CASE_COUNT,
        target_chat_url: "http://evo-chat:8046/api/chat",
        dataset_name: "algo",
      },
    });
    const threadId = extractThreadId(createResponse.data);
    if (!threadId) {
      throw new Error("创建 thread 成功但响应中缺少 thread_id");
    }

    await axiosInstance.post(`${AGENT_API_BASE}/threads/${encodeURIComponent(threadId)}:start`, {});
    return threadId;
  };

  const appendMessageToSession = (
    sessionId: string,
    nextMessage: ChatMessage,
    options?: { title?: string; dedupeLast?: boolean },
  ) => {
    setChatSessions((prev) =>
      prev.map((session) => {
        if (session.id !== sessionId) {
          return session;
        }

        const lastMessage = session.messages[session.messages.length - 1];
        if (
          options?.dedupeLast &&
          lastMessage?.role === nextMessage.role &&
          lastMessage.content === nextMessage.content
        ) {
          return {
            ...session,
            updatedAt: nextMessage.time,
          };
        }

        return {
          ...session,
          title: options?.title || session.title,
          updatedAt: nextMessage.time,
          messages: [...session.messages, nextMessage],
        };
      }),
    );
  };

  const appendSystemMessage = (content: string, sessionId = activeSessionId) => {
    const nowLabel = getTimeLabel();
    appendMessageToSession(sessionId, {
      id: `assistant-${Date.now()}-${Math.random().toString(16).slice(2)}`,
      role: "assistant",
      content,
      time: nowLabel,
    });
  };

  const replaceThreadEvents = (events: NormalizedThreadEvent[]) => {
    threadEventsRef.current = events;
    setLiveCheckpointWaitPrompt(undefined);
    setThreadEvents(events);
  };

  const mergeThreadEvents = (events: NormalizedThreadEvent[]) => {
    const mergedEvents = dedupeNormalizedEvents([...threadEventsRef.current, ...events]);
    threadEventsRef.current = mergedEvents;
    setThreadEvents(mergedEvents);
    return mergedEvents;
  };

  const appendStreamDeltaToSession = (
    sessionId: string,
    kind: ChatStreamDeltaKind,
    delta: string | undefined,
    streamId = "default",
  ) => {
    if (!delta) {
      return;
    }

    const nowLabel = getTimeLabel();
    const streamMessageId = `${sessionId}-${kind}-stream-${streamId}`;
    const initialContent = kind === "thinking" ? `思考过程：${delta}` : delta;

    setChatSessions((prev) =>
      prev.map((session) => {
        if (session.id !== sessionId) {
          return session;
        }

        const existingIndex = session.messages.findIndex((item) => item.id === streamMessageId);
        if (existingIndex >= 0) {
          const messages = [...session.messages];
          const current = messages[existingIndex];
          messages[existingIndex] = {
            ...current,
            content: `${current.content}${delta}`,
            time: nowLabel,
          };
          return {
            ...session,
            updatedAt: nowLabel,
            messages,
          };
        }

        return {
          ...session,
          updatedAt: nowLabel,
          messages: [
            ...session.messages,
            {
              id: streamMessageId,
              role: "assistant",
              content: initialContent,
              time: nowLabel,
            },
          ],
        };
      }),
    );
  };

  const applyWorkflowEvent = (event: NormalizedThreadEvent, sessionId = activeSessionId) => {
    const isNewEvent = !processedWorkflowEventKeysRef.current.has(event.key);
    if (!isNewEvent) {
      return;
    }

    processedWorkflowEventKeysRef.current.add(event.key);
    mergeThreadEvents([event]);
    if (event.checkpointWait) {
      setLiveCheckpointWaitPrompt(event.checkpointWait);
    } else {
      setLiveCheckpointWaitPrompt((prev) => {
        if (!prev) {
          return prev;
        }
        if (event.type === "checkpoint.continue") {
          return undefined;
        }
        if (prev.nextStage && event.stage === prev.nextStage) {
          return undefined;
        }
        return prev;
      });
    }

    const chatStreamDeltaKind = getChatStreamDeltaKind(event.type);
    if (chatStreamDeltaKind) {
      const streamId = getStringField(event.payload, ["message_id", "messageId", "id"]) || event.taskId || "default";
      appendStreamDeltaToSession(sessionId, chatStreamDeltaKind, event.content, streamId);
    }
    const dialogueAgentLabel = getDialogueEventAgentLabel(event);
    if (event.role && event.content && dialogueAgentLabel) {
      appendMessageToSession(
        sessionId,
        {
          id: `event-chat-${event.key}`,
          role: event.role,
          content: event.content,
          time: formatThreadTime(event.timestamp),
          sortTime:
            getThreadTimeSortValue(event.timestamp) ||
            (typeof event.sequence === "number" ? event.sequence : undefined),
          agentLabel: dialogueAgentLabel,
        },
        { dedupeLast: true },
      );
    }
    if (!event.stage) {
      return;
    }
    setWorkflowRuntimeState((prev) => reduceWorkflowRuntimeState(prev, event));
  };

  const consumeThreadMessageStream = async (
    response: Response,
    sessionId: string,
    signal?: AbortSignal,
  ) => {
    if (!response.body) {
      return;
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder("utf-8");
    let buffer = "";

    while (true) {
      const { value, done } = await reader.read();
      if (done || signal?.aborted) {
        break;
      }

      buffer += decoder.decode(value, { stream: true });
      const frames = buffer.split(/\r?\n\r?\n/);
      buffer = frames.pop() || "";

      for (const rawFrame of frames) {
        const frame = parseSSEFrame(rawFrame.trim());
        if (!frame) {
          continue;
        }

        const event = normalizeThreadEvent(frame);
        const chatStreamDeltaKind = getChatStreamDeltaKind(event.type);
        if (chatStreamDeltaKind) {
          const streamId = getStringField(event.payload, ["message_id", "messageId", "id"]) || event.taskId || "default";
          appendStreamDeltaToSession(sessionId, chatStreamDeltaKind, event.content, streamId);
        }
        if (isTerminalThreadEvent(event.type)) {
          return;
        }
      }
    }

    const trailingText = buffer.trim();
    if (trailingText) {
      const frame = parseSSEFrame(trailingText);
      if (frame) {
        const event = normalizeThreadEvent(frame);
        const chatStreamDeltaKind = getChatStreamDeltaKind(event.type);
        if (chatStreamDeltaKind) {
          const streamId = getStringField(event.payload, ["message_id", "messageId", "id"]) || event.taskId || "default";
          appendStreamDeltaToSession(sessionId, chatStreamDeltaKind, event.content, streamId);
        }
      }
    }
  };

  const subscribeThreadEvents = async (threadId: string, sessionId = activeSessionId) => {
    const activeSubscription = threadEventsAbortRef.current;
    if (activeSubscription?.threadId === threadId && !activeSubscription.controller.signal.aborted) {
      return;
    }

    if (activeSubscription && !activeSubscription.controller.signal.aborted) {
      activeSubscription.controller.abort();
    }
    processedThreadEventIdsRef.current = new Set();

    const controller = new AbortController();
    const subscription = { threadId, controller };
    threadEventsAbortRef.current = subscription;

    try {
      const response = await fetch(`${AGENT_API_BASE}/threads/${encodeURIComponent(threadId)}:events`, {
        method: "GET",
        headers: {
          Accept: "text/event-stream",
          ...AgentAppsAuth.getAuthHeaders(),
        },
        signal: controller.signal,
      });

      if (!response.ok) {
        throw new Error(`事件流连接失败：HTTP ${response.status}`);
      }
      if (!response.body) {
        throw new Error("事件流连接失败：浏览器未返回可读流");
      }

      const reader = response.body.getReader();
      const decoder = new TextDecoder("utf-8");
      let buffer = "";

      while (true) {
        const { value, done } = await reader.read();
        if (done || controller.signal.aborted) {
          break;
        }

        buffer += decoder.decode(value, { stream: true });
        const frames = buffer.split(/\r?\n\r?\n/);
        buffer = frames.pop() || "";

        for (const rawFrame of frames) {
          const frame = parseSSEFrame(rawFrame.trim());
          if (!frame) {
            continue;
          }

          if (frame.id) {
            if (processedThreadEventIdsRef.current.has(frame.id)) {
              continue;
            }
            processedThreadEventIdsRef.current.add(frame.id);
          }

          const event = normalizeThreadEvent(frame);
          applyWorkflowEvent(event, sessionId);
          if (isTerminalThreadEvent(event.type)) {
            controller.abort();
            break;
          }
        }
      }

      const trailingText = buffer.trim();
      if (!controller.signal.aborted && trailingText) {
        const frame = parseSSEFrame(trailingText);
        if (frame) {
          applyWorkflowEvent(normalizeThreadEvent(frame), sessionId);
        }
      }
    } catch (error) {
      if (controller.signal.aborted) {
        return;
      }
      message.error(getLocalizedErrorMessage(error, "线程事件流连接失败，请检查 SSE 接口。"), 2);
    } finally {
      if (threadEventsAbortRef.current === subscription) {
        threadEventsAbortRef.current = null;
      }
    }
  };

  const restoreThreadDetail = async (threadId: string, signal?: AbortSignal) => {
    const requestId = restoreRequestIdRef.current + 1;
    restoreRequestIdRef.current = requestId;
    setIsRestoringThread(true);
    setThreadRestoreError("");
    setIsWorkbenchVisible(true);
    replaceThreadEvents([]);
    processedWorkflowEventKeysRef.current = new Set();

    const restoredSessionId = `thread-${threadId}`;
    subscribeThreadEvents(threadId, restoredSessionId);
    setActiveSessionId(restoredSessionId);
    setChatSessions([
      {
        id: restoredSessionId,
        title: "线程恢复中",
        updatedAt: getTimeLabel(),
        threadId,
        messages: [
          {
            id: `${threadId}-restore-loading`,
            role: "assistant",
            content: `正在恢复自进化线程：${threadId}`,
            time: getTimeLabel(),
          },
        ],
      },
    ]);

    try {
      const encodedThreadId = encodeURIComponent(threadId);
      const threadResult = await axiosInstance.get(`${AGENT_API_BASE}/threads/${encodedThreadId}`, { signal });

      if (signal?.aborted || restoreRequestIdRef.current !== requestId) {
        return;
      }

      const threadPayload = threadResult.data as ThreadRestorePayload;
      const title =
        getThreadTitleFromPayload(threadPayload) ||
        `自进化详情 ${threadId.slice(0, 8)}`;
      const knowledgeBaseId = getThreadKnowledgeBaseId(threadPayload);
      if (knowledgeBaseId) {
        setSelectedKb(knowledgeBaseId);
      }
      const restoredMode = getThreadModeFromPayload(threadPayload);
      if (restoredMode) {
        setMode(restoredMode);
      }
      const nowLabel = getTimeLabel();

      setChatSessions((prev) =>
        prev.map((session) =>
          session.id === restoredSessionId
            ? {
                ...session,
                title,
                updatedAt: nowLabel,
                threadId,
                messages:
                  session.messages.length === 1 &&
                  session.messages[0]?.id === `${threadId}-restore-loading`
                    ? []
                    : session.messages,
              }
            : session,
        ),
      );
      setActiveSessionId(restoredSessionId);
      window.localStorage.setItem(SELF_EVOLUTION_LAST_THREAD_STORAGE_KEY, threadId);
    } catch (error) {
      if (signal?.aborted || isCanceledRequest(error)) {
        return;
      }
      const responseStatus = (error as AxiosError | undefined)?.response?.status;
      const errorTextRaw = getLocalizedErrorMessage(error, "线程详情恢复失败，请稍后重试。") || "";
      const isThreadNotFound = responseStatus === 404 && errorTextRaw.toLowerCase().includes("thread not found");
      if (isThreadNotFound) {
        setWorkflowRuntimeState(createThreadRestoreWorkflowRuntimeState());
        setWorkflowResults(createInitialWorkflowResultsState());
      }
      const errorText =
        errorTextRaw ||
        "线程详情恢复失败，请稍后重试。";
      setThreadRestoreError(errorText);
      setChatSessions([
        {
          id: restoredSessionId,
          title: `自进化详情 ${threadId.slice(0, 8)}`,
          updatedAt: getTimeLabel(),
          threadId,
          messages: [
            {
              id: `${threadId}-restore-error`,
              role: "assistant",
              content: errorText,
              time: getTimeLabel(),
            },
          ],
        },
      ]);
    } finally {
      if (!signal?.aborted && restoreRequestIdRef.current === requestId) {
        setIsRestoringThread(false);
      }
    }
  };

  useEffect(() => {
    if (!routeThreadId) {
      return;
    }

    const controller = new AbortController();
    void restoreThreadDetail(routeThreadId, controller.signal);

    return () => {
      controller.abort();
    };
  }, [routeThreadId]);

  const onSend = async (command?: string) => {
    const trimmedPrompt = (command ?? prompt).trim();
    const activeThreadId = activeSession?.threadId;
    if (isKnowledgeBaseRequired && !activeThreadId) {
      setHasLaunchValidationTriggered(true);
      message.warning("必须选择知识库才可以生成数据集。", 1.2);
      return;
    }
    if (!trimmedPrompt) {
      return;
    }

    const firstRound = !isWorkbenchVisible;
    const nowLabel = getTimeLabel();
    appendMessageToSession(
      activeSessionId,
      {
        id: `user-${Date.now()}`,
        role: "user",
        content: trimmedPrompt,
        time: nowLabel,
      },
    );
    setPrompt("");

    if (activeThreadId) {
      setIsSendingMessage(true);
      const controller = new AbortController();
      try {
        const response = await fetch(`${AGENT_API_BASE}/threads/${encodeURIComponent(activeThreadId)}:messages`, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Accept: "text/event-stream",
            ...AgentAppsAuth.getAuthHeaders(),
          },
          body: JSON.stringify({
            type: "message.user",
            role: "user",
            message: trimmedPrompt,
            content: trimmedPrompt,
          }),
          signal: controller.signal,
        });

        if (!response.ok) {
          throw new Error(`消息发送失败：HTTP ${response.status}`);
        }

        const contentType = response.headers.get("content-type") || "";
        if (contentType.includes("text/event-stream")) {
          await consumeThreadMessageStream(response, activeSessionId, controller.signal);
          return;
        }

        const responseData = await response.json().catch(() => undefined);
        const responsePayload = isRecord(responseData) ? responseData : undefined;
        const responseText = getNestedStringField(responsePayload, ["message", "content", "text", "reply"]);
        if (responseText) {
          appendMessageToSession(
            activeSessionId,
            {
              id: `assistant-${Date.now()}-${Math.random().toString(16).slice(2)}`,
              role: "assistant",
              content: responseText,
              time: getTimeLabel(),
            },
            { dedupeLast: true },
          );
        }
      } catch (error) {
        appendSystemMessage(
          getLocalizedErrorMessage(error, "消息发送失败，请检查 message 接口。") ||
            "消息发送失败，请检查 message 接口。",
          activeSessionId,
        );
      } finally {
        setIsSendingMessage(false);
      }
      return;
    }

    if (firstRound) {
      setIsWorkbenchVisible(true);
      message.success(`已基于「${selectedKnowledgeBase}」启动流程：${activeStepText}`, 1.2);
    }
    appendSystemMessage(
      firstRound
        ? `已收到任务「${trimmedPrompt}」，进入第一步：生成数据集。我会先产出样本结构和数据集数据。`
        : "已继续推进当前流程，我会优先补齐数据集样本并回传下一步状态。",
      activeSessionId,
    );
  };

  const onStartSession = async () => {
    if (isStartingSession) {
      return;
    }
    if (!isLaunchConfigValid) {
      setHasLaunchValidationTriggered(true);
      if (!selectedKb) {
        message.warning("必须先选择知识库才可以开始。", 1.2);
        return;
      }
      if (!selectedEvalSet) {
        message.warning("请先选择已有评测集策略。", 1.2);
        return;
      }
      if (!extraEvalStrategy) {
        message.warning("请先选择补充评测集策略。", 1.2);
        return;
      }
      if (!mode) {
        message.warning("请先选择过程干预方式。", 1.2);
        return;
      }
      message.warning("当前配置还不完整，请先完成前 4 步。", 1.2);
      return;
    }

    setIsStartingSession(true);
    try {
      const threadId = await createAndStartThread();
      setWorkflowRuntimeState(createInitialWorkflowRuntimeState());
      replaceThreadEvents([]);
      processedWorkflowEventKeysRef.current = new Set();
      void subscribeThreadEvents(threadId, activeSessionId);
      setIsWorkbenchVisible(true);
      window.localStorage.setItem(SELF_EVOLUTION_LAST_THREAD_STORAGE_KEY, threadId);
      const nowLabel = getTimeLabel();
      setChatSessions((prev) =>
        prev.map((session) =>
          session.id === activeSessionId
            ? {
                ...session,
                threadId,
                title: session.title === "当前会话" ? selectedKnowledgeBase : session.title,
                updatedAt: nowLabel,
                messages:
                  session.messages.length === 0
                    ? [
                        {
                          id: `assistant-${Date.now()}`,
                          role: "assistant",
                          content: `${buildSessionIntroContent(
                            selectedKnowledgeBase,
                            selectedEvalSetLabel,
                            extraEvalLabel,
                            interventionLabel,
                          )}\n\n线程 ID：${threadId}`,
                          time: nowLabel,
                        },
                      ]
                    : session.messages,
              }
            : session,
        ),
      );
      navigate(`/self-evolution/detail/${encodeURIComponent(threadId)}`);
      message.success("已调用接口并启动自进化流程。", 1.2);
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, "启动自进化流程失败，请检查接口联调状态。"), 2);
    } finally {
      setIsStartingSession(false);
    }
  };

  const onCreateSession = () => {
    setNewSessionDraft({
      selectedEvalSet: FIXED_EVAL_SET,
      extraEvalStrategy: FIXED_EXTRA_EVAL_STRATEGY,
    });
    setHasNewSessionValidationTriggered(false);
    setIsNewSessionConfigOpen(true);
  };

  const onCancelCreateSession = () => {
    setIsNewSessionConfigOpen(false);
    setHasNewSessionValidationTriggered(false);
  };

  const onConfirmCreateSession = () => {
    if (!isNewSessionDraftValid) {
      setHasNewSessionValidationTriggered(true);
      if (!newSessionDraft.selectedKb) {
        message.warning("请先选择知识库，再开始新会话。", 1.2);
        return;
      }
      if (!newSessionDraft.selectedEvalSet) {
        message.warning("请先选择已有评测集策略。", 1.2);
        return;
      }
      if (!newSessionDraft.extraEvalStrategy) {
        message.warning("请先选择补充评测集策略。", 1.2);
        return;
      }
      if (!newSessionDraft.mode) {
        message.warning("请先选择过程干预方式。", 1.2);
        return;
      }
      message.warning("当前配置还不完整，请检查 1-4 步。", 1.2);
      return;
    }

    const nextMode = newSessionDraft.mode as EvolutionMode;
    const nextKnowledgeBase = newSessionDraft.selectedKb as string;
    const nextEvalSet = newSessionDraft.selectedEvalSet as string;
    const nextExtraEvalStrategy = newSessionDraft.extraEvalStrategy as ExtraEvalStrategy;
    const nextKnowledgeBaseLabel =
      knowledgeBaseOptions.find((item) => item.value === nextKnowledgeBase)?.label || "知识库";
    const nextEvalSetLabel = getExistingEvalSetLabel(nextEvalSet);
    const nextExtraEvalLabel = nextExtraEvalStrategy === "generate" ? "是，补充评测集" : "否，不补充";
    const nextInterventionLabel = nextMode === "interactive" ? "是，人工干预" : "否，自动处理";
    const nowLabel = getTimeLabel();
    const nextIndex = chatSessions.length + 1;
    const newSessionId = `session-${Date.now()}`;
    const newSession: ChatSession = {
      id: newSessionId,
      title: `新会话 ${nextIndex}`,
      updatedAt: nowLabel,
      messages: [
        {
          id: `assistant-${Date.now() + 2}`,
          role: "assistant",
          content: buildSessionIntroContent(
            nextKnowledgeBaseLabel,
            nextEvalSetLabel,
            nextExtraEvalLabel,
            nextInterventionLabel,
          ),
          time: nowLabel,
        },
      ],
    };

    setSelectedKb(nextKnowledgeBase);
    setSelectedEvalSet(nextEvalSet);
    setExtraEvalStrategy(nextExtraEvalStrategy);
    setMode(nextMode);
    setHasLaunchValidationTriggered(false);
    setWorkflowRuntimeState(createInitialWorkflowRuntimeState());
    replaceThreadEvents([]);
    processedWorkflowEventKeysRef.current = new Set();
    setChatSessions((prev) => [...prev, newSession]);
    setActiveSessionId(newSessionId);
    setPrompt("");
    setIsWorkbenchVisible(true);
    setIsNewSessionConfigOpen(false);
    setHasNewSessionValidationTriggered(false);
    navigate("/self-evolution");
    message.success("已创建新会话并重新进入五步流程。", 1.2);
  };

  const onCloseSession = (sessionId: string) => {
    if (chatSessions.length <= 1) {
      message.info("至少保留一个会话标签。", 1);
      return;
    }
    const nextSessions = chatSessions.filter((item) => item.id !== sessionId);
    setChatSessions(nextSessions);
    if (activeSessionId === sessionId) {
      setActiveSessionId(nextSessions[0].id);
    }
  };

  const fetchThreadHistoryList = async () => {
    if (isLoadingThreadHistoryList) {
      return;
    }

    setIsLoadingThreadHistoryList(true);
    setThreadHistoryListError("");
    try {
      const response = await axiosInstance.get(`${AGENT_API_BASE}/threads`, {
        params: { page_size: 50 },
      });
      const nextRemoteThreads = normalizeThreadListPayload(response.data);
      setRemoteThreadHistory(nextRemoteThreads);
      if (nextRemoteThreads.length === 0) {
        message.info("暂未获取到服务端历史会话。", 1.2);
      }
    } catch (error) {
      const errorText =
        getLocalizedErrorMessage(error, "获取历史会话列表失败，请稍后重试。") ||
        "获取历史会话列表失败，请稍后重试。";
      setThreadHistoryListError(errorText);
      message.error(errorText, 2);
    } finally {
      setIsLoadingThreadHistoryList(false);
    }
  };

  const onOpenHistorySessionModal = () => {
    setIsHistorySessionModalOpen(true);
    void fetchThreadHistoryList();
  };

  const onSelectHistorySession = (entry: {
    sessionId?: string;
    threadId?: string;
  }) => {
    if (entry.threadId) {
      const matchedSession = chatSessions.find((session) => session.threadId === entry.threadId);
      if (matchedSession) {
        setActiveSessionId(matchedSession.id);
      }
      setIsHistorySessionModalOpen(false);
      if (entry.threadId !== routeThreadId) {
        navigate(`/self-evolution/detail/${encodeURIComponent(entry.threadId)}`);
      }
      return;
    }

    if (entry.sessionId) {
      setActiveSessionId(entry.sessionId);
    }
    setIsHistorySessionModalOpen(false);
  };

  const resetToEmptySession = () => {
    const nowLabel = getTimeLabel();
    const nextSessionId = `session-${Date.now()}`;
    setChatSessions([
      {
        id: nextSessionId,
        title: "当前会话",
        updatedAt: nowLabel,
        messages: [],
      },
    ]);
    setActiveSessionId(nextSessionId);
    setIsWorkbenchVisible(false);
    setWorkflowRuntimeState(createInitialWorkflowRuntimeState());
    setWorkflowResults(createInitialWorkflowResultsState());
    replaceThreadEvents([]);
    processedWorkflowEventKeysRef.current = new Set();
    setThreadRestoreError("");
    setPrompt("");
    navigate("/self-evolution");
  };

  const deleteHistorySession = async (entry: HistorySessionEntry) => {
    if (deletingHistoryKeys.includes(entry.key)) {
      return;
    }

    setDeletingHistoryKeys((prev) => [...prev, entry.key]);
    try {
      if (entry.threadId) {
        await createCoreAgentApiClient().apiCoreAgentThreadsThreadIdHistoryDelete({
          threadId: entry.threadId,
        });
        setRemoteThreadHistory((prev) => prev.filter((item) => item.threadId !== entry.threadId));
        setChatSessions((prev) => prev.filter((session) => session.threadId !== entry.threadId));

        if (window.localStorage.getItem(SELF_EVOLUTION_LAST_THREAD_STORAGE_KEY) === entry.threadId) {
          window.localStorage.removeItem(SELF_EVOLUTION_LAST_THREAD_STORAGE_KEY);
        }
        if (entry.threadId === activeThreadId || entry.threadId === routeThreadId) {
          threadEventsAbortRef.current?.controller.abort();
          threadEventsAbortRef.current = null;
          resetToEmptySession();
        }
      } else if (entry.sessionId) {
        setChatSessions((prev) => prev.filter((session) => session.id !== entry.sessionId));
        if (entry.sessionId === activeSessionId) {
          resetToEmptySession();
        }
      }

      message.success("会话历史已删除。", 1.2);
    } catch (error) {
      message.error(
        getLocalizedErrorMessage(error, "删除会话历史失败，请稍后重试。") ||
          "删除会话历史失败，请稍后重试。",
        2,
      );
    } finally {
      setDeletingHistoryKeys((prev) => prev.filter((key) => key !== entry.key));
    }
  };

  const onDeleteHistorySession = (entry: HistorySessionEntry, event: MouseEvent<HTMLElement>) => {
    event.stopPropagation();
    Modal.confirm({
      title: "删除会话历史",
      content: entry.threadId
        ? "确认删除该线程的会话历史？删除后将调用服务端历史删除接口。"
        : "确认删除该本地会话？",
      okText: "删除",
      okButtonProps: { danger: true },
      cancelText: "取消",
      centered: true,
      onOk: () => deleteHistorySession(entry),
    });
  };

  const renderKnowledgeBaseButton = (extraClassName = "", isLocked = false) => (
    <Dropdown
      trigger={["click"]}
      placement="topLeft"
      overlayClassName="self-evolution-chatlike-dropdown"
      disabled={isLocked}
      menu={{
        items: knowledgeBaseMenuItems,
        selectable: true,
        selectedKeys: selectedKb ? [selectedKb] : [],
        onClick: ({ key }) => {
          if (isLocked) {
            return;
          }
          onKnowledgeBaseMenuClick(String(key), (nextKnowledgeBase) => {
            setSelectedKb(nextKnowledgeBase);
            setHasLaunchValidationTriggered(false);
          });
        },
      }}
    >
      <button
        type="button"
        className={`self-evolution-chatlike-tool ${extraClassName}${isLocked ? " is-disabled" : ""}`.trim()}
        disabled={isLocked}
        aria-busy={isKnowledgeBaseLoading}
        aria-label={isLocked ? `当前知识库已锁定：${selectedKnowledgeBase}` : `选择知识库：${selectedKnowledgeBase}`}
        title={isLocked ? "进入流程后不可修改知识库" : undefined}
      >
        <DatabaseOutlined />
        <span>{selectedKnowledgeBase}</span>
        <DownOutlined className="self-evolution-chatlike-select-caret" />
      </button>
    </Dropdown>
  );

  const renderModeButton = (extraClassName = "", isLocked = false) => (
    <Dropdown
      trigger={["click"]}
      placement="topLeft"
      overlayClassName="self-evolution-chatlike-dropdown"
      disabled={isLocked}
      menu={{
        items: modeMenuItems,
        selectable: true,
        selectedKeys: [mode],
        onClick: ({ key }) => {
          if (isLocked) {
            return;
          }
          setMode(key as EvolutionMode);
        },
      }}
    >
      <button
        type="button"
        className={`self-evolution-chatlike-tool ${extraClassName}${isLocked ? " is-disabled" : ""}`.trim()}
        disabled={isLocked}
        aria-label={isLocked ? `当前处理模式已锁定：${modeLabel}` : `选择处理模式：${modeLabel}`}
        title={isLocked ? "进入流程后不可修改处理模式" : undefined}
      >
        <MessageOutlined />
        <span>{modeLabel}</span>
        <DownOutlined className="self-evolution-chatlike-select-caret" />
      </button>
    </Dropdown>
  );

  const renderExistingEvalSetButton = (extraClassName = "") => (
    <Dropdown
      trigger={["click"]}
      placement="topLeft"
      overlayClassName="self-evolution-chatlike-dropdown"
      menu={{
        items: existingEvalSetMenuItems,
        selectable: true,
        selectedKeys: [selectedEvalSet],
        onClick: ({ key }) => {
          const nextEvalSet = String(key);
          setSelectedEvalSet(nextEvalSet);
          if (nextEvalSet === "__none__") {
            setExtraEvalStrategy("generate");
          }
        },
      }}
    >
      <button
        type="button"
        className={`self-evolution-chatlike-tool ${extraClassName}`.trim()}
      >
        <FileTextOutlined />
        <span>{selectedEvalSetLabel}</span>
        <DownOutlined className="self-evolution-chatlike-select-caret" />
      </button>
    </Dropdown>
  );

  const renderExtraEvalStrategyButton = (extraClassName = "") => (
    <Dropdown
      trigger={["click"]}
      placement="topLeft"
      overlayClassName="self-evolution-chatlike-dropdown"
      menu={{
        items: extraEvalStrategyMenuItems,
        selectable: true,
        selectedKeys: [extraEvalStrategy],
        onClick: ({ key }) => {
          const nextStrategy = key as ExtraEvalStrategy;
          if (isExtraEvalRequired && nextStrategy === "skip") {
            setExtraEvalStrategy("generate");
            message.warning("不使用已有评测集时，必须补充生成评测集。", 1.2);
            return;
          }
          setExtraEvalStrategy(nextStrategy);
        },
      }}
    >
      <button
        type="button"
        className={`self-evolution-chatlike-tool ${extraClassName}`.trim()}
      >
        <ExperimentOutlined />
        <span>{extraEvalLabel}</span>
        <DownOutlined className="self-evolution-chatlike-select-caret" />
      </button>
    </Dropdown>
  );

  const renderInterventionButton = (extraClassName = "") => (
    <Dropdown
      trigger={["click"]}
      placement="topLeft"
      overlayClassName="self-evolution-chatlike-dropdown"
      menu={{
        items: modeMenuItems,
        selectable: true,
        selectedKeys: [mode],
        onClick: ({ key }) => {
          setMode(key as EvolutionMode);
        },
      }}
    >
      <button
        type="button"
        className={`self-evolution-chatlike-tool ${extraClassName}`.trim()}
      >
        <MessageOutlined />
        <span>{interventionLabel}</span>
        <DownOutlined className="self-evolution-chatlike-select-caret" />
      </button>
    </Dropdown>
  );

  const renderNewSessionKnowledgeBaseButton = () => (
    <Dropdown
      trigger={["click"]}
      placement="bottomLeft"
      overlayClassName="self-evolution-chatlike-dropdown"
      menu={{
        items: knowledgeBaseMenuItems,
        selectable: true,
        selectedKeys: newSessionDraft.selectedKb ? [newSessionDraft.selectedKb] : [],
        onClick: ({ key }) => {
          onKnowledgeBaseMenuClick(String(key), (nextKnowledgeBase) => {
            setNewSessionDraft((prev) => ({
              ...prev,
              selectedKb: nextKnowledgeBase,
            }));
            setHasNewSessionValidationTriggered(false);
          });
        },
      }}
    >
      <button
        type="button"
        className={`self-evolution-chatlike-tool is-launch-control${
          hasNewSessionValidationTriggered && !newSessionDraft.selectedKb ? " is-warning" : ""
        }`}
        aria-busy={isKnowledgeBaseLoading}
        aria-label={`选择新会话知识库：${draftKnowledgeBaseLaunchLabel}`}
      >
        <DatabaseOutlined />
        <span>{draftKnowledgeBaseLaunchLabel}</span>
        <DownOutlined className="self-evolution-chatlike-select-caret" />
      </button>
    </Dropdown>
  );

  const renderNewSessionEvalSetButton = () => (
    <Dropdown
      trigger={["click"]}
      placement="bottomLeft"
      overlayClassName="self-evolution-chatlike-dropdown"
      menu={{
        items: existingEvalSetMenuItems,
        selectable: true,
        selectedKeys: newSessionDraft.selectedEvalSet ? [newSessionDraft.selectedEvalSet] : [],
        onClick: ({ key }) => {
          const nextEvalSet = String(key);
          setNewSessionDraft((prev) => ({
            ...prev,
            selectedEvalSet: nextEvalSet,
            extraEvalStrategy:
              nextEvalSet === "__none__" && prev.extraEvalStrategy === "skip"
                ? "generate"
                : prev.extraEvalStrategy,
          }));
          setHasNewSessionValidationTriggered(false);
        },
      }}
    >
      <button
        type="button"
        className={`self-evolution-chatlike-tool is-launch-control${
          hasNewSessionValidationTriggered && !newSessionDraft.selectedEvalSet ? " is-warning" : ""
        }`}
      >
        <FileTextOutlined />
        <span>{draftEvalSetLabel}</span>
        <DownOutlined className="self-evolution-chatlike-select-caret" />
      </button>
    </Dropdown>
  );

  const renderNewSessionExtraEvalStrategyButton = () => (
    <Dropdown
      trigger={["click"]}
      placement="bottomLeft"
      overlayClassName="self-evolution-chatlike-dropdown"
      menu={{
        items: newSessionExtraEvalStrategyMenuItems,
        selectable: true,
        selectedKeys: newSessionDraft.extraEvalStrategy ? [newSessionDraft.extraEvalStrategy] : [],
        onClick: ({ key }) => {
          const nextStrategy = key as ExtraEvalStrategy;
          if (isDraftExtraEvalRequired && nextStrategy === "skip") {
            message.warning("不使用已有评测集时，必须补充生成评测集。", 1.2);
            return;
          }
          setNewSessionDraft((prev) => ({
            ...prev,
            extraEvalStrategy: nextStrategy,
          }));
          setHasNewSessionValidationTriggered(false);
        },
      }}
    >
      <button
        type="button"
        className={`self-evolution-chatlike-tool is-launch-control${
          hasNewSessionValidationTriggered && !newSessionDraft.extraEvalStrategy ? " is-warning" : ""
        }`}
      >
        <ExperimentOutlined />
        <span>{draftExtraEvalLabel}</span>
        <DownOutlined className="self-evolution-chatlike-select-caret" />
      </button>
    </Dropdown>
  );

  const renderNewSessionInterventionButton = () => (
    <Dropdown
      trigger={["click"]}
      placement="bottomLeft"
      overlayClassName="self-evolution-chatlike-dropdown"
      menu={{
        items: modeMenuItems,
        selectable: true,
        selectedKeys: newSessionDraft.mode ? [newSessionDraft.mode] : [],
        onClick: ({ key }) => {
          setNewSessionDraft((prev) => ({
            ...prev,
            mode: key as EvolutionMode,
          }));
          setHasNewSessionValidationTriggered(false);
        },
      }}
    >
      <button
        type="button"
        className={`self-evolution-chatlike-tool is-launch-control${
          hasNewSessionValidationTriggered && !newSessionDraft.mode ? " is-warning" : ""
        }`}
      >
        <MessageOutlined />
        <span>{draftInterventionLabel}</span>
        <DownOutlined className="self-evolution-chatlike-select-caret" />
      </button>
    </Dropdown>
  );

  const launchOptionCards = [
    {
      key: "knowledge-base",
      step: "1",
      title: "选择知识库",
      description: "请您选择一个知识库，用作优化目标",
      currentValue: knowledgeBaseLaunchLabel,
      toneClassName: "is-blue",
      icon: <DatabaseOutlined />,
      isHighlighted: isKnowledgeBaseRequired && hasLaunchValidationTriggered,
      isDescSingleLine: false,
      control: renderKnowledgeBaseButton("is-launch-control"),
    },
    {
      key: "existing-eval-set",
      step: "2",
      title: "已有评测集",
      description: "您是否要选择一个已经存在的评测集",
      currentValue: selectedEvalSetLabel,
      toneClassName: "is-green",
      icon: <FileTextOutlined />,
      isHighlighted: false,
      isDescSingleLine: false,
      control: renderExistingEvalSetButton("is-launch-control"),
    },
    {
      key: "extra-eval-set",
      step: "3",
      title: "补充评测集",
      description: "是否补充生成评测集",
      currentValue: extraEvalLabel,
      toneClassName: "is-amber",
      icon: <ExperimentOutlined />,
      isHighlighted: false,
      isDescSingleLine: true,
      control: renderExtraEvalStrategyButton("is-launch-control"),
    },
    {
      key: "intervention",
      step: "4",
      title: "过程干预",
      description: "您是否要干预整个优化过程",
      currentValue: interventionLabel,
      toneClassName: "is-violet",
      icon: <MessageOutlined />,
      isHighlighted: false,
      isDescSingleLine: false,
      control: renderInterventionButton("is-launch-control"),
    },
  ];

  const launchSummaryItems = [
    { label: "优化目标", value: knowledgeBaseLaunchLabel },
    { label: "已有评测集", value: selectedEvalSetLabel },
    { label: "补充评测集", value: extraEvalLabel },
    { label: "过程干预", value: interventionLabel },
  ];

  const newSessionOptionCards = [
    {
      key: "new-session-knowledge-base",
      step: "1",
      title: "选择知识库",
      description: "请您重新选择本轮会话的优化目标知识库",
      currentValue: draftKnowledgeBaseLaunchLabel,
      toneClassName: "is-blue",
      icon: <DatabaseOutlined />,
      isHighlighted: hasNewSessionValidationTriggered && !newSessionDraft.selectedKb,
      isDescSingleLine: false,
      control: renderNewSessionKnowledgeBaseButton(),
    },
    {
      key: "new-session-existing-eval-set",
      step: "2",
      title: "已有评测集",
      description: "您可以沿用历史评测集，也可以选择不使用已有评测集",
      currentValue: draftEvalSetLabel,
      toneClassName: "is-green",
      icon: <FileTextOutlined />,
      isHighlighted: hasNewSessionValidationTriggered && !newSessionDraft.selectedEvalSet,
      isDescSingleLine: false,
      control: renderNewSessionEvalSetButton(),
    },
    {
      key: "new-session-extra-eval-set",
      step: "3",
      title: "补充评测集",
      description: "如未选择已有评测集，本步必须选择“是，补充评测集”",
      currentValue: draftExtraEvalLabel,
      toneClassName: "is-amber",
      icon: <ExperimentOutlined />,
      isHighlighted: hasNewSessionValidationTriggered && !newSessionDraft.extraEvalStrategy,
      isDescSingleLine: false,
      control: renderNewSessionExtraEvalStrategyButton(),
    },
    {
      key: "new-session-intervention",
      step: "4",
      title: "过程干预",
      description: "选择本会话采用交互处理或自动处理",
      currentValue: draftInterventionLabel,
      toneClassName: "is-violet",
      icon: <MessageOutlined />,
      isHighlighted: hasNewSessionValidationTriggered && !newSessionDraft.mode,
      isDescSingleLine: true,
      control: renderNewSessionInterventionButton(),
    },
  ];

  const newSessionSummaryItems = [
    { label: "优化目标", value: draftKnowledgeBaseLaunchLabel },
    { label: "已有评测集", value: draftEvalSetLabel },
    { label: "补充评测集", value: draftExtraEvalLabel },
    { label: "过程干预", value: draftInterventionLabel },
  ];

  const renderKnowledgeAndModeTools = () => (
    <div className="self-evolution-chatlike-tools">
      {renderKnowledgeBaseButton("", isRuntimeConfigLocked)}
      {renderModeButton("", isRuntimeConfigLocked)}
    </div>
  );

  const renderSendButton = () => (
    <button
      type="button"
      onClick={() => void onSend()}
      disabled={isSendDisabled}
      className={`self-evolution-chatlike-send-button${isSendDisabled ? " disabled" : ""}`}
      aria-label="发送"
    >
      <SendIcon />
    </button>
  );

  const renderWorkflowResultPayload = (kind: WorkflowResultKind) => {
    const resultState = workflowResults[kind];
    const label = workflowResultLabels[kind];

    if (resultState.loading) {
      return (
        <div className="self-evolution-result-state is-loading">
          <LoadingOutlined spin />
          <span>{`正在请求${label}接口...`}</span>
        </div>
      );
    }

    if (resultState.error) {
      return (
        <div className="self-evolution-result-state is-error" role="alert">
          <span>{resultState.error}</span>
          <button type="button" onClick={() => void fetchWorkflowResult(kind, { force: true })}>
            重试
          </button>
        </div>
      );
    }

    if (!resultState.loaded) {
      return (
        <Paragraph className="self-evolution-px-empty">
          展开后会请求当前线程的{label}接口。
        </Paragraph>
      );
    }

    if (isEmptyResultPayload(resultState.data)) {
      return (
        <Paragraph className="self-evolution-px-empty">
          {`${label}接口已返回，当前暂无可展示结果。`}
        </Paragraph>
      );
    }

    return (
      <div className="self-evolution-result-json">
        <div className="self-evolution-result-json-head">
          <Text>{`${label}接口返回`}</Text>
          <Text>{`${getResultItems(resultState.data).length || 1} 条`}</Text>
        </div>
        <pre>{stringifyResultPayload(resultState.data)}</pre>
      </div>
    );
  };

  const renderPxSingleCategoryPie = (categoryMetric: PxCategoryMetricAverage) => {
    const chartSize = 220;
    const center = chartSize / 2;
    const radius = 74;
    const strokeWidth = 34;
    const circumference = 2 * Math.PI * radius;
    const metricValues = pxMetricMeta.map((metric) => ({
      ...metric,
      value: clampScore(categoryMetric.metrics[metric.key]),
    }));
    const valueSum = metricValues.reduce((acc, item) => acc + item.value, 0);
    const normalized = metricValues.map((item) => ({
      ...item,
      ratio: valueSum > 0 ? item.value / valueSum : 1 / metricValues.length,
    }));
    let cumulativeOffset = 0;

    return (
      <div className="self-evolution-px-chart-wrap" aria-label="单分类指标饼图">
        <svg className="self-evolution-px-pie-chart" viewBox={`0 0 ${chartSize} ${chartSize}`} role="img">
          <title>{`${categoryMetric.category} 指标分布`}</title>
          <circle
            cx={center}
            cy={center}
            r={radius}
            fill="none"
            stroke="#ecf2fb"
            strokeWidth={strokeWidth}
          />
          <g transform={`rotate(-90 ${center} ${center})`}>
            {normalized.map((item) => {
              const dashLength = item.ratio * circumference;
              const currentOffset = cumulativeOffset;
              cumulativeOffset += dashLength;
              return (
                <circle
                  key={item.key}
                  cx={center}
                  cy={center}
                  r={radius}
                  fill="none"
                  stroke={item.color}
                  strokeWidth={strokeWidth}
                  strokeDasharray={`${dashLength} ${circumference - dashLength}`}
                  strokeDashoffset={-currentOffset}
                />
              );
            })}
          </g>
          <text x={center} y={center - 4} textAnchor="middle" className="self-evolution-px-pie-center-title">
            {categoryMetric.category}
          </text>
          <text x={center} y={center + 20} textAnchor="middle" className="self-evolution-px-pie-center-value">
            {`${categoryMetric.caseCount} 条`}
          </text>
        </svg>
      </div>
    );
  };

  const renderPxMultiCategoryBars = (categoryMetrics: PxCategoryMetricAverage[]) => {
    const width = 640;
    const height = 280;
    const padding = { top: 18, right: 24, bottom: 46, left: 44 };
    const chartWidth = width - padding.left - padding.right;
    const chartHeight = height - padding.top - padding.bottom;
    const categoryCount = categoryMetrics.length;
    const yToPx = (value: number) => padding.top + (1 - clampScore(value)) * chartHeight;
    const groupWidth = chartWidth / Math.max(categoryCount, 1);
    const metricCount = pxMetricMeta.length;
    const barGap = 4;
    const groupInnerWidth = Math.min(96, groupWidth * 0.74);
    const barWidth = Math.max(5, Math.min(18, (groupInnerWidth - barGap * (metricCount - 1)) / metricCount));
    const groupBarsWidth = barWidth * metricCount + barGap * (metricCount - 1);
    const xToCenter = (index: number) => padding.left + groupWidth * index + groupWidth / 2;
    const axisTicks = [0, 0.25, 0.5, 0.75, 1];

    return (
      <div className="self-evolution-px-chart-wrap" aria-label="多分类指标柱状图">
        <svg className="self-evolution-px-bar-chart" viewBox={`0 0 ${width} ${height}`} role="img">
          <title>问题分类指标均值柱状图</title>
          {axisTicks.map((tick) => {
            const y = yToPx(tick);
            return (
              <g key={tick}>
                <line
                  x1={padding.left}
                  y1={y}
                  x2={width - padding.right}
                  y2={y}
                  className="self-evolution-px-grid-line"
                />
                <text x={padding.left - 8} y={y + 4} textAnchor="end" className="self-evolution-px-axis-label">
                  {tick.toFixed(2)}
                </text>
              </g>
            );
          })}

          <line
            x1={padding.left}
            y1={padding.top + chartHeight}
            x2={width - padding.right}
            y2={padding.top + chartHeight}
            className="self-evolution-px-axis-line"
          />

          {categoryMetrics.map((item, categoryIndex) => {
            const groupStartX = xToCenter(categoryIndex) - groupBarsWidth / 2;
            return (
              <g key={`px-bar-group-${item.category}`}>
                {pxMetricMeta.map((metric, metricIndex) => {
                  const value = clampScore(item.metrics[metric.key]);
                  const y = yToPx(value);
                  return (
                    <rect
                      key={`${item.category}-${metric.key}`}
                      x={groupStartX + metricIndex * (barWidth + barGap)}
                      y={y}
                      width={barWidth}
                      height={padding.top + chartHeight - y}
                      rx={3}
                      fill={metric.color}
                      className="self-evolution-px-bar"
                    >
                      <title>{`${metric.label} ${item.category}: ${formatPercent(value)}`}</title>
                    </rect>
                  );
                })}
              </g>
            );
          })}

          {categoryMetrics.map((item, index) => {
            const x = xToCenter(index);
            return (
              <text
                key={item.category}
                x={x}
                y={height - 16}
                textAnchor="middle"
                className="self-evolution-px-axis-label"
              >
                {item.category}
              </text>
            );
          })}
        </svg>
      </div>
    );
  };

  const renderPxCategorySummaryGrid = (categoryMetrics: PxCategoryMetricAverage[]) => (
    <div className="self-evolution-px-summary-grid" aria-label="问题类别指标概览">
      {categoryMetrics.map((item) => (
        <article key={item.category} className="self-evolution-px-summary-card">
          <div className="self-evolution-px-summary-card-head">
            <div className="self-evolution-px-summary-card-title-group">
              <span className="self-evolution-px-summary-card-icon" aria-hidden>
                {item.category.slice(0, 1)}
              </span>
              <div className="self-evolution-px-summary-card-copy">
                <strong>{item.category}</strong>
                <span>{`${item.caseCount} 条样本`}</span>
              </div>
            </div>
          </div>
          <div className="self-evolution-px-summary-metrics">
            {pxMetricMeta.map((metric) => (
              <div key={`${item.category}-${metric.key}`} className="self-evolution-px-summary-metric-row">
                <span className="self-evolution-px-summary-metric-label">
                  <span className="self-evolution-px-legend-dot" style={{ backgroundColor: metric.color }} />
                  {metric.label}
                </span>
                <strong>{formatPercent(item.metrics[metric.key])}</strong>
              </div>
            ))}
          </div>
        </article>
      ))}
    </div>
  );

  const renderPxReportPreview = () => (
    <section className="self-evolution-px-report" aria-label="评测报告指标展示">
      {workflowResults["eval-reports"].loading ? (
        renderWorkflowResultPayload("eval-reports")
      ) : workflowResults["eval-reports"].error ? (
        renderWorkflowResultPayload("eval-reports")
      ) : (
        <>
      <div className="self-evolution-px-report-head">
        <Text>按问题类别聚合四项指标均值</Text>
        <Text>{`样本数 ${pxReportTotalCases}，分类数 ${pxReportCategoryMetrics.length}`}</Text>
      </div>

      {pxReportCategoryMetrics.length === 0 ? (
        <Paragraph className="self-evolution-px-empty">当前报告无可用指标数据。</Paragraph>
      ) : isSinglePxCategory ? (
        <div className="self-evolution-px-panel">
          {renderPxSingleCategoryPie(pxReportCategoryMetrics[0])}
          <div className="self-evolution-px-legend">
            {pxMetricMeta.map((metric) => (
              <div key={metric.key} className="self-evolution-px-legend-item">
                <span className="self-evolution-px-legend-dot" style={{ backgroundColor: metric.color }} />
                <span className="self-evolution-px-legend-label">{metric.label}</span>
                <span className="self-evolution-px-legend-value">
                  {formatPercent(pxReportCategoryMetrics[0].metrics[metric.key])}
                </span>
              </div>
            ))}
          </div>
        </div>
      ) : (
        <div className="self-evolution-px-panel is-bar">
          {renderPxMultiCategoryBars(pxReportCategoryMetrics)}
          <div className="self-evolution-px-legend is-compact">
            {pxMetricMeta.map((metric) => (
              <div key={metric.key} className="self-evolution-px-legend-item">
                <span className="self-evolution-px-legend-dot" style={{ backgroundColor: metric.color }} />
                <span className="self-evolution-px-legend-label">{metric.label}</span>
              </div>
            ))}
          </div>
        </div>
      )}
      {renderPxCategorySummaryGrid(pxReportCategoryMetrics)}
        </>
      )}
    </section>
  );

  const renderAnalysisReportPreview = () => (
    <section className="self-evolution-analysis-report" aria-label="分析报告展示">
      <div className="self-evolution-analysis-head">
        <Text>完整分析报告</Text>
      </div>
      <div className="self-evolution-analysis-body">
        {workflowResults["analysis-reports"].loaded ||
        workflowResults["analysis-reports"].loading ||
        workflowResults["analysis-reports"].error ? (
          fetchedAnalysisReportMarkdown ? (
            <div className="self-evolution-analysis-markdown">
              <MarkdownViewer>{fetchedAnalysisReportMarkdown}</MarkdownViewer>
            </div>
          ) : (
            renderWorkflowResultPayload("analysis-reports")
          )
        ) : (
          renderWorkflowResultPayload("analysis-reports")
        )}
      </div>
    </section>
  );

  const renderAnalysisRuntimeSummary = () => {
    if (!analysisRunSummary) {
      return null;
    }

    const statItems = [
      { label: "调查项", value: String(analysisRunSummary.hypothesisCount) },
      { label: "子代理", value: String(analysisRunSummary.agentCount) },
      { label: "已回收结论", value: String(analysisRunSummary.completedAgentCount) },
      {
        label: "编排轮次",
        value: analysisRunSummary.iterationCount ? `${analysisRunSummary.iterationCount} 轮` : "进行中",
      },
    ];

    return (
      <section className="self-evolution-execution-summary" aria-label="分析执行概览">
        <div className="self-evolution-execution-summary-head">
          <Text>分析执行概览</Text>
          <span className={`self-evolution-inline-status is-${analysisRunSummary.status}`}>
            {getStepStatusLabel(analysisRunSummary.status)}
          </span>
        </div>

        <div className="self-evolution-execution-stat-grid" role="list" aria-label="分析执行统计">
          {statItems.map((item) => (
            <div key={item.label} className="self-evolution-execution-stat" role="listitem">
              <span className="self-evolution-execution-stat-label">{item.label}</span>
              <strong className="self-evolution-execution-stat-value">{item.value}</strong>
            </div>
          ))}
        </div>

        {analysisRunSummary.timeline.length > 0 && (
          <div className="self-evolution-execution-section">
            <Text className="self-evolution-execution-section-title">关键过程</Text>
            <div className="self-evolution-execution-timeline">
              {analysisRunSummary.timeline.slice(-5).map((item) => (
                <div key={item.key} className="self-evolution-execution-timeline-item">
                  <div className="self-evolution-execution-timeline-meta">
                    <strong>{item.title}</strong>
                    {item.time && <span>{formatThreadTime(item.time)}</span>}
                  </div>
                  <p>{item.detail}</p>
                </div>
              ))}
            </div>
          </div>
        )}

        {analysisRunSummary.hypotheses.length > 0 && (
          <div className="self-evolution-execution-section">
            <Text className="self-evolution-execution-section-title">调查结论</Text>
            <div className="self-evolution-execution-list">
              {analysisRunSummary.hypotheses.slice(0, 4).map((item) => (
                <div key={item.id} className="self-evolution-execution-list-item">
                  <div className="self-evolution-execution-list-head">
                    <div className="self-evolution-execution-list-title">
                      <strong>{item.id}</strong>
                      <span>{formatAnalysisCategory(item.category)}</span>
                    </div>
                    <div className="self-evolution-execution-list-tags">
                      <span className={`self-evolution-inline-tag is-${item.verdict || "pending"}`}>
                        {formatAnalysisVerdict(item.verdict)}
                      </span>
                      {formatConfidencePercent(item.confidence) && (
                        <span className="self-evolution-inline-tag is-neutral">
                          {formatConfidencePercent(item.confidence)}
                        </span>
                      )}
                    </div>
                  </div>
                  <p>{item.refinedClaim || item.claim}</p>
                  {item.suggestedAction && (
                    <span className="self-evolution-execution-list-note">{`建议动作：${item.suggestedAction}`}</span>
                  )}
                </div>
              ))}
            </div>
          </div>
        )}

        {analysisRunSummary.agents.length > 0 && (
          <div className="self-evolution-execution-section">
            <Text className="self-evolution-execution-section-title">子代理进展</Text>
            <div className="self-evolution-execution-agent-list">
              {analysisRunSummary.agents.slice(0, 5).map((item) => (
                <div key={item.agent} className="self-evolution-execution-agent-row">
                  <div className="self-evolution-execution-agent-main">
                    <strong>{formatAnalysisAgentName(item.agent)}</strong>
                    <span>{`工具 ${item.toolCallCount} 次${item.rounds ? `，调查 ${item.rounds} 轮` : ""}`}</span>
                  </div>
                  <div className="self-evolution-execution-agent-side">
                    {item.hypothesisId && <span>{item.hypothesisId}</span>}
                    <span className={`self-evolution-inline-tag is-${item.verdict || "pending"}`}>
                      {formatAnalysisVerdict(item.verdict)}
                    </span>
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}

        {analysisRunSummary.crossStepNarrative && (
          <Paragraph className="self-evolution-execution-summary-note">
            {analysisRunSummary.crossStepNarrative}
          </Paragraph>
        )}
      </section>
    );
  };

  const renderApplyRuntimeSummary = () => {
    if (!applyRunSummary) {
      return null;
    }

    const statItems = [
      { label: "优化轮次", value: applyRunSummary.roundCount ? `${applyRunSummary.roundCount} 轮` : "准备中" },
      { label: "改动文件", value: `${applyRunSummary.changedFileCount} 个` },
      { label: "测试状态", value: applyRunSummary.testStatusText || "待确认" },
    ];

    return (
      <section className="self-evolution-execution-summary" aria-label="代码优化执行概览">
        <div className="self-evolution-execution-summary-head">
          <Text>代码优化概览</Text>
          <span className={`self-evolution-inline-status is-${applyRunSummary.status}`}>
            {getStepStatusLabel(applyRunSummary.status)}
          </span>
        </div>

        <div className="self-evolution-execution-stat-grid" role="list" aria-label="代码优化统计">
          {statItems.map((item) => (
            <div key={item.label} className="self-evolution-execution-stat" role="listitem">
              <span className="self-evolution-execution-stat-label">{item.label}</span>
              <strong className="self-evolution-execution-stat-value">{item.value}</strong>
            </div>
          ))}
        </div>

        {applyRunSummary.timeline.length > 0 && (
          <div className="self-evolution-execution-section">
            <Text className="self-evolution-execution-section-title">执行过程</Text>
            <div className="self-evolution-execution-timeline">
              {applyRunSummary.timeline.map((item) => (
                <div key={item.key} className="self-evolution-execution-timeline-item">
                  <div className="self-evolution-execution-timeline-meta">
                    <strong>{item.title}</strong>
                    {item.time && <span>{formatThreadTime(item.time)}</span>}
                  </div>
                  <p>{item.detail}</p>
                </div>
              ))}
            </div>
          </div>
        )}

        {applyRunSummary.changedFiles.length > 0 && (
          <div className="self-evolution-execution-section">
            <Text className="self-evolution-execution-section-title">涉及文件</Text>
            <div className="self-evolution-file-chip-list" role="list" aria-label="改动文件列表">
              {applyRunSummary.changedFiles.map((file) => (
                <span key={file} className="self-evolution-file-chip" role="listitem">
                  {file}
                </span>
              ))}
            </div>
          </div>
        )}
      </section>
    );
  };

  const renderCodeOptimizeDiffPreview = () => {
    if (!directFetchedDiffText && diffArtifactContent.loading && !diffArtifactContent.content) {
      return (
        <section className="self-evolution-optimize-report" aria-label="代码优化 Diff 展示">
          <div className="self-evolution-optimize-head">
            <Text>代码改动详情</Text>
            <Text>正在加载文件内容</Text>
          </div>
          <Paragraph className="self-evolution-px-empty">正在读取代码文件内容...</Paragraph>
        </section>
      );
    }

    if (!directFetchedDiffText && diffArtifactContent.error && !diffArtifactContent.content) {
      return (
        <section className="self-evolution-optimize-report" aria-label="代码优化 Diff 展示">
          <div className="self-evolution-optimize-head">
            <Text>代码改动详情</Text>
          </div>
          <Paragraph className="self-evolution-px-empty">{diffArtifactContent.error}</Paragraph>
        </section>
      );
    }

    if (
      (workflowResults.diffs.loaded || workflowResults.diffs.loading || workflowResults.diffs.error) &&
      !fetchedDiffText
    ) {
      return (
        <section className="self-evolution-optimize-report" aria-label="代码优化 Diff 展示">
          <div className="self-evolution-optimize-head">
            <Text>代码改动详情</Text>
          </div>
          {renderWorkflowResultPayload("diffs")}
        </section>
      );
    }

    const renderTreeNodes = (nodes: DiffFileTreeNode[], depth = 0): ReactNode[] =>
      nodes.map((node) => {
        if (node.nodeType === "dir") {
          const isCollapsed = !!collapsedDiffDirs[node.path];
          return (
            <div key={`dir-${node.path}`}>
              <button
                type="button"
                className={`self-evolution-diff-tree-node is-dir${isCollapsed ? " is-collapsed" : ""}`}
                style={{ paddingLeft: `${depth * 14 + 8}px` }}
                onClick={() =>
                  setCollapsedDiffDirs((prev) => ({
                    ...prev,
                    [node.path]: !prev[node.path],
                  }))
                }
              >
                <span className="self-evolution-diff-tree-icon">{isCollapsed ? "▸" : "▾"}</span>
                <span className="self-evolution-diff-tree-text">{node.name}</span>
              </button>
              {!isCollapsed && renderTreeNodes(node.children, depth + 1)}
            </div>
          );
        }

        const isActive = node.fileId === activeDiffFile?.id;
        return (
          <button
            key={`file-${node.path}-${node.fileId}`}
            type="button"
            className={`self-evolution-diff-tree-node is-file${isActive ? " is-active" : ""}`}
            style={{ paddingLeft: `${depth * 14 + 8}px` }}
            onClick={() => node.fileId && setActiveDiffFileId(node.fileId)}
          >
            <span className="self-evolution-diff-tree-icon">•</span>
            <span className="self-evolution-diff-tree-text">{node.name}</span>
          </button>
        );
      });

    if (!activeDiffFile) {
      return (
        <section className="self-evolution-optimize-report" aria-label="代码优化 Diff 展示">
          <div className="self-evolution-optimize-head">
            <Text>代码改动详情</Text>
          </div>
          <Paragraph className="self-evolution-px-empty">当前没有可展示的变更文件。</Paragraph>
        </section>
      );
    }

    const allLineCount = parsedDiffFiles.reduce((total, file) => total + file.lines.length, 0);
    return (
      <section className="self-evolution-optimize-report" aria-label="代码优化 Diff 展示">
        <div className="self-evolution-optimize-head">
          <Text>代码改动详情</Text>
          <Text>{`文件 ${parsedDiffFiles.length} 个，总代码行 ${allLineCount} 行`}</Text>
        </div>
        <div className="self-evolution-optimize-layout">
          <aside className="self-evolution-optimize-tree" aria-label="变更文件结构">
            <div className="self-evolution-optimize-tree-head">文件结构</div>
            <div className="self-evolution-optimize-tree-body">{renderTreeNodes(diffFileTree)}</div>
          </aside>
          <div className="self-evolution-optimize-viewer" aria-label="变更代码内容">
            <div className="self-evolution-optimize-file-head">
              <Text className="self-evolution-optimize-file-path">{activeDiffFile.displayPath}</Text>
              <Text className="self-evolution-optimize-file-stat">
                {`+${activeDiffFile.additions} / -${activeDiffFile.deletions}`}
              </Text>
            </div>
            <div className="self-evolution-optimize-body">
              <pre className="self-evolution-optimize-diff">
                {activeDiffFile.lines.map((line, index) => {
                  const lineType = getDiffLineType(line);
                  return (
                    <div key={`diff-line-${activeDiffFile.id}-${index}`} className={`self-evolution-diff-line is-${lineType}`}>
                      <span className="self-evolution-diff-line-no">{index + 1}</span>
                      <span className="self-evolution-diff-line-code">{line || " "}</span>
                    </div>
                  );
                })}
              </pre>
            </div>
          </div>
        </div>
      </section>
    );
  };

  const renderAbSingleCategoryBars = (comparison: AbCategoryComparison) => {
    const width = 700;
    const height = 300;
    const padding = { top: 24, right: 24, bottom: 58, left: 44 };
    const chartWidth = width - padding.left - padding.right;
    const chartHeight = height - padding.top - padding.bottom;
    const yToPx = (value: number) => padding.top + (1 - clampScore(value)) * chartHeight;
    const ticks = [0, 0.25, 0.5, 0.75, 1];
    const groupWidth = chartWidth / pxMetricMeta.length;
    const barWidth = Math.min(24, groupWidth * 0.28);
    const aColor = "#7f97ba";
    const bColor = "#1a73e8";

    return (
      <div className="self-evolution-ab-chart-wrap">
        <svg className="self-evolution-ab-single-chart" viewBox={`0 0 ${width} ${height}`} role="img">
          <title>{`${comparison.category} A/B 指标对比`}</title>
          {ticks.map((tick) => {
            const y = yToPx(tick);
            return (
              <g key={`ab-single-tick-${tick}`}>
                <line
                  x1={padding.left}
                  y1={y}
                  x2={width - padding.right}
                  y2={y}
                  className="self-evolution-px-grid-line"
                />
                <text x={padding.left - 8} y={y + 4} textAnchor="end" className="self-evolution-px-axis-label">
                  {tick.toFixed(2)}
                </text>
              </g>
            );
          })}

          {pxMetricMeta.map((metric, index) => {
            const groupCenter = padding.left + groupWidth * index + groupWidth / 2;
            const baselineValue = comparison.baseline[metric.key];
            const experimentValue = comparison.experiment[metric.key];
            const baselineY = yToPx(baselineValue);
            const experimentY = yToPx(experimentValue);
            const delta = comparison.delta[metric.key];
            return (
              <g key={`ab-single-group-${metric.key}`}>
                <rect
                  x={groupCenter - barWidth - 4}
                  y={baselineY}
                  width={barWidth}
                  height={padding.top + chartHeight - baselineY}
                  fill={aColor}
                  rx={3}
                />
                <rect
                  x={groupCenter + 4}
                  y={experimentY}
                  width={barWidth}
                  height={padding.top + chartHeight - experimentY}
                  fill={bColor}
                  rx={3}
                />
                <text
                  x={groupCenter}
                  y={Math.min(baselineY, experimentY) - 8}
                  textAnchor="middle"
                  className={`self-evolution-ab-delta-text${delta >= 0 ? " is-up" : " is-down"}`}
                >
                  {`${delta >= 0 ? "+" : ""}${(delta * 100).toFixed(1)}%`}
                </text>
                <text
                  x={groupCenter}
                  y={height - 16}
                  textAnchor="middle"
                  className="self-evolution-px-axis-label"
                >
                  {metric.label}
                </text>
              </g>
            );
          })}
        </svg>

        <div className="self-evolution-ab-legend">
          <span className="self-evolution-ab-legend-item">
            <span className="self-evolution-ab-legend-dot is-a" />
            A 评测（基线）
          </span>
          <span className="self-evolution-ab-legend-item">
            <span className="self-evolution-ab-legend-dot is-b" />
            B 评测（优化后）
          </span>
        </div>
      </div>
    );
  };

  const renderAbFacetCharts = (comparisons: AbCategoryComparison[]) => {
    const aColor = "#7f97ba";
    const bColor = "#1a73e8";
    return (
      <div className="self-evolution-ab-facet-grid">
        {pxMetricMeta.map((metric) => {
          const width = Math.max(320, comparisons.length * 96);
          const height = 220;
          const padding = { top: 20, right: 16, bottom: 54, left: 36 };
          const chartWidth = width - padding.left - padding.right;
          const chartHeight = height - padding.top - padding.bottom;
          const yToPx = (value: number) => padding.top + (1 - clampScore(value)) * chartHeight;
          const ticks = [0, 0.5, 1];
          const groupWidth = chartWidth / Math.max(comparisons.length, 1);
          const barWidth = Math.min(14, groupWidth * 0.24);

          return (
            <div key={`ab-facet-${metric.key}`} className="self-evolution-ab-facet-card">
              <div className="self-evolution-ab-facet-title">{metric.label}</div>
              <div className="self-evolution-ab-facet-scroller">
                <svg className="self-evolution-ab-facet-chart" viewBox={`0 0 ${width} ${height}`} role="img">
                  <title>{`${metric.label} 分类型 A/B 对比`}</title>
                  {ticks.map((tick) => {
                    const y = yToPx(tick);
                    return (
                      <g key={`ab-facet-${metric.key}-${tick}`}>
                        <line
                          x1={padding.left}
                          y1={y}
                          x2={width - padding.right}
                          y2={y}
                          className="self-evolution-px-grid-line"
                        />
                        <text x={padding.left - 6} y={y + 4} textAnchor="end" className="self-evolution-px-axis-label">
                          {tick.toFixed(1)}
                        </text>
                      </g>
                    );
                  })}
                  {comparisons.map((comparison, index) => {
                    const groupCenter = padding.left + groupWidth * index + groupWidth / 2;
                    const aValue = comparison.baseline[metric.key];
                    const bValue = comparison.experiment[metric.key];
                    const aY = yToPx(aValue);
                    const bY = yToPx(bValue);
                    return (
                      <g key={`ab-facet-group-${metric.key}-${comparison.category}`}>
                        <rect
                          x={groupCenter - barWidth - 3}
                          y={aY}
                          width={barWidth}
                          height={padding.top + chartHeight - aY}
                          fill={aColor}
                          rx={2}
                        />
                        <rect
                          x={groupCenter + 3}
                          y={bY}
                          width={barWidth}
                          height={padding.top + chartHeight - bY}
                          fill={bColor}
                          rx={2}
                        />
                        <text
                          x={groupCenter}
                          y={height - 16}
                          textAnchor="middle"
                          className="self-evolution-px-axis-label"
                        >
                          {getShortLabel(comparison.category, 4)}
                        </text>
                      </g>
                    );
                  })}
                </svg>
              </div>
            </div>
          );
        })}

        <div className="self-evolution-ab-legend is-facet">
          <span className="self-evolution-ab-legend-item">
            <span className="self-evolution-ab-legend-dot is-a" />
            A 评测（基线）
          </span>
          <span className="self-evolution-ab-legend-item">
            <span className="self-evolution-ab-legend-dot is-b" />
            B 评测（优化后）
          </span>
        </div>
      </div>
    );
  };

  const renderAbSummaryMetricChart = (rows: AbSummaryMetricRow[]) => {
    const width = Math.max(620, rows.length * 132);
    const height = 300;
    const padding = { top: 28, right: 24, bottom: 62, left: 44 };
    const chartWidth = width - padding.left - padding.right;
    const chartHeight = height - padding.top - padding.bottom;
    const yToPx = (value: number) => padding.top + (1 - clampScore(value)) * chartHeight;
    const ticks = [0, 0.25, 0.5, 0.75, 1];
    const groupWidth = chartWidth / Math.max(rows.length, 1);
    const barWidth = Math.min(24, groupWidth * 0.26);
    const aColor = "#7f97ba";
    const bColor = "#1a73e8";

    return (
      <div className="self-evolution-ab-summary-chart-scroller">
        <svg className="self-evolution-ab-summary-chart" viewBox={`0 0 ${width} ${height}`} role="img">
          <title>A/B 测试 summary 指标对比</title>
          {ticks.map((tick) => {
            const y = yToPx(tick);
            return (
              <g key={`ab-summary-tick-${tick}`}>
                <line
                  x1={padding.left}
                  y1={y}
                  x2={width - padding.right}
                  y2={y}
                  className="self-evolution-px-grid-line"
                />
                <text x={padding.left - 8} y={y + 4} textAnchor="end" className="self-evolution-px-axis-label">
                  {tick.toFixed(2)}
                </text>
              </g>
            );
          })}
          {rows.map((row, index) => {
            const groupCenter = padding.left + groupWidth * index + groupWidth / 2;
            const meanAY = yToPx(row.meanA);
            const meanBY = yToPx(row.meanB);
            return (
              <g key={`ab-summary-group-${row.key}`}>
                <rect
                  x={groupCenter - barWidth - 4}
                  y={meanAY}
                  width={barWidth}
                  height={padding.top + chartHeight - meanAY}
                  fill={aColor}
                  rx={3}
                />
                <rect
                  x={groupCenter + 4}
                  y={meanBY}
                  width={barWidth}
                  height={padding.top + chartHeight - meanBY}
                  fill={bColor}
                  rx={3}
                />
                <text
                  x={groupCenter}
                  y={Math.min(meanAY, meanBY) - 8}
                  textAnchor="middle"
                  className={`self-evolution-ab-delta-text${row.deltaMean >= 0 ? " is-up" : " is-down"}`}
                >
                  {`${row.deltaMean >= 0 ? "+" : ""}${(row.deltaMean * 100).toFixed(1)}%`}
                </text>
                <text x={groupCenter} y={height - 28} textAnchor="middle" className="self-evolution-px-axis-label">
                  {getShortLabel(row.metricLabel, 7)}
                </text>
                <text x={groupCenter} y={height - 12} textAnchor="middle" className="self-evolution-px-axis-label">
                  {`胜率 ${formatPercent(row.winRateB)}`}
                </text>
              </g>
            );
          })}
        </svg>
      </div>
    );
  };

  const renderAbSummaryReport = (report: AbSummaryReport) => {
    const metricColumns: ColumnsType<AbSummaryMetricRow> = [
      { title: "指标", dataIndex: "metricLabel", key: "metricLabel", width: 150 },
      { title: "mean A", dataIndex: "meanA", key: "meanA", width: 110, render: (value: number) => formatPercent(value) },
      { title: "mean B", dataIndex: "meanB", key: "meanB", width: 110, render: (value: number) => formatPercent(value) },
      {
        title: "Δmean",
        dataIndex: "deltaMean",
        key: "deltaMean",
        width: 110,
        render: (value: number) => <span className={value >= 0 ? "is-up" : "is-down"}>{formatMetricDelta(value)}</span>,
      },
      { title: "B 胜率", dataIndex: "winRateB", key: "winRateB", width: 110, render: (value: number) => formatPercent(value) },
      { title: "sign p", dataIndex: "signP", key: "signP", width: 100, render: (value: number | null | undefined) => formatMaybePValue(value) },
    ];
    const topDiffColumns: ColumnsType<AbTopDiffRow> = [
      {
        title: "case",
        dataIndex: "caseKey",
        key: "caseKey",
        width: 280,
        render: (value: string) => (
          <span className="self-evolution-table-ellipsis" title={value}>
            {value}
          </span>
        ),
      },
      { title: "A", dataIndex: "a", key: "a", width: 90 },
      { title: "B", dataIndex: "b", key: "b", width: 90 },
      {
        title: "Δ",
        dataIndex: "delta",
        key: "delta",
        width: 90,
        render: (value: number) => <span className={value >= 0 ? "is-up" : "is-down"}>{value}</span>,
      },
    ];

    return (
      <div key={report.id} className="self-evolution-ab-summary-report">
        <div className="self-evolution-ab-summary-head">
          <div>
            <Text strong>{report.id}</Text>
            <div className="self-evolution-ab-summary-meta">
              {report.alignedCases !== undefined && <span>{`对齐样本 ${report.alignedCases}`}</span>}
              {report.primaryMetric && <span>{`主指标 ${formatAbMetricLabel(report.primaryMetric)}`}</span>}
              {report.guardMetrics.length > 0 && (
                <span>{`保护指标 ${report.guardMetrics.map(formatAbMetricLabel).join(" / ")}`}</span>
              )}
            </div>
          </div>
          {report.verdict && <Tag color={report.verdict === "pass" ? "success" : "warning"}>{report.verdict}</Tag>}
        </div>

        {report.metricRows.length > 0 && (
          <div className="self-evolution-ab-chart-shell">
            {renderAbSummaryMetricChart(report.metricRows)}
            <div className="self-evolution-ab-legend">
              <span className="self-evolution-ab-legend-item">
                <span className="self-evolution-ab-legend-dot is-a" />
                A 评测（基线）
              </span>
              <span className="self-evolution-ab-legend-item">
                <span className="self-evolution-ab-legend-dot is-b" />
                B 评测（优化后）
              </span>
            </div>
          </div>
        )}

        {report.metricRows.length > 0 && (
          <Table<AbSummaryMetricRow>
            className="self-evolution-dataset-table self-evolution-ab-table self-evolution-ab-summary-table"
            size="small"
            rowKey="key"
            columns={metricColumns}
            dataSource={report.metricRows}
            pagination={false}
            scroll={{ x: 690 }}
          />
        )}

        {report.markdown && (
          <div className="self-evolution-ab-markdown">
            <div className="self-evolution-ab-section-title">Markdown 报告</div>
            <div className="self-evolution-ab-markdown-body">
              <MarkdownViewer>{report.markdown}</MarkdownViewer>
            </div>
          </div>
        )}

        {report.topDiffRows.length > 0 && (
          <div className="self-evolution-ab-top-diff">
            <div className="self-evolution-ab-section-title">Top diff cases</div>
            <Table<AbTopDiffRow>
              className="self-evolution-dataset-table self-evolution-ab-table"
              size="small"
              rowKey="key"
              columns={topDiffColumns}
              dataSource={report.topDiffRows}
              pagination={false}
              scroll={{ x: 550 }}
            />
          </div>
        )}

        {(report.reasons.length > 0 || report.missingMetrics.length > 0) && (
          <div className="self-evolution-ab-reasons">
            {report.reasons.map((reason) => (
              <span key={`reason-${report.id}-${reason}`}>{reason}</span>
            ))}
            {report.missingMetrics.length > 0 && <span>{`缺失指标：${report.missingMetrics.join(" / ")}`}</span>}
          </div>
        )}
      </div>
    );
  };

  const renderAbTestPreview = () => (
    <section className="self-evolution-ab-report" aria-label="A/B 对比展示">
      {workflowResults.abtests.loaded && abSummaryReports.length > 0 ? (
        <>
          <div className="self-evolution-ab-head">
            <Text>ABTest 对照报告</Text>
            <Text>{`当前展示 ${abSummaryReports.length} 条`}</Text>
          </div>
          <div className="self-evolution-ab-summary-list">{abSummaryReports.map(renderAbSummaryReport)}</div>
        </>
      ) : workflowResults.abtests.loaded || workflowResults.abtests.loading || workflowResults.abtests.error ? (
        renderWorkflowResultPayload("abtests")
      ) : (
        <>
      <div className="self-evolution-ab-head">
        <Text>对照结果明细</Text>
        <Text>{`当前展示 ${abComparisonRows.length} / 共 ${abCategoryComparisons.length} 条`}</Text>
      </div>
      {abCategoryComparisons.length === 0 ? (
        <Paragraph className="self-evolution-px-empty">暂无可用 A/B 对比数据。</Paragraph>
      ) : (
        <>
          <Table<AbComparisonRow>
            className="self-evolution-dataset-table self-evolution-ab-table"
            size="small"
            rowKey="key"
            columns={abComparisonColumns}
            dataSource={abComparisonRows}
            pagination={false}
            scroll={{ x: 1100, y: 320 }}
          />
          <div className="self-evolution-ab-chart-shell">
            {isSingleAbCategory
              ? renderAbSingleCategoryBars(abCategoryComparisons[0])
              : renderAbFacetCharts(abCategoryComparisons)}
          </div>
        </>
      )}
        </>
      )}
    </section>
  );

  if (isWorkbenchVisible) {
    return (
      <div className="self-evolution-session-page">
        <div className="self-evolution-workbench">
          <section className="self-evolution-workflow-panel" aria-label="执行步骤">
            <div className="self-evolution-workflow-head">
              <Title level={3}>自进化执行编排</Title>
              <Paragraph>当前聚焦：{activeStepText}</Paragraph>
              {routeThreadId && (
                <Text className="self-evolution-detail-thread">
                  {`线程 ID：${routeThreadId}${isRestoringThread ? " · 正在恢复详情" : ""}`}
                </Text>
              )}
              {threadRestoreError && routeThreadId && (
                <div className="self-evolution-restore-error" role="alert">
                  <span>{threadRestoreError}</span>
                  <button
                    type="button"
                    onClick={() => {
                      const controller = new AbortController();
                      void restoreThreadDetail(routeThreadId, controller.signal);
                    }}
                  >
                    重试
                  </button>
                </div>
              )}
            </div>

            <div className="self-evolution-step-list">
              <div className="self-evolution-step-scroll">
                {workflowSteps.map((step, index) => (
                  <article
                    key={step.renderKey || step.id}
                    className={`self-evolution-step-card is-${step.status}`}
                    style={{ animationDelay: `${index * 70}ms` }}
                  >
                    <div className="self-evolution-step-main">
                      <div className="self-evolution-step-title-row">
                        <Text className="self-evolution-step-title">{step.title}</Text>
                        <span className={`self-evolution-step-status is-${step.status}`}>
                          {step.status === "done" && <CheckCircleFilled />}
                          {step.status === "running" && <ClockCircleFilled />}
                          {step.status === "paused" && <ClockCircleFilled />}
                          {step.status === "canceled" && <CloseOutlined />}
                          {step.status === "pending" && <FileTextOutlined />}
                          <span>{getStepStatusLabel(step.status)}</span>
                        </span>
                      </div>
                      <Paragraph className="self-evolution-step-desc">{step.desc}</Paragraph>
                      {step.progress && (
                        <div className="self-evolution-step-progress" aria-label={`${step.title}进度`}>
                          <div className="self-evolution-step-progress-meta">
                            <span>{`状态：${step.progress.statusText}`}</span>
                            <strong>{`${step.progress.percent}%`}</strong>
                          </div>
                          <div className="self-evolution-step-progress-track">
                            <span style={{ width: `${step.progress.percent}%` }} />
                          </div>
                        </div>
                      )}
                      {step.id === "analysis" && renderAnalysisRuntimeSummary()}
                      {step.id === "code-optimize" && renderApplyRuntimeSummary()}
                      {step.runtimeText &&
                        !((step.id === "analysis" && analysisRunSummary) ||
                          (step.id === "code-optimize" && applyRunSummary)) && (
                        <Paragraph className="self-evolution-step-runtime">{step.runtimeText}</Paragraph>
                      )}
                      {step.id === "dataset" && (
                        <section className="self-evolution-dataset-static-block" aria-label="数据集结果展示">
                          <div className="self-evolution-dataset-static-head">
                            <span>数据集结果仅支持下载查看</span>
                            <a
                              className="self-evolution-dataset-download-link"
                              href={datasetResultDownloadUrl || datasetDownloadUrl || undefined}
                              download={getDownloadFileName(
                                datasetResultDownloadUrl || datasetDownloadUrl,
                                datasetDownloadFileName,
                              )}
                              onClick={(event) =>
                                void handleWorkflowDownload(
                                  "datasets",
                                  datasetDownloadUrl,
                                  datasetDownloadFileName,
                                  event,
                                )
                              }
                            >
                              下载查看
                            </a>
                          </div>
                        </section>
                      )}
                      {step.id === "px-report" && (
                        <Collapse
                          className="self-evolution-dataset-collapse self-evolution-px-collapse"
                          bordered={false}
                          onChange={handleWorkflowResultCollapseChange("eval-reports")}
                          items={[
                            {
                              key: "px-report-preview",
                              label: (
                                <span className="self-evolution-dataset-collapse-label">
                                  <span>
                                    {pxReportCategoryMetrics.length === 0
                                      ? "查看评测图表"
                                      : isSinglePxCategory
                                        ? "查看评测图表（单分类饼图）"
                                        : "查看评测图表（多分类折线图）"}
                                  </span>
                                  <a
                                    className="self-evolution-dataset-download-link"
                                    href={evalReportDownloadUrl || undefined}
                                    download={getDownloadFileName(evalReportDownloadUrl, "eval-report.json")}
                                    onClick={(event) =>
                                      void handleWorkflowDownload(
                                        "eval-reports",
                                        "",
                                        "eval-report.json",
                                        event,
                                      )
                                    }
                                  >
                                    下载查看
                                  </a>
                                </span>
                              ),
                              children: renderPxReportPreview(),
                            },
                          ]}
                        />
                      )}
                      {step.id === "analysis" && (
                        <Collapse
                          className="self-evolution-dataset-collapse self-evolution-analysis-collapse"
                          bordered={false}
                          onChange={handleWorkflowResultCollapseChange("analysis-reports")}
                          items={[
                            {
                              key: "analysis-report-preview",
                              label: "查看完整分析报告",
                              children: renderAnalysisReportPreview(),
                            },
                          ]}
                        />
                      )}
                      {step.id === "code-optimize" && (
                        <Collapse
                          className="self-evolution-dataset-collapse self-evolution-optimize-collapse"
                          bordered={false}
                          onChange={handleWorkflowResultCollapseChange("diffs")}
                          items={[
                            {
                              key: "code-optimize-diff-preview",
                              label: (
                                <span className="self-evolution-dataset-collapse-label">
                                  <span>查看代码改动详情</span>
                                  <a
                                    className="self-evolution-dataset-download-link"
                                    href={diffResultDownloadUrl || undefined}
                                    download={getDownloadFileName(diffResultDownloadUrl, "code-diff.diff")}
                                    onClick={(event) =>
                                      void handleWorkflowDownload("diffs", diffResultDownloadUrl, "code-diff.diff", event)
                                    }
                                  >
                                    下载查看
                                  </a>
                                </span>
                              ),
                              children: renderCodeOptimizeDiffPreview(),
                            },
                          ]}
                        />
                      )}
                      {step.id === "ab-test" && (
                        <Collapse
                          className="self-evolution-dataset-collapse self-evolution-ab-collapse"
                          bordered={false}
                          onChange={handleWorkflowResultCollapseChange("abtests")}
                          items={[
                            {
                              key: "ab-test-preview",
                              label: (
                                <span className="self-evolution-dataset-collapse-label">
                                  <span>{`查看 A/B 测试结果（${abSummaryReports.length || abComparisonRows.length}/${abSummaryReports.length || abCategoryComparisons.length}）`}</span>
                                  <a
                                    className="self-evolution-dataset-download-link"
                                    href={abtestResultDownloadUrl || abComparisonDownloadUrl || undefined}
                                    download={getDownloadFileName(
                                      abtestResultDownloadUrl || abComparisonDownloadUrl,
                                      "ab-test-comparison.json",
                                    )}
                                    onClick={(event) =>
                                      void handleWorkflowDownload(
                                        "abtests",
                                        abComparisonDownloadUrl,
                                        "ab-test-comparison.json",
                                        event,
                                      )
                                    }
                                  >
                                    下载查看
                                  </a>
                                </span>
                              ),
                              children: renderAbTestPreview(),
                            },
                          ]}
                        />
                      )}
                    </div>
                  </article>
                ))}
              </div>
            </div>
          </section>

          <section className="self-evolution-chat-panel" aria-label="历史会话窗口">
            <div className="self-evolution-history-shell">
              <div className="self-evolution-history-tabs" aria-label="历史会话标签栏">
                <div className="self-evolution-history-tabs-scroll">
                  <button
                    type="button"
                    className="self-evolution-history-tab is-active"
                    title={activeSession.title}
                  >
                    <span className="self-evolution-history-tab-icon">
                      <MessageOutlined />
                    </span>
                    <span className="self-evolution-history-tab-content">
                      <span className="self-evolution-history-tab-label">{activeSession.title}</span>
                    </span>
                    {chatSessions.length > 1 && (
                      <span
                        className="self-evolution-history-tab-close"
                        onClick={(event) => {
                          event.stopPropagation();
                          onCloseSession(activeSession.id);
                        }}
                      >
                        <CloseOutlined />
                      </span>
                    )}
                  </button>
                  {historySessionEntries.map((entry) => (
                    <HistorySessionTab
                      key={entry.key}
                      entry={entry}
                      isDeleting={deletingHistoryKeys.includes(entry.key)}
                      onSelect={onSelectHistorySession}
                      onDelete={onDeleteHistorySession}
                    />
                  ))}
                </div>
                <button
                  type="button"
                  className="self-evolution-history-tab-create"
                  onClick={onCreateSession}
                  title="新建会话"
                >
                  <PlusOutlined />
                  <span>新建</span>
                </button>
                <button
                  type="button"
                  className="self-evolution-history-tab-fetch"
                  onClick={onOpenHistorySessionModal}
                  title="打开历史会话列表"
                  aria-label="打开历史会话列表"
                >
                  <HistoryOutlined />
                  <span>历史</span>
                </button>
              </div>
            </div>

            <ChatMessageStream messages={displayedMessages} streamRef={chatStreamRef} />

            <ChatComposer
              activeStepText={activeStepText}
              isAutoInteractionActive={isAutoInteractionActive}
              isSendingMessage={isSendingMessage}
              pendingCheckpointWaitPrompt={displayedCheckpointWaitPrompt}
              prompt={prompt}
              onPromptChange={setPrompt}
              onSend={(command) => void onSend(command)}
              renderKnowledgeAndModeTools={renderKnowledgeAndModeTools}
              renderSendButton={renderSendButton}
            />
          </section>

          <Modal
            open={isHistorySessionModalOpen}
            onCancel={() => setIsHistorySessionModalOpen(false)}
            footer={null}
            width={720}
            centered
            className="self-evolution-history-modal"
            title={null}
          >
            <section className="self-evolution-history-modal-shell" aria-label="历史会话选择">
              <header className="self-evolution-history-modal-head">
                <div className="self-evolution-history-modal-copy">
                  <Text className="self-evolution-history-modal-kicker">历史会话</Text>
                  <Title level={4} className="self-evolution-history-modal-title">
                    选择要进入的会话
                  </Title>
                  <Text className="self-evolution-history-modal-subtitle">
                    从服务端会话列表选择线程，进入后会自动恢复详情、历史消息与执行记录。
                  </Text>
                </div>
              </header>

              {threadHistoryListError && (
                <div className="self-evolution-history-modal-alert">
                  <span>{threadHistoryListError}</span>
                  <button type="button" onClick={() => void fetchThreadHistoryList()}>
                    重试
                  </button>
                </div>
              )}

              <div className="self-evolution-history-modal-list" role="list" aria-label="历史会话列表">
                {isLoadingThreadHistoryList && historySessionEntries.length === 0 ? (
                  <div className="self-evolution-history-modal-empty is-loading">
                    <LoadingOutlined spin />
                    <Text>正在获取历史会话...</Text>
                  </div>
                ) : historySessionEntries.length > 0 ? (
                  historySessionEntries.map((entry) => (
                    <HistorySessionItem
                      key={entry.key}
                      entry={entry}
                      isDeleting={deletingHistoryKeys.includes(entry.key)}
                      onSelect={onSelectHistorySession}
                      onDelete={onDeleteHistorySession}
                    />
                  ))
                ) : (
                  <div className="self-evolution-history-modal-empty">
                    <Text>还没有可选择的历史会话。</Text>
                  </div>
                )}
              </div>
            </section>
          </Modal>

          <Modal
            open={isNewSessionConfigOpen}
            onCancel={onCancelCreateSession}
            footer={null}
            width={980}
            centered
            maskClosable={false}
            className="self-evolution-new-session-modal"
            destroyOnClose={false}
            title={null}
          >
            <section className="self-evolution-new-session-shell" aria-label="新会话五步配置">
              <header className="self-evolution-new-session-head">
                <Text className="self-evolution-new-session-kicker">新会话 · 五步重选</Text>
                <Title level={4} className="self-evolution-new-session-title">
                  创建前请重新确认本轮配置
                </Title>
                <Text className="self-evolution-new-session-subtitle">
                  1-4 步为必选项，第 5 步确认后会创建新会话并自动进入 Step 1。
                </Text>
              </header>

              <div className="self-evolution-new-session-step-rail" aria-label="五步流程状态">
                <span className={`self-evolution-new-session-step-chip${isNewSessionStepOneDone ? " is-done" : ""}`}>
                  1. 选择知识库
                </span>
                <span className={`self-evolution-new-session-step-chip${isNewSessionStepTwoDone ? " is-done" : ""}`}>
                  2. 已有评测集
                </span>
                <span className={`self-evolution-new-session-step-chip${isNewSessionStepThreeDone ? " is-done" : ""}`}>
                  3. 补充评测集
                </span>
                <span className={`self-evolution-new-session-step-chip${isNewSessionStepFourDone ? " is-done" : ""}`}>
                  4. 过程干预
                </span>
                <span className="self-evolution-new-session-step-chip is-focus">5. 开始</span>
              </div>

              <div
                className="self-evolution-launch-compact-grid self-evolution-new-session-grid"
                role="list"
                aria-label="新会话启动配置"
              >
                {newSessionOptionCards.map((item) => (
                  <article
                    key={item.key}
                    className={`self-evolution-launch-compact-item ${item.toneClassName}${item.isHighlighted ? " is-highlighted" : ""}`}
                    role="listitem"
                  >
                    <div className="self-evolution-launch-compact-meta">
                      <span className="self-evolution-launch-card-icon" aria-hidden>
                        {item.icon}
                      </span>
                      <div className="self-evolution-launch-compact-copy">
                        <Text className="self-evolution-launch-card-title">{item.title}</Text>
                        <Text className="self-evolution-launch-card-current-value">当前：{item.currentValue}</Text>
                        <Text
                          className={`self-evolution-launch-compact-desc${item.isDescSingleLine ? " is-single-line" : ""}`}
                        >
                          {item.description}
                        </Text>
                      </div>
                    </div>
                    {item.control}
                  </article>
                ))}
              </div>

              <footer className="self-evolution-launch-start-bar self-evolution-new-session-start-bar">
                <div className="self-evolution-launch-start-copy">
                  <Text className="self-evolution-launch-start-step">5. 开始</Text>
                  <Text className="self-evolution-launch-start-title">确认后启动新会话流程</Text>
                  <div className="self-evolution-launch-summary" aria-label="新会话配置摘要">
                    {newSessionSummaryItems.map((item) => (
                      <div key={item.label} className="self-evolution-launch-summary-pill">
                        <Text className="self-evolution-launch-summary-label">{item.label}</Text>
                        <Text className="self-evolution-launch-summary-value">{item.value}</Text>
                      </div>
                    ))}
                  </div>
                </div>

                <div className="self-evolution-new-session-actions">
                  <button
                    type="button"
                    className="self-evolution-new-session-cancel"
                    onClick={onCancelCreateSession}
                  >
                    取消
                  </button>
                  <button
                    type="button"
                    className="self-evolution-chatlike-start-button"
                    onClick={onConfirmCreateSession}
                    disabled={!isNewSessionDraftValid}
                  >
                    开始新会话
                  </button>
                </div>
              </footer>
            </section>
          </Modal>
        </div>
      </div>
    );
  }

  return (
    <div className="self-evolution-chatlike-page admin-page">
      <header className="self-evolution-chatlike-top">
        <Tag color="blue" className="self-evolution-chatlike-tag">
          单线程会话
        </Tag>
        <div className="self-evolution-chatlike-top-actions">
          <button
            type="button"
            className="self-evolution-chatlike-top-history"
            onClick={onOpenHistorySessionModal}
            aria-label="打开历史会话列表"
          >
            {isLoadingThreadHistoryList ? <LoadingOutlined spin /> : <HistoryOutlined />}
            <span>历史会话</span>
          </button>
        </div>
      </header>

      <section className="self-evolution-welcome-container" aria-label="欢迎与配置">
        <div className="self-evolution-welcome-shell">
          <figure className="self-evolution-welcome-visual">
            <img
              className="self-evolution-welcome-visual-image"
              src={workflowImageSrc}
              alt="自进化系统五步流程示意图：生成数据集、评测报告、分析报告、代码优化、A/B 测试"
            />
            <figcaption className="self-evolution-welcome-visual-meta">
              <Text className="self-evolution-welcome-visual-title">自进化执行路径</Text>
              <div className="self-evolution-welcome-visual-badges" role="list" aria-label="流程状态">
                {workflowSteps.map((step) => (
                  <span
                    key={`welcome-badge-${step.renderKey || step.id}`}
                    className={`self-evolution-welcome-visual-badge is-${step.status}`}
                    role="listitem"
                  >
                    {step.title}
                  </span>
                ))}
              </div>
            </figcaption>
          </figure>

          <div className="self-evolution-chatlike-launchpad-content">
            <div className="self-evolution-chatlike-launchpad-header">
              <Text className="self-evolution-chatlike-launchpad-kicker">启动配置</Text>
              <Paragraph className="self-evolution-chatlike-launchpad-subtitle">
                选择知识库、评测集和干预方式后即可开始。
              </Paragraph>
            </div>

            <div className="self-evolution-launch-compact-grid" role="list" aria-label="启动配置选项">
              {launchOptionCards.map((item) => (
                <article
                  key={item.key}
                  className={`self-evolution-launch-compact-item ${item.toneClassName}${item.isHighlighted ? " is-highlighted" : ""}`}
                  role="listitem"
                >
                  <div className="self-evolution-launch-compact-meta">
                    <span className="self-evolution-launch-card-icon" aria-hidden>
                      {item.icon}
                    </span>
                    <div className="self-evolution-launch-compact-copy">
                      <Text className="self-evolution-launch-card-title">{item.title}</Text>
                      <Text className="self-evolution-launch-card-current-value">当前：{item.currentValue}</Text>
                      <Text
                        className={`self-evolution-launch-compact-desc${item.isDescSingleLine ? " is-single-line" : ""}`}
                      >
                        {item.description}
                      </Text>
                    </div>
                  </div>
                  {item.control}
                </article>
              ))}
            </div>

            <div className="self-evolution-launch-start-bar" aria-labelledby="self-evolution-launch-start-title">
              <div className="self-evolution-launch-start-copy">
                <Text className="self-evolution-launch-start-step">5. 开始</Text>
                <Text className="self-evolution-launch-start-title" id="self-evolution-launch-start-title">
                  确认后启动本轮优化
                </Text>
                <div className="self-evolution-launch-summary" id="self-evolution-launch-summary" aria-label="当前配置摘要">
                  {launchSummaryItems.map((item) => (
                    <div key={item.label} className="self-evolution-launch-summary-pill">
                      <Text className="self-evolution-launch-summary-label">{item.label}</Text>
                      <Text className="self-evolution-launch-summary-value">{item.value}</Text>
                    </div>
                  ))}
                </div>
              </div>

              <button
                type="button"
                className="self-evolution-chatlike-start-button"
                onClick={onStartSession}
                disabled={!isLaunchConfigValid || isStartingSession}
                aria-describedby="self-evolution-launch-summary"
              >
                {isStartingSession ? "启动中..." : "开始"}
              </button>
            </div>
          </div>
        </div>
      </section>

      <Modal
        open={isHistorySessionModalOpen}
        onCancel={() => setIsHistorySessionModalOpen(false)}
        footer={null}
        width={720}
        centered
        className="self-evolution-history-modal"
        title={null}
      >
        <section className="self-evolution-history-modal-shell" aria-label="历史会话选择">
          <header className="self-evolution-history-modal-head">
            <div className="self-evolution-history-modal-copy">
              <Text className="self-evolution-history-modal-kicker">历史会话</Text>
              <Title level={4} className="self-evolution-history-modal-title">
                选择要进入的会话
              </Title>
              <Text className="self-evolution-history-modal-subtitle">
                从服务端会话列表选择线程，进入后会自动恢复详情、历史消息与执行记录。
              </Text>
            </div>
          </header>

          {threadHistoryListError && (
            <div className="self-evolution-history-modal-alert">
              <span>{threadHistoryListError}</span>
              <button type="button" onClick={() => void fetchThreadHistoryList()}>
                重试
              </button>
            </div>
          )}

          <div className="self-evolution-history-modal-list" role="list" aria-label="历史会话列表">
            {isLoadingThreadHistoryList && historySessionEntries.length === 0 ? (
              <div className="self-evolution-history-modal-empty is-loading">
                <LoadingOutlined spin />
                <Text>正在获取历史会话...</Text>
              </div>
            ) : historySessionEntries.length > 0 ? (
              historySessionEntries.map((entry) => (
                <HistorySessionItem
                  key={entry.key}
                  entry={entry}
                  isDeleting={deletingHistoryKeys.includes(entry.key)}
                  onSelect={onSelectHistorySession}
                  onDelete={onDeleteHistorySession}
                />
              ))
            ) : (
              <div className="self-evolution-history-modal-empty">
                <Text>还没有可选择的历史会话。</Text>
              </div>
            )}
          </div>
        </section>
      </Modal>
    </div>
  );
}
