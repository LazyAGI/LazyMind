import type { ReactNode } from "react";
import { Alert, Button, Table, Tag, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  AimOutlined,
  FileSearchOutlined,
  SearchOutlined,
  ThunderboltOutlined,
  WarningOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import type { CsvBadcaseRow, EvalReportSummary } from "./types";
import { formatOptionalPercent } from "./traceUtils";

const { Text, Title } = Typography;

function MetricCard({
  icon,
  label,
  value,
  tone,
}: {
  icon: ReactNode;
  label: string;
  value: string;
  tone: "blue" | "red" | "green" | "purple";
}) {
  return (
    <article className={`self-evolution-eval-metric is-${tone}`}>
      <span>{icon}</span>
      <div>
        <em>{label}</em>
        <strong>{value}</strong>
      </div>
    </article>
  );
}

export function EvalReportPanel({
  summary,
  rows,
  rowsError,
  rowsLoading,
  selectedCaseId,
  onSelectCase,
  onReloadRows,
}: {
  summary: EvalReportSummary;
  rows: CsvBadcaseRow[];
  rowsError?: string;
  rowsLoading?: boolean;
  selectedCaseId: string;
  onSelectCase: (caseId: string) => void;
  onReloadRows: () => void;
}) {
  const { t } = useTranslation();
  const selectedRow = rows.find((item) => item.caseId === selectedCaseId) || rows[0];
  const columns: ColumnsType<CsvBadcaseRow> = [
    { title: "Case", dataIndex: "caseId", key: "caseId", width: 104 },
    {
      title: "Score",
      dataIndex: "score",
      key: "score",
      width: 78,
      render: (value: number) => <span className={value < 0.5 ? "is-low-score" : ""}>{value.toFixed(2)}</span>,
    },
    {
      title: t("selfEvolutionRun.observation.failureReason"),
      dataIndex: "failureType",
      key: "failureType",
      width: 110,
      render: (value: string, row) => <Tag className={`self-evolution-eval-reason is-${row.failureTone}`}>{value}</Tag>,
    },
    {
      title: "Defect",
      dataIndex: "defect",
      key: "defect",
      width: 230,
      render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span>,
    },
    {
      title: "Reason",
      dataIndex: "reason",
      key: "reason",
      width: 360,
      render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span>,
    },
    {
      title: "Trace",
      dataIndex: "traceId",
      key: "traceId",
      width: 170,
      render: (value: string) => <span className="self-evolution-table-ellipsis" title={value}>{value}</span>,
    },
  ];

  return (
    <section className="self-evolution-eval-report-card" aria-label={t("selfEvolutionRun.observation.evalReportAria")}>
      <div className="self-evolution-eval-report-head">
        <div>
          <Title level={3}>{t("selfEvolutionRun.observation.evalReportTitle")}</Title>
          <Text>{t("selfEvolutionRun.observation.reportIdLabel", { id: summary.reportId })}</Text>
          <Text>{t("selfEvolutionRun.observation.datasetInfo", { dataset: summary.dataset, badcaseCount: summary.badCaseCount ?? "-" })}</Text>
        </div>
      </div>
      <div className="self-evolution-eval-metric-grid">
        <MetricCard icon={<AimOutlined />} label={t("selfEvolutionRun.observation.accuracy")} value={formatOptionalPercent(summary.correctRate)} tone="blue" />
        <MetricCard icon={<WarningOutlined />} label="Badcase" value={String(summary.badCaseCount ?? "-")} tone="red" />
        <MetricCard icon={<ThunderboltOutlined />} label={t("selfEvolutionRun.observation.traceCoverage")} value={formatOptionalPercent(summary.traceCoverageRate)} tone="purple" />
      </div>
      <div className="self-evolution-eval-badcase-panel">
        <div className="self-evolution-eval-section-title">
          <Text strong>{t("selfEvolutionRun.observation.badcaseList")}</Text>
          <span>{t("selfEvolutionRun.observation.badcaseSource", { reportId: summary.reportId })}</span>
        </div>
        <div className="self-evolution-eval-filter-row">
          <label>
            {t("selfEvolutionRun.observation.statusLabel")}
            <select aria-label={t("selfEvolutionRun.observation.badcaseStatusFilterAria")}>
              <option>{t("selfEvolutionRun.observation.all")}</option>
            </select>
          </label>
          <label>
            {t("selfEvolutionRun.observation.failureTypeLabel")}
            <select aria-label={t("selfEvolutionRun.observation.badcaseFailureTypeFilterAria")}>
              <option>{t("selfEvolutionRun.observation.all")}</option>
            </select>
          </label>
          <label className="self-evolution-eval-search">
            <SearchOutlined />
            <input
              aria-label={t("selfEvolutionRun.observation.searchCaseAria")}
              placeholder={t("selfEvolutionRun.observation.searchCasePlaceholder")}
            />
          </label>
          <Button size="small">{t("selfEvolutionRun.observation.reset")}</Button>
        </div>
        {rowsError ? (
          <Alert
            type="error"
            showIcon
            message={rowsError}
            action={<Button size="small" onClick={onReloadRows}>{t("selfEvolutionRun.observation.retry")}</Button>}
          />
        ) : (
          <Table<CsvBadcaseRow>
            className="self-evolution-eval-badcase-table"
            size="small"
            rowKey="caseId"
            columns={columns}
            dataSource={rows}
            loading={rowsLoading}
            pagination={false}
            rowClassName={(row) => row.caseId === selectedCaseId ? "is-selected" : ""}
            scroll={{ x: 1052 }}
            onRow={(row) => ({
              onClick: () => onSelectCase(row.caseId),
            })}
          />
        )}
      </div>
      {selectedRow && (
        <div className="self-evolution-eval-case-result">
          <div className="self-evolution-eval-section-title">
            <Text strong>{t("selfEvolutionRun.observation.caseResult", { caseId: selectedRow.caseId })}</Text>
          </div>
          <dl>
            <dt>Score</dt>
            <dd>
              <span className="is-low-score">{selectedRow.score.toFixed(2)}</span>
              <Tag className={`self-evolution-eval-reason is-${selectedRow.failureTone}`}>{selectedRow.failureType}</Tag>
            </dd>
            <dt>{t("selfEvolutionRun.observation.failureReason")}</dt>
            <dd>{selectedRow.failureReason}</dd>
            <dt>Defect</dt>
            <dd>{selectedRow.defect}</dd>
            <dt>Reason</dt>
            <dd>{selectedRow.reason}</dd>
          </dl>
          <div className="self-evolution-eval-case-actions">
            <Button type="primary">{t("selfEvolutionRun.observation.viewAgenticTrace")}</Button>
            <Button icon={<FileSearchOutlined />}>{t("selfEvolutionRun.observation.viewRawTrace")}</Button>
          </div>
        </div>
      )}
    </section>
  );
}
