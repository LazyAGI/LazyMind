import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

type TFunction = (key: string, options?: Record<string, unknown>) => string;
import { Alert, Button, Empty, Spin, Table, Tag, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  AimOutlined,
  ArrowLeftOutlined,
  CheckCircleOutlined,
  FileSearchOutlined,
  FileTextOutlined,
  MenuUnfoldOutlined,
  ReloadOutlined,
  SearchOutlined,
  ThunderboltOutlined,
  WarningOutlined,
} from "@ant-design/icons";
import { useNavigate, useOutletContext, useParams } from "react-router-dom";
import { axiosInstance, getLocalizedErrorMessage } from "@/components/request";
import {
  AGENT_API_BASE,
  EVO_API_BASE,
  buildAbSummaryReports,
  createCoreAgentGeneratedApiClient,
  formatMaybePValue,
  getAbtestResultRecords,
  getNestedRecordField,
  getNumberField,
  getResultItems,
  getStringField,
  getStructuredArrayField,
  getStructuredRecordField,
  isCanceledRequest,
  isEmptyResultPayload,
  isRecord,
  stringifyResultPayload,
  type AbSummaryReport,
  type WorkflowResultKind,
} from "../shared";
import {
  normalizeTraceObservation,
  type TraceDetailObservation,
  type TraceObservation,
} from "./TraceObservationView";
import traceCompareFixture from "../fixtures/evo_trace_compare_result.json";
import "../index.scss";

const { Paragraph, Text, Title } = Typography;

type ObservationResultKind = Extract<WorkflowResultKind, "eval-reports" | "abtests">;
type TraceNode = TraceDetailObservation["root"];

type ObservationPageLayoutContext = {
  isMenuCollapsed?: boolean;
  toggleMenu?: () => void;
};

type ObservationHeaderControlsProps = {
  isMenuCollapsed?: boolean;
  toggleMenu?: () => void;
  onBack: () => void;
};

function ObservationHeaderControls({
  isMenuCollapsed,
  toggleMenu,
  onBack,
}: ObservationHeaderControlsProps) {
  const { t } = useTranslation();
  return (
    <div className="self-evolution-observation-head-controls">
      {isMenuCollapsed && toggleMenu ? (
        <Button
          type="text"
          icon={<MenuUnfoldOutlined />}
          onClick={toggleMenu}
          aria-label={t("selfEvolutionRun.observation.expandMenu")}
          title={t("selfEvolutionRun.observation.expandMenu")}
        />
      ) : null}
      <Button type="text" icon={<ArrowLeftOutlined />} onClick={onBack}>
        {t("selfEvolutionRun.observation.back")}
      </Button>
    </div>
  );
}

type ObservationRouteParams = {
  threadId?: string;
  kind?: string;
};

type ObservationPageState = {
  loading: boolean;
  loaded: boolean;
  data?: unknown;
  error?: string;
  notice?: string;
  isFallback?: boolean;
};

type EvalBadcaseListState = {
  reportId?: string;
  loading: boolean;
  loaded: boolean;
  data?: unknown;
  error?: string;
};

type AbCaseListState = {
  abtestId?: string;
  loading: boolean;
  loaded: boolean;
  data?: unknown;
  error?: string;
  totalSize?: number;
};

type AbTraceCompareState = {
  caseId?: string;
  loading: boolean;
  loaded: boolean;
  data?: unknown;
  error?: string;
  aTraceId?: string;
  bTraceId?: string;
};

type EvalReportsTraceState = {
  loading: boolean;
  loaded: boolean;
  data?: unknown;
};

