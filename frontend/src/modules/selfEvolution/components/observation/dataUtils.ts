import {
  buildAbSummaryReports,
  formatMaybePValue,
  getAbtestResultRecords,
  getNestedRecordField,
  getNumberField,
  getResultItems,
  getStringField,
  getStructuredArrayField,
  getStructuredRecordField,
  isRecord,
  type AbSummaryReport,
} from "../../shared";
import type {
  AbCaseRow,
  AbMetricRow,
  CsvBadcaseRow,
  EvalReportSummary,
  ObservationResultKind,
  TFunction,
} from "./types";

const observationKindMap: Record<string, ObservationResultKind> = {
  eval: "eval-reports",
  "eval-reports": "eval-reports",
  abtest: "abtests",
  abtests: "abtests",
};

export const EVAL_BADCASE_PAGE_SIZE = 10;
export const AB_CASE_DETAIL_PAGE_SIZE = 10;
const syntheticAbtestIdPattern = /^abtest-\d+$/;

export function normalizeObservationKind(kind?: string): ObservationResultKind | undefined {
  return kind ? observationKindMap[kind] : undefined;
}

function getBadcaseSourceRecords(value: unknown): Record<string, unknown>[] {
  const rawSources = Array.isArray(value) ? value : [value];
  return rawSources.filter(isRecord).flatMap((item) => {
    const dataRecord = getStructuredRecordField(item, ["data"]);
    return dataRecord ? [dataRecord, item] : [item];
  });
}

export function normalizeBadcaseRows(t: TFunction, value: unknown): CsvBadcaseRow[] {
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

export function normalizeAbCaseRows(t: TFunction, value: unknown): AbCaseRow[] {
  return getAbCaseSourceRecords(value)
    .filter(isRecord)
    .map((item, index) => normalizeAbCaseRowFromRecord(t, item, index));
}

export function toAbMetricRows(summary: AbSummaryReport | undefined): AbMetricRow[] {
  if (!summary?.metricRows.length) {
    return [];
  }
  return summary.metricRows.map((row) => ({
    key: row.metric,
    label: row.metricLabel,
    meanA: row.meanA,
    meanB: row.meanB,
    winRate: row.winRateB ?? 0,
    signP: formatMaybePValue(row.signP),
  }));
}

export function resolveAbtestIdFromPayload(value: unknown): string | undefined {
  const summary = buildAbSummaryReports(value)[0];
  if (!summary?.id || syntheticAbtestIdPattern.test(summary.id)) {
    return undefined;
  }
  return summary.id;
}

export function getAbtestVerdictColor(verdict?: string) {
  const normalized = (verdict || "").toLowerCase();
  if (["pass", "accept", "improved"].some((item) => normalized.includes(item))) {
    return "success";
  }
  if (["fail", "reject", "regressed"].some((item) => normalized.includes(item))) {
    return "error";
  }
  return "orange";
}

export function findAbCaseDetailItem(data: unknown, caseId: string): Record<string, unknown> | undefined {
  if (!isRecord(data)) {
    return undefined;
  }
  const items = getStructuredArrayField(data, ["items"]);
  return items?.find((item) => isRecord(item) && getStringField(item, ["case_id", "caseId", "case", "id"]) === caseId);
}

export function buildAbCaseTraceIdMap(evalReportsData: unknown): Map<string, { a?: string; b?: string }> {
  const map = new Map<string, { a?: string; b?: string }>();
  getAbtestResultRecords(evalReportsData).forEach((record) => {
    const artifactId =
      getStringField(record, ["artifact_id", "runtime_artifact_id", "source_artifact_id"]) ||
      getStringField(getStructuredRecordField(record, ["data"]), ["id"]) ||
      "";
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

export function resolveCaseTraceIds(
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

function getPrimaryEvalReportRecord(value: unknown) {
  const items = getResultItems(value);
  const itemRecord = items.find((item): item is Record<string, unknown> => isRecord(item));
  if (itemRecord) {
    return itemRecord;
  }
  return isRecord(value) ? value : undefined;
}

export function normalizeEvalReportSummary(value: unknown): EvalReportSummary {
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
