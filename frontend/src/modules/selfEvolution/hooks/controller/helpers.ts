import { axiosInstance } from "@/components/request";
import {
  StepStatus,
  ThreadRestorePayload,
  NormalizedThreadEvent,
  CheckpointWaitPrompt,
  isRecord,
  getStringField,
  getNumberField,
  getResultItems,
  getResultStringField,
  getNestedStringField,
  getNestedArrayField,
  getNestedRecordField,
  getStructuredArrayField,
  getStructuredRecordField,
  getEventCaseId,
  getEventFlowKind,
  getEventPayloadData,
  getCaseProgressActionStatus,
  formatPercent,
  formatAbMetricLabel,
  stageStepMap,
  toThreadEventStage,
  getStageLabel,
  getCompletedProgressSnapshot,
  t,
  isCheckpointGateFlowStatus,
  getFlowStatusFromPayload,
  resolveTerminalStepStatusFromFlowStatus,
  type ThreadEventStage,
  type WorkflowRuntimeState,
  AGENT_API_BASE,
} from "../../shared";
import { EVAL_REPORT_BAD_CASES_FETCH_CHUNK_SIZE, THREAD_STEP_SUBSCRIBE_POLL_INTERVAL_MS, THREAD_STEP_SUBSCRIBE_POLL_MAX_ATTEMPTS } from "./constants";
import type {
  TFunction,
  ThreadStepSummary,
  ThreadStepListState,
  PxCaseDetailRow,
  AnalysisCategorySummaryRow,
  AnalysisActionableCaseRow,
  DatasetCasePreviewRow,
  DatasetStreamingRow,
  EvalStreamingRow,
  AbtestStreamingRow,
  AnalysisStreamingRow,
} from "./types";
import { analysisCategoryColors } from "./constants";

const DATASET_ARTIFACT_IDS = new Set(["eval_dataset", "eval.dataset"]);

function isDatasetArtifactRecord(candidate: Record<string, unknown>): boolean {
  return (
    Array.isArray(candidate.cases) ||
    typeof candidate.case_num === "number" ||
    Boolean(getStringField(candidate, ["run_id"]))
  );
}

function unwrapDatasetArtifactRecord(
  value: Record<string, unknown>,
): Record<string, unknown> | undefined {
  if (isDatasetArtifactRecord(value)) {
    return value;
  }

  for (const key of ["data", "content", "payload", "result"]) {
    const nested =
      getNestedRecordField(value, [key]) ||
      getStructuredRecordField(value, [key]);
    if (nested && isDatasetArtifactRecord(nested)) {
      return nested;
    }
  }

  return undefined;
}