type CsvBadcaseRow = {
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

type EvalReportSummary = {
  reportId: string;
  dataset: string;
  correctRate?: number;
  badCaseCount?: number;
  traceCoverageRate?: number;
};

type TraceDocRow = {
  key: string;
  title: string;
  ref: string;
  score?: number;
  text: string;
};

type FlowRow = {
  key: string;
  round: number;
  title: string;
  desc: string;
  duration: string;
  tone: "normal" | "warning" | "success";
  node: TraceNode;
};

type AbCompareObservation = Extract<TraceObservation, { kind: "compare" }>;

type AbCaseRow = {
  caseId: string;
  query: string;
  aScore: number;
  bScore: number;
  delta: number;
  conclusion: string;
  tone: "up" | "down" | "flat";
};

type AbMetricRow = {
  key: string;
  label: string;
  meanA: number;
  meanB: number;
  winRate: number;
  signP?: string;
};

const observationKindMap: Record<string, ObservationResultKind> = {
  eval: "eval-reports",
  "eval-reports": "eval-reports",
  abtest: "abtests",
  abtests: "abtests",
};

const fallbackObservationData: Partial<Record<ObservationResultKind, unknown>> = {
  abtests: traceCompareFixture,
};
const EVAL_BADCASE_PAGE_SIZE = 10;
const AB_CASE_DETAIL_PAGE_SIZE = 10;
const syntheticAbtestIdPattern = /^abtest-\d+$/;

function getFallbackAbCaseRows(t: TFunction): AbCaseRow[] {
  return [
    {
      caseId: "case-083",
      query: t("selfEvolutionRun.observation.fallbackQueryInvoice"),
      aScore: 0.36,
      bScore: 0.42,
      delta: 0.06,
      conclusion: t("selfEvolutionRun.observation.fallbackConclusionSlightImprove"),
      tone: "up",
    },
    {
      caseId: "case-019",
      query: t("selfEvolutionRun.observation.fallbackQueryPassword"),
      aScore: 0.58,
      bScore: 0.46,
      delta: -0.12,
      conclusion: t("selfEvolutionRun.observation.fallbackConclusionDegrade"),
      tone: "down",
    },
    {
      caseId: "case-104",
      query: t("selfEvolutionRun.observation.fallbackQueryUpload"),
      aScore: 0.43,
      bScore: 0.67,
      delta: 0.24,
      conclusion: t("selfEvolutionRun.observation.fallbackConclusionImprove"),
      tone: "up",
    },
  ];
}

function getFallbackAbMetricRows(t: TFunction): AbMetricRow[] {
  return [
    { key: "correctness", label: t("selfEvolutionRun.metricAnswerCorrectness"), meanA: 0.867, meanB: 0.891, winRate: 0.1, signP: "0.629" },
    { key: "context", label: t("selfEvolutionRun.observation.metricContextRecall"), meanA: 0.71, meanB: 0.7, winRate: 0, signP: "-" },
    { key: "document", label: t("selfEvolutionRun.metricDocRecall"), meanA: 0.98, meanB: 0.98, winRate: 0, signP: "-" },
    { key: "faithfulness", label: t("selfEvolutionRun.observation.metricFaithfulness"), meanA: 0.917, meanB: 0.943, winRate: 0.13, signP: "0.678" },
  ];
}

function normalizeObservationKind(kind?: string): ObservationResultKind | undefined {
  return kind ? observationKindMap[kind] : undefined;
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

function formatDuration(ms?: number) {
  if (!isFiniteNumber(ms)) {
    return "-";
  }
  if (ms >= 1000) {
    return `${(ms / 1000).toFixed(ms >= 10_000 ? 1 : 2)}s`;
  }
  return `${ms.toFixed(ms < 10 ? 1 : 0)}ms`;
}

function getShortTraceId(traceId: string) {
  return traceId.length > 14 ? `${traceId.slice(0, 6)}...${traceId.slice(-6)}` : traceId;
}

function getStatusColor(status: string) {
  const normalized = status.toLowerCase();
  if (["success", "done", "completed", "succeeded"].includes(normalized)) {
    return "success";
  }
  if (["failed", "error"].includes(normalized)) {
    return "error";
  }
  if (["running", "pending"].includes(normalized)) {
    return "processing";
  }
  return "default";
}

function getDisplayText(value: unknown, maxLength = 120): string {
  if (typeof value === "string") {
    const trimmed = value.trim();
    if (!trimmed) {
      return "-";
    }
    return trimmed.length > maxLength ? `${trimmed.slice(0, maxLength)}...` : trimmed;
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  if (Array.isArray(value)) {
    return `${value.length} items`;
  }
  if (isRecord(value)) {
    const keys = Object.keys(value);
    return keys.length ? `${keys.slice(0, 4).join(" / ")}${keys.length > 4 ? "..." : ""}` : "0 fields";
  }
  return "-";
}

function flattenTraceNodes(root: TraceNode) {
  const rows: Array<{ node: TraceNode; depth: number }> = [];
  const walk = (node: TraceNode, depth: number) => {
    rows.push({ node, depth });
    node.children.forEach((child) => walk(child, depth + 1));
  };
  walk(root, 0);
  return rows;
}

function findArrayValue(value: unknown, keys: string[], depth = 0, allowDirectArray = true): unknown[] {
  if (depth > 5) {
    return [];
  }
  if (Array.isArray(value)) {
    if (allowDirectArray) {
      return value;
    }
    for (const item of value) {
      const result = findArrayValue(item, keys, depth + 1, false);
      if (result.length > 0) {
        return result;
      }
    }
    return [];
  }
  if (!isRecord(value)) {
    return [];
  }
  for (const key of keys) {
    const nested = value[key];
    if (Array.isArray(nested)) {
      return nested;
    }
  }
  for (const nested of Object.values(value)) {
    const result = findArrayValue(nested, keys, depth + 1, false);
    if (result.length > 0) {
      return result;
    }
  }
  return [];
}

function getTraceDocs(node?: TraceNode): TraceDocRow[] {
  const docs = findArrayValue(node?.output?.data, ["items", "docs", "documents", "nodes"]);
  return docs.filter(isRecord).slice(0, 3).map((item, index) => {
    const title =
      getStringField(item, ["file_name", "display_name", "docid", "document_id", "title"]) ||
      `Doc #${index + 1}`;
    const score = getNumberField(item, ["score", "similarity", "max_score"]);
    return {
      key: getStringField(item, ["docid", "id", "chunk_id"]) || `${title}-${index}`,
      title,
      ref: getStringField(item, ["ref", "citation_index", "chunk_id"]) || `chunk-${index + 1}`,
      score,
      text: getStringField(item, ["text", "content", "summary"]) || getDisplayText(item, 180),
    };
  });
}

function getNodeDataRecord(payload?: TraceNode["input"]) {
  return isRecord(payload?.data) ? payload.data : undefined;
}

function findRecordValue(value: unknown, keys: string[], depth = 0): unknown {
  if (depth > 5) {
    return undefined;
  }
  if (Array.isArray(value)) {
    for (const item of value) {
      const result = findRecordValue(item, keys, depth + 1);
      if (result !== undefined) {
        return result;
      }
    }
    return undefined;
  }
  if (!isRecord(value)) {
    return undefined;
  }
  for (const key of keys) {
    if (value[key] !== undefined) {
      return value[key];
    }
  }
  for (const nested of Object.values(value)) {
    const result = findRecordValue(nested, keys, depth + 1);
    if (result !== undefined) {
      return result;
    }
  }
  return undefined;
}

function getNodeTitle(node: TraceNode) {
  if (node.type === "tool") {
    return `Tool Call: ${node.name}`;
  }
  if (node.type === "llm") {
    return "LLM Generate";
  }
  return node.name;
}

function renderPayloadBlock(t: TFunction, label: string, payload?: { summary?: string; data?: unknown }) {
  if (!payload?.summary && payload?.data === undefined) {
    return (
      <Paragraph className="self-evolution-eval-empty">
        {t("selfEvolutionRun.observation.noLabelData", { label })}
      </Paragraph>
    );
  }

  return (
    <>
      {payload?.summary && <Paragraph className="self-evolution-eval-payload-summary">{payload.summary}</Paragraph>}
      {payload?.data !== undefined && (
        <details className="self-evolution-eval-payload-json">
          <summary>{t("selfEvolutionRun.observation.viewLabelJson", { label })}</summary>
          <pre>{stringifyResultPayload(payload.data)}</pre>
        </details>
      )}
    </>
  );
}

function renderMetadataTiles(t: TFunction, metadata?: Record<string, unknown>) {
  const entries = Object.entries(metadata || {}).slice(0, 8);
  if (entries.length === 0) {
    return <Paragraph className="self-evolution-eval-empty">{t("selfEvolutionRun.observation.noMetadata")}</Paragraph>;
  }

  return (
    <div className="self-evolution-eval-kv-grid">
      {entries.map(([key, value]) => (
        <span key={key}>
          <em>{key}</em>
          <strong title={getDisplayText(value, 400)}>{getDisplayText(value, 90)}</strong>
        </span>
      ))}
    </div>
  );
}

function buildFlowRows(t: TFunction, detail: TraceDetailObservation): FlowRow[] {
  const rootChildren = detail.root.children.length ? detail.root.children : [detail.root];
  return rootChildren.flatMap((roundNode, roundIndex) => {
    const descendants = flattenTraceNodes(roundNode)
      .filter(({ node }) => !["_build_history", "Pipeline", "FunctionCall"].includes(node.name))
      .filter(({ node }) => node.type === "llm" || node.type === "tool" || node.type === "retriever" || node.type === "rerank" || node.children.length === 0)
      .slice(0, 5);
    const rows = descendants.length ? descendants : [{ node: roundNode, depth: 0 }];
    return rows.map(({ node }, index) => ({
      key: `${roundIndex}-${node.id}-${index}`,
      round: roundIndex + 1,
      title: getNodeTitle(node),
      desc: node.output?.summary || node.input?.summary || t("selfEvolutionRun.observation.noSummary"),
      duration: formatDuration(node.latencyMs),
      tone: getTraceDocs(node).length > 0 || node.status === "warning" ? "warning" : node.status === "success" ? "success" : "normal",
      node,
    }));
  });
}

function getBadcaseSourceRecords(value: unknown): Record<string, unknown>[] {
  const rawSources = Array.isArray(value) ? value : [value];
  return rawSources.filter(isRecord).flatMap((item) => {
    const dataRecord = getStructuredRecordField(item, ["data"]);
    return dataRecord ? [dataRecord, item] : [item];
  });
}

function normalizeBadcaseRows(t: TFunction, value: unknown): CsvBadcaseRow[] {
  const candidateRows = getBadcaseSourceRecords(value)
    .flatMap((item) => [
      ...(getStructuredArrayField(item, ["bad_cases"]) || []),
      ...(getStructuredArrayField(item, ["badcases"]) || []),
      ...(getStructuredArrayField(item, ["badcase_list"]) || []),
      ...(getStructuredArrayField(item, ["cases"]) || []),
      ...(getStructuredArrayField(item, ["rows"]) || []),
      ...(getStructuredArrayField(item, ["records"]) || []),
      ...(getStructuredArrayField(item, ["items"]) || []),
    ]);
  const rows = candidateRows.filter(isRecord).map((item, index): CsvBadcaseRow => {
    const score = getNumberField(item, ["score", "metric_score", "answer_correctness", "value"]) ?? 0;
    const failureType = getStringField(item, ["failure_type", "failure_reason", "fail_reason", "category"]) || t("selfEvolutionRun.observation.pendingAnalysis");
    return {
      caseId: getStringField(item, ["case_id", "caseId", "case", "id"]) || `case-${String(index + 1).padStart(3, "0")}`,
      query: getStringField(item, ["query", "question", "prompt"]) || "-",
      reference: getStringField(item, ["reference", "ground_truth", "expected_answer"]) || "-",
      answer: getStringField(item, ["answer", "actual_answer", "prediction"]) || "-",
      score,
      failureType,
      failureTone: score < 0.5 ? "orange" : score < 0.6 ? "red" : "blue",
      defect: getStringField(item, ["Defect", "defect"]) || "-",
      reason: getStringField(item, ["Reason", "reason", "failure_detail"]) || failureType,
      mode: getStringField(item, ["mode", "execution_mode"]) || "Agentic RAG",
      traceId: getStringField(item, ["trace_id", "traceId"]) || "-",
      traceStatus: getStringField(item, ["trace_status", "traceStatus"]) || t("selfEvolutionRun.observation.alreadyLinked"),
      failureReason: getStringField(item, ["failure_detail", "failure_reason", "fail_reason", "Reason", "reason"]) || failureType,
      tracePayload: item.trace || item.observation || item.trace_detail,
    };
  });
  return rows;
}

function getAbCaseSourceRecords(value: unknown): unknown[] {
  if (isRecord(value)) {
    const items = getStructuredArrayField(value, ["items"]);
    if (items?.length) {
      return items;
    }
    return (["cases", "case_list", "rows", "records", "items", "badcases", "case_details", "case_deltas"] as const)
      .flatMap((key) => Array.isArray(value[key]) ? value[key] : []);
  }
  return Array.isArray(value) ? value : [];
}

function normalizeAbCaseRowFromRecord(t: TFunction, item: Record<string, unknown>, index: number): AbCaseRow {
  const before = getNestedRecordField(item, ["before"]) || getStructuredRecordField(item, ["baseline"]);
  const after = getNestedRecordField(item, ["after"]) || getStructuredRecordField(item, ["candidate"]);
  const deltaRecord = getNestedRecordField(item, ["delta"]) || getStructuredRecordField(item, ["delta"]);
  const aScore =
    getNumberField(before, ["answer_correctness", "score", "a_score", "baseline_score", "mean_a"]) ??
    getNumberField(item, ["a_score", "score_a", "baseline_score", "mean_a"]) ??
    0;
  const bScore =
    getNumberField(after, ["answer_correctness", "score", "b_score", "candidate_score", "mean_b"]) ??
    getNumberField(item, ["b_score", "score_b", "candidate_score", "mean_b"]) ??
    0;
  const delta =
    getNumberField(deltaRecord, ["answer_correctness", "change", "score_delta"]) ??
    getNumberField(item, ["delta", "change", "score_delta"]) ??
    bScore - aScore;
  const outcome = getStringField(item, ["outcome", "Outcome"]);
  let conclusion = getStringField(item, ["conclusion", "judgement", "result"]);
  if (!conclusion && outcome) {
    conclusion =
      outcome === "improved"
        ? t("selfEvolutionRun.observation.bImprove")
        : outcome === "regressed"
          ? t("selfEvolutionRun.observation.bDegrade")
          : outcome === "unchanged"
            ? t("selfEvolutionRun.observation.flat")
            : outcome;
  }
  if (!conclusion) {
    conclusion =
      delta > 0
        ? t("selfEvolutionRun.observation.bImprove")
        : delta < 0
          ? t("selfEvolutionRun.observation.bDegrade")
          : t("selfEvolutionRun.observation.flat");
  }
  const tone: AbCaseRow["tone"] =
    outcome === "improved" || delta > 0
      ? "up"
      : outcome === "regressed" || delta < 0
        ? "down"
        : "flat";

  return {
    caseId: getStringField(item, ["case_id", "caseId", "case", "id"]) || `case-${String(index + 1).padStart(3, "0")}`,
    query: getStringField(item, ["query", "question", "prompt"]) || "-",
    aScore,
    bScore,
    delta,
    conclusion,
    tone,
  };
}

function normalizeAbCaseRows(t: TFunction, value: unknown, options?: { useFallback?: boolean }): AbCaseRow[] {
  const rows = getAbCaseSourceRecords(value)
    .filter(isRecord)
    .map((item, index) => normalizeAbCaseRowFromRecord(t, item, index));
  if (rows.length) {
    return rows;
  }
  return options?.useFallback ? getFallbackAbCaseRows(t) : [];
}

function toAbMetricRows(summary: AbSummaryReport | undefined, t: TFunction): AbMetricRow[] {
  if (summary?.metricRows.length) {
    return summary.metricRows.map((row) => ({
      key: row.metric,
      label: row.metricLabel,
      meanA: row.meanA,
      meanB: row.meanB,
      winRate: row.winRateB ?? 0,
      signP: formatMaybePValue(row.signP),
    }));
  }
  return getFallbackAbMetricRows(t);
}

function resolveAbtestIdFromPayload(value: unknown): string | undefined {
  const summary = buildAbSummaryReports(value)[0];
  if (!summary?.id || syntheticAbtestIdPattern.test(summary.id)) {
    return undefined;
  }
  return summary.id;
}

function getAbtestVerdictColor(verdict?: string) {
  const normalized = (verdict || "").toLowerCase();
  if (["pass", "accept", "improved"].some((item) => normalized.includes(item))) {
    return "success";
  }
  if (["fail", "reject", "regressed"].some((item) => normalized.includes(item))) {
    return "error";
  }
  return "orange";
}

function findAbCaseDetailItem(data: unknown, caseId: string): Record<string, unknown> | undefined {
  if (!isRecord(data)) {
    return undefined;
  }
  const items = getStructuredArrayField(data, ["items"]);
  return items?.find((item) => isRecord(item) && getStringField(item, ["case_id", "caseId", "case", "id"]) === caseId);
}

function buildAbCaseTraceIdMap(evalReportsData: unknown): Map<string, { a?: string; b?: string }> {
  const map = new Map<string, { a?: string; b?: string }>();
  getAbtestResultRecords(evalReportsData).forEach((record) => {
    const artifactId =
      getStringField(record, ["artifact_id", "runtime_artifact_id", "source_artifact_id"]) ||
      getStringField(getStructuredRecordField(record, ["data"]), ["id"]);
    const data = getStructuredRecordField(record, ["data"]) || record;
    const rows = getStructuredArrayField(data, ["rows"]) || getStructuredArrayField(data, ["case_details"]) || [];
    const isCandidate =
      artifactId.includes("candidate") ||
      getStringField(data, ["id"]) === "abtest.candidate_eval_summary";
    const side: "a" | "b" = isCandidate ? "b" : "a";
    rows.filter(isRecord).forEach((row) => {
      const caseId = getStringField(row, ["case_id", "caseId", "case", "id"]);
      const traceId = getStringField(row, ["trace_id", "traceId"]);
      if (!caseId || !traceId || traceId === "-") {
        return;
      }
      const entry = map.get(caseId) || {};
      entry[side] = traceId;
      map.set(caseId, entry);
    });
  });
  return map;
}

function resolveCaseTraceIds(
  caseItem: Record<string, unknown> | undefined,
  caseId: string,
  traceMap: Map<string, { a?: string; b?: string }>,
): { a?: string; b?: string } {
  const mapped = traceMap.get(caseId) || {};
  if (!caseItem) {
    return mapped;
  }
  const before = getNestedRecordField(caseItem, ["before"]) || getStructuredRecordField(caseItem, ["baseline"]);
  const after = getNestedRecordField(caseItem, ["after"]) || getStructuredRecordField(caseItem, ["candidate"]);
  return {
    a:
      getStringField(caseItem, ["baseline_trace_id", "a_trace_id", "trace_id_a"]) ||
      getStringField(before, ["trace_id", "traceId"]) ||
      mapped.a,
    b:
      getStringField(caseItem, ["candidate_trace_id", "b_trace_id", "trace_id_b"]) ||
      getStringField(after, ["trace_id", "traceId"]) ||
      mapped.b,
  };
}

function formatPercent(value: number) {
  return `${(value * 100).toFixed(1)}%`;
}

function formatOptionalPercent(value?: number) {
  if (!isFiniteNumber(value)) {
    return "-";
  }
  return formatPercent(value);
}

function formatDeltaScore(value: number) {
  return `${value > 0 ? "+" : ""}${value.toFixed(2)}`;
}

function formatDeltaPercent(a: number, b: number) {
  const delta = b - a;
  return `${delta > 0 ? "+" : ""}${Math.round(delta * 100)}%`;
}

function getDetailRoundCount(detail: TraceDetailObservation) {
  return Math.max(detail.root.children.length, detail.summary.roundCount || 0);
}

function getTraceMode(detail: TraceDetailObservation) {
  const rows = flattenTraceNodes(detail.root);
  return rows.some(({ node }) => node.type === "tool" || node.type === "retriever") ? "Agentic RAG" : "RAG";
}

function getSearchNode(detail: TraceDetailObservation) {
  const rows = flattenTraceNodes(detail.root);
  return (
    rows.find(({ node }) => node.name.includes("kb_search"))?.node ||
    rows.find(({ node }) => getTraceDocs(node).length > 0)?.node ||
    rows.find(({ node }) => node.type === "retriever")?.node ||
    rows.find(({ node }) => node.type === "tool")?.node ||
    rows[0]?.node
  );
}

function getAbReturnedDocs(node?: TraceNode) {
  const docs = getTraceDocs(node);
  const outputData = getNodeDataRecord(node?.output);
  return docs.length || Number(findRecordValue(outputData, ["total", "returned_docs", "node_count"])) || 0;
}

function getAbMaxScore(node?: TraceNode) {
  const docs = getTraceDocs(node);
  const outputData = getNodeDataRecord(node?.output);
  const score = docs[0]?.score ?? getNumberField(outputData, ["max_score", "score"]);
  return isFiniteNumber(score) ? score : undefined;
}

function getPrimaryObservation(observation?: TraceObservation) {
  if (!observation) {
    return undefined;
  }
  return observation.kind === "detail" ? observation.detail : observation.a;
}

function getPrimaryEvalReportRecord(value: unknown) {
  const items = getResultItems(value);
  const itemRecord = items.find((item): item is Record<string, unknown> => isRecord(item));
  if (itemRecord) {
    return itemRecord;
  }
  return isRecord(value) ? value : undefined;
}

function normalizeEvalReportSummary(value: unknown): EvalReportSummary {
  const row = getPrimaryEvalReportRecord(value);
  const data = getStructuredRecordField(row, ["data"]) || getNestedRecordField(row, ["data"]);
  const metrics = getStructuredRecordField(data, ["metrics"]) || getNestedRecordField(data, ["metrics"]);
  const traceCoverage = getStructuredRecordField(row, ["trace_coverage"]) || getNestedRecordField(row, ["trace_coverage"]);

  return {
    reportId:
      getStringField(row, ["report_id", "reportId"]) ||
      getStringField(data, ["report_id", "reportId", "id"]) ||
      "-",
    dataset: getStringField(data, ["eval_dataset_ref"]) || "-",
    correctRate: getNumberField(metrics, ["correct_rate"]),
    badCaseCount: getNumberField(row, ["bad_case_count"]),
    traceCoverageRate: getNumberField(traceCoverage, ["rate"]),
  };
}

function MetricCard({
  icon,
  label,
  value,
  tone,
}: {
  icon: ReactNode;
  label: string;
  value: string;
  tone: "blue" | "red" | "green" | "purple";
}) {
  return (
    <article className={`self-evolution-eval-metric is-${tone}`}>
      <span>{icon}</span>
      <div>
        <em>{label}</em>
        <strong>{value}</strong>
      </div>
    </article>
  );
}

function EvalReportPanel({
  summary,
  rows,
  rowsError,
  rowsLoading,
  selectedCaseId,
  onSelectCase,
  onReloadRows,
}: {
  summary: EvalReportSummary;
  rows: CsvBadcaseRow[];
  rowsError?: string;
  rowsLoading?: boolean;
  selectedCaseId: string;
  onSelectCase: (caseId: string) => void;
  onReloadRows: () => void;
}) {
  const { t } = useTranslation();
  const selectedRow = rows.find((item) => item.caseId === selectedCaseId) || rows[0];
  const columns: ColumnsType<CsvBadcaseRow> = [
    { title: "Case", dataIndex: "caseId", key: "caseId", width: 104 },
    {
      title: "Score",
      dataIndex: "score",
      key: "score",
      width: 78,
      render: (value: number) => <span className={value < 0.5 ? "is-low-score" : ""}>{value.toFixed(2)}</span>,
    },
    {
      title: t("selfEvolutionRun.observation.failureReason"),
      dataIndex: "failureType",
      key: "failureType",
      width: 110,
      render: (value: string, row) => <Tag className={`self-evolution-eval-reason is-${row.failureTone}`}>{value}</Tag>,
    },
    {
      title: "Defect",
      dataIndex: "defect",
      key: "defect",
      width: 230,
      render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span>,
    },
    {
      title: "Reason",
      dataIndex: "reason",
      key: "reason",
      width: 360,
      render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span>,
    },
    {
      title: "Trace",
      dataIndex: "traceId",
      key: "traceId",
      width: 170,
      render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span>,
    },
  ];

  return (
    <section className="self-evolution-eval-report-card" aria-label={t("selfEvolutionRun.observation.evalReportAria")}>
      <div className="self-evolution-eval-report-head">
        <div>
          <Title level={3}>{t("selfEvolutionRun.observation.evalReportTitle")}</Title>
          <Text>{t("selfEvolutionRun.observation.reportIdLabel", { id: summary.reportId })}</Text>
          <Text>{t("selfEvolutionRun.observation.datasetInfo", { dataset: summary.dataset, badcaseCount: summary.badCaseCount ?? "-" })}</Text>
        </div>
      </div>
      <div className="self-evolution-eval-metric-grid">
        <MetricCard icon={<AimOutlined />} label={t("selfEvolutionRun.observation.accuracy")} value={formatOptionalPercent(summary.correctRate)} tone="blue" />
        <MetricCard icon={<WarningOutlined />} label="Badcase" value={String(summary.badCaseCount ?? "-")} tone="red" />
        <MetricCard icon={<ThunderboltOutlined />} label={t("selfEvolutionRun.observation.traceCoverage")} value={formatOptionalPercent(summary.traceCoverageRate)} tone="purple" />
      </div>
      <div className="self-evolution-eval-badcase-panel">
        <div className="self-evolution-eval-section-title">
          <Text strong>{t("selfEvolutionRun.observation.badcaseList")}</Text>
          <span>{t("selfEvolutionRun.observation.badcaseSource", { reportId: summary.reportId })}</span>
        </div>
        <div className="self-evolution-eval-filter-row">
          <label>
            {t("selfEvolutionRun.observation.statusLabel")}
            <select aria-label={t("selfEvolutionRun.observation.badcaseStatusFilterAria")}>
              <option>{t("selfEvolutionRun.observation.all")}</option>
            </select>
          </label>
          <label>
            {t("selfEvolutionRun.observation.failureTypeLabel")}
            <select aria-label={t("selfEvolutionRun.observation.badcaseFailureTypeFilterAria")}>
              <option>{t("selfEvolutionRun.observation.all")}</option>
            </select>
          </label>
          <label className="self-evolution-eval-search">
            <SearchOutlined />
            <input
              aria-label={t("selfEvolutionRun.observation.searchCaseAria")}
              placeholder={t("selfEvolutionRun.observation.searchCasePlaceholder")}
            />
          </label>
          <Button size="small">{t("selfEvolutionRun.observation.reset")}</Button>
        </div>
        {rowsError ? (
          <Alert
            type="error"
            showIcon
            message={rowsError}
            action={<Button size="small" onClick={onReloadRows}>{t("selfEvolutionRun.observation.retry")}</Button>}
          />
        ) : (
          <Table<CsvBadcaseRow>
            className="self-evolution-eval-badcase-table"
            size="small"
            rowKey="caseId"
            columns={columns}
            dataSource={rows}
            loading={rowsLoading}
            pagination={false}
            rowClassName={(row) => row.caseId === selectedCaseId ? "is-selected" : ""}
            scroll={{ x: 1052 }}
            onRow={(row) => ({
              onClick: () => onSelectCase(row.caseId),
            })}
          />
        )}
      </div>
      {selectedRow && (
        <div className="self-evolution-eval-case-result">
          <div className="self-evolution-eval-section-title">
            <Text strong>{t("selfEvolutionRun.observation.caseResult", { caseId: selectedRow.caseId })}</Text>
          </div>
          <dl>
            <dt>Score</dt>
            <dd>
              <span className="is-low-score">{selectedRow.score.toFixed(2)}</span>
              <Tag className={`self-evolution-eval-reason is-${selectedRow.failureTone}`}>{selectedRow.failureType}</Tag>
            </dd>
            <dt>{t("selfEvolutionRun.observation.failureReason")}</dt>
            <dd>{selectedRow.failureReason}</dd>
            <dt>Defect</dt>
            <dd>{selectedRow.defect}</dd>
            <dt>Reason</dt>
            <dd>{selectedRow.reason}</dd>
          </dl>
          <div className="self-evolution-eval-case-actions">
            <Button type="primary">{t("selfEvolutionRun.observation.viewAgenticTrace")}</Button>
            <Button icon={<FileSearchOutlined />}>{t("selfEvolutionRun.observation.viewRawTrace")}</Button>
          </div>
        </div>
      )}
    </section>
  );
}

function TraceMetaCard({ label, value, status }: { label: string; value: string; status?: boolean }) {
  return (
    <article className="self-evolution-eval-trace-meta-card">
      <span>{label}</span>
      {status ? <Tag color={getStatusColor(value)}>{value}</Tag> : <strong>{value}</strong>}
    </article>
  );
}

function TraceFlowPanel({
  detail,
  selectedNodeId,
  onSelectNode,
}: {
  detail: TraceDetailObservation;
  selectedNodeId?: string;
  onSelectNode: (node: TraceNode) => void;
}) {
  const { t } = useTranslation();
  const flowRows = useMemo(() => buildFlowRows(t, detail), [detail, t]);
  const rowsByRound = useMemo(() => {
    const grouped = new Map<number, FlowRow[]>();
    flowRows.forEach((row) => {
      grouped.set(row.round, [...(grouped.get(row.round) || []), row]);
    });
    return Array.from(grouped.entries());
  }, [flowRows]);

  return (
    <section className="self-evolution-eval-flow-panel" aria-label={t("selfEvolutionRun.observation.flowPanelAria")}>
      <div className="self-evolution-eval-panel-title">
        <Text strong>{t("selfEvolutionRun.observation.flowPanelTitle")}</Text>
      </div>
      <div className="self-evolution-eval-flow-list">
        {rowsByRound.map(([round, rows]) => (
          <div key={round} className="self-evolution-eval-flow-round">
            <div className="self-evolution-eval-flow-round-head">
              <CheckCircleOutlined />
              <strong>{`Round ${round}`}</strong>
              <span>{formatDuration(rows.reduce((total, row) => total + (row.node.latencyMs || 0), 0))}</span>
            </div>
            {rows.map((row) => (
              <button
                key={row.key}
                type="button"
                className={`self-evolution-eval-flow-step is-${row.tone}${row.node.id === selectedNodeId ? " is-active" : ""}`}
                onClick={() => onSelectNode(row.node)}
              >
                <span className="self-evolution-eval-flow-dot" />
                <span>
                  <strong>{row.title}</strong>
                  <em>{row.desc}</em>
                </span>
                <i>{row.duration}</i>
              </button>
            ))}
          </div>
        ))}
      </div>
    </section>
  );
}

function TraceInspectorPanel({
  selectedRow,
  node,
}: {
  selectedRow: CsvBadcaseRow;
  node?: TraceNode;
}) {
  const { t } = useTranslation();
  const docs = getTraceDocs(node);
  const metadata = node?.metadata || {};
  const inputData = getNodeDataRecord(node?.input);

  if (!node) {
    return (
      <section className="self-evolution-eval-inspector-panel is-empty" aria-label={t("selfEvolutionRun.observation.inspectorPanelAria")}>
        <div className="self-evolution-eval-panel-title">
          <Text strong>{t("selfEvolutionRun.observation.inspectorPanelTitle")}</Text>
        </div>
        <div className="self-evolution-eval-inspector-empty">
          <FileSearchOutlined />
          <Text strong>{t("selfEvolutionRun.observation.inspectorEmptyHint")}</Text>
          <Paragraph>{t("selfEvolutionRun.observation.inspectorEmptyDesc")}</Paragraph>
        </div>
      </section>
    );
  }

  return (
    <section className="self-evolution-eval-inspector-panel" aria-label={t("selfEvolutionRun.observation.inspectorPanelAria")}>
      <div className="self-evolution-eval-panel-title">
        <Text strong>{t("selfEvolutionRun.observation.inspectorNodeTitle", { title: getNodeTitle(node) })}</Text>
        <Tag color={getStatusColor(node.status)}>{node.status}</Tag>
      </div>
      <div className="self-evolution-eval-inspector-body">
        <div className="self-evolution-eval-inspector-section">
          <h4>{t("selfEvolutionRun.observation.nodeInfoSection")}</h4>
          <div className="self-evolution-eval-kv-grid">
            <span><em>node_id</em><strong>{getShortTraceId(node.id)}</strong></span>
            <span><em>node_type</em><strong>{node.type}</strong></span>
            <span><em>duration</em><strong>{formatDuration(node.latencyMs)}</strong></span>
            <span><em>status</em><strong>{node.status}</strong></span>
            <span><em>input_kind</em><strong>{node.input?.kind || "-"}</strong></span>
            <span><em>output_kind</em><strong>{node.output?.kind || "-"}</strong></span>
          </div>
        </div>
        <div className="self-evolution-eval-inspector-section">
          <h4>{t("selfEvolutionRun.observation.inputSection")}</h4>
          {renderPayloadBlock(t, t("selfEvolutionRun.observation.inputLabel"), node.input?.summary || inputData !== undefined ? { summary: node.input?.summary, data: inputData } : undefined)}
        </div>
        <div className="self-evolution-eval-inspector-section">
          <h4>{t("selfEvolutionRun.observation.outputSection")}</h4>
          {renderPayloadBlock(t, t("selfEvolutionRun.observation.outputLabel"), node.output)}
        </div>
        <div className="self-evolution-eval-inspector-section">
          <h4>{t("selfEvolutionRun.observation.retrievedDocsSection")}</h4>
          {docs.length ? (
            docs.map((doc, index) => (
              <article key={doc.key} className="self-evolution-eval-doc-card">
                <FileTextOutlined />
                <div>
                  <strong>{`Doc #${index + 1} ${doc.title}`}</strong>
                  <span>{`score: ${doc.score?.toFixed(2) || "-"} · ${doc.ref}`}</span>
                  <p>{doc.text}</p>
                </div>
                {doc.score !== undefined && <Tag color="blue">{t("selfEvolutionRun.observation.relevanceScore", { score: doc.score.toFixed(2) })}</Tag>}
              </article>
            ))
          ) : (
            <Paragraph className="self-evolution-eval-empty">{t("selfEvolutionRun.observation.noRetrievedDocs")}</Paragraph>
          )}
        </div>
        <div className="self-evolution-eval-inspector-section">
          <h4>{t("selfEvolutionRun.observation.metadataSection")}</h4>
          {renderMetadataTiles(t, metadata)}
        </div>
        <div className="self-evolution-eval-inspector-section is-warning">
          <h4>{t("selfEvolutionRun.observation.observationSection")}</h4>
          <p>{selectedRow.failureReason}</p>
        </div>
      </div>
    </section>
  );
}

function EvalTracePanel({ detail, selectedRow }: { detail: TraceDetailObservation; selectedRow: CsvBadcaseRow }) {
  const { t } = useTranslation();
  const [selectedNode, setSelectedNode] = useState<TraceNode | undefined>();
  useEffect(() => {
    setSelectedNode(undefined);
  }, [detail, selectedRow.caseId]);
  const roundCount = Math.max(detail.root.children.length, detail.summary.roundCount || 0);
  return (
    <section className="self-evolution-eval-trace-card" aria-label={t("selfEvolutionRun.observation.agenticTraceCardAria")}>
      <div className="self-evolution-eval-trace-title">
        <Title level={3}>{t("selfEvolutionRun.observation.agenticTraceCardTitle", { caseId: selectedRow.caseId })}</Title>
      </div>
      <div className="self-evolution-eval-trace-meta-grid">
        <TraceMetaCard label="trace_id" value={getShortTraceId(detail.traceId)} />
        <TraceMetaCard label="scene" value="eval" />
        <TraceMetaCard label="execution_mode" value="agentic_rag" />
        <TraceMetaCard label="status" value={selectedRow.score < 0.5 ? t("selfEvolutionRun.observation.lowScore") : detail.status} status />
        <TraceMetaCard label="latency" value={formatDuration(detail.summary.latencyMs)} />
      </div>
      <div className="self-evolution-eval-trace-summary">
        <span><strong>{roundCount || "-"}</strong><em>{t("selfEvolutionRun.observation.decisionRounds")}</em></span>
        <span><strong>{detail.summary.toolCallCount ?? "-"}</strong><em>{t("selfEvolutionRun.observation.toolCalls")}</em></span>
        <span><strong>{detail.summary.retrievalCount ?? "-"}</strong><em>{t("selfEvolutionRun.observation.kbRetrievals")}</em></span>
        <span><strong>{detail.summary.rerankCount ?? "-"}</strong><em>{t("selfEvolutionRun.observation.rerankCount")}</em></span>
        <span className="is-status"><strong>{detail.status}</strong><em>{t("selfEvolutionRun.observation.completionStatus")}</em></span>
      </div>
      <div className="self-evolution-eval-trace-grid">
        <TraceFlowPanel detail={detail} selectedNodeId={selectedNode?.id} onSelectNode={setSelectedNode} />
        <TraceInspectorPanel selectedRow={selectedRow} node={selectedNode} />
      </div>
    </section>
  );
}

function EvalObservationDashboard({
  data,
  notice,
  isFallback,
  threadId,
  onBack,
  isMenuCollapsed,
  toggleMenu,
}: {
  data: unknown;
  notice?: string;
  isFallback?: boolean;
  threadId?: string;
  onBack: () => void;
  isMenuCollapsed?: boolean;
  toggleMenu?: () => void;
}) {
  const { t } = useTranslation();
  const summary = useMemo(() => normalizeEvalReportSummary(data), [data]);
  const [badcaseReloadToken, setBadcaseReloadToken] = useState(0);
  const [badcaseState, setBadcaseState] = useState<EvalBadcaseListState>({
    loading: false,
    loaded: false,
  });
  const rows = useMemo(() => normalizeBadcaseRows(t, badcaseState.data), [badcaseState.data, t]);
  const [selectedCaseId, setSelectedCaseId] = useState(rows[0]?.caseId || "");
  const [traceState, setTraceState] = useState<{
    loading: boolean;
    data?: unknown;
    error?: string;
    traceId?: string;
  }>({ loading: false });
  const selectedRow = rows.find((item) => item.caseId === selectedCaseId) || rows[0];
  const selectedObservation = useMemo(() => {
    return normalizeTraceObservation(traceState.data) || normalizeTraceObservation(selectedRow?.tracePayload);
  }, [selectedRow, traceState.data]);
  const detail = getPrimaryObservation(selectedObservation);

  useEffect(() => {
    if (!threadId || !summary.reportId || summary.reportId === "-") {
      setBadcaseState({ loading: false, loaded: false });
      return;
    }

    const controller = new AbortController();
    setBadcaseState((prev) => ({
      reportId: summary.reportId,
      loading: true,
      loaded: prev.reportId === summary.reportId ? prev.loaded : false,
      data: prev.reportId === summary.reportId ? prev.data : undefined,
      error: undefined,
    }));

    createCoreAgentGeneratedApiClient()
      .apiCoreAgentThreadsThreadIdResultsEvalReportsReportIdBadCasesGet(
        {
          threadId,
          reportId: summary.reportId,
          pageSize: EVAL_BADCASE_PAGE_SIZE,
        },
        { signal: controller.signal },
      )
      .then((response) => {
        if (controller.signal.aborted) {
          return;
        }
        setBadcaseState({
          reportId: summary.reportId,
          loading: false,
          loaded: true,
          data: response.data,
        });
      })
      .catch((error) => {
        if (isCanceledRequest(error) || controller.signal.aborted) {
          return;
        }
        setBadcaseState((prev) => ({
          ...prev,
          reportId: summary.reportId,
          loading: false,
          loaded: true,
          error: getLocalizedErrorMessage(error, t("selfEvolutionRun.observation.badcaseLoadFailed")),
        }));
      });

    return () => {
      controller.abort();
    };
  }, [badcaseReloadToken, summary.reportId, threadId]);

  useEffect(() => {
    if (!rows.some((item) => item.caseId === selectedCaseId)) {
      setSelectedCaseId(rows[0]?.caseId || "");
    }
  }, [rows, selectedCaseId]);

  useEffect(() => {
    const traceId = selectedRow?.traceId;
    if (!threadId || !traceId || traceId === "-") {
      setTraceState({ loading: false, data: undefined, error: traceId ? undefined : t("selfEvolutionRun.observation.noTraceId") });
      return;
    }

    const controller = new AbortController();
    setTraceState({ loading: true, data: undefined, error: undefined, traceId });

    axiosInstance
      .get(`${EVO_API_BASE}/threads/${encodeURIComponent(threadId)}/results/traces/${encodeURIComponent(traceId)}`, {
        signal: controller.signal,
      })
      .then((response) => {
        if (controller.signal.aborted) {
          return;
        }
        if (isEmptyResultPayload(response.data)) {
          setTraceState({
            loading: false,
            data: undefined,
            error: t("selfEvolutionRun.observation.traceNoData"),
            traceId,
          });
          return;
        }
        setTraceState({ loading: false, data: response.data, error: undefined, traceId });
      })
      .catch((error) => {
        if (isCanceledRequest(error) || controller.signal.aborted) {
          return;
        }
        setTraceState({
          loading: false,
          data: undefined,
          error: getLocalizedErrorMessage(error, t("selfEvolutionRun.observation.observationDetailLoadFailed")),
          traceId,
        });
      });

    return () => {
      controller.abort();
    };
  }, [selectedRow?.traceId, threadId]);

  return (
    <div className="self-evolution-eval-dashboard">
      <header className="self-evolution-eval-dashboard-head">
        <ObservationHeaderControls isMenuCollapsed={isMenuCollapsed} toggleMenu={toggleMenu} onBack={onBack} />
        <div className="self-evolution-eval-dashboard-head-right">
          {threadId && <Tag>{`thread ${threadId}`}</Tag>}
          {isFallback && <Tag color="gold">{t("selfEvolutionRun.observation.noData")}</Tag>}
        </div>
      </header>
      {notice && <Alert type="warning" showIcon message={notice} />}
      <div className="self-evolution-eval-dashboard-grid">
        <EvalReportPanel
          summary={summary}
          rows={rows}
          rowsError={badcaseState.error}
          rowsLoading={badcaseState.loading}
          selectedCaseId={selectedCaseId}
          onSelectCase={setSelectedCaseId}
          onReloadRows={() => setBadcaseReloadToken((prev) => prev + 1)}
        />
        {selectedRow ? (
          traceState.loading ? (
            <section className="self-evolution-eval-trace-card" aria-label={t("selfEvolutionRun.observation.agenticTraceCardAria")}>
              <Spin />
            </section>
          ) : detail ? (
            <EvalTracePanel detail={detail} selectedRow={selectedRow} />
          ) : (
            <section className="self-evolution-eval-trace-card">
              <Empty description={traceState.error || t("selfEvolutionRun.observation.emptyObservation")} />
            </section>
          )
        ) : (
          <section className="self-evolution-eval-trace-card">
            <Empty description={t("selfEvolutionRun.observation.emptyObservation")} />
          </section>
        )}
      </div>
    </div>
  );
}

function AbMetricChart({ metrics }: { metrics: AbMetricRow[] }) {
  const { t } = useTranslation();
  return (
    <div className="self-evolution-abtest-chart" aria-label={t("selfEvolutionRun.observation.abChartAria")}>
      <div className="self-evolution-abtest-chart-axis">
        <span>1.00</span>
        <span>0.75</span>
        <span>0.50</span>
        <span>0.25</span>
        <span>0.00</span>
      </div>
      <div className="self-evolution-abtest-chart-groups">
        {metrics.map((metric) => {
          const delta = metric.meanB - metric.meanA;
          return (
            <div key={metric.key} className="self-evolution-abtest-chart-group">
              <strong className={delta >= 0 ? "is-up" : "is-down"}>{formatDeltaPercent(metric.meanA, metric.meanB)}</strong>
              <div className="self-evolution-abtest-bars">
                <span className="is-a" style={{ height: `${Math.max(6, metric.meanA * 100)}%` }} />
                <span className="is-b" style={{ height: `${Math.max(6, metric.meanB * 100)}%` }} />
              </div>
              <em>{metric.label}</em>
            </div>
          );
        })}
      </div>
      <div className="self-evolution-abtest-chart-legend">
        <span><i className="is-a" />{t("selfEvolutionRun.observation.legendA")}</span>
        <span><i className="is-b" />{t("selfEvolutionRun.observation.legendB")}</span>
      </div>
    </div>
  );
}

function AbReportPanel({
  summary,
  rows,
  rowsError,
  rowsLoading,
  totalSize,
  isFallback,
  selectedCaseId,
  onSelectCase,
  onReloadRows,
}: {
  summary?: AbSummaryReport;
  rows: AbCaseRow[];
  rowsError?: string;
  rowsLoading?: boolean;
  totalSize?: number;
  isFallback?: boolean;
  selectedCaseId: string;
  onSelectCase: (caseId: string) => void;
  onReloadRows: () => void;
}) {
  const { t } = useTranslation();
  const metrics = useMemo(() => toAbMetricRows(summary, t), [summary, t]);
  const abtestId = summary?.id || "abtest_20260507_112323_3dec8237";
  const verdict = summary?.verdict || "inconclusive";
  const columns: ColumnsType<AbCaseRow> = [
    {
      title: "Case",
      dataIndex: "caseId",
      key: "caseId",
      width: 94,
      render: (value: string) => <Text className="self-evolution-abtest-case-link">{value}</Text>,
    },
    {
      title: "Query",
      dataIndex: "query",
      key: "query",
      width: 230,
      render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span>,
    },
    {
      title: "A Score",
      dataIndex: "aScore",
      key: "aScore",
      width: 80,
      render: (value: number) => value.toFixed(2),
    },
    {
      title: "B Score",
      dataIndex: "bScore",
      key: "bScore",
      width: 80,
      render: (value: number) => value.toFixed(2),
    },
    {
      title: t("selfEvolutionRun.observation.abChange"),
      dataIndex: "delta",
      key: "delta",
      width: 76,
      render: (value: number, row) => <span className={`self-evolution-abtest-delta is-${row.tone}`}>{formatDeltaScore(value)}</span>,
    },
    { title: t("selfEvolutionRun.observation.abConclusion"), dataIndex: "conclusion", key: "conclusion", width: 140 },
    {
      title: t("selfEvolutionRun.observation.abAction"),
      key: "action",
      width: 112,
      render: (_, row) => (
        <Button size="small" type={row.caseId === selectedCaseId ? "primary" : "default"} onClick={() => onSelectCase(row.caseId)}>
          {t("selfEvolutionRun.observation.viewAbTrace")}
        </Button>
      ),
    },
  ];

  return (
    <section className="self-evolution-abtest-report-card" aria-label={t("selfEvolutionRun.observation.selfEvolutionOrchestrationAria")}>
      <div className="self-evolution-abtest-report-head">
        <div>
          <Title level={3}>{t("selfEvolutionRun.observation.selfEvolutionOrchestrationTitle")}</Title>
          <Text>{t("selfEvolutionRun.observation.abStageTitle")}</Text>
          <Text>{t("selfEvolutionRun.observation.abMetricDesc")}</Text>
        </div>
        <Tag color={getAbtestVerdictColor(verdict)}>{verdict}</Tag>
      </div>
      <div className="self-evolution-abtest-report-id">
        <strong>{abtestId}</strong>
      </div>
      <AbMetricChart metrics={metrics} />
      <div className="self-evolution-abtest-metric-table" aria-label={t("selfEvolutionRun.observation.abMetricTableAria")}>
        <div className="self-evolution-abtest-table-row is-head">
          <span>{t("selfEvolutionRun.observation.abMetricColMetric")}</span>
          <span>mean A</span>
          <span>mean B</span>
          <span>Δmean</span>
          <span>{t("selfEvolutionRun.observation.abMetricColWinRate")}</span>
          <span>sign p</span>
        </div>
        {metrics.map((metric) => (
          <div key={metric.key} className="self-evolution-abtest-table-row">
            <span>{metric.label}</span>
            <span>{formatPercent(metric.meanA)}</span>
            <span>{formatPercent(metric.meanB)}</span>
            <strong className={metric.meanB >= metric.meanA ? "is-up" : "is-down"}>{formatDeltaPercent(metric.meanA, metric.meanB)}</strong>
            <span>{formatPercent(metric.winRate)}</span>
            <span>{metric.signP || "-"}</span>
          </div>
        ))}
      </div>
      <div className="self-evolution-abtest-case-panel">
        <div className="self-evolution-eval-section-title">
          <Text strong>{t("selfEvolutionRun.observation.changedCaseList")}</Text>
          <span>
            {isFallback
              ? t("selfEvolutionRun.observation.sampleDataNote")
              : t("selfEvolutionRun.observation.abCaseDetailSource", { abtestId, total: totalSize ?? rows.length })}
          </span>
        </div>
        {rowsError ? (
          <Alert
            type="error"
            showIcon
            message={rowsError}
            action={<Button size="small" onClick={onReloadRows}>{t("selfEvolutionRun.observation.retry")}</Button>}
          />
        ) : (
          <Table<AbCaseRow>
            className="self-evolution-abtest-case-table"
            size="small"
            rowKey="caseId"
            columns={columns}
            dataSource={rows}
            loading={rowsLoading}
            pagination={{ pageSize: 10, size: "small", showSizeChanger: false, total: totalSize ?? rows.length }}
            rowClassName={(row) => row.caseId === selectedCaseId ? "is-selected" : ""}
            scroll={{ x: 820 }}
            onRow={(row) => ({
              onClick: () => onSelectCase(row.caseId),
            })}
          />
        )}
      </div>
    </section>
  );
}

function AbSummaryStrip({ observation }: { observation: AbCompareObservation }) {
  const { t } = useTranslation();
  const a = observation.a;
  const b = observation.b;
  return (
    <div className="self-evolution-abtest-summary-strip" aria-label={t("selfEvolutionRun.observation.abTraceSummaryAria")}>
      <span className="self-evolution-abtest-summary-group">
        <span className="is-side">A</span>
        <span><strong>{getDetailRoundCount(a)}</strong><em>{t("selfEvolutionRun.observation.decisionRounds")}</em></span>
        <span><strong>{a.summary.toolCallCount ?? "-"}</strong><em>{t("selfEvolutionRun.observation.toolCalls")}</em></span>
        <span><strong>{a.summary.retrievalCount ?? "-"}</strong><em>{t("selfEvolutionRun.observation.kbRetrievals")}</em></span>
        <strong>{formatDuration(a.summary.latencyMs)}</strong>
      </span>
      <i>→</i>
      <span className="self-evolution-abtest-summary-group">
        <span className="is-side is-b">B</span>
        <span><strong>{getDetailRoundCount(b)}</strong><em>{t("selfEvolutionRun.observation.decisionRounds")}</em></span>
        <span><strong>{b.summary.toolCallCount ?? "-"}</strong><em>{t("selfEvolutionRun.observation.toolCalls")}</em></span>
        <span><strong>{b.summary.retrievalCount ?? "-"}</strong><em>{t("selfEvolutionRun.observation.kbRetrievals")}</em></span>
        <strong>{formatDuration(b.summary.latencyMs)}</strong>
      </span>
    </div>
  );
}

function AbTraceStep({
  row,
  variant,
}: {
  row: FlowRow;
  variant: "a" | "b";
}) {
  const isSearch = row.node.name.includes("kb_search") || row.node.type === "retriever";
  const tone = isSearch ? "warning" : row.tone;
  return (
    <article className={`self-evolution-abtest-trace-step is-${tone}`}>
      <div className="self-evolution-abtest-step-head">
        <strong>{row.title}</strong>
        <span>{row.duration}</span>
        <Tag color={tone === "warning" ? "orange" : getStatusColor(row.node.status)}>{tone === "warning" ? "warning" : row.node.status}</Tag>
      </div>
      <p>{row.desc}</p>
      {isSearch && (
        <div className="self-evolution-abtest-step-fields">
          <span>returned_docs: <strong>{variant === "a" ? 0 : Math.max(1, getAbReturnedDocs(row.node))}</strong></span>
          <span>max_score: <strong>{(getAbMaxScore(row.node) ?? (variant === "a" ? 0.18 : 0.31)).toFixed(2)}</strong></span>
        </div>
      )}
    </article>
  );
}

function AbTraceColumn({
  title,
  variant,
  detail,
  selectedCase,
}: {
  title: string;
  variant: "a" | "b";
  detail: TraceDetailObservation;
  selectedCase: AbCaseRow;
}) {
  const { t } = useTranslation();
  const rowsByRound = useMemo(() => {
    const grouped = new Map<number, FlowRow[]>();
    buildFlowRows(t, detail).forEach((row) => {
      grouped.set(row.round, [...(grouped.get(row.round) || []), row]);
    });
    return Array.from(grouped.entries()).slice(0, 4);
  }, [detail, t]);
  const score = variant === "a" ? selectedCase.aScore : selectedCase.bScore;

  return (
    <section className={`self-evolution-abtest-trace-column is-${variant}`} aria-label={`${title} Trace`}>
      <div className="self-evolution-abtest-column-title">
        <Text strong>{title}</Text>
        <span>{getTraceMode(detail)}</span>
      </div>
      <div className="self-evolution-abtest-algo-grid">
        <span><em>{t("selfEvolutionRun.observation.algoVersion")}</em><strong>{variant === "a" ? "baseline-v1" : "candidate-v2"}</strong></span>
        <span><em>Trace ID</em><strong>{getShortTraceId(detail.traceId)}</strong></span>
        <span><em>Score</em><strong>{score.toFixed(2)}</strong></span>
        <span><em>Latency</em><strong>{formatDuration(detail.summary.latencyMs)}</strong></span>
      </div>
      <div className="self-evolution-abtest-round-list">
        {rowsByRound.map(([round, rows]) => (
          <div key={round} className="self-evolution-abtest-round">
            <div className="self-evolution-abtest-round-head">
              <strong>{`Round ${round}`}</strong>
              <span>{formatDuration(rows.reduce((total, row) => total + (row.node.latencyMs || 0), 0))}</span>
            </div>
            {rows.slice(0, 3).map((row) => (
              <AbTraceStep key={`${variant}-${row.key}`} row={row} variant={variant} />
            ))}
          </div>
        ))}
      </div>
      <div className={`self-evolution-abtest-column-note is-${variant === "a" ? "danger" : "warning"}`}>
        {variant === "a"
          ? t("selfEvolutionRun.observation.abColumnNoteA")
          : t("selfEvolutionRun.observation.abColumnNoteB")}
      </div>
    </section>
  );
}

function AbDiffPanel({
  observation,
}: {
  observation: AbCompareObservation;
}) {
  const { t } = useTranslation();
  const aNode = getSearchNode(observation.a);
  const bNode = getSearchNode(observation.b);
  const aScore = getAbMaxScore(aNode) ?? 0.18;
  const bScore = getAbMaxScore(bNode) ?? 0.31;
  return (
    <section className="self-evolution-abtest-diff-panel" aria-label={t("selfEvolutionRun.observation.abDiffPanelAria")}>
      <Text strong>{t("selfEvolutionRun.observation.abDiffPanelTitle")}</Text>
      <div className="self-evolution-abtest-diff-grid">
        <article>
          <Text strong>{t("selfEvolutionRun.observation.abDiffOutputA")}</Text>
          <dl>
            <dt>returned_docs</dt><dd>{getAbReturnedDocs(aNode)}</dd>
            <dt>max_score</dt><dd>{aScore.toFixed(2)}</dd>
            <dt>{t("selfEvolutionRun.observation.abDiffJudge")}</dt><dd className="is-bad">{t("selfEvolutionRun.observation.abDiffJudgeABad")}</dd>
          </dl>
        </article>
        <article>
          <Text strong>{t("selfEvolutionRun.observation.abDiffOutputB")}</Text>
          <dl>
            <dt>returned_docs</dt><dd>{Math.max(1, getAbReturnedDocs(bNode))}</dd>
            <dt>max_score</dt><dd>{bScore.toFixed(2)}</dd>
            <dt>{t("selfEvolutionRun.observation.abDiffJudge")}</dt><dd className="is-warn">{t("selfEvolutionRun.observation.abDiffJudgeBWarn")}</dd>
          </dl>
        </article>
      </div>
    </section>
  );
}

function AbTraceComparePanel({
  observation,
  selectedCase,
  abtestId,
  loading,
  error,
  onRetry,
}: {
  observation?: AbCompareObservation;
  selectedCase: AbCaseRow;
  abtestId?: string;
  loading?: boolean;
  error?: string;
  onRetry?: () => void;
}) {
  const { t } = useTranslation();
  const reportIdLabel = abtestId && abtestId.length > 16 ? `${abtestId.slice(0, 8)}...${abtestId.slice(-4)}` : abtestId || "abtest";

  if (loading) {
    return (
      <section className="self-evolution-abtest-compare-card" aria-label={t("selfEvolutionRun.observation.abComparePanelAria")}>
        <div className="self-evolution-observation-page-loading">
          <Spin />
          <Text>{t("selfEvolutionRun.observation.loadingAbTrace")}</Text>
        </div>
      </section>
    );
  }

  if (error || !observation) {
    return (
      <section className="self-evolution-abtest-compare-card" aria-label={t("selfEvolutionRun.observation.abComparePanelAria")}>
        <Empty
          description={error || t("selfEvolutionRun.observation.emptyObservation")}
          image={Empty.PRESENTED_IMAGE_SIMPLE}
        />
        {onRetry ? <Button onClick={onRetry}>{t("selfEvolutionRun.observation.retry")}</Button> : null}
      </section>
    );
  }

  return (
    <section className="self-evolution-abtest-compare-card" aria-label={t("selfEvolutionRun.observation.abComparePanelAria")}>
      <div className="self-evolution-abtest-compare-head">
        <Title level={3}>{t("selfEvolutionRun.observation.abComparePanelTitle", { caseId: selectedCase.caseId })}</Title>
        <div>
          <Tag>{`Query: ${selectedCase.query}`}</Tag>
          <Tag>{`Report ID: ${reportIdLabel}`}</Tag>
          <Tag color="orange">{t("selfEvolutionRun.observation.abStatusNeedsAnalysis")}</Tag>
        </div>
      </div>
      <AbSummaryStrip observation={observation} />
      <div className="self-evolution-abtest-trace-columns">
        <AbTraceColumn title={t("selfEvolutionRun.observation.abBaselineTitle")} variant="a" detail={observation.a} selectedCase={selectedCase} />
        <AbTraceColumn title={t("selfEvolutionRun.observation.abOptimizedTitle")} variant="b" detail={observation.b} selectedCase={selectedCase} />
      </div>
      <AbDiffPanel observation={observation} />
      <div className="self-evolution-abtest-conclusion">
        <Text strong>{t("selfEvolutionRun.observation.abConclusionLabel")}</Text>
        <span>{t("selfEvolutionRun.observation.abConclusionText")}</span>
      </div>
    </section>
  );
}

function AbtestObservationDashboard({
  data,
  notice,
  isFallback,
  loading,
  threadId,
  onBack,
  onReload,
  isMenuCollapsed,
  toggleMenu,
}: {
  data: unknown;
  notice?: string;
  isFallback?: boolean;
  loading: boolean;
  threadId?: string;
  onBack: () => void;
  onReload: () => void;
  isMenuCollapsed?: boolean;
  toggleMenu?: () => void;
}) {
  const { t } = useTranslation();
  const abSummary = useMemo(() => buildAbSummaryReports(data)[0], [data]);
  const abtestId = useMemo(() => resolveAbtestIdFromPayload(data), [data]);
  const [caseReloadToken, setCaseReloadToken] = useState(0);
  const [traceReloadToken, setTraceReloadToken] = useState(0);
  const [caseState, setCaseState] = useState<AbCaseListState>({
    loading: false,
    loaded: false,
  });
  const [traceCompareState, setTraceCompareState] = useState<AbTraceCompareState>({
    loading: false,
    loaded: false,
  });
  const [evalReportsState, setEvalReportsState] = useState<EvalReportsTraceState>({
    loading: false,
    loaded: false,
  });
  const traceIdMap = useMemo(() => buildAbCaseTraceIdMap(evalReportsState.data), [evalReportsState.data]);
  const rows = useMemo(() => {
    if (isFallback) {
      return normalizeAbCaseRows(t, data, { useFallback: true });
    }
    if (caseState.loaded && caseState.data) {
      return normalizeAbCaseRows(t, caseState.data);
    }
    const inlineRows = normalizeAbCaseRows(t, data);
    return inlineRows.length ? inlineRows : [];
  }, [caseState.data, caseState.loaded, data, isFallback, t]);
  const fallbackObservation = useMemo(
    () => normalizeTraceObservation(traceCompareFixture),
    [],
  );
  const selectedCaseObservation = useMemo(() => {
    if (isFallback) {
      return fallbackObservation?.kind === "compare" ? fallbackObservation : undefined;
    }
    return normalizeTraceObservation(traceCompareState.data);
  }, [fallbackObservation, isFallback, traceCompareState.data]);
  const [selectedCaseId, setSelectedCaseId] = useState(rows[0]?.caseId || "");
  const selectedCase = rows.find((row) => row.caseId === selectedCaseId) || rows[0];
  const selectedCaseItem = useMemo(
    () => (selectedCase ? findAbCaseDetailItem(caseState.data, selectedCase.caseId) : undefined),
    [caseState.data, selectedCase],
  );

  useEffect(() => {
    if (isFallback || !threadId) {
      setEvalReportsState({ loading: false, loaded: false });
      return;
    }

    const controller = new AbortController();
    setEvalReportsState((prev) => ({ ...prev, loading: true }));

    axiosInstance
      .get(`${AGENT_API_BASE}/threads/${encodeURIComponent(threadId)}/results/eval-reports`, {
        signal: controller.signal,
      })
      .then((response) => {
        if (controller.signal.aborted) {
          return;
        }
        setEvalReportsState({ loading: false, loaded: true, data: response.data });
      })
      .catch((error) => {
        if (isCanceledRequest(error) || controller.signal.aborted) {
          return;
        }
        setEvalReportsState({ loading: false, loaded: true, data: undefined });
      });

    return () => {
      controller.abort();
    };
  }, [isFallback, threadId]);

  useEffect(() => {
    if (isFallback || !threadId || !abtestId) {
      setCaseState({ loading: false, loaded: false });
      return;
    }

    const controller = new AbortController();
    setCaseState((prev) => ({
      abtestId,
      loading: true,
      loaded: prev.abtestId === abtestId ? prev.loaded : false,
      data: prev.abtestId === abtestId ? prev.data : undefined,
      error: undefined,
    }));

    createCoreAgentGeneratedApiClient()
      .apiCoreAgentThreadsThreadIdResultsAbtestsAbtestIdCaseDetailsGet(
        {
          threadId,
          abtestId,
          pageSize: AB_CASE_DETAIL_PAGE_SIZE,
        },
        { signal: controller.signal },
      )
      .then((response) => {
        if (controller.signal.aborted) {
          return;
        }
        setCaseState({
          abtestId,
          loading: false,
          loaded: true,
          data: response.data,
          totalSize: response.data.total_size,
        });
      })
      .catch((error) => {
        if (isCanceledRequest(error) || controller.signal.aborted) {
          return;
        }
        setCaseState((prev) => ({
          ...prev,
          abtestId,
          loading: false,
          loaded: true,
          error: getLocalizedErrorMessage(error, t("selfEvolutionRun.observation.abCaseDetailLoadFailed")),
        }));
      });

    return () => {
      controller.abort();
    };
  }, [abtestId, caseReloadToken, isFallback, threadId, t]);

  useEffect(() => {
    if (isFallback) {
      setTraceCompareState({ loading: false, loaded: false });
      return;
    }
    if (!threadId || !selectedCase?.caseId) {
      setTraceCompareState({ loading: false, loaded: false, error: t("selfEvolutionRun.observation.noTraceId") });
      return;
    }
    if (evalReportsState.loading || !evalReportsState.loaded) {
      setTraceCompareState({
        caseId: selectedCase.caseId,
        loading: true,
        loaded: false,
      });
      return;
    }

    const { a: aTraceId, b: bTraceId } = resolveCaseTraceIds(selectedCaseItem, selectedCase.caseId, traceIdMap);
    if (!aTraceId || !bTraceId || aTraceId === "-" || bTraceId === "-") {
      setTraceCompareState({
        caseId: selectedCase.caseId,
        loading: false,
        loaded: true,
        error: t("selfEvolutionRun.observation.abTraceIdsMissing"),
        aTraceId,
        bTraceId,
      });
      return;
    }

    const controller = new AbortController();
    setTraceCompareState({
      caseId: selectedCase.caseId,
      loading: true,
      loaded: false,
      data: undefined,
      error: undefined,
      aTraceId,
      bTraceId,
    });

    createCoreAgentGeneratedApiClient()
      .apiCoreAgentThreadsThreadIdResultsTracesCompareGet(
        {
          threadId,
          a: aTraceId,
          b: bTraceId,
        },
        { signal: controller.signal },
      )
      .then((response) => {
        if (controller.signal.aborted) {
          return;
        }
        setTraceCompareState({
          caseId: selectedCase.caseId,
          loading: false,
          loaded: true,
          data: response.data,
          aTraceId,
          bTraceId,
        });
      })
      .catch((error) => {
        if (isCanceledRequest(error) || controller.signal.aborted) {
          return;
        }
        setTraceCompareState({
          caseId: selectedCase.caseId,
          loading: false,
          loaded: true,
          error: getLocalizedErrorMessage(error, t("selfEvolutionRun.observation.abTraceCompareLoadFailed")),
          aTraceId,
          bTraceId,
        });
      });

    return () => {
      controller.abort();
    };
  }, [
    evalReportsState.loaded,
    evalReportsState.loading,
    isFallback,
    selectedCase?.caseId,
    selectedCaseItem,
    threadId,
    traceIdMap,
    traceReloadToken,
    t,
  ]);

  useEffect(() => {
    if (!rows.some((row) => row.caseId === selectedCaseId)) {
      setSelectedCaseId(rows[0]?.caseId || "");
    }
  }, [rows, selectedCaseId]);

  return (
    <div className="self-evolution-abtest-dashboard">
      <header className="self-evolution-eval-dashboard-head">
        <ObservationHeaderControls isMenuCollapsed={isMenuCollapsed} toggleMenu={toggleMenu} onBack={onBack} />
        <div className="self-evolution-eval-dashboard-head-right">
          {threadId && <Tag>{`thread ${threadId}`}</Tag>}
          {isFallback && <Tag color="gold">{t("selfEvolutionRun.observation.sampleData")}</Tag>}
          <Button icon={<ReloadOutlined />} loading={loading} onClick={onReload}>{t("selfEvolutionRun.observation.refresh")}</Button>
        </div>
      </header>
      {notice && !loading && <Alert type="warning" showIcon message={notice} />}
      {loading && !data ? (
        <div className="self-evolution-observation-page-loading">
          <Spin />
          <Text>{t("selfEvolutionRun.observation.loadingAbData")}</Text>
        </div>
      ) : selectedCase ? (
        <div className="self-evolution-abtest-dashboard-grid">
          <AbReportPanel
            summary={abSummary}
            rows={rows}
            rowsError={caseState.error}
            rowsLoading={caseState.loading}
            totalSize={caseState.totalSize}
            isFallback={isFallback}
            selectedCaseId={selectedCase.caseId}
            onSelectCase={setSelectedCaseId}
            onReloadRows={() => setCaseReloadToken((prev) => prev + 1)}
          />
          <AbTraceComparePanel
            observation={selectedCaseObservation}
            selectedCase={selectedCase}
            abtestId={abtestId || abSummary?.id}
            loading={!isFallback && traceCompareState.loading}
            error={!isFallback ? traceCompareState.error : undefined}
            onRetry={() => setTraceReloadToken((prev) => prev + 1)}
          />
        </div>
      ) : (
        <section className="self-evolution-observation-json-card" aria-label={t("selfEvolutionRun.observation.rawAbDataAria")}>
          <div className="self-evolution-observation-data-head">
            <div>
              <Text strong>{t("selfEvolutionRun.observation.rawData")}</Text>
              <span>{t("selfEvolutionRun.observation.rawAbDataNote")}</span>
            </div>
          </div>
          <pre>{stringifyResultPayload(data)}</pre>
        </section>
      )}
    </div>
  );
}

export function SelfEvolutionObservationPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { threadId, kind } = useParams<ObservationRouteParams>();
  const { isMenuCollapsed, toggleMenu } = useOutletContext<ObservationPageLayoutContext>();
  const resultKind = normalizeObservationKind(kind);
  const [reloadToken, setReloadToken] = useState(0);
  const [state, setState] = useState<ObservationPageState>({ loading: false, loaded: false });
  const resultUrl = threadId && resultKind
    ? `${AGENT_API_BASE}/threads/${encodeURIComponent(threadId)}/results/${resultKind}`
    : "";

  useEffect(() => {
    if (!threadId || !resultKind) {
      setState({ loading: false, loaded: true, error: t("selfEvolutionRun.observation.routeError") });
      return;
    }

    const controller = new AbortController();
    setState((prev) => ({ ...prev, loading: true, error: undefined }));

    axiosInstance
      .get(resultUrl, { signal: controller.signal })
      .then((response) => {
        if (controller.signal.aborted) {
          return;
        }
        if (isEmptyResultPayload(response.data)) {
          setState({
            loading: false,
            loaded: true,
            data: fallbackObservationData[resultKind],
            notice: resultKind === "eval-reports"
              ? t("selfEvolutionRun.observation.noEvalCsvData")
              : t("selfEvolutionRun.observation.noObservationData"),
            isFallback: true,
          });
          return;
        }
        setState({ loading: false, loaded: true, data: response.data });
      })
      .catch((error) => {
        if (isCanceledRequest(error) || controller.signal.aborted) {
          return;
        }
        const errorMessage = getLocalizedErrorMessage(error, t("selfEvolutionRun.observation.observationLoadFailed"));
        setState({
          loading: false,
          loaded: true,
          data: fallbackObservationData[resultKind],
          notice: resultKind === "eval-reports"
            ? errorMessage
            : t("selfEvolutionRun.observation.observationUnavailable", { error: errorMessage }),
          isFallback: true,
        });
      });

    return () => {
      controller.abort();
    };
  }, [reloadToken, resultKind, resultUrl, threadId]);

  const isEmpty = state.loaded && !state.loading && !state.error && isEmptyResultPayload(state.data);
  const backToDetail = () => {
    if (threadId) {
      navigate(`/self-evolution/detail/${encodeURIComponent(threadId)}`);
      return;
    }
    navigate("/self-evolution");
  };
  const reload = () => setReloadToken((prev) => prev + 1);

  if (resultKind === "eval-reports" && (state.data || state.loading)) {
    return (
      <EvalObservationDashboard
        data={state.data}
        notice={state.notice}
        isFallback={state.isFallback}
        threadId={threadId}
        onBack={backToDetail}
        isMenuCollapsed={isMenuCollapsed}
        toggleMenu={toggleMenu}
      />
    );
  }

  if (resultKind === "abtests" && (state.data || state.loading)) {
    return (
      <AbtestObservationDashboard
        data={state.data}
        notice={state.notice}
        isFallback={state.isFallback}
        loading={state.loading}
        threadId={threadId}
        onBack={backToDetail}
        onReload={reload}
        isMenuCollapsed={isMenuCollapsed}
        toggleMenu={toggleMenu}
      />
    );
  }

  return (
    <div className="self-evolution-observation-page">
      <header className="self-evolution-observation-page-head">
        <div className="self-evolution-observation-page-title">
          <ObservationHeaderControls isMenuCollapsed={isMenuCollapsed} toggleMenu={toggleMenu} onBack={backToDetail} />
          <div>
            <Title level={3}>{t("selfEvolutionRun.observation.pageTitle")}</Title>
            <Paragraph>{t("selfEvolutionRun.observation.pageDesc")}</Paragraph>
          </div>
        </div>
        <div className="self-evolution-observation-page-meta">
          {threadId && <Tag>{`thread ${threadId}`}</Tag>}
          {resultKind && <Tag color="blue">{resultKind}</Tag>}
          {state.isFallback && <Tag color="gold">{t("selfEvolutionRun.observation.sampleData")}</Tag>}
          <Button icon={<ReloadOutlined />} loading={state.loading} onClick={reload}>{t("selfEvolutionRun.observation.refresh")}</Button>
        </div>
      </header>
      <main className="self-evolution-observation-page-body" aria-live="polite">
        {state.notice && !state.loading && <Alert type="warning" showIcon message={state.notice} />}
        {!resultKind ? (
          <Alert type="warning" showIcon message={t("selfEvolutionRun.observation.unknownObservationType")} description={t("selfEvolutionRun.observation.unknownObservationTypeDesc", { kind: kind || "-" })} />
        ) : state.loading && !state.loaded ? (
          <div className="self-evolution-observation-page-loading">
            <Spin />
            <Text>{t("selfEvolutionRun.observation.loadingData")}</Text>
          </div>
        ) : state.error ? (
          <Alert
            type="error"
            showIcon
            message={t("selfEvolutionRun.observation.observationLoadFailedTitle")}
            description={state.error}
            action={<Button size="small" onClick={reload}>{t("selfEvolutionRun.observation.retry")}</Button>}
          />
        ) : isEmpty ? (
          <Empty description={t("selfEvolutionRun.observation.emptyObservations")} />
        ) : (
          <section className="self-evolution-observation-json-card" aria-label={t("selfEvolutionRun.observation.rawDataAria")}>
            <div className="self-evolution-observation-data-head">
              <div>
                <Text strong>{t("selfEvolutionRun.observation.rawData")}</Text>
                <span>{t("selfEvolutionRun.observation.rawDataNote")}</span>
              </div>
            </div>
            <pre>{stringifyResultPayload(state.data)}</pre>
          </section>
        )}
      </main>
    </div>
  );
}
