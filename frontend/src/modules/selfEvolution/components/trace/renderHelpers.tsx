import { type CSSProperties } from "react";
import { Tag, Typography } from "antd";
import { stringifyResultPayload } from "../../shared";
import type { FlatTraceNode, TFunction, TraceDetailObservation, TraceNode, TracePayloadPreview } from "./types";
import {
  extractDocsFromNode,
  findRecordValue,
  formatCount,
  formatDuration,
  getDisplayText,
  getInsightNode,
  getModeLabel,
  getNodeDataRecord,
  getShortTraceId,
  getStatusColor,
  getTraceConclusion,
  getTraceTypeLabels,
  isFiniteNumber,
} from "./utils";

const { Paragraph, Text } = Typography;

export function renderMetaTiles(t: TFunction, detail: TraceDetailObservation, rows: FlatTraceNode[]) {
  const tiles = [
    { key: "trace_id", label: "trace_id", value: getShortTraceId(detail.traceId) },
    { key: "scene", label: "scene", value: "eval" },
    { key: "execution_mode", label: "execution_mode", value: getModeLabel(rows) },
    { key: "status", label: "status", value: detail.status, status: true },
    { key: "latency", label: "latency", value: formatDuration(detail.summary.latencyMs) },
  ];

  return (
    <div className="self-evolution-trace-meta-grid" aria-label={t("selfEvolutionRun.trace.metaAria")}>
      {tiles.map((tile) => (
        <div key={tile.key} className={`self-evolution-trace-meta-card${tile.status ? " is-status" : ""}`}>
          <span>{tile.label}</span>
          {tile.status ? <Tag color={getStatusColor(detail.status)}>{tile.value}</Tag> : <strong>{tile.value}</strong>}
        </div>
      ))}
    </div>
  );
}

export function renderSummaryStrip(t: TFunction, detail: TraceDetailObservation) {
  return (
    <div className="self-evolution-trace-summary-strip" aria-label={t("selfEvolutionRun.trace.summaryAria")}>
      <span>
        <strong>{formatCount(detail.summary.roundCount)}</strong>
        <em>{t("selfEvolutionRun.trace.roundLabel")}</em>
      </span>
      <span>
        <strong>{formatCount(detail.summary.toolCallCount)}</strong>
        <em>{t("selfEvolutionRun.trace.toolCallLabel")}</em>
      </span>
      <span>
        <strong>{formatCount(detail.summary.retrievalCount)}</strong>
        <em>{t("selfEvolutionRun.trace.retrievalLabel")}</em>
      </span>
      <span>
        <strong>{formatCount(detail.summary.rerankCount)}</strong>
        <em>{t("selfEvolutionRun.trace.rerankLabel")}</em>
      </span>
      <span className="is-finish">
        <strong>{detail.status}</strong>
        <em>finish_reason</em>
      </span>
    </div>
  );
}

export function renderPayloadSummary(label: string, payload?: TracePayloadPreview) {
  if (!payload?.summary && !payload?.kind) {
    return null;
  }

  return (
    <div className="self-evolution-trace-io-block">
      <span>{label}</span>
      {payload.kind && <Text>{payload.kind}</Text>}
      {payload.summary && <p>{payload.summary}</p>}
    </div>
  );
}

export function renderPayloadDetails(label: string, payload?: TracePayloadPreview) {
  if (payload?.data === undefined) {
    return null;
  }

  return (
    <details className="self-evolution-trace-payload">
      <summary>{label}</summary>
      <pre>{stringifyResultPayload(payload.data)}</pre>
    </details>
  );
}

export function renderMetadata(t: TFunction, metadata?: Record<string, unknown>) {
  if (!metadata || Object.keys(metadata).length === 0) {
    return null;
  }

  return (
    <div className="self-evolution-trace-node-metadata" aria-label={t("selfEvolutionRun.trace.nodeMetaAria")}>
      {Object.entries(metadata).slice(0, 8).map(([key, value]) => (
        <span key={key}>
          <strong>{key}</strong>
          <em>{getDisplayText(value, 80) || "-"}</em>
        </span>
      ))}
    </div>
  );
}

