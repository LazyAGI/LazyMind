import { useTranslation } from "react-i18next";
import { Tag, Typography } from "antd";
import type { TraceObservation } from "./types";
import {
  flattenTraceNodes,
  formatCount,
  formatDuration,
  formatDurationDelta,
  formatNumberDelta,
  getInsightNode,
  getShortTraceId,
  getStatusColor,
} from "./utils";
import { renderDocList } from "./renderHelpers";
import { TraceDetailPanel } from "./TraceDetailPanel";

const { Paragraph, Text } = Typography;

export function TraceComparePanel({ observation, title }: { observation: Extract<TraceObservation, { kind: "compare" }>; title: string }) {
  const { t } = useTranslation();
  const compareRows = [
    {
      key: "latency",
      label: t("selfEvolutionRun.trace.totalLatency"),
      a: formatDuration(observation.a.summary.latencyMs),
      b: formatDuration(observation.b.summary.latencyMs),
      delta: formatDurationDelta(observation.a.summary.latencyMs, observation.b.summary.latencyMs),
    },
    {
      key: "tool",
      label: t("selfEvolutionRun.trace.toolCallCount"),
      a: formatCount(observation.a.summary.toolCallCount),
      b: formatCount(observation.b.summary.toolCallCount),
      delta: formatNumberDelta(observation.a.summary.toolCallCount, observation.b.summary.toolCallCount),
    },
    {
      key: "retrieval",
      label: t("selfEvolutionRun.trace.retrievalCount"),
      a: formatCount(observation.a.summary.retrievalCount),
      b: formatCount(observation.b.summary.retrievalCount),
      delta: formatNumberDelta(observation.a.summary.retrievalCount, observation.b.summary.retrievalCount),
    },
    {
      key: "rerank",
      label: t("selfEvolutionRun.trace.rerankCount"),
      a: formatCount(observation.a.summary.rerankCount),
      b: formatCount(observation.b.summary.rerankCount),
      delta: formatNumberDelta(observation.a.summary.rerankCount, observation.b.summary.rerankCount),
    },
    {
      key: "node",
      label: t("selfEvolutionRun.trace.nodeCount"),
      a: formatCount(observation.a.summary.nodeCount),
      b: formatCount(observation.b.summary.nodeCount),
      delta: formatNumberDelta(observation.a.summary.nodeCount, observation.b.summary.nodeCount),
    },
  ];

  return (
    <section className="self-evolution-trace-observation is-compare" aria-label={t("selfEvolutionRun.trace.compareAria")}>
      <div className="self-evolution-trace-observation-head">
        <div>
          <Text strong>{title}</Text>
          <span>{t("selfEvolutionRun.trace.compareTitle")}</span>
        </div>
      </div>
      {observation.query && <Paragraph className="self-evolution-trace-query is-main">{observation.query}</Paragraph>}
      <div className="self-evolution-trace-ab-summary">
        <article>
          <Text strong>{t("selfEvolutionRun.trace.baselineAlgorithm")}</Text>
          <span>{`Trace ID ${getShortTraceId(observation.a.traceId)}`}</span>
          <span>{`Latency ${formatDuration(observation.a.summary.latencyMs)}`}</span>
          <Tag color={getStatusColor(observation.a.status)}>{observation.a.status}</Tag>
        </article>
        <article>
          <Text strong>{t("selfEvolutionRun.trace.optimizedAlgorithm")}</Text>
          <span>{`Trace ID ${getShortTraceId(observation.b.traceId)}`}</span>
          <span>{`Latency ${formatDuration(observation.b.summary.latencyMs)}`}</span>
          <Tag color={getStatusColor(observation.b.status)}>{observation.b.status}</Tag>
        </article>
      </div>
      <div className="self-evolution-trace-compare-grid" aria-label={t("selfEvolutionRun.trace.compareGridAria")}>
        <div className="self-evolution-trace-compare-row is-head">
          <span>{t("selfEvolutionRun.trace.metricHeader")}</span>
          <span>{t("selfEvolutionRun.trace.aBaselineHeader")}</span>
          <span>{t("selfEvolutionRun.trace.bCandidateHeader")}</span>
          <span>{t("selfEvolutionRun.trace.changeHeader")}</span>
        </div>
        {compareRows.map((row) => (
          <div key={row.key} className="self-evolution-trace-compare-row">
            <span>{row.label}</span>
            <strong>{row.a}</strong>
            <strong>{row.b}</strong>
            <em>{row.delta}</em>
          </div>
        ))}
      </div>
      <div className="self-evolution-trace-compare-columns">
        <TraceDetailPanel detail={observation.a} label={t("selfEvolutionRun.trace.baselineTrace")} compact />
        <TraceDetailPanel detail={observation.b} label={t("selfEvolutionRun.trace.candidateTrace")} compact />
      </div>
      <div className="self-evolution-trace-ab-docs">
        <div className="self-evolution-trace-panel-head">
          <Text>{t("selfEvolutionRun.trace.retrievedDocsCompare")}</Text>
        </div>
        <div className="self-evolution-trace-ab-doc-columns">
          <section>
            <Text strong>{t("selfEvolutionRun.trace.baselineAlgorithm")}</Text>
            {renderDocList(t, getInsightNode(flattenTraceNodes(observation.a.root)))}
          </section>
          <section>
            <Text strong>{t("selfEvolutionRun.trace.optimizedAlgorithm")}</Text>
            {renderDocList(t, getInsightNode(flattenTraceNodes(observation.b.root)))}
          </section>
        </div>
      </div>
      <div className="self-evolution-trace-ab-conclusion">
        <Text strong>{t("selfEvolutionRun.trace.observationConclusion")}</Text>
        <span>{t("selfEvolutionRun.trace.abConclusion", {
          latency: formatDurationDelta(observation.a.summary.latencyMs, observation.b.summary.latencyMs),
          toolCallDelta: formatNumberDelta(observation.a.summary.toolCallCount, observation.b.summary.toolCallCount),
        })}</span>
      </div>
    </section>
  );
}
