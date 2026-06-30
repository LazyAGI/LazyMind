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
  getNestedStringField,
  getNestedArrayField,
  getNestedRecordField,
  getStructuredArrayField,
  getStructuredRecordField,
  formatPercent,
  formatAbMetricLabel,
  parseSSEFrame,
  normalizeThreadEvent,
} from "../../shared";
import type {
  TFunction,
  ThreadStepSummary,
  ThreadStepListState,
  PxCaseDetailRow,
  AnalysisCategorySummaryRow,
} from "./types";
import { analysisCategoryColors } from "./constants";

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

export function parseThreadRecordFrames(rawData: string, fallbackId?: string, fallbackEventName = "message") {
  const text = rawData.trim();
  if (!text) {
    return [];
  }

  const parsedFrames = text
    .split(/\r?\n\r?\n/)
    .map((rawFrame) => parseSSEFrame(rawFrame.trim()))
    .filter((frame): frame is NonNullable<ReturnType<typeof parseSSEFrame>> => Boolean(frame));
  if (parsedFrames.length > 0) {
    return parsedFrames.map((frame) => ({
      ...frame,
      id: frame.id || fallbackId,
      eventName: frame.eventName || fallbackEventName,
    }));
  }

  return [{
    id: fallbackId,
    eventName: fallbackEventName,
    data: text,
  }];
}

export function normalizeThreadRecordEvents(payload: ThreadRestorePayload): NormalizedThreadEvent[] {
  const records = getNestedArrayField(payload, ["records", "events", "items", "data"]);
  const sourceRecords = records.length > 0 ? records : isRecord(payload) ? [payload] : [];

  return sourceRecords.flatMap((record, index) => {
    if (typeof record === "string") {
      return parseThreadRecordFrames(record).map((frame) => normalizeThreadEvent(frame));
    }
    if (!isRecord(record)) {
      return [];
    }

    const rawFrame = getStringField(record, ["raw_frame", "rawFrame", "frame", "sse", "raw"]);
    const fallbackId = getStringField(record, ["id", "event_id", "eventId", "record_id", "recordId"]) || `record-${index}`;
    const fallbackEventName = getStringField(record, ["event_name", "eventName", "event", "type"]) || "message";
    if (rawFrame) {
      return parseThreadRecordFrames(rawFrame, fallbackId, fallbackEventName)
        .map((frame) => normalizeThreadEvent(frame));
    }

    const dataValue =
      record.data ??
      record.payload ??
      record.event_payload ??
      record.body ??
      record.message ??
      record.content ??
      record;
    const data = typeof dataValue === "string" ? dataValue : JSON.stringify(dataValue);
    return parseThreadRecordFrames(data, fallbackId, fallbackEventName)
      .map((frame) => normalizeThreadEvent(frame));
  });
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

export function buildPxCaseDetailRows(caseRecords: Record<string, unknown>[]) {
  const seen = new Set<string>();

  return caseRecords.flatMap((item, index): PxCaseDetailRow[] => {
    const caseId = getStringField(item, ["case_id", "caseId", "case", "id"]) || `case-${index + 1}`;
    if (seen.has(caseId)) {
      return [];
    }
    seen.add(caseId);
    const score = getNumberField(item, ["score", "metric_score", "answer_correctness", "value"]);

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

export function buildAnalysisCategorySummaryRows(
  summary: Record<string, unknown> | undefined,
  uncategorizedLabel = "Uncategorized",
): AnalysisCategorySummaryRow[] {
  const coarseCounts =
    getStructuredRecordField(summary, ["coarse_category_counts", "coarseCategoryCounts"]) ||
    getNestedRecordField(summary, ["coarse_category_counts", "coarseCategoryCounts"]);
  const countedRows = Object.entries(coarseCounts || {})
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
