import { useMemo } from "react";
import { Alert, Button, Table, Tag, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useTranslation } from "react-i18next";
import type { AbSummaryReport } from "../../shared";
import type { AbCaseRow, AbMetricRow } from "./types";
import { formatDeltaPercent, formatDeltaScore, formatPercent } from "./traceUtils";
import { getAbtestVerdictColor, toAbMetricRows } from "./dataUtils";

const { Text, Title } = Typography;

function AbMetricChart({ metrics }: { metrics: AbMetricRow[] }) {
  const { t } = useTranslation();
  return (
    <div className="self-evolution-abtest-chart" aria-label={t("selfEvolutionRun.observation.abChartAria")}>
      <div className="self-evolution-abtest-chart-axis">
        <span>1.00</span>
        <span>0.75</span>
        <span>0.50</span>
        <span>0.25</span>
        <span>0.00</span>
      </div>
      <div className="self-evolution-abtest-chart-groups">
        {metrics.map((metric) => {
          const delta = metric.meanB - metric.meanA;
          return (
            <div key={metric.key} className="self-evolution-abtest-chart-group">
              <strong className={delta >= 0 ? "is-up" : "is-down"}>{formatDeltaPercent(metric.meanA, metric.meanB)}</strong>
              <div className="self-evolution-abtest-bars">
                <span className="is-a" style={{ height: `${Math.max(6, metric.meanA * 100)}%` }} />
                <span className="is-b" style={{ height: `${Math.max(6, metric.meanB * 100)}%` }} />
              </div>
              <em>{metric.label}</em>
            </div>
          );
        })}
      </div>
      <div className="self-evolution-abtest-chart-legend">
        <span><i className="is-a" />{t("selfEvolutionRun.observation.legendA")}</span>
        <span><i className="is-b" />{t("selfEvolutionRun.observation.legendB")}</span>
      </div>
    </div>
  );
}

export function AbReportPanel({
  summary,
  rows,
  rowsError,
  rowsLoading,
  totalSize,
  selectedCaseId,
  onSelectCase,
  onReloadRows,
}: {
  summary?: AbSummaryReport;
  rows: AbCaseRow[];
  rowsError?: string;
  rowsLoading?: boolean;
  totalSize?: number;
  selectedCaseId: string;
  onSelectCase: (caseId: string) => void;
  onReloadRows: () => void;
}) {
  const { t } = useTranslation();
  const metrics = useMemo(() => toAbMetricRows(summary), [summary]);
  const abtestId = summary?.id || "-";
  const verdict = summary?.verdict || "inconclusive";
  const columns: ColumnsType<AbCaseRow> = [
    {
      title: "Case",
      dataIndex: "caseId",
      key: "caseId",
      width: 94,
      render: (value: string) => <Text className="self-evolution-abtest-case-link">{value}</Text>,
    },
    {
      title: "Query",
      dataIndex: "query",
      key: "query",
      width: 230,
      render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span>,
    },
    {
      title: "A Score",
      dataIndex: "aScore",
      key: "aScore",
      width: 80,
      render: (value: number) => value.toFixed(2),
    },
    {
      title: "B Score",
      dataIndex: "bScore",
      key: "bScore",
      width: 80,
      render: (value: number) => value.toFixed(2),
    },
    {
      title: t("selfEvolutionRun.observation.abChange"),
      dataIndex: "delta",
      key: "delta",
      width: 76,
      render: (value: number, row) => <span className={`self-evolution-abtest-delta is-${row.tone}`}>{formatDeltaScore(value)}</span>,
    },
    { title: t("selfEvolutionRun.observation.abConclusion"), dataIndex: "conclusion", key: "conclusion", width: 140 },
    {
      title: t("selfEvolutionRun.observation.abAction"),
      key: "action",
      width: 112,
      render: (_, row) => (
        <Button size="small" type={row.caseId === selectedCaseId ? "primary" : "default"} onClick={() => onSelectCase(row.caseId)}>
          {t("selfEvolutionRun.observation.viewAbTrace")}
        </Button>
      ),
    },
  ];

  return (
    <section className="self-evolution-abtest-report-card" aria-label={t("selfEvolutionRun.observation.selfEvolutionOrchestrationAria")}>
      <div className="self-evolution-abtest-report-head">
        <div>
          <Title level={3}>{t("selfEvolutionRun.observation.selfEvolutionOrchestrationTitle")}</Title>
          <Text>{t("selfEvolutionRun.observation.abStageTitle")}</Text>
          <Text>{t("selfEvolutionRun.observation.abMetricDesc")}</Text>
        </div>
        <Tag color={getAbtestVerdictColor(verdict)}>{verdict}</Tag>
      </div>
      <div className="self-evolution-abtest-report-id">
        <strong>{abtestId}</strong>
      </div>
      <AbMetricChart metrics={metrics} />
      <div className="self-evolution-abtest-metric-table" aria-label={t("selfEvolutionRun.observation.abMetricTableAria")}>
        <div className="self-evolution-abtest-table-row is-head">
          <span>{t("selfEvolutionRun.observation.abMetricColMetric")}</span>
          <span>mean A</span>
          <span>mean B</span>
          <span>Δmean</span>
          <span>{t("selfEvolutionRun.observation.abMetricColWinRate")}</span>
          <span>sign p</span>
        </div>
        {metrics.map((metric) => (
          <div key={metric.key} className="self-evolution-abtest-table-row">
            <span>{metric.label}</span>
            <span>{formatPercent(metric.meanA)}</span>
            <span>{formatPercent(metric.meanB)}</span>
            <strong className={metric.meanB >= metric.meanA ? "is-up" : "is-down"}>{formatDeltaPercent(metric.meanA, metric.meanB)}</strong>
            <span>{formatPercent(metric.winRate)}</span>
            <span>{metric.signP || "-"}</span>
          </div>
        ))}
      </div>
      <div className="self-evolution-abtest-case-panel">
        <div className="self-evolution-eval-section-title">
          <Text strong>{t("selfEvolutionRun.observation.changedCaseList")}</Text>
          <span>
            {t("selfEvolutionRun.observation.abCaseDetailSource", { abtestId, total: totalSize ?? rows.length })}
          </span>
        </div>
        {rowsError ? (
          <Alert
            type="error"
            showIcon
            message={rowsError}
            action={<Button size="small" onClick={onReloadRows}>{t("selfEvolutionRun.observation.retry")}</Button>}
          />
        ) : (
          <Table<AbCaseRow>
            className="self-evolution-abtest-case-table"
            size="small"
            rowKey="caseId"
            columns={columns}
            dataSource={rows}
            loading={rowsLoading}
            pagination={{ pageSize: 10, size: "small", showSizeChanger: false, total: totalSize ?? rows.length }}
            rowClassName={(row) => row.caseId === selectedCaseId ? "is-selected" : ""}
            scroll={{ x: 820 }}
            onRow={(row) => ({
              onClick: () => onSelectCase(row.caseId),
            })}
          />
        )}
      </div>
    </section>
  );
}
