import {
  type EvalReportMetricKey,
  type EvalReportQuestionTypeSummary,
  type PxCategoryMetricAverage,
} from "./types";
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

const evalReportMetricKeys: EvalReportMetricKey[] = [
  "correctness",
  "relevance",
  "completeness",
  "groundedness",
  "format_compliance",
  "answer_quality",
  "retrieval_quality",
  "overall",
];

function getEvalReportMetrics(
  record: Record<string, unknown> | undefined,
): Record<EvalReportMetricKey, number> {
  return Object.fromEntries(
    evalReportMetricKeys.map((key) => [
      key,
      clampGateMetric(getNumberField(record, [key]) ?? 0),
    ]),
  ) as Record<EvalReportMetricKey, number>;
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

export function getGateEvalMetrics(
  payload: unknown,
): Record<EvalReportMetricKey, number> | undefined {
  const record = unwrapGateEvalContent(payload);
  const metrics = getNestedRecordField(record, ["metrics"]);
  return metrics ? getEvalReportMetrics(metrics) : undefined;
}

export function getGateEvalQuestionTypeSummaries(
  payload: unknown,
): EvalReportQuestionTypeSummary[] {
  const record = unwrapGateEvalContent(payload);
  return (getStructuredArrayField(record, ["question_type_summaries"]) || [])
    .filter(isRecord)
    .map((item) => ({
      questionType:
        (typeof item.question_type === "string" && item.question_type.trim()) ||
        t("selfEvolutionRun.uncategorized"),
      caseCount: getNumberField(item, ["case_num"]) || 0,
      scoredCaseCount: getNumberField(item, ["scored_case_num"]) || 0,
      metrics: getEvalReportMetrics(getNestedRecordField(item, ["metrics"])),
    }));
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
