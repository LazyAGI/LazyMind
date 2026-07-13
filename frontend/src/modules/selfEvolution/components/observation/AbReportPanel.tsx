import { useMemo } from "react";
import { Alert, Button, Table, Tag, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useTranslation } from "react-i18next";
import type { AbSummaryReport, AbtestComparisonArtifact } from "../../shared";
import { getAbtestVerdictTagColor, type AbtestComparisonMetricRow } from "../../shared/abtestComparison";
import { formatMetricDelta, formatPercent } from "../../shared/format";
import type { AbCaseRow, AbMetricRow } from "./types";
import { formatDeltaScore } from "./traceUtils";
import { toAbMetricRows } from "./dataUtils";

const { Text, Title } = Typography;

function MetricBar({
  value,
  variant,
}: {
  value: number;
  variant: "origin" | "candidate";
}) {
  const width = `${Math.max(4, Math.min(100, value * 100))}%`;
  return (
    <span className={`self-evolution-abtest-comparison-bar is-${variant}`}>
      <span
        className="self-evolution-abtest-comparison-bar-fill"
        style={{ width }}
      />
    </span>
  );
}

function buildMetricTableRows(
  comparisonArtifact: AbtestComparisonArtifact | undefined,
  summaryMetrics: AbMetricRow[],
): AbtestComparisonMetricRow[] {
  if (comparisonArtifact) {
    return comparisonArtifact.metricRows;
  }
  return summaryMetrics.map((row) => ({
    key: row.key,
    label: row.key,
    origin: row.meanA,
    candidate: row.meanB,
    delta: row.meanB - row.meanA,
  }));
}

function pickHighlightMetric(rows: AbtestComparisonMetricRow[]) {
  return (
    rows.find((row) => row.key === "avg_overall" || row.label === "overall") ||
    rows.find((row) => row.key === "avg_correctness" || row.label === "correctness") ||
    rows[0]
  );
}