function formatDatasetScalar(value: unknown): string {
  if (value === undefined || value === null) {
    return "-";
  }
  if (typeof value === "string") {
    return value.trim() || "-";
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

function collectDatasetReferenceLabels(item: Record<string, unknown>): string {
  const references: string[] = [];
  for (const key of ["reference_doc", "reference_doc_ids", "reference_context", "reference_chunk_ids"]) {
    const value = item[key];
    if (Array.isArray(value)) {
      references.push(...value.map((entry) => String(entry || "").trim()).filter(Boolean));
      continue;
    }
    const text = getStringField(item, [key]);
    if (text) {
      references.push(text);
    }
  }
  const source = getStringField(item, ["source"]);
  if (source) {
    references.push(source);
  }
  return references.slice(0, 2).join(" / ") || "-";
}

export function extractDatasetArtifactData(value: unknown): Record<string, unknown> | undefined {
  if (!isRecord(value)) {
    return undefined;
  }

  const direct = unwrapDatasetArtifactRecord(value);
  if (direct) {
    return direct;
  }

  const items = getResultItems(value).filter(isRecord);
  const row =
    items.find((item) => {
      const artifactId = getResultStringField(item, ["artifact_id"]);
      return artifactId ? DATASET_ARTIFACT_IDS.has(artifactId) : false;
    }) || items[0];
  if (!row) {
    return undefined;
  }

  return unwrapDatasetArtifactRecord(row) || row;
}

export function buildDatasetCasePreviewRows(data: Record<string, unknown> | undefined): DatasetCasePreviewRow[] {
  const contentRecord = data
    ? getNestedRecordField(data, ["content"]) ||
      getStructuredRecordField(data, ["content"])
    : undefined;
  const source =
    contentRecord && isDatasetArtifactRecord(contentRecord) ? contentRecord : data;
  const caseRecords = (getStructuredArrayField(source, ["cases"]) ||
    getStructuredArrayField(source, ["preview"]) || []).filter(isRecord);
  return caseRecords.map((item, index) => {
    const caseId =
      getStringField(item, ["case_id", "id"]) ||
      getStringField(item, ["original_id"]) ||
      `case_${String(index + 1).padStart(4, "0")}`;
    return {
      key: caseId,
      caseId,
      question: formatDatasetScalar(item.question ?? getStringField(item, ["question"])),
      answer: formatDatasetScalar(item.answer ?? getStringField(item, ["answer", "ground_truth"])),
      questionType: getStringField(item, ["question_type", "question_type_name"]) || "-",
      difficulty: getStringField(item, ["difficulty"]) || "-",
      references: collectDatasetReferenceLabels(item),
    };
  });
}

export function buildDatasetQuestionTypeCounts(data: Record<string, unknown> | undefined): Record<string, number> {
  const stats = getStructuredRecordField(data, ["stats"]) || getNestedRecordField(data, ["stats"]);
  const legacyCounts =
    getStructuredRecordField(stats, ["question_type_counts"]) ||
    getNestedRecordField(stats, ["question_type_counts"]);
  if (legacyCounts && isRecord(legacyCounts)) {
    return Object.fromEntries(
      Object.entries(legacyCounts).flatMap(([key, count]) =>
        typeof count === "number" ? [[key, count] as const] : [],
      ),
    );
  }

  const counts: Record<string, number> = {};
  for (const item of (getStructuredArrayField(data, ["cases"]) || []).filter(isRecord)) {
    const type = getStringField(item, ["question_type", "question_type_name"]) || "unknown";
    counts[type] = (counts[type] || 0) + 1;
  }
  return counts;
}

export function getDatasetTotalCaseCount(data: Record<string, unknown> | undefined, shownCount: number): number {
  return (
    getNumberField(data, ["case_num", "size", "total_nums", "case_count"]) ||
    (getStructuredArrayField(data, ["case_ids"]) || []).length ||
    shownCount
  );
}

function getStreamingDatasetEventType(event: NormalizedThreadEvent) {
  return (
    getStringField(event.payload, ["event_type"]) ||
    getEventFlowKind(event.payload) ||
    event.type
  );
}

function getStreamingDatasetCaseEventStatus(event: NormalizedThreadEvent): StepStatus | undefined {
  const status = getCaseProgressActionStatus(event);
  if (status) {
    return status;
  }
  const rawAction = getStringField(event.payload, ["action"]);
  if (rawAction === "completed") {
    return "done";
  }
  if (rawAction === "running") {
    return "running";
  }
  return undefined;
}

export function buildStreamingDatasetCaseRows(events: NormalizedThreadEvent[]): DatasetStreamingRow[] {
  const rows = new Map<string, DatasetStreamingRow & { order: number }>();

  events.forEach((event, index) => {
    if (event.stage !== "dataset") {
      return;
    }
    const eventType = getStreamingDatasetEventType(event);
    if (eventType !== "dataset.generate_case" && eventType !== "dataset.prepare_case") {
      return;
    }

    const caseId = getEventCaseId(event.payload);
    if (!caseId) {
      return;
    }

    const status = getStreamingDatasetCaseEventStatus(event);
    if (status !== "running" && status !== "done") {
      return;
    }

    const caseRecord = getNestedRecordField(event.payload, ["case"]);
    const caseIndex = getNumberField(caseRecord, ["index"]) ?? getNumberField(event.payload, ["case_index"]);
    const existing = rows.get(caseId);
    const row: DatasetStreamingRow & { order: number } = {
      key: caseId,
      caseId,
      generateStatus: existing?.generateStatus,
      prepareStatus: existing?.prepareStatus,
      order: existing?.order ?? caseIndex ?? index,
    };

    if (eventType === "dataset.generate_case") {
      row.generateStatus = status;
    } else if (eventType === "dataset.prepare_case") {
      row.prepareStatus = status;
    }

    rows.set(caseId, row);
  });

  return Array.from(rows.values()).sort((left, right) => left.order - right.order);
}

export function getStreamingDatasetProgress(events: NormalizedThreadEvent[]) {
  let current = 0;
  let total = 0;
  events.forEach((event) => {
    if (event.stage !== "dataset") {
      return;
    }
    const eventType = getStreamingDatasetEventType(event);
    if (eventType !== "dataset.generate_case" && eventType !== "dataset.prepare_case") {
      return;
    }
    const progress = getNestedRecordField(event.payload, ["progress"]);
    const eventData = getEventPayloadData(event.payload);
    const nextCurrent = getNumberField(progress, ["current"]) ?? getNumberField(eventData, ["current"]);
    const nextTotal = getNumberField(progress, ["total"]) ?? getNumberField(eventData, ["total", "case_num"]);
    if (eventType === "dataset.generate_case" && typeof nextCurrent === "number") {
      current = Math.max(current, nextCurrent);
    }
    if (typeof nextTotal === "number") {
      total = Math.max(total, nextTotal);
    }
  });
  return { current, total };
}

const ANALYSIS_STREAMING_EVENT_TYPES = new Set([
  "analysis.trace_summary",
  "analysis.classify_case",
]);

function getStreamingAnalysisEventType(event: NormalizedThreadEvent) {
  return (
    getStringField(event.payload, ["event_type"]) ||
    getEventFlowKind(event.payload) ||
    event.type
  );
}

export function buildStreamingAnalysisCaseRows(
  events: NormalizedThreadEvent[],
): AnalysisStreamingRow[] {
  const rows = new Map<string, AnalysisStreamingRow & { order: number }>();

  events.forEach((event, index) => {
    if (event.stage !== "analysis") {
      return;
    }
    const eventType = getStreamingAnalysisEventType(event);
    if (!ANALYSIS_STREAMING_EVENT_TYPES.has(eventType)) {
      return;
    }

    const caseId = getEventCaseId(event.payload);
    if (!caseId) {
      return;
    }

    const status = getStreamingDatasetCaseEventStatus(event);
    if (status !== "running" && status !== "done") {
      return;
    }

    const caseRecord = getNestedRecordField(event.payload, ["case"]);
    const caseIndex =
      getNumberField(caseRecord, ["index"]) ??
      getNumberField(event.payload, ["case_index"]);
    const existing = rows.get(caseId);
    const row: AnalysisStreamingRow & { order: number } = {
      key: caseId,
      caseId,
      traceSummaryStatus: existing?.traceSummaryStatus,
      classifyCaseStatus: existing?.classifyCaseStatus,
      order: existing?.order ?? caseIndex ?? index,
    };

    if (eventType === "analysis.trace_summary") {
      row.traceSummaryStatus = status;
    }
    if (eventType === "analysis.classify_case") {
      row.classifyCaseStatus = status;
    }

    rows.set(caseId, row);
  });

  return Array.from(rows.values()).sort((left, right) => left.order - right.order);
}

export function getStreamingAnalysisProgress(events: NormalizedThreadEvent[]) {
  let current = 0;
  let total = 0;
  events.forEach((event) => {
    if (event.stage !== "analysis") {
      return;
    }
    const eventType = getStreamingAnalysisEventType(event);
    if (eventType !== "analysis.classify_case") {
      return;
    }
    const progress = getNestedRecordField(event.payload, ["progress"]);
    const eventData = getEventPayloadData(event.payload);
    const nextCurrent =
      getNumberField(progress, ["current"]) ??
      getNumberField(eventData, ["current"]);
    const nextTotal =
      getNumberField(progress, ["total"]) ??
      getNumberField(eventData, ["total", "case_num"]);
    if (typeof nextCurrent === "number") {
      current = Math.max(current, nextCurrent);
    }
    if (typeof nextTotal === "number") {
      total = Math.max(total, nextTotal);
    }
  });
  if (total > 0) {
    return { current, total };
  }

  events.forEach((event) => {
    if (event.stage !== "analysis") {
      return;
    }
    const eventType = getStreamingAnalysisEventType(event);
    if (eventType !== "analysis.trace_summary") {
      return;
    }
    const progress = getNestedRecordField(event.payload, ["progress"]);
    const eventData = getEventPayloadData(event.payload);
    const nextCurrent =
      getNumberField(progress, ["current"]) ??
      getNumberField(eventData, ["current"]);
    const nextTotal =
      getNumberField(progress, ["total"]) ??
      getNumberField(eventData, ["total", "case_num"]);
    if (typeof nextCurrent === "number") {
      current = Math.max(current, nextCurrent);
    }
    if (typeof nextTotal === "number") {
      total = Math.max(total, nextTotal);
    }
  });
  return { current, total };
}

const EVAL_STREAMING_EVENT_TYPES = new Set(["eval.answer", "eval.judge", "eval.answer_and_judge"]);

function getStreamingEvalEventType(event: NormalizedThreadEvent) {
  return (
    getStringField(event.payload, ["event_type"]) ||
    getEventFlowKind(event.payload) ||
    event.type
  );
}

export function buildStreamingEvalCaseRows(events: NormalizedThreadEvent[]): EvalStreamingRow[] {
  const rows = new Map<string, EvalStreamingRow & { order: number }>();

  events.forEach((event, index) => {
    if (event.stage !== "eval") {
      return;
    }
    const eventType = getStreamingEvalEventType(event);
    if (!EVAL_STREAMING_EVENT_TYPES.has(eventType)) {
      return;
    }

    const caseId = getEventCaseId(event.payload);
    if (!caseId) {
      return;
    }

    const status = getStreamingDatasetCaseEventStatus(event);
    if (status !== "running" && status !== "done") {
      return;
    }

    const caseRecord = getNestedRecordField(event.payload, ["case"]);
    const caseIndex = getNumberField(caseRecord, ["index"]) ?? getNumberField(event.payload, ["case_index"]);
    const existing = rows.get(caseId);
    const row: EvalStreamingRow & { order: number } = {
      key: caseId,
      caseId,
      answerStatus: existing?.answerStatus,
      judgeStatus: existing?.judgeStatus,
      order: existing?.order ?? caseIndex ?? index,
    };

    if (eventType === "eval.answer" || eventType === "eval.answer_and_judge") {
      row.answerStatus = status;
    }
    if (eventType === "eval.judge" || eventType === "eval.answer_and_judge") {
      row.judgeStatus = status;
    }

    rows.set(caseId, row);
  });

  return Array.from(rows.values()).sort((left, right) => left.order - right.order);
}

function collectStreamingEvalProgressFromEvents(
  events: NormalizedThreadEvent[],
  eventTypes: Set<string>,
) {
  let current = 0;
  let total = 0;
  events.forEach((event) => {
    if (event.stage !== "eval") {
      return;
    }
    const eventType = getStreamingEvalEventType(event);
    if (!eventTypes.has(eventType)) {
      return;
    }
    const progress = getNestedRecordField(event.payload, ["progress"]);
    const eventData = getEventPayloadData(event.payload);
    const nextCurrent =
      getNumberField(progress, ["current"]) ?? getNumberField(eventData, ["current"]);
    const nextTotal =
      getNumberField(progress, ["total"]) ?? getNumberField(eventData, ["total", "case_num"]);
    if (typeof nextCurrent === "number") {
      current = Math.max(current, nextCurrent);
    }
    if (typeof nextTotal === "number") {
      total = Math.max(total, nextTotal);
    }
  });
  return { current, total };
}

export function getStreamingEvalProgress(events: NormalizedThreadEvent[]) {
  const judgeProgress = collectStreamingEvalProgressFromEvents(
    events,
    new Set(["eval.judge", "eval.answer_and_judge"]),
  );
  if (judgeProgress.total > 0) {
    return judgeProgress;
  }
  return collectStreamingEvalProgressFromEvents(events, new Set(["eval.answer"]));
}

const ABTEST_STREAMING_EVENT_TYPES = new Set([
  "abtest.candidate_answer",
  "abtest.candidate_rag_answer",
  "abtest.candidate_judge",
]);

function getStreamingAbtestEventType(event: NormalizedThreadEvent) {
  return (
    getStringField(event.payload, ["event_type"]) ||
    getEventFlowKind(event.payload) ||
    event.type
  );
}

function resolveAbtestStreamingStep(eventType: string): "answer" | "judge" | undefined {
  if (eventType === "abtest.candidate_answer" || eventType === "abtest.candidate_rag_answer") {
    return "answer";
  }
  if (eventType === "abtest.candidate_judge") {
    return "judge";
  }
  return undefined;
}

export function buildStreamingAbtestCaseRows(events: NormalizedThreadEvent[]): AbtestStreamingRow[] {
  const rows = new Map<string, AbtestStreamingRow & { order: number }>();

  events.forEach((event, index) => {
    if (event.stage !== "abtest") {
      return;
    }
    const eventType = getStreamingAbtestEventType(event);
    if (!ABTEST_STREAMING_EVENT_TYPES.has(eventType)) {
      return;
    }
    const step = resolveAbtestStreamingStep(eventType);
    if (!step) {
      return;
    }

    const caseId = getEventCaseId(event.payload);
    if (!caseId) {
      return;
    }

    const status = getStreamingDatasetCaseEventStatus(event);
    if (status !== "running" && status !== "done") {
      return;
    }

    const caseRecord = getNestedRecordField(event.payload, ["case"]);
    const caseIndex =
      getNumberField(caseRecord, ["index"]) ?? getNumberField(event.payload, ["case_index"]);
    const existing = rows.get(caseId);
    const row: AbtestStreamingRow & { order: number } = {
      key: caseId,
      caseId,
      answerStatus: existing?.answerStatus,
      judgeStatus: existing?.judgeStatus,
      order: existing?.order ?? caseIndex ?? index,
    };

    if (step === "answer") {
      row.answerStatus = status;
    }
    if (step === "judge") {
      row.judgeStatus = status;
    }

    rows.set(caseId, row);
  });

  return Array.from(rows.values()).sort((left, right) => left.order - right.order);
}

function collectStreamingAbtestProgressFromEvents(
  events: NormalizedThreadEvent[],
  eventTypes: Set<string>,
) {
  let current = 0;
  let total = 0;
  events.forEach((event) => {
    if (event.stage !== "abtest") {
      return;
    }
    const eventType = getStreamingAbtestEventType(event);
    if (!eventTypes.has(eventType)) {
      return;
    }
    const progress = getNestedRecordField(event.payload, ["progress"]);
    const eventData = getEventPayloadData(event.payload);
    const nextCurrent =
      getNumberField(progress, ["current"]) ?? getNumberField(eventData, ["current"]);
    const nextTotal =
      getNumberField(progress, ["total"]) ?? getNumberField(eventData, ["total", "case_num"]);
    if (typeof nextCurrent === "number") {
      current = Math.max(current, nextCurrent);
    }
    if (typeof nextTotal === "number") {
      total = Math.max(total, nextTotal);
    }
  });
  return { current, total };
}

export function getStreamingAbtestProgress(events: NormalizedThreadEvent[]) {
  const judgeProgress = collectStreamingAbtestProgressFromEvents(
    events,
    new Set(["abtest.candidate_judge"]),
  );
  if (judgeProgress.total > 0) {
    return judgeProgress;
  }
  return collectStreamingAbtestProgressFromEvents(
    events,
    new Set(["abtest.candidate_answer", "abtest.candidate_rag_answer"]),
  );
}

export function buildDatasetCasePreviewRowFromArtifact(caseId: string, value: unknown): Partial<DatasetCasePreviewRow> {
  if (!isRecord(value)) {
    return { caseId };
  }
  const payload =
    unwrapDatasetArtifactRecord(value) ||
    getStructuredRecordField(value, ["data"]) ||
    getNestedRecordField(value, ["data"]) ||
    value;
  if (!isRecord(payload)) {
    return { caseId };
  }
  const [previewRow] = buildDatasetCasePreviewRows({
    cases: [{ ...payload, case_id: getStringField(payload, ["case_id", "id"]) || caseId }],
  });
  return previewRow || { caseId };
}

export function resolveCaseArtifactId(artifactId: string, caseId?: string) {
  const normalizedArtifactId = artifactId.trim();
  const normalizedCaseId = `${caseId || ""}`.trim();
  if (!normalizedArtifactId || !normalizedCaseId) {
    return normalizedArtifactId;
  }
  const versionIndex = normalizedArtifactId.lastIndexOf("@v");
  const baseArtifactId =
    versionIndex >= 0 ? normalizedArtifactId.slice(0, versionIndex) : normalizedArtifactId;
  const versionSuffix =
    versionIndex >= 0 ? normalizedArtifactId.slice(versionIndex) : "";
  if (baseArtifactId.endsWith("]") && baseArtifactId.includes("[")) {
    return normalizedArtifactId;
  }
  return `${baseArtifactId}[${normalizedCaseId}]${versionSuffix}`;
}

export function getFinalResultMetricLabels(t: TFunction): Record<string, string> {
  return {
    answer_correctness: t("selfEvolutionRun.metricAnswerCorrectness"),
    answer_correctness_avg: t("selfEvolutionRun.metricAnswerCorrectness"),
    answer_score: t("selfEvolutionRun.metricAnswerScore"),
    answer_score_avg: t("selfEvolutionRun.metricAnswerScore"),
    chunk_recall: t("selfEvolutionRun.metricChunkRecall"),
    chunk_recall_avg: t("selfEvolutionRun.metricChunkRecall"),
    doc_recall: t("selfEvolutionRun.metricDocRecall"),
    doc_recall_avg: t("selfEvolutionRun.metricDocRecall"),
  };
}

export const formatSignedFinalPercent = (value: number) => `${value > 0 ? "+" : ""}${(value * 100).toFixed(1)}%`;

export function getFinalResultMetricLabel(t: TFunction, metric?: string, fallback?: string) {
  const labels = getFinalResultMetricLabels(t);
  const rawMetric = (metric || "").trim();
  const normalizedMetric = rawMetric.replace(/_(avg|mean)$/, "");
  const knownLabel = labels[rawMetric] || labels[normalizedMetric];
  if (knownLabel) return knownLabel;
  const sharedLabel = formatAbMetricLabel(normalizedMetric || rawMetric);
  if (sharedLabel && sharedLabel !== (normalizedMetric || rawMetric)) return sharedLabel;
  return fallback && !fallback.includes("_") ? fallback : t("selfEvolutionRun.metricOverall");
}

export function humanizeFinalResultReason(t: TFunction, reason: string, primaryMetricLabel: string) {
  const trimmed = reason.trim();
  const primaryMatch = trimmed.match(/primary metric delta\s+(-?\d+(?:\.\d+)?)\s*<\s*target\s+(-?\d+(?:\.\d+)?)/i);
  if (primaryMatch) {
    return t("selfEvolutionRun.reasonPrimaryBelowTarget", {
      metric: primaryMetricLabel,
      delta: formatSignedFinalPercent(Number(primaryMatch[1])),
    });
  }
  const regressionMatch = trimmed.match(/goodcase regression ratio\s+(-?\d+(?:\.\d+)?)\s*<=\s*limit\s+(-?\d+(?:\.\d+)?)/i);
  if (regressionMatch) {
    return t("selfEvolutionRun.reasonRegressionExceeds", {
      ratio: formatPercent(Number(regressionMatch[1])),
      limit: formatPercent(Number(regressionMatch[2])),
    });
  }
  return trimmed
    .replace(/primary metric/gi, primaryMetricLabel)
    .replace(/goodcase regression ratio/gi, t("selfEvolutionRun.goodcaseRegression"))
    .replace(/target/gi, t("selfEvolutionRun.reasonThreshold"))
    .replace(/limit/gi, t("selfEvolutionRun.reasonLimit"))
    .replace(/_/g, " ");
}

export function getBooleanishField(record: Record<string, unknown>, keys: string[]) {
  for (const key of keys) {
    const value = record[key];
    if (typeof value === "boolean") {
      return value;
    }
    if (typeof value === "number" && Number.isFinite(value)) {
      return value !== 0;
    }
    if (typeof value === "string") {
      const normalizedValue = value.trim().toLowerCase();
      if (["true", "1", "yes", "active"].includes(normalizedValue)) {
        return true;
      }
      if (["false", "0", "no", "inactive"].includes(normalizedValue)) {
        return false;
      }
    }
  }
  return false;
}

export function normalizeThreadStepStatus(status?: string): StepStatus | undefined {
  const normalizedStatus = status?.trim().toLowerCase();
  if (!normalizedStatus) {
    return undefined;
  }
  if (["running", "active", "in_progress", "processing", "started", "进行中", "运行中", "执行中"].includes(normalizedStatus)) {
    return "running";
  }
  if (["done", "complete", "completed", "success", "succeeded", "finished", "ended", "已完成", "完成"].includes(normalizedStatus)) {
    return "done";
  }
  if (["cancel", "cancelled", "canceled", "stop", "stopped", "已取消", "取消", "已停止", "停止"].includes(normalizedStatus)) {
    return "canceled";
  }
  if (["failed", "failure", "error", "errored", "已失败", "失败"].includes(normalizedStatus)) {
    return "failed";
  }
  if (["pause", "paused", "waiting_checkpoint", "checkpoint_wait", "已暂停", "暂停"].includes(normalizedStatus)) {
    return "paused";
  }
  if (["pending", "created", "queued", "waiting", "待执行", "等待中"].includes(normalizedStatus)) {
    return "pending";
  }
  return undefined;
}

export function isThreadStepRunning(step: ThreadStepSummary) {
  const normalizedStatus = normalizeThreadStepStatus(step.status);
  return normalizedStatus ? normalizedStatus === "running" : step.active;
}

export function isThreadFlowRunning(status?: string) {
  return normalizeThreadStepStatus(status) === "running";
}

export function getSilentRestoreRequestConfig(signal?: AbortSignal) {
  return { signal, silentError: true } as Parameters<typeof axiosInstance.get>[1];
}

export function shouldUseEventTraceStream(step?: ThreadStepSummary): boolean {
  const stage = step ? toThreadEventStage(step.stage || step.title) : undefined;
  return stage === "repair";
}

export function buildThreadStepEventsStreamUrl(
  threadId: string,
  stepId: string,
  step?: ThreadStepSummary,
): string {
  const streamSegment = shouldUseEventTraceStream(step)
    ? "event-trace:stream"
    : "events:stream";
  const query = new URLSearchParams({ step_id: stepId });
  return `${AGENT_API_BASE}/threads/${encodeURIComponent(threadId)}/${streamSegment}?${query}`;
}

export function normalizeThreadStepListPayload(payload: ThreadRestorePayload): ThreadStepListState {
  const payloadRecord = isRecord(payload) ? payload : undefined;
  const activeStepId = getNestedStringField(payloadRecord, ["active_step_id", "activeStepId"]);
  const stepRecords = getNestedArrayField(payload, ["steps", "items", "records", "data"]);
  const steps = stepRecords
    .filter((item): item is Record<string, unknown> => isRecord(item))
    .flatMap<ThreadStepSummary>((item) => {
      const stepId = getStringField(item, ["step_id", "stepId", "id"]);
      if (!stepId) {
        return [];
      }
      return [{
        stepId,
        title: getStringField(item, ["title", "name"]),
        stage: getStringField(item, ["stage", "step", "step_name", "stepName"]),
        status: getStringField(item, ["status", "state"]),
        active: activeStepId ? activeStepId === stepId : getBooleanishField(item, ["active", "is_active", "isActive"]),
        orderIndex: getNumberField(item, ["order_index", "orderIndex"]),
        eventCount: getNumberField(item, ["event_count", "eventCount"]),
        currentTaskId: getStringField(item, ["current_task_id", "currentTaskId", "task_id", "taskId"]),
        nextStepRunId: getStringField(item, ["next_step_run_id", "nextStepRunId"]),
        startedAt: getStringField(item, ["started_at", "startedAt", "start_time", "startTime"]),
        endedAt: getStringField(item, ["ended_at", "endedAt", "end_time", "endTime"]),
      }];
    });
  return { steps, activeStepId };
}

export function buildThreadStepStatusByStage(
  stepList: ThreadStepListState,
  flowStatus?: string,
): Partial<Record<ThreadEventStage, StepStatus>> {
  const result: Partial<Record<ThreadEventStage, StepStatus>> = {};
  for (const step of stepList.steps) {
    const stage = toThreadEventStage(step.stage || step.title);
    const normalizedStatus = resolveCheckpointAwareStepStatus(
      normalizeThreadStepStatus(step.status),
      { flowStatus, step, stage },
    );
    if (stage && normalizedStatus) {
      result[stage] = normalizedStatus;
    }
  }
  return result;
}

export function applyThreadStepListToWorkflowRuntimeState(
  prev: WorkflowRuntimeState,
  stepList: ThreadStepListState,
): WorkflowRuntimeState {
  if (!stepList.steps.length) {
    return prev;
  }

  const next: WorkflowRuntimeState = { ...prev };
  for (const stage of Object.keys(stageStepMap) as ThreadEventStage[]) {
    const stepId = stageStepMap[stage];
    next[stepId] = { ...prev[stepId] };
  }

  for (const step of stepList.steps) {
    const stage = toThreadEventStage(step.stage || step.title);
    const normalizedStatus = normalizeThreadStepStatus(step.status);
    if (!stage || !normalizedStatus) {
      continue;
    }
    const workflowStepId = stageStepMap[stage];
    next[workflowStepId] = {
      ...next[workflowStepId],
      status: normalizedStatus,
      progress:
        normalizedStatus === "done"
          ? next[workflowStepId].progress || getCompletedProgressSnapshot()
          : next[workflowStepId].progress,
    };
  }

  return next;
}

export function getDefaultThreadStep(stepList: ThreadStepListState): ThreadStepSummary | undefined {
  const activeStep = stepList.activeStepId
    ? stepList.steps.find((step) => step.stepId === stepList.activeStepId)
    : undefined;
  return activeStep ||
    stepList.steps[stepList.steps.length - 1] ||
    (stepList.activeStepId ? { stepId: stepList.activeStepId, active: true, status: "running" } : undefined);
}

export function resolveNextStepRunIdFromStepList(stepList: ThreadStepListState): string | undefined {
  for (let index = stepList.steps.length - 1; index >= 0; index -= 1) {
    const step = stepList.steps[index];
    if (!step.nextStepRunId || isThreadStepRunning(step)) {
      continue;
    }
    return step.nextStepRunId;
  }
  return undefined;
}

export function resolveContinueThreadStepId(
  stepList: ThreadStepListState,
): string | undefined {
  const waitingStep = getCheckpointWaitingStep(stepList);
  if (waitingStep?.nextStepRunId?.trim()) {
    return waitingStep.nextStepRunId.trim();
  }
  return resolveNextStepRunIdFromStepList(stepList);
}

const THREAD_EVENT_STAGE_ORDER: ThreadEventStage[] = [
  "dataset",
  "eval",
  "analysis",
  "repair",
  "abtest",
];

export function resolveArtifactItemForThreadStep<T extends { kind: string }>(
  step: ThreadStepSummary,
  fallbackIndex: number,
  artifactItems: T[],
  stageKindMap: Record<string, string>,
): T | undefined {
  const stage = toThreadEventStage(step.stage || step.title);
  if (stage) {
    const kind = stageKindMap[stage];
    if (kind) {
      const matched = artifactItems.find((item) => item.kind === kind);
      if (matched) {
        return matched;
      }
    }
  }
  return artifactItems[fallbackIndex];
}

export function resolveThreadStepViewStage(
  step: ThreadStepSummary,
  workflowStepId?: string,
  workflowStepStageMap?: Record<string, string>,
): ThreadEventStage | undefined {
  return (
    toThreadEventStage(step.stage || step.title) ||
    (workflowStepId && workflowStepStageMap
      ? toThreadEventStage(workflowStepStageMap[workflowStepId])
      : undefined)
  );
}

export function sortThreadStepsByOrder(steps: ThreadStepSummary[]) {
  return [...steps].sort((left, right) => {
    const leftOrder = left.orderIndex;
    const rightOrder = right.orderIndex;
    if (typeof leftOrder === "number" && typeof rightOrder === "number" && leftOrder !== rightOrder) {
      return leftOrder - rightOrder;
    }
    if (typeof leftOrder === "number" && typeof rightOrder !== "number") {
      return -1;
    }
    if (typeof leftOrder !== "number" && typeof rightOrder === "number") {
      return 1;
    }
    return left.stepId.localeCompare(right.stepId);
  });
}

export function isStepCheckpointWaiting(step: ThreadStepSummary) {
  if (isThreadStepRunning(step)) {
    return false;
  }
  const status = normalizeThreadStepStatus(step.status);
  if (status !== "done" && status !== "paused") {
    return false;
  }
  return Boolean(step.nextStepRunId?.trim());
}

export function resolveCheckpointAwareStepStatus(
  status: StepStatus | undefined,
  options?: {
    flowStatus?: string;
    step?: ThreadStepSummary;
    completedStage?: ThreadEventStage;
    stage?: ThreadEventStage;
  },
): StepStatus | undefined {
  if (!status || status !== "paused") {
    return status;
  }
  if (isCheckpointGateFlowStatus(options?.flowStatus)) {
    return "done";
  }
  if (options?.step && isStepCheckpointWaiting(options.step)) {
    return "done";
  }
  if (
    options?.completedStage &&
    options?.stage &&
    options.completedStage === options.stage
  ) {
    return "done";
  }
  return status;
}

export function getCheckpointWaitingStep(
  stepList: ThreadStepListState,
): ThreadStepSummary | undefined {
  const ordered = sortThreadStepsByOrder(stepList.steps);
  if (!ordered.length) {
    return undefined;
  }

  let waitingIndex = -1;
  for (let index = ordered.length - 1; index >= 0; index -= 1) {
    const step = ordered[index];
    const stage = toThreadEventStage(step.stage || step.title);
    if (stage === "abtest") {
      return undefined;
    }
    if (isStepCheckpointWaiting(step)) {
      waitingIndex = index;
      break;
    }
  }

  if (waitingIndex < 0) {
    return undefined;
  }

  const waitingStep = ordered[waitingIndex];
  const waitingStage = toThreadEventStage(waitingStep.stage || waitingStep.title);
  if (!waitingStage || waitingStage === "abtest") {
    return undefined;
  }

  const subsequentStarted = ordered.slice(waitingIndex + 1).some((step) => {
    const status = normalizeThreadStepStatus(step.status);
    return isThreadStepRunning(step) || status === "done";
  });
  if (subsequentStarted) {
    return undefined;
  }

  return waitingStep;
}

export function buildCheckpointPromptForCompletedStage(
  completedStage: ThreadEventStage,
): CheckpointWaitPrompt | undefined {
  const stageIndex = THREAD_EVENT_STAGE_ORDER.indexOf(completedStage);
  const nextStage = stageIndex >= 0 ? THREAD_EVENT_STAGE_ORDER[stageIndex + 1] : undefined;
  if (!nextStage) {
    return undefined;
  }

  const completedStageLabel = getStageLabel(completedStage);
  const nextOperationLabel = getStageLabel(nextStage);
  return {
    kind: "checkpoint",
    message: t("selfEvolutionRun.checkpointStageDoneConfirmNext", {
      stageLabel: completedStageLabel,
    }),
    completedStage,
    completedStageLabel,
    nextStage,
    nextOperationLabel,
    command: t("selfEvolutionRun.continueExecution"),
  };
}

function isStageAlreadyStarted(
  stage: ThreadEventStage,
  stepList: ThreadStepListState,
  stepStatusByStage?: Partial<Record<ThreadEventStage, StepStatus>>,
) {
  const status = stepStatusByStage?.[stage];
  if (status === "running" || status === "done") {
    return true;
  }

  const step = sortThreadStepsByOrder(stepList.steps).find(
    (item) => toThreadEventStage(item.stage || item.title) === stage,
  );
  if (!step) {
    return false;
  }

  const normalizedStatus = normalizeThreadStepStatus(step.status);
  return isThreadStepRunning(step) || normalizedStatus === "done";
}

export function isCheckpointPromptSuperseded(
  prompt: CheckpointWaitPrompt,
  stepList: ThreadStepListState,
  stepStatusByStage?: Partial<Record<ThreadEventStage, StepStatus>>,
) {
  if (
    prompt.kind === "failure" ||
    prompt.checkpointKind === "intent_confirmation" ||
    prompt.checkpointKind === "manual_cutover"
  ) {
    return false;
  }

  if (prompt.nextStage && isStageAlreadyStarted(prompt.nextStage, stepList, stepStatusByStage)) {
    return true;
  }

  if (!prompt.completedStage) {
    return false;
  }

  const completedIndex = THREAD_EVENT_STAGE_ORDER.indexOf(prompt.completedStage);
  if (completedIndex < 0) {
    return false;
  }

  return THREAD_EVENT_STAGE_ORDER.slice(completedIndex + 1).some((stage) =>
    isStageAlreadyStarted(stage, stepList, stepStatusByStage),
  );
}

function finalizeCheckpointPrompt(
  prompt: CheckpointWaitPrompt | undefined,
  stepList: ThreadStepListState,
  stepStatusByStage?: Partial<Record<ThreadEventStage, StepStatus>>,
) {
  if (!prompt || isCheckpointPromptSuperseded(prompt, stepList, stepStatusByStage)) {
    return undefined;
  }
  return prompt;
}

export function resolveStepListCheckpointPrompt(
  stepList: ThreadStepListState,
  flowStatus?: string,
  stepStatusByStage?: Partial<Record<ThreadEventStage, StepStatus>>,
): CheckpointWaitPrompt | undefined {
  const waitingStep = getCheckpointWaitingStep(stepList);
  if (waitingStep) {
    const completedStage = toThreadEventStage(waitingStep.stage || waitingStep.title);
    if (!completedStage || completedStage === "abtest") {
      return undefined;
    }
    return finalizeCheckpointPrompt(
      buildCheckpointPromptForCompletedStage(completedStage),
      stepList,
      stepStatusByStage,
    );
  }

  if (normalizeThreadStepStatus(flowStatus) !== "paused") {
    return undefined;
  }

  const ordered = sortThreadStepsByOrder(stepList.steps);
  const lastStep = ordered[ordered.length - 1];
  if (!lastStep) {
    return undefined;
  }

  const lastStage = toThreadEventStage(lastStep.stage || lastStep.title);
  if (!lastStage || lastStage === "abtest") {
    return undefined;
  }

  const lastStatus = normalizeThreadStepStatus(lastStep.status);
  if ((lastStatus !== "done" && lastStatus !== "paused") || isThreadStepRunning(lastStep)) {
    return undefined;
  }

  return finalizeCheckpointPrompt(
    buildCheckpointPromptForCompletedStage(lastStage),
    stepList,
    stepStatusByStage,
  );
}

export function buildCompletedFlowCheckpointPrompt(
  event: Pick<NormalizedThreadEvent, "stage" | "payload">,
): CheckpointWaitPrompt | undefined {
  if (getFlowStatusFromPayload(event.payload) !== "completed" || !event.stage) {
    return undefined;
  }

  const completedStage = toThreadEventStage(event.stage);
  if (!completedStage || completedStage === "abtest") {
    return undefined;
  }

  return buildCheckpointPromptForCompletedStage(completedStage);
}

export function markThreadStepStageCompleted(
  stepList: ThreadStepListState,
  completedStage: ThreadEventStage,
  flowStatus?: string,
): ThreadStepListState {
  const stepStatus = resolveTerminalStepStatusFromFlowStatus(flowStatus);
  return {
    ...stepList,
    activeStepId: undefined,
    steps: stepList.steps.map((step) => {
      const stage = toThreadEventStage(step.stage || step.title);
      if (stage !== completedStage) {
        return step;
      }
      return {
        ...step,
        status: stepStatus,
        active: false,
      };
    }),
  };
}

export function isCheckpointContinueCommand(
  text: string,
  checkpointPrompt: CheckpointWaitPrompt | undefined,
  continueExecutionText: string,
  checkpointCommandText: string,
) {
  const normalized = text.trim();
  if (!normalized) {
    return false;
  }
  const candidates = new Set(
    [continueExecutionText, checkpointCommandText, checkpointPrompt?.command]
      .filter((item): item is string => Boolean(item?.trim())),
  );
  return candidates.has(normalized);
}

export function getNextStepRunId(event: NormalizedThreadEvent) {
  const payload = event.payload;
  const eventPayload = getNestedRecordField(payload, ["payload"]);
  const dataPayload = getNestedRecordField(payload, ["data"]);
  const eventDataPayload = getNestedRecordField(eventPayload, ["data"]);
  const rawEvent = getNestedRecordField(payload, ["raw_event", "rawEvent"]);
  const rawEventDataPayload = getNestedRecordField(rawEvent, ["data"]);

  return (
    getStringField(payload, ["next_step_run_id", "nextStepRunId"]) ||
    getStringField(eventPayload, ["next_step_run_id", "nextStepRunId"]) ||
    getStringField(dataPayload, ["next_step_run_id", "nextStepRunId"]) ||
    getStringField(eventDataPayload, ["next_step_run_id", "nextStepRunId"]) ||
    getStringField(rawEvent, ["next_step_run_id", "nextStepRunId"]) ||
    getStringField(rawEventDataPayload, ["next_step_run_id", "nextStepRunId"])
  );
}

function sleep(ms: number, signal?: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    if (signal?.aborted) {
      reject(new DOMException("Aborted", "AbortError"));
      return;
    }
    const timer = window.setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, ms);
    const onAbort = () => {
      window.clearTimeout(timer);
      reject(new DOMException("Aborted", "AbortError"));
    };
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

export function resolveSubscribeThreadStepId(
  stepList: ThreadStepListState,
  threadId?: string,
  resolveFallbackStepId?: (threadId: string) => string,
): string | undefined {
  if (getCheckpointWaitingStep(stepList)) {
    return undefined;
  }

  const activeStep = stepList.activeStepId
    ? stepList.steps.find((step) => step.stepId === stepList.activeStepId)
    : undefined;
  if (activeStep?.stepId) {
    return activeStep.stepId;
  }

  const runningStep = [...stepList.steps].reverse().find((step) => isThreadStepRunning(step));
  if (runningStep?.stepId) {
    return runningStep.stepId;
  }

  const latestStep = getDefaultThreadStep(stepList);
  if (latestStep?.stepId) {
    return latestStep.stepId;
  }

  if (threadId && resolveFallbackStepId) {
    return resolveFallbackStepId(threadId);
  }
  return undefined;
}

export async function waitForSubscribableThreadStep(
  fetchStepList: () => Promise<ThreadStepListState>,
  options?: {
    signal?: AbortSignal;
    maxAttempts?: number;
    intervalMs?: number;
  },
): Promise<ThreadStepListState | undefined> {
  const maxAttempts = options?.maxAttempts ?? THREAD_STEP_SUBSCRIBE_POLL_MAX_ATTEMPTS;
  const intervalMs = options?.intervalMs ?? THREAD_STEP_SUBSCRIBE_POLL_INTERVAL_MS;

  for (let attempt = 0; attempt < maxAttempts; attempt += 1) {
    if (options?.signal?.aborted) {
      return undefined;
    }

    const stepList = await fetchStepList();
    const activeOrRunningStep = stepList.steps.find(
      (step) => step.active || isThreadStepRunning(step),
    );
    if (activeOrRunningStep?.stepId) {
      return stepList;
    }

    if (stepList.steps.length > 0 && attempt >= 4) {
      return stepList;
    }

    if (attempt < maxAttempts - 1) {
      try {
        await sleep(intervalMs, options?.signal);
      } catch {
        return undefined;
      }
    }
  }

  try {
    return await fetchStepList();
  } catch {
    return undefined;
  }
}

export function getEvalReportSourceRecord(resultData: unknown) {
  const resultItems = getResultItems(resultData).filter(isRecord);
  if (resultItems.length > 0) {
    return resultItems[0];
  }
  return isRecord(resultData) ? resultData : undefined;
}

export function getEvalReportPayloadRecord(sourceRecord: Record<string, unknown> | undefined) {
  return (
    getStructuredRecordField(sourceRecord, ["data"]) ||
    getNestedRecordField(sourceRecord, ["data"]) ||
    sourceRecord
  );
}

export function getEvalReportId(resultData: unknown) {
  const sourceRecord = getEvalReportSourceRecord(resultData);
  const reportRecord = getEvalReportPayloadRecord(sourceRecord);

  return (
    getStringField(sourceRecord, ["report_id", "reportId"]) ||
    getStringField(reportRecord, ["report_id", "reportId", "id"])
  );
}

export function getEvalReportBadCaseListRecords(resultData: unknown): Record<string, unknown>[] {
  if (Array.isArray(resultData)) {
    return resultData.filter(isRecord);
  }
  if (!isRecord(resultData)) {
    return [];
  }

  const payloadRecord =
    getStructuredRecordField(resultData, ["data"]) ||
    getNestedRecordField(resultData, ["data"]) ||
    resultData;
  return (getStructuredArrayField(payloadRecord, ["items"]) || []).filter(isRecord);
}

export function getEvalReportBadCasesPayloadRecord(resultData: unknown) {
  if (!isRecord(resultData)) {
    return undefined;
  }

  return (
    getStructuredRecordField(resultData, ["data"]) ||
    getNestedRecordField(resultData, ["data"]) ||
    resultData
  );
}

export function mergeEvalReportBadCasesResponses(responses: unknown[]) {
  const mergedItems = responses.flatMap((response) => getEvalReportBadCaseListRecords(response));
  const lastResponse = responses[responses.length - 1];
  const lastRecord = isRecord(lastResponse) ? lastResponse : {};
  const lastPayload = getEvalReportBadCasesPayloadRecord(lastResponse) || {};

  return {
    data: {
      ...lastRecord,
      data: {
        ...(isRecord(lastPayload) ? lastPayload : {}),
        items: mergedItems,
        total_size: mergedItems.length,
        total_count: mergedItems.length,
      },
    },
    totalSize: mergedItems.length,
  };
}

export async function fetchAllEvalReportBadCases(
  threadId: string,
  reportId: string,
  options?: { signal?: AbortSignal },
) {
  const responses: unknown[] = [];
  let pageToken: string | undefined;

  while (true) {
    const response = await axiosInstance.get(
      `${AGENT_API_BASE}/threads/${encodeURIComponent(threadId)}/results/eval-reports/${encodeURIComponent(reportId)}/bad-cases`,
      {
        params: {
          ...(pageToken ? { page_token: pageToken } : {}),
          page_size: EVAL_REPORT_BAD_CASES_FETCH_CHUNK_SIZE,
        },
        signal: options?.signal,
      },
    );
    responses.push(response.data);
    const responseRecord = getEvalReportBadCasesPayloadRecord(response.data);
    const items = getEvalReportBadCaseListRecords(response.data);
    const totalSize = getNumberField(responseRecord, ["total_size", "total_count", "total"]);
    const nextPageToken = getStringField(responseRecord, ["next_page_token", "nextPageToken"]);
    const mergedCount = responses.flatMap((item) => getEvalReportBadCaseListRecords(item)).length;
    if (!nextPageToken || items.length === 0) {
      break;
    }
    if (typeof totalSize === "number" && mergedCount >= totalSize) {
      break;
    }
    pageToken = nextPageToken;
  }

  return mergeEvalReportBadCasesResponses(responses);
}

export function buildPxCaseDetailRows(caseRecords: Record<string, unknown>[]) {
  const seen = new Set<string>();

  return caseRecords.flatMap((item, index): PxCaseDetailRow[] => {
    const caseId = getStringField(item, ["case_id", "caseId", "case", "id"]) || `case-${index + 1}`;
    if (seen.has(caseId)) {
      return [];
    }
    seen.add(caseId);
    const score =
      getNumberField(item, [
        "score",
        "metric_score",
        "answer_correctness",
        "value",
        "overall",
        "correctness",
      ]);

    return [{
      key: caseId,
      caseId,
      question: getStringField(item, ["query", "question", "prompt", "Question"]) || "-",
      score: typeof score === "number" ? score.toFixed(2) : "-",
      failureType: getStringField(item, ["failure_type", "failureType", "failure_reason", "fail_reason", "category"]) || "-",
      defect: getStringField(item, ["Defect", "defect"]) || "-",
      reason: getStringField(item, ["Reason", "reason", "failure_detail"]) || "-",
      traceId: getStringField(item, ["trace_id", "traceId"]) || "-",
    }];
  });
}

export function getAnalysisCategoryCount(value: unknown) {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string" && value.trim() && Number.isFinite(Number(value))) {
    return Number(value);
  }
  if (isRecord(value)) {
    return getNumberField(value, ["count", "case_count", "caseCount", "total", "value"]);
  }
  return undefined;
}

function buildKeyedCountSummaryRows(
  counts: Record<string, unknown> | undefined,
  uncategorizedLabel: string,
): AnalysisCategorySummaryRow[] {
  const countedRows = Object.entries(counts || {})
    .map(([category, value]) => ({
      category,
      count: getAnalysisCategoryCount(value),
    }))
    .filter((item): item is { category: string; count: number } => typeof item.count === "number");
  const total = countedRows.reduce((sum, item) => sum + item.count, 0);

  return countedRows
    .sort((a, b) => b.count - a.count || a.category.localeCompare(b.category))
    .map((item, index) => ({
      key: item.category,
      category: item.category || uncategorizedLabel,
      count: item.count,
      ratio: total > 0 ? formatPercent(item.count / total) : "-",
      ratioValue: total > 0 ? item.count / total : 0,
      color: analysisCategoryColors[index % analysisCategoryColors.length],
    }));
}

export function extractAnalysisSummaryContent(
  data: unknown,
): Record<string, unknown> | undefined {
  if (!isRecord(data)) {
    return undefined;
  }

  const directId = getStringField(data, ["id"]);
  if (
    directId === "analysis.summary" ||
    data.actionable_cases !== undefined ||
    data.actionable_case !== undefined ||
    data.affected_block_counts !== undefined
  ) {
    return data;
  }

  const content =
    getStructuredRecordField(data, ["content"]) ||
    getNestedRecordField(data, ["content"]);
  if (isRecord(content)) {
    const nested = extractAnalysisSummaryContent(content);
    if (nested) {
      return nested;
    }
  }

  const items = getResultItems(data).filter(isRecord);
  for (const item of items) {
    const artifactId =
      getResultStringField(item, ["artifact_id", "artifactId", "id"]) || "";
    if (
      artifactId !== "analysis.summary" &&
      artifactId !== "classification_report"
    ) {
      continue;
    }
    const nestedData =
      getStructuredRecordField(item, ["data"]) ||
      getNestedRecordField(item, ["data"]) ||
      item;
    const nested = extractAnalysisSummaryContent(nestedData);
    if (nested) {
      return nested;
    }
  }

  return undefined;
}

export function buildAnalysisActionableCaseRows(
  content: Record<string, unknown> | undefined,
): AnalysisActionableCaseRow[] {
  const cases =
    getStructuredArrayField(content, ["actionable_cases", "actionable_case"]) ||
    [];
  return cases
    .filter(isRecord)
    .map((item, index) => ({
      key:
        getStringField(item, ["case_id", "id"]) ||
        `actionable-case-${index + 1}`,
      caseId:
        getStringField(item, ["case_id", "id"]) || `case_${index + 1}`,
      issueType: getStringField(item, ["issue_type", "issueType"]) || "-",
      affectedBlock:
        getStringField(item, ["affected_block", "affectedBlock"]) || "-",
      failureMode:
        getStringField(item, ["failure_mode", "failureMode"]) || "-",
      confidence: getStringField(item, ["confidence"]) || "-",
      reason:
        getStringField(item, ["reason", "root_cause_reason", "rootCauseReason"]) ||
        "-",
      clusterId: getStringField(item, ["cluster_id", "clusterId"]) || "-",
      outlierScore: String(getNumberField(item, ["outlier_score", "outlierScore"]) ?? "-"),
    }));
}

export function buildAffectedBlockCountRows(
  content: Record<string, unknown> | undefined,
  uncategorizedLabel = "Uncategorized",
): AnalysisCategorySummaryRow[] {
  const counts =
    getStructuredRecordField(content, [
      "affected_block_counts",
      "affectedBlockCounts",
    ]) ||
    getNestedRecordField(content, [
      "affected_block_counts",
      "affectedBlockCounts",
    ]);
  if (counts && Object.keys(counts).length > 0) {
    return buildKeyedCountSummaryRows(counts, uncategorizedLabel);
  }

  const fallbackCounts =
    getStructuredRecordField(content, [
      "issue_category_counts",
      "issueCategoryCounts",
    ]) ||
    getNestedRecordField(content, [
      "issue_category_counts",
      "issueCategoryCounts",
    ]);
  return buildKeyedCountSummaryRows(fallbackCounts, uncategorizedLabel);
}

export function buildAnalysisCategorySummaryRows(
  summary: Record<string, unknown> | undefined,
  uncategorizedLabel = "Uncategorized",
): AnalysisCategorySummaryRow[] {
  const coarseCounts =
    getStructuredRecordField(summary, ["coarse_category_counts", "coarseCategoryCounts"]) ||
    getNestedRecordField(summary, ["coarse_category_counts", "coarseCategoryCounts"]);
  return buildKeyedCountSummaryRows(coarseCounts, uncategorizedLabel);
}
