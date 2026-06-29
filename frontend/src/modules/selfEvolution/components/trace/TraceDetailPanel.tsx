import { useTranslation } from "react-i18next";
import { Tag, Typography } from "antd";
import type { TraceDetailObservation } from "./types";
import {
  flattenTraceNodes,
  getMetricItems,
  getShortTraceId,
  getStatusColor,
  getTraceTypeLabels,
  getTypeStats,
} from "./utils";
import { renderFlowPanel, renderInspectorPanel, renderMetaTiles, renderSummaryStrip } from "./renderHelpers";

const { Paragraph, Text } = Typography;

export function TraceDetailPanel({ detail, label, compact = false }: { detail: TraceDetailObservation; label: string; compact?: boolean }) {
  const { t } = useTranslation();
  const rows = flattenTraceNodes(detail.root);
  const typeStats = getTypeStats(rows);
  const traceTypeLabels = getTraceTypeLabels(t);

  return (
    <section className={`self-evolution-trace-detail-card${compact ? " is-compact" : ""}`} aria-label={t("selfEvolutionRun.trace.detailAria", { label })}>
      <div className="self-evolution-trace-detail-head">
        <div>
          <Text strong>{label}</Text>
          <span>{detail.traceId}</span>
        </div>
        <Tag color={getStatusColor(detail.status)}>{detail.status}</Tag>
      </div>
      {detail.query && <Paragraph className="self-evolution-trace-query">{detail.query}</Paragraph>}
      <div className="self-evolution-trace-metrics" aria-label={t("selfEvolutionRun.trace.metricAria", { label })}>
        {getMetricItems(t, detail).map((metric) => (
          <span key={metric.key}>
            <em>{metric.label}</em>
            <strong>{metric.value}</strong>
          </span>
        ))}
      </div>
      <div className="self-evolution-trace-type-strip" aria-label={t("selfEvolutionRun.trace.typeStatsAria", { label })}>
        {typeStats.map(([type, count]) => (
          <span key={type}>{`${traceTypeLabels[type] || type} ${count}`}</span>
        ))}
      </div>
      {renderFlowPanel(t, detail, rows, compact)}
    </section>
  );
}

export function TraceDetailWorkspace({ detail, title }: { detail: TraceDetailObservation; title: string }) {
  const { t } = useTranslation();
  const rows = flattenTraceNodes(detail.root);

  return (
    <section className="self-evolution-trace-observation" aria-label={t("selfEvolutionRun.agenticRagObservationTitle")}>
      <div className="self-evolution-trace-observation-head">
        <div>
          <Text strong>{title}</Text>
          <span>{t("selfEvolutionRun.trace.agenticRagDetail", { traceId: getShortTraceId(detail.traceId) })}</span>
        </div>
      </div>
      {renderMetaTiles(t, detail, rows)}
      {renderSummaryStrip(t, detail)}
      <div className="self-evolution-trace-workspace">
        {renderFlowPanel(t, detail, rows)}
        {renderInspectorPanel(t, detail, rows)}
      </div>
    </section>
  );
}