function renderNodeRow(t: TFunction, row: FlatTraceNode, index: number, maxLatencyMs: number, compact = false) {
  const { node, depth } = row;
  const traceTypeLabels = getTraceTypeLabels(t);
  const style = {
    "--trace-depth": depth,
  } as CSSProperties;
  const latencyRatio = node.latencyMs && maxLatencyMs > 0
    ? Math.max(3, Math.min(100, (node.latencyMs / maxLatencyMs) * 100))
    : 0;

  return (
    <li key={`${node.id}-${index}`} className={`self-evolution-trace-node is-${node.status}`} style={style}>
      <details open={!compact && index === 0}>
        <summary>
          <span className="self-evolution-trace-node-rail" aria-hidden />
          <span className="self-evolution-trace-node-main">
            <span className="self-evolution-trace-node-title">
              <strong>{node.name}</strong>
              <em>{traceTypeLabels[node.type] || node.type}</em>
              <Tag color={getStatusColor(node.status)}>{node.status}</Tag>
            </span>
            <span className="self-evolution-trace-node-subtitle">
              {node.input?.summary || node.output?.summary || t("selfEvolutionRun.trace.noIoSummary")}
            </span>
          </span>
          <span className="self-evolution-trace-node-duration">
            <span>{formatDuration(node.latencyMs)}</span>
            <i style={{ width: `${latencyRatio}%` }} />
          </span>
        </summary>
        <div className="self-evolution-trace-node-detail">
          <div className="self-evolution-trace-node-io">
            {renderPayloadSummary(t("selfEvolutionRun.trace.ioInput"), node.input)}
            {renderPayloadSummary(t("selfEvolutionRun.trace.ioOutput"), node.output)}
          </div>
          {renderMetadata(t, node.metadata)}
          {renderPayloadDetails(t("selfEvolutionRun.trace.ioInputJson"), node.input)}
          {renderPayloadDetails(t("selfEvolutionRun.trace.ioOutputJson"), node.output)}
        </div>
      </details>
    </li>
  );
}

export function renderFlowPanel(t: TFunction, detail: TraceDetailObservation, rows: FlatTraceNode[], compact = false) {
  const maxLatencyMs = rows.reduce((max, { node }) => Math.max(max, node.latencyMs || 0), 0);
  const visibleRows = compact ? rows.filter(({ depth }) => depth <= 3).slice(0, 12) : rows;

  return (
    <section className="self-evolution-trace-flow-panel" aria-label={t("selfEvolutionRun.trace.flowAria")}>
      <div className="self-evolution-trace-panel-head">
        <Text>{t("selfEvolutionRun.trace.flowTitle")}</Text>
        <span>{formatDuration(detail.summary.latencyMs)}</span>
      </div>
      <ol className="self-evolution-trace-node-list">
        {visibleRows.map((row, index) => renderNodeRow(t, row, index, maxLatencyMs, compact))}
      </ol>
    </section>
  );
}

function renderNodeInfoGrid(t: TFunction, node: TraceNode) {
  const traceTypeLabels = getTraceTypeLabels(t);
  const inputData = getNodeDataRecord(node.input);
  const outputData = getNodeDataRecord(node.output);
  const rows = [
    { label: "node_id", value: getShortTraceId(node.id) },
    { label: "node_type", value: traceTypeLabels[node.type] || node.type },
    { label: "status", value: node.status },
    { label: "duration", value: formatDuration(node.latencyMs) },
    { label: "input_kind", value: node.input?.kind || "-" },
    { label: "output_kind", value: node.output?.kind || "-" },
    { label: "top_k", value: getDisplayText(findRecordValue(inputData, "topk") ?? findRecordValue(inputData, "top_k")) || "-" },
    { label: "returned_docs", value: getDisplayText(findRecordValue(outputData, "total") ?? findRecordValue(outputData, "node_count")) || "-" },
  ];

  return (
    <div className="self-evolution-trace-inspector-grid">
      {rows.map((row) => (
        <span key={row.label}>
          <em>{row.label}</em>
          <strong>{row.value}</strong>
        </span>
      ))}
    </div>
  );
}

