import { type AbCategoryComparison, type AbSummaryReport, type PxMetricKey } from "./types";
import { pxMetricFieldAliases } from "./constants";
import { getPxMetricMeta, t } from "./i18n";
import { getNestedRecordField, getNumberField, getResultItems, getResultStringField, getStringField, getStructuredArrayField, getStructuredRecordField, isRecord } from "./fields";
import { clampScore, formatAbMetricLabel, getMetricFieldNumber, toFiniteNumber } from "./format";

export function buildAbCategoryComparisons(reports: AbSummaryReport[]): AbCategoryComparison[] {
  return reports
    .filter((report) => report.metricRows.length > 0)
    .map((report, index) => {
      const metricMap = new Map(report.metricRows.map((row) => [row.metric, row]));
      const baseline = {} as Record<PxMetricKey, number>;
      const experiment = {} as Record<PxMetricKey, number>;
      const delta = {} as Record<PxMetricKey, number>;

      getPxMetricMeta().forEach((metric) => {
        const row = metricMap.get(metric.key);
        baseline[metric.key] = clampScore(row?.meanA ?? 0);
        experiment[metric.key] = clampScore(row?.meanB ?? 0);
        delta[metric.key] = row?.deltaMean ?? experiment[metric.key] - baseline[metric.key];
      });

      return {
        category: reports.length === 1 ? t("selfEvolutionRun.categoryOverall") : report.id || t("selfEvolutionRun.reportN", { n: index + 1 }),
        baseline,
        experiment,
        delta,
      };
    });
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
        ? getPxMetricMeta().map((metric) => ({
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
