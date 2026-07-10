import { useMemo, useState } from "react";
import { Alert, Table, Tag, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useTranslation } from "react-i18next";
import {
  type AbtestComparisonArtifact,
  type AbtestComparisonCaseRow,
  type AbtestComparisonMetricRow,
  getAbtestVerdictTagColor,
} from "../../shared/abtestComparison";
import { formatMetricDelta, formatPercent } from "../../shared/format";

const { Text, Title } = Typography;
const CASE_PAGE_SIZE = 10;

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
      <span className="self-evolution-abtest-comparison-bar-fill" style={{ width }} />
    </span>
  );
}

export function AbtestComparisonPanel({
  artifact,
}: {
  artifact: AbtestComparisonArtifact;
}) {
  const { t } = useTranslation();
  const [casePage, setCasePage] = useState(1);
  const metricLabel = (key: string) =>
    t(`selfEvolutionRun.abtestComparisonMetric.${key}`, { defaultValue: key });
  const verdictLabel = t(`selfEvolutionRun.abtestVerdict.${artifact.verdict}`, {
    defaultValue: artifact.verdict,
  });
  const statusLabel = t(`selfEvolutionRun.abtestStatus.${artifact.status}`, {
    defaultValue: artifact.status,
  });

  const metricColumns = useMemo<ColumnsType<AbtestComparisonMetricRow>>(
    () => [
      {
        title: t("selfEvolutionRun.abtestComparisonColMetric"),
        dataIndex: "label",
        key: "label",
        width: 132,
        render: (value: string) => metricLabel(value),
      },
      {
        title: t("selfEvolutionRun.abtestComparisonColOrigin"),
        dataIndex: "origin",
        key: "origin",
        width: 148,
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
        width: 148,
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
        width: 96,
        render: (value: number) => (
          <span className={value >= 0 ? "is-up" : "is-down"}>
            {formatMetricDelta(value)}
          </span>
        ),
      },
    ],
    [t],
  );

  const caseColumns = useMemo<ColumnsType<AbtestComparisonCaseRow>>(
    () => [
      {
        title: "case",
        dataIndex: "caseId",
        key: "caseId",
        width: 120,
      },
      {
        title: t("selfEvolutionRun.abtestComparisonColOriginOverall"),
        dataIndex: "originOverall",
        key: "originOverall",
        width: 108,
        render: (value: number) => formatPercent(value),
      },
      {
        title: t("selfEvolutionRun.abtestComparisonColCandidateOverall"),
        dataIndex: "candidateOverall",
        key: "candidateOverall",
        width: 108,
        render: (value: number) => formatPercent(value),
      },
      {
        title: t("selfEvolutionRun.abtestComparisonColDelta"),
        dataIndex: "deltaOverall",
        key: "deltaOverall",
        width: 88,
        render: (value: number) => (
          <span className={value >= 0 ? "is-up" : "is-down"}>
            {formatMetricDelta(value)}
          </span>
        ),
      },
      {
        title: t("selfEvolutionRun.abtestComparisonColReason"),
        dataIndex: "reason",
        key: "reason",
        ellipsis: true,
        render: (value?: string) => value || "-",
      },
    ],
    [t],
  );

  const totalCasePages = Math.max(1, Math.ceil(artifact.caseRows.length / CASE_PAGE_SIZE));
  const pageCases = artifact.caseRows.slice(
    (casePage - 1) * CASE_PAGE_SIZE,
    casePage * CASE_PAGE_SIZE,
  );

  return (
    <section
      className="self-evolution-abtest-comparison"
      aria-label={t("selfEvolutionRun.abtestComparisonAria")}
    >
      <div className="self-evolution-abtest-comparison-head">
        <div>
          <Title level={5}>{t("selfEvolutionRun.abtestComparisonTitle")}</Title>
          <Text className="self-evolution-abtest-comparison-subtitle">
            {t("selfEvolutionRun.abtestComparisonSubtitle", {
              count: artifact.caseRows.length,
            })}
          </Text>
        </div>
        <div className="self-evolution-abtest-comparison-badges">
          <Tag color={getAbtestVerdictTagColor(artifact.verdict)}>{verdictLabel}</Tag>
          <Tag>{statusLabel}</Tag>
        </div>
      </div>

      <div className="self-evolution-abtest-comparison-meta">
        <div>
          <span>{t("selfEvolutionRun.abtestComparisonRunId")}</span>
          <strong>{artifact.runId}</strong>
        </div>
        <div>
          <span>{t("selfEvolutionRun.abtestComparisonAlgoId")}</span>
          <strong>{artifact.algoId}</strong>
        </div>
        <div>
          <span>{t("selfEvolutionRun.abtestComparisonCandidateAlgoId")}</span>
          <strong>{artifact.candidateAlgoId}</strong>
        </div>
      </div>

      {artifact.reasons.length > 0 ? (
        <Alert
          type="info"
          showIcon
          className="self-evolution-abtest-comparison-reasons"
          message={t("selfEvolutionRun.abtestComparisonReasonsTitle")}
          description={
            <ul>
              {artifact.reasons.map((reason) => (
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
          dataSource={artifact.metricRows}
          pagination={false}
          scroll={{ x: 520 }}
        />
      </div>

      <div className="self-evolution-abtest-comparison-section">
        <Text className="self-evolution-abtest-comparison-section-title">
          {t("selfEvolutionRun.abtestComparisonCasesTitle")}
        </Text>
        <Table<AbtestComparisonCaseRow>
          className="self-evolution-dataset-table self-evolution-abtest-comparison-table"
          size="small"
          rowKey="key"
          columns={caseColumns}
          dataSource={pageCases}
          pagination={false}
          locale={{ emptyText: t("selfEvolutionRun.abtestComparisonCasesEmpty") }}
          scroll={{ x: 640 }}
        />
        {artifact.caseRows.length > CASE_PAGE_SIZE ? (
          <div className="self-evolution-dataset-pagination">
            <button
              type="button"
              className="self-evolution-dataset-page-btn"
              disabled={casePage <= 1}
              onClick={() => setCasePage((page) => Math.max(1, page - 1))}
            >
              {t("selfEvolutionRun.abtestStreamingPrevPage")}
            </button>
            <Text className="self-evolution-dataset-page-indicator">
              {t("selfEvolutionRun.abtestStreamingPageIndicator", {
                current: casePage,
                total: totalCasePages,
              })}
            </Text>
            <button
              type="button"
              className="self-evolution-dataset-page-btn"
              disabled={casePage >= totalCasePages}
              onClick={() => setCasePage((page) => Math.min(totalCasePages, page + 1))}
            >
              {t("selfEvolutionRun.abtestStreamingNextPage")}
            </button>
          </div>
        ) : null}
      </div>
    </section>
  );
}
