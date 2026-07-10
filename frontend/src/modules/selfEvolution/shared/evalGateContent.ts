import { type PxCategoryMetricAverage } from "./types";
import { t } from "./i18n";
import {
  getNestedRecordField,
  getNumberField,
  getStructuredArrayField,
  getStructuredRecordField,
  isRecord,
} from "./fields";

function clampGateMetric(value: number) {
  if (!Number.isFinite(value)) {
    return 0;
  }
  return Math.min(1, Math.max(0, value));
}

export function isGateEvalContent(record: Record<string, unknown>): boolean {
  return (
    typeof getNumberField(record, ["avg_correctness", "correct_rate"]) === "number" ||
    Array.isArray(record.cases)
  );
}

export function unwrapGateEvalContent(payload: unknown): Record<string, unknown> | undefined {
  if (!isRecord(payload)) {
    return undefined;
  }
  if (isGateEvalContent(payload)) {
    return payload;
  }
  for (const key of ["content", "data", "result", "payload"]) {
    const nested =
      getNestedRecordField(payload, [key]) ||
      getStructuredRecordField(payload, [key]);
    if (nested && isGateEvalContent(nested)) {
      return nested;
    }
  }
  return undefined;
}

export function getGateEvalCaseRecords(payload: unknown): Record<string, unknown>[] {
  const record = unwrapGateEvalContent(payload);
  if (!record) {
    return [];
  }
  return (getStructuredArrayField(record, ["cases"]) || []).filter(isRecord);
}

export function getGateEvalCaseCount(payload: unknown): number {
  const record = unwrapGateEvalContent(payload);
  if (!record) {
    return 0;
  }
  return (
    getNumberField(record, ["case_num", "case_count", "total_cases"]) ||
    getGateEvalCaseRecords(payload).length
  );
}

export function buildPxCategoryMetricAveragesFromGateEval(
  payload: unknown,
): PxCategoryMetricAverage[] {
  const record = unwrapGateEvalContent(payload);
  if (!record) {
    return [];
  }

  const caseCount = getGateEvalCaseCount(record);
  const hasAggregateMetrics =
    typeof getNumberField(record, ["avg_correctness", "correct_rate"]) === "number" ||
    typeof getNumberField(record, ["avg_overall", "avg_answer_quality"]) === "number" ||
    typeof getNumberField(record, ["avg_retrieval_quality"]) === "number" ||
    typeof getNumberField(record, ["avg_groundedness", "avg_relevance"]) === "number";

  if (!hasAggregateMetrics && caseCount === 0) {
    return [];
  }

  return [
    {
      category: t("selfEvolutionRun.categoryOverall"),
      caseCount,
      metrics: {
        answer_correctness: clampGateMetric(
          getNumberField(record, ["avg_correctness", "correct_rate"]) ?? 0,
        ),
        answer_score: clampGateMetric(
          getNumberField(record, ["avg_overall", "avg_answer_quality"]) ?? 0,
        ),
        chunk_recall: clampGateMetric(getNumberField(record, ["avg_retrieval_quality"]) ?? 0),
        doc_recall: clampGateMetric(
          getNumberField(record, ["avg_groundedness", "avg_relevance"]) ?? 0,
        ),
      },
    },
  ];
}

export function hasEmbeddedGateEvalCases(payload: unknown): boolean {
  return getGateEvalCaseRecords(payload).length > 0;
}
