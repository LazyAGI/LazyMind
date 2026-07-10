import {
  getNestedRecordField,
  getNumberField,
  getStringField,
  getStructuredArrayField,
  isRecord,
} from "./fields";
import { formatAbMetricLabel } from "./format";
import type { AbSummaryReport } from "./types";

export type AbtestComparisonMetricRow = {
  key: string;
  label: string;
  origin: number;
  candidate: number;
  delta: number;
};

export type AbtestComparisonCaseRow = {
  key: string;
  caseId: string;
  originOverall: number;
  candidateOverall: number;
  deltaOverall: number;
  originCorrectness: number;
  candidateCorrectness: number;
  originTraceId?: string;
  candidateTraceId?: string;
  reason?: string;
};

export type AbtestComparisonArtifact = {
  runId: string;
  algoId: string;
  candidateAlgoId: string;
  status: string;
  verdict: string;
  reasons: string[];
  metricRows: AbtestComparisonMetricRow[];
  caseRows: AbtestComparisonCaseRow[];
};

const COMPARISON_METRIC_FIELDS = [
  "avg_correctness",
  "avg_relevance",
  "avg_completeness",
  "avg_groundedness",
  "avg_format_compliance",
  "avg_answer_quality",
  "avg_retrieval_quality",
  "avg_overall",
  "correct_rate",
] as const;

const COMPARISON_METRIC_LABELS: Record<(typeof COMPARISON_METRIC_FIELDS)[number], string> = {
  avg_correctness: "correctness",
  avg_relevance: "relevance",
  avg_completeness: "completeness",
  avg_groundedness: "groundedness",
  avg_format_compliance: "formatCompliance",
  avg_answer_quality: "answerQuality",
  avg_retrieval_quality: "retrievalQuality",
  avg_overall: "overall",
  correct_rate: "correctRate",
};

const COMPARISON_FIELD_TO_PX_METRIC: Partial<Record<string, string>> = {
  avg_correctness: "answer_correctness",
  avg_overall: "answer_score",
  avg_retrieval_quality: "chunk_recall",
  avg_groundedness: "doc_recall",
};

function getComparisonMetricDisplayLabel(field: string): string {
  const pxKey = COMPARISON_FIELD_TO_PX_METRIC[field];
  if (pxKey) {
    return formatAbMetricLabel(pxKey);
  }
  return field.replace(/^avg_/, "").replace(/_/g, " ");
}

function unwrapAbtestComparisonRecord(
  payload: unknown,
): Record<string, unknown> | undefined {
  if (!isRecord(payload)) {
    return undefined;
  }
  if (isRecord(payload.origin) && isRecord(payload.candidate)) {
    return payload;
  }
  for (const key of ["data", "result", "content", "payload", "comparison"]) {
    const nested = getNestedRecordField(payload, [key]);
    if (nested && isRecord(nested.origin) && isRecord(nested.candidate)) {
      return nested;
    }
  }
  const items = getStructuredArrayField(payload, ["items", "records", "results"]);
  const first = items?.find(
    (item) => isRecord(item) && isRecord(item.origin) && isRecord(item.candidate),
  );
  return first && isRecord(first) ? first : undefined;
}

function readMetricValue(
  body: Record<string, unknown> | undefined,
  field: string,
): number {
  return getNumberField(body, [field]) ?? 0;
}

function buildMetricRows(
  origin: Record<string, unknown>,
  candidate: Record<string, unknown>,
  delta: Record<string, unknown> | undefined,
): AbtestComparisonMetricRow[] {
  return COMPARISON_METRIC_FIELDS.map((field) => {
    const originValue = readMetricValue(origin, field);
    const candidateValue = readMetricValue(candidate, field);
    const deltaValue =
      getNumberField(delta, [field]) ?? candidateValue - originValue;
    return {
      key: field,
      label: COMPARISON_METRIC_LABELS[field],
      origin: originValue,
      candidate: candidateValue,
      delta: deltaValue,
    };
  });
}

