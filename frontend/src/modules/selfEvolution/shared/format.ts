import { type EvalQuestionTypeSummary, type PxCategoryMetricAverage, type PxMetricKey } from "./types";
import { pxMetricFieldAliases } from "./constants";
import { getAnalysisCategoryLabels, getAnalysisVerdictLabels, getPxMetricMeta, getQuestionTypeLabelMap, t } from "./i18n";
import { getNestedRecordField, getNumberField, getStructuredArrayField, getStructuredRecordField, isRecord } from "./fields";

export function getMetricFieldNumber(payload: Record<string, unknown> | undefined, key: PxMetricKey, fallback = 0) {
  return clampScore(getNumberField(payload, pxMetricFieldAliases[key]) ?? fallback);
}

export const formatQuestionType = (questionType: number) => {
  const label = getQuestionTypeLabelMap()[questionType];
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
  return t("selfEvolutionRun.categoryN", { n: index + 1 });
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
      category: t("selfEvolutionRun.categoryOverall"),
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

  return t("selfEvolutionRun.justNow");
}

export function clampPercent(value: number) {
  if (!Number.isFinite(value)) {
    return 0;
  }
  return Math.min(100, Math.max(0, Math.round(value)));
}

export function formatAnalysisVerdict(verdict: string | undefined) {
  if (!verdict) {
    return t("selfEvolutionRun.verdictInvestigating");
  }
  return getAnalysisVerdictLabels()[verdict] || verdict;
}

export function formatAnalysisCategory(category: string | undefined) {
  if (!category) {
    return t("selfEvolutionRun.categoryUncategorized");
  }
  return getAnalysisCategoryLabels()[category] || category;
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
    return t("selfEvolutionRun.researchSubagent");
  }
  if (agent.startsWith("researcher:")) {
    return t("selfEvolutionRun.researcherN", { n: agent.slice("researcher:".length) });
  }
  return agent;
}

export function getShortLabel(text: string, maxLength = 6) {
  if (text.length <= maxLength) {
    return text;
  }
  return `${text.slice(0, maxLength)}…`;
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
    t("selfEvolutionRun.metricCorrectnessSummary", { value: formatMetricPercent(metrics.answer_correctness) }),
    t("selfEvolutionRun.metricAnswerScoreSummary", { value: formatMetricPercent(metrics.answer_score) }),
    t("selfEvolutionRun.metricChunkRecallSummary", { value: formatMetricPercent(metrics.chunk_recall) }),
    t("selfEvolutionRun.metricDocRecallSummary", { value: formatMetricPercent(metrics.doc_recall) }),
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
  return getPxMetricMeta().find((item) => item.key === metric)?.label || metric;
}

export function formatMaybePValue(value: number | null | undefined) {
  if (value === null || value === undefined || !Number.isFinite(value)) {
    return "-";
  }
  return value < 0.001 ? "<0.001" : value.toFixed(3);
}