export function AbReportPanel({
  summary,
  comparisonArtifact,
  rows,
  rowsError,
  rowsLoading,
  totalSize,
  selectedCaseId,
  onSelectCase,
  onReloadRows,
}: {
  summary?: AbSummaryReport;
  comparisonArtifact?: AbtestComparisonArtifact;
  rows: AbCaseRow[];
  rowsError?: string;
  rowsLoading?: boolean;
  totalSize?: number;
  selectedCaseId: string;
  onSelectCase: (caseId: string) => void;
  onReloadRows: () => void;
}) {
  const { t } = useTranslation();
  const summaryMetrics = useMemo(() => toAbMetricRows(summary), [summary]);
  const metricRows = useMemo(
    () => buildMetricTableRows(comparisonArtifact, summaryMetrics),
    [comparisonArtifact, summaryMetrics],
  );
  const highlightMetric = useMemo(() => pickHighlightMetric(metricRows), [metricRows]);
  const metricLabel = (key: string) =>
    t(`selfEvolutionRun.abtestComparisonMetric.${key}`, { defaultValue: key });
  const verdict = comparisonArtifact?.verdict || summary?.verdict || "inconclusive";
  const verdictLabel = t(`selfEvolutionRun.abtestVerdict.${verdict}`, {
    defaultValue: verdict,
  });
  const status = comparisonArtifact?.status;
  const statusLabel = status
    ? t(`selfEvolutionRun.abtestStatus.${status}`, { defaultValue: status })
    : undefined;
  const caseCount = totalSize ?? rows.length;
  const runId = comparisonArtifact?.runId || summary?.id || "-";
  const algoId = comparisonArtifact?.algoId;
  const candidateAlgoId = comparisonArtifact?.candidateAlgoId;
  const reasons = comparisonArtifact?.reasons.length
    ? comparisonArtifact.reasons
    : summary?.reasons || [];

  const metricColumns = useMemo<ColumnsType<AbtestComparisonMetricRow>>(
    () => [
      {
        title: t("selfEvolutionRun.abtestComparisonColMetric"),
        dataIndex: "label",
        key: "label",
        width: 120,
        render: (value: string) => metricLabel(value),
      },
      {
        title: t("selfEvolutionRun.abtestComparisonColOrigin"),
        dataIndex: "origin",
        key: "origin",
        width: 132,
        render: (value: number) => (
          <div className="self-evolution-abtest-comparison-metric-cell">
            <Text>{formatPercent(value)}</Text>
            <MetricBar value={value} variant="origin" />
          </div>
        ),
      },
      {
        title: t("selfEvolutionRun.abtestComparisonColCandidate"),
        dataIndex: "candidate",
        key: "candidate",
        width: 132,
        render: (value: number) => (
          <div className="self-evolution-abtest-comparison-metric-cell">
            <Text>{formatPercent(value)}</Text>
            <MetricBar value={value} variant="candidate" />
          </div>
        ),
      },
      {
        title: t("selfEvolutionRun.abtestComparisonColDelta"),
        dataIndex: "delta",
        key: "delta",
        width: 88,
        render: (value: number) => (
          <span className={value >= 0 ? "is-up" : "is-down"}>
            {formatMetricDelta(value)}
          </span>
        ),
      },
    ],
    [t],
  );

  const caseColumns = useMemo<ColumnsType<AbCaseRow>>(
    () => [
      {
        title: "Case",
        dataIndex: "caseId",
        key: "caseId",
        width: 96,
        render: (value: string) => (
          <Text className="self-evolution-abtest-case-link">{value}</Text>
        ),
      },
      {
        title: t("selfEvolutionRun.abtestComparisonColOriginOverall"),
        dataIndex: "aScore",
        key: "aScore",
        width: 92,
        render: (value: number) => formatPercent(value),
      },
      {
        title: t("selfEvolutionRun.abtestComparisonColCandidateOverall"),
        dataIndex: "bScore",
        key: "bScore",
        width: 92,
        render: (value: number) => formatPercent(value),
      },
      {
        title: t("selfEvolutionRun.observation.abChange"),
        dataIndex: "delta",
        key: "delta",
        width: 80,
        render: (value: number, row) => (
          <span className={`self-evolution-abtest-delta is-${row.tone}`}>
            {formatDeltaScore(value)}
          </span>
        ),
      },
      {
        title: t("selfEvolutionRun.observation.abConclusion"),
        dataIndex: "conclusion",
        key: "conclusion",
        width: 88,
      },
      {
        title: t("selfEvolutionRun.observation.abAction"),
        key: "action",
        width: 108,
        fixed: "right",
        render: (_, row) => (
          <Button
            size="small"
            type={row.caseId === selectedCaseId ? "primary" : "default"}
            onClick={(event) => {
              event.stopPropagation();
              onSelectCase(row.caseId);
            }}
          >
            {t("selfEvolutionRun.observation.viewAbTrace")}
          </Button>
        ),
      },
    ],
    [onSelectCase, selectedCaseId, t],
  );

  return (
    <section
      className="self-evolution-abtest-report-card self-evolution-abtest-comparison"
      aria-label={t("selfEvolutionRun.observation.selfEvolutionOrchestrationAria")}
    >
      <div className="self-evolution-abtest-comparison-head">
        <div>
          <Title level={5}>{t("selfEvolutionRun.abtestComparisonTitle")}</Title>
          <Text className="self-evolution-abtest-comparison-subtitle">
            {t("selfEvolutionRun.abtestComparisonSubtitle", { count: caseCount })}
          </Text>
        </div>
        <div className="self-evolution-abtest-comparison-badges">
          <Tag color={getAbtestVerdictTagColor(verdict)}>{verdictLabel}</Tag>
          {statusLabel ? <Tag>{statusLabel}</Tag> : null}
        </div>
      </div>

      <div className="self-evolution-abtest-comparison-meta">
        <div>
          <span>{t("selfEvolutionRun.abtestComparisonRunId")}</span>
          <strong>{runId}</strong>
        </div>
        <div>
          <span>{t("selfEvolutionRun.abtestComparisonAlgoId")}</span>
          <strong>{algoId || "-"}</strong>
        </div>
        <div>
          <span>{t("selfEvolutionRun.abtestComparisonCandidateAlgoId")}</span>
          <strong>{candidateAlgoId || "-"}</strong>
        </div>
      </div>

      {highlightMetric ? (
        <div className="self-evolution-abtest-report-highlight" aria-label={t("selfEvolutionRun.observation.abMetricTableAria")}>
          <div className="self-evolution-abtest-report-highlight-card is-origin">
            <span>{t("selfEvolutionRun.abtestComparisonColOrigin")}</span>
            <strong>{formatPercent(highlightMetric.origin)}</strong>
            <em>{metricLabel(highlightMetric.label)}</em>
          </div>
          <div className="self-evolution-abtest-report-highlight-card is-candidate">
            <span>{t("selfEvolutionRun.abtestComparisonColCandidate")}</span>
            <strong>{formatPercent(highlightMetric.candidate)}</strong>
            <em>{metricLabel(highlightMetric.label)}</em>
          </div>
          <div className={`self-evolution-abtest-report-highlight-card is-delta ${highlightMetric.delta >= 0 ? "is-up" : "is-down"}`}>
            <span>{t("selfEvolutionRun.abtestComparisonColDelta")}</span>
            <strong>{formatMetricDelta(highlightMetric.delta)}</strong>
            <em>{metricLabel(highlightMetric.label)}</em>
          </div>
        </div>
      ) : null}

      {reasons.length > 0 ? (
        <Alert
          type="info"
          showIcon
          className="self-evolution-abtest-comparison-reasons"
          message={t("selfEvolutionRun.abtestComparisonReasonsTitle")}
          description={
            <ul>
              {reasons.map((reason) => (
                <li key={reason}>{reason}</li>
              ))}
            </ul>
          }
        />
      ) : null}

      <div className="self-evolution-abtest-comparison-section">
        <Text className="self-evolution-abtest-comparison-section-title">
          {t("selfEvolutionRun.abtestComparisonMetricsTitle")}
        </Text>
        <Table<AbtestComparisonMetricRow>
          className="self-evolution-dataset-table self-evolution-abtest-comparison-table"
          size="small"
          rowKey="key"
          columns={metricColumns}
          dataSource={metricRows}
          pagination={false}
          scroll={{ x: 480 }}
        />
      </div>

      <div className="self-evolution-abtest-comparison-section self-evolution-abtest-case-panel">
        <Text className="self-evolution-abtest-comparison-section-title">
          {t("selfEvolutionRun.abtestComparisonCasesTitle")}
        </Text>
        {rowsError ? (
          <Alert
            type="error"
            showIcon
            message={rowsError}
            action={
              <Button size="small" onClick={onReloadRows}>
                {t("selfEvolutionRun.observation.retry")}
              </Button>
            }
          />
        ) : (
          <Table<AbCaseRow>
            className="self-evolution-abtest-case-table"
            size="small"
            rowKey="caseId"
            columns={caseColumns}
            dataSource={rows}
            loading={rowsLoading}
            pagination={{
              pageSize: 10,
              size: "small",
              showSizeChanger: false,
              total: caseCount,
            }}
            rowClassName={(row) => (row.caseId === selectedCaseId ? "is-selected" : "")}
            scroll={{ x: 640 }}
            onRow={(row) => ({
              onClick: () => onSelectCase(row.caseId),
            })}
          />
        )}
      </div>
    </section>
  );
}