function buildCaseRows(
  origin: Record<string, unknown>,
  candidate: Record<string, unknown>,
): AbtestComparisonCaseRow[] {
  const originCases = (getStructuredArrayField(origin, ["cases"]) || []).filter(isRecord);
  const candidateCases = (getStructuredArrayField(candidate, ["cases"]) || []).filter(isRecord);
  const originMap = new Map(
    originCases.map((item) => [
      getStringField(item, ["case_id", "caseId", "id"]) || "",
      item,
    ]),
  );
  const candidateMap = new Map(
    candidateCases.map((item) => [
      getStringField(item, ["case_id", "caseId", "id"]) || "",
      item,
    ]),
  );
  const caseIds = Array.from(
    new Set([...originMap.keys(), ...candidateMap.keys()].filter(Boolean)),
  ).sort((left, right) => left.localeCompare(right, "zh-CN", { numeric: true }));

  return caseIds.map((caseId) => {
    const originCase = originMap.get(caseId);
    const candidateCase = candidateMap.get(caseId);
    const originOverall = readMetricValue(originCase, "overall");
    const candidateOverall = readMetricValue(candidateCase, "overall");
    return {
      key: caseId,
      caseId,
      originOverall,
      candidateOverall,
      deltaOverall: candidateOverall - originOverall,
      originCorrectness: readMetricValue(originCase, "correctness"),
      candidateCorrectness: readMetricValue(candidateCase, "correctness"),
      originTraceId: getStringField(originCase, ["trace_id", "traceId"]) || undefined,
      candidateTraceId: getStringField(candidateCase, ["trace_id", "traceId"]) || undefined,
      reason:
        getStringField(candidateCase, ["reason"]) ||
        getStringField(originCase, ["reason"]) ||
        undefined,
    };
  });
}

export function parseAbtestComparisonArtifact(
  payload: unknown,
): AbtestComparisonArtifact | undefined {
  const record = unwrapAbtestComparisonRecord(payload);
  if (!record) {
    return undefined;
  }

  const origin = getNestedRecordField(record, ["origin"]);
  const candidate = getNestedRecordField(record, ["candidate"]);
  if (!origin || !candidate) {
    return undefined;
  }

  const delta = getNestedRecordField(record, ["delta"]);
  const reasons = (getStructuredArrayField(record, ["reasons"]) || []).filter(
    (item): item is string => typeof item === "string" && item.trim().length > 0,
  );

  return {
    runId: getStringField(record, ["run_id", "runId"]) || "-",
    algoId: getStringField(record, ["algo_id", "algoId"]) || "-",
    candidateAlgoId:
      getStringField(record, ["candidate_algo_id", "candidateAlgoId"]) || "-",
    status: getStringField(record, ["status"]) || "-",
    verdict: getStringField(record, ["verdict"]) || "inconclusive",
    reasons,
    metricRows: buildMetricRows(origin, candidate, delta),
    caseRows: buildCaseRows(origin, candidate),
  };
}

export function getAbtestVerdictTagColor(verdict?: string) {
  const normalized = (verdict || "").toLowerCase();
  if (["pass", "accept", "improved"].some((item) => normalized.includes(item))) {
    return "success";
  }
  if (["fail", "reject", "regressed", "failed"].some((item) => normalized.includes(item))) {
    return "error";
  }
  if (normalized.includes("skipped")) {
    return "default";
  }
  return "warning";
}

export function buildAbSummaryFromComparisonArtifact(
  artifact: AbtestComparisonArtifact,
): AbSummaryReport {
  return {
    id: artifact.runId,
    verdict: artifact.verdict,
    alignedCases: artifact.caseRows.length,
    reasons: artifact.reasons,
    metricRows: artifact.metricRows.map((row) => ({
      key: row.key,
      metric: row.key,
      metricLabel: getComparisonMetricDisplayLabel(row.key),
      meanA: row.origin,
      meanB: row.candidate,
      deltaMean: row.delta,
      winRateB: 0,
      signP: null,
      n: artifact.caseRows.length,
    })),
    topDiffRows: artifact.caseRows.map((row) => ({
      key: row.caseId,
      caseKey: row.caseId,
      a: row.originCorrectness,
      b: row.candidateCorrectness,
      delta: row.candidateCorrectness - row.originCorrectness,
    })),
    missingMetrics: [],
    guardMetrics: [],
  };
}

export function buildAbCaseTraceMapFromComparisonArtifact(
  artifact: AbtestComparisonArtifact | undefined,
): Map<string, { a?: string; b?: string }> {
  const map = new Map<string, { a?: string; b?: string }>();
  if (!artifact) {
    return map;
  }
  artifact.caseRows.forEach((row) => {
    if (!row.originTraceId && !row.candidateTraceId) {
      return;
    }
    map.set(row.caseId, {
      a: row.originTraceId,
      b: row.candidateTraceId,
    });
  });
  return map;
}

export function buildAbCaseDetailItemFromComparisonCase(
  row: AbtestComparisonCaseRow,
): Record<string, unknown> {
  return {
    case_id: row.caseId,
    baseline: { trace_id: row.originTraceId },
    candidate: { trace_id: row.candidateTraceId },
    before: { trace_id: row.originTraceId },
    after: { trace_id: row.candidateTraceId },
  };
}