export function renderDocList(t: TFunction, node?: TraceNode) {
  const docs = extractDocsFromNode(node);
  if (docs.length === 0) {
    return <Paragraph className="self-evolution-trace-empty">{t("selfEvolutionRun.trace.noRetrievedDocs")}</Paragraph>;
  }

  return (
    <div className="self-evolution-trace-doc-list">
      {docs.map((doc, index) => (
        <article key={doc.key} className="self-evolution-trace-doc-card">
          <div>
            <Text strong>{`Doc #${index + 1} ${doc.title}`}</Text>
            <span>{doc.ref || "chunk"}</span>
            {isFiniteNumber(doc.score) && (
              <Tag color="blue">{t("selfEvolutionRun.trace.relevanceScore", { score: doc.score.toFixed(2) })}</Tag>
            )}
          </div>
          <p>{doc.text}</p>
        </article>
      ))}
    </div>
  );
}

export function renderInspectorPanel(t: TFunction, detail: TraceDetailObservation, rows: FlatTraceNode[]) {
  const node = getInsightNode(rows);
  if (!node) {
    return (
      <section className="self-evolution-trace-inspector" aria-label={t("selfEvolutionRun.trace.inspectorAria")}>
        <div className="self-evolution-trace-panel-head">
          <Text>{t("selfEvolutionRun.trace.inspectorTitle")}</Text>
        </div>
        <Paragraph className="self-evolution-trace-empty">{t("selfEvolutionRun.trace.noNodes")}</Paragraph>
      </section>
    );
  }

  return (
    <section className="self-evolution-trace-inspector" aria-label={t("selfEvolutionRun.trace.inspectorAria")}>
      <div className="self-evolution-trace-panel-head">
        <Text>{t("selfEvolutionRun.trace.nodeDetailTitle", { name: node.name })}</Text>
        <Tag color={getStatusColor(node.status)}>{node.status}</Tag>
      </div>
      <div className="self-evolution-trace-inspector-section">
        <h4>{t("selfEvolutionRun.trace.nodeInfoSection")}</h4>
        {renderNodeInfoGrid(t, node)}
      </div>
      <div className="self-evolution-trace-inspector-section">
        <h4>{t("selfEvolutionRun.trace.inputSection")}</h4>
        {renderPayloadSummary("input", node.input) || (
          <Paragraph className="self-evolution-trace-empty">{t("selfEvolutionRun.trace.noInputSummary")}</Paragraph>
        )}
      </div>
      <div className="self-evolution-trace-inspector-section">
        <h4>{t("selfEvolutionRun.trace.outputSection")}</h4>
        {renderPayloadSummary("output", node.output) || (
          <Paragraph className="self-evolution-trace-empty">{t("selfEvolutionRun.trace.noOutputSummary")}</Paragraph>
        )}
      </div>
      <div className="self-evolution-trace-inspector-section">
        <h4>{t("selfEvolutionRun.trace.retrievedDocsSection")}</h4>
        {renderDocList(t, node)}
      </div>
      <div className="self-evolution-trace-inspector-section">
        <h4>{t("selfEvolutionRun.trace.metadataSection")}</h4>
        {renderMetadata(t, node.metadata) || (
          <Paragraph className="self-evolution-trace-empty">{t("selfEvolutionRun.trace.noMetadata")}</Paragraph>
        )}
      </div>
      <div className="self-evolution-trace-inspector-section is-judgement">
        <h4>{t("selfEvolutionRun.trace.observationSection")}</h4>
        <p>{getTraceConclusion(t, detail, node)}</p>
      </div>
      {renderPayloadDetails(t("selfEvolutionRun.trace.viewInputJson"), node.input)}
      {renderPayloadDetails(t("selfEvolutionRun.trace.viewOutputJson"), node.output)}
    </section>
  );
}
