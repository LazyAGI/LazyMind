import { type CSSProperties } from "react";
import { useTranslation } from "react-i18next";
import { Tag, Typography } from "antd";
import {
  getNumberField,
  getStringField,
  isRecord,
  stringifyResultPayload,
} from "../shared";

type TFunction = (key: string, options?: Record<string, unknown>) => string;

const { Paragraph, Text } = Typography;

type TracePayloadPreview = {
  kind?: string;
  summary?: string;
  data?: unknown;
};

type TraceNode = {
  id: string;
  name: string;
  type: string;
  status: string;
  latencyMs?: number;
  input?: TracePayloadPreview;
  output?: TracePayloadPreview;
  metadata?: Record<string, unknown>;
  children: TraceNode[];
};

type TraceSummary = {
  status: string;
  latencyMs?: number;
  roundCount?: number;
  toolCallCount?: number;
  retrievalCount?: number;
  rerankCount?: number;
  nodeCount: number;
};

export type TraceDetailObservation = {
  traceId: string;
  query: string;
  status: string;
  summary: TraceSummary;
  root: TraceNode;
};

export type TraceObservation =
  | {
    kind: "detail";
    detail: TraceDetailObservation;
  }
  | {
    kind: "compare";
    query: string;
    a: TraceDetailObservation;
    b: TraceDetailObservation;
  };

type TraceObservationViewProps = {
  observation: TraceObservation;
  title: string;
};

type FlatTraceNode = {
  node: TraceNode;
  depth: number;
};

type MetricItem = {
  key: string;
  label: string;
  value: string;
};

type TraceDocPreview = {
  key: string;
  title: string;
  text: string;
  score?: number;
  ref?: string;
};

const nestedObservationKeys = [
  "data",
  "result",
  "results",
  "payload",
  "detail",
  "trace_detail",
  "trace_result",
  "observation",
  "observability",
];

function getTraceTypeLabels(t: TFunction): Record<string, string> {
  return {
    flow: "Flow",
    workflow_control: t("selfEvolutionRun.trace.controlFlow"),
    callable: "Callable",
    llm: "LLM",
    tool: "Tool",
    retriever: t("selfEvolutionRun.trace.retriever"),
    rerank: t("selfEvolutionRun.trace.rerank"),
    module: t("selfEvolutionRun.trace.module"),
  };
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

function getRecordField(payload: Record<string, unknown> | undefined, keys: string[]) {
  if (!payload) {
    return undefined;
  }
  for (const key of keys) {
    const value = payload[key];
    if (isRecord(value)) {
      return value;
    }
  }
  return undefined;
}

function getArrayField(payload: Record<string, unknown> | undefined, keys: string[]) {
  if (!payload) {
    return [];
  }
  for (const key of keys) {
    const value = payload[key];
    if (Array.isArray(value)) {
      return value;
    }
  }
  return [];
}

function getDisplayText(value: unknown, maxLength = 160): string | undefined {
  if (typeof value === "string") {
    const trimmed = value.trim();
    if (!trimmed) {
      return undefined;
    }
    return trimmed.length > maxLength ? `${trimmed.slice(0, maxLength)}...` : trimmed;
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  if (Array.isArray(value)) {
    return `${value.length} items`;
  }
  if (isRecord(value)) {
    const keys = Object.keys(value);
    return keys.length > 0 ? `${keys.slice(0, 4).join(" / ")}${keys.length > 4 ? "..." : ""}` : "0 fields";
  }
  return undefined;
}

function normalizePayloadPreview(value: unknown): TracePayloadPreview | undefined {
  if (value === undefined || value === null) {
    return undefined;
  }
  if (!isRecord(value)) {
    return { summary: getDisplayText(value), data: value };
  }

  const data = value.data;
  return {
    kind: getStringField(value, ["kind", "type"]),
    summary: getStringField(value, ["summary", "text", "content"]) || getDisplayText(data),
    data,
  };
}

function normalizeTraceNode(value: unknown, fallbackId: string): TraceNode | undefined {
  if (!isRecord(value)) {
    return undefined;
  }

  const children = getArrayField(value, ["children"]).flatMap((child, index) => {
    const normalized = normalizeTraceNode(child, `${fallbackId}-${index + 1}`);
    return normalized ? [normalized] : [];
  });
  const id = getStringField(value, ["id", "node_id", "trace_id"]) || fallbackId;

  return {
    id,
    name: getStringField(value, ["name", "title", "module_name"]) || "Unnamed",
    type: getStringField(value, ["type", "kind"]) || "node",
    status: getStringField(value, ["status", "state"]) || "unknown",
    latencyMs: isFiniteNumber(value.latency)
      ? value.latency * 1000
      : getNumberField(value, ["latency_ms", "latencyMs", "duration_ms", "durationMs"]),
    input: normalizePayloadPreview(value.input),
    output: normalizePayloadPreview(value.output),
    metadata: getRecordField(value, ["metadata", "meta"]),
    children,
  };
}

function flattenTraceNodes(root: TraceNode) {
  const rows: FlatTraceNode[] = [];
  const walk = (node: TraceNode, depth: number) => {
    rows.push({ node, depth });
    node.children.forEach((child) => walk(child, depth + 1));
  };
  walk(root, 0);
  return rows;
}

function countTraceType(rows: FlatTraceNode[], type: string) {
  return rows.filter(({ node }) => node.type === type).length;
}

function normalizeTraceDetailRecord(value: unknown): TraceDetailObservation | undefined {
  if (!isRecord(value)) {
    return undefined;
  }

  const traceRecord = getRecordField(value, ["trace"]) || value;
  const rootRecord = getRecordField(traceRecord, ["root"]) || getRecordField(value, ["root"]);
  const root = normalizeTraceNode(rootRecord, "root");
  if (!root) {
    return undefined;
  }

  const rows = flattenTraceNodes(root);
  const summaryRecord =
    getRecordField(value, ["summary"]) ||
    getRecordField(traceRecord, ["summary"]) ||
    getRecordField(traceRecord, ["metadata"]);
  const latencyMs =
    getNumberField(summaryRecord, ["latency_ms", "latencyMs", "duration_ms", "durationMs"]) ||
    getNumberField(getRecordField(traceRecord, ["metadata"]), ["latency_ms", "latencyMs"]) ||
    root.latencyMs;
  const status =
    getStringField(value, ["trace_status", "status"]) ||
    getStringField(summaryRecord, ["trace_status", "status"]) ||
    root.status;
  const query =
    getStringField(value, ["query", "question", "prompt"]) ||
    root.input?.summary ||
    root.output?.summary ||
    "";

  return {
    traceId:
      getStringField(value, ["trace_id", "id"]) ||
      getStringField(traceRecord, ["trace_id", "id"]) ||
      root.id,
    query,
    status,
    summary: {
      status,
      latencyMs,
      roundCount: getNumberField(summaryRecord, ["round_count", "roundCount", "rounds"]),
      toolCallCount: getNumberField(summaryRecord, ["tool_call_count", "toolCallCount"]) ?? countTraceType(rows, "tool"),
      retrievalCount: getNumberField(summaryRecord, ["retrieval_count", "retrievalCount"]) ?? countTraceType(rows, "retriever"),
      rerankCount: getNumberField(summaryRecord, ["rerank_count", "rerankCount"]) ?? countTraceType(rows, "rerank"),
      nodeCount: rows.length,
    },
    root,
  };
}

function normalizeTraceCompareRecord(value: unknown): TraceObservation | undefined {
  if (!isRecord(value)) {
    return undefined;
  }

  const a = normalizeTraceDetailRecord(value.a);
  const b = normalizeTraceDetailRecord(value.b);
  if (!a || !b) {
    return undefined;
  }

  return {
    kind: "compare",
    query: getStringField(value, ["query", "question", "prompt"]) || a.query || b.query,
    a,
    b,
  };
}

export function normalizeTraceObservation(value: unknown, depth = 0): TraceObservation | undefined {
  if (depth > 5 || value === undefined || value === null) {
    return undefined;
  }
  if (Array.isArray(value)) {
    for (const item of value) {
      const observation = normalizeTraceObservation(item, depth + 1);
      if (observation) {
        return observation;
      }
    }
    return undefined;
  }
  if (!isRecord(value)) {
    return undefined;
  }

  const compare = normalizeTraceCompareRecord(value);
  if (compare) {
    return compare;
  }
  const detail = normalizeTraceDetailRecord(value);
  if (detail) {
    return { kind: "detail", detail };
  }

  for (const key of nestedObservationKeys) {
    const nested: unknown = value[key];
    if (nested === value) {
      continue;
    }
    const observation = normalizeTraceObservation(nested, depth + 1);
    if (observation) {
      return observation;
    }
  }

  return undefined;
}

function formatDuration(ms?: number) {
  if (!isFiniteNumber(ms)) {
    return "-";
  }
  if (ms >= 1000) {
    return `${(ms / 1000).toFixed(ms >= 10_000 ? 1 : 2)}s`;
  }
  return `${ms.toFixed(ms < 10 ? 1 : 0)}ms`;
}

function formatCount(value?: number) {
  return isFiniteNumber(value) ? String(Math.round(value)) : "-";
}

function formatNumberDelta(a?: number, b?: number) {
  if (!isFiniteNumber(a) || !isFiniteNumber(b)) {
    return "-";
  }
  const delta = b - a;
  return `${delta > 0 ? "+" : ""}${Number.isInteger(delta) ? delta : delta.toFixed(1)}`;
}

function formatDurationDelta(a?: number, b?: number) {
  if (!isFiniteNumber(a) || !isFiniteNumber(b)) {
    return "-";
  }
  const delta = b - a;
  return `${delta > 0 ? "+" : delta < 0 ? "-" : ""}${formatDuration(Math.abs(delta))}`;
}

function getStatusColor(status: string) {
  const normalized = status.toLowerCase();
  if (["success", "done", "completed", "succeeded"].includes(normalized)) {
    return "success";
  }
  if (["failed", "error"].includes(normalized)) {
    return "error";
  }
  if (["running", "pending"].includes(normalized)) {
    return "processing";
  }
  return "default";
}

function getMetricItems(t: TFunction, detail: TraceDetailObservation): MetricItem[] {
  return [
    { key: "latency", label: t("selfEvolutionRun.trace.totalLatency"), value: formatDuration(detail.summary.latencyMs) },
    { key: "round", label: t("selfEvolutionRun.trace.roundCount"), value: formatCount(detail.summary.roundCount) },
    { key: "tool", label: t("selfEvolutionRun.trace.toolCallCount"), value: formatCount(detail.summary.toolCallCount) },
    { key: "retrieval", label: t("selfEvolutionRun.trace.retrievalCount"), value: formatCount(detail.summary.retrievalCount) },
    { key: "rerank", label: t("selfEvolutionRun.trace.rerankCount"), value: formatCount(detail.summary.rerankCount) },
    { key: "node", label: t("selfEvolutionRun.trace.nodeCount"), value: formatCount(detail.summary.nodeCount) },
  ];
}

function getShortTraceId(traceId: string) {
  if (traceId.length <= 14) {
    return traceId;
  }
  return `${traceId.slice(0, 6)}...${traceId.slice(-6)}`;
}

function getModeLabel(rows: FlatTraceNode[]) {
  return rows.some(({ node }) => node.type === "tool" || node.type === "retriever")
    ? "agentic_rag"
    : "rag";
}

function getNodeDataRecord(payload?: TracePayloadPreview) {
  return isRecord(payload?.data) ? payload.data : undefined;
}

function findRecordValue(value: unknown, key: string, depth = 0): unknown {
  if (depth > 5) {
    return undefined;
  }
  if (Array.isArray(value)) {
    for (const item of value) {
      const result = findRecordValue(item, key, depth + 1);
      if (result !== undefined) {
        return result;
      }
    }
    return undefined;
  }
  if (!isRecord(value)) {
    return undefined;
  }
  if (value[key] !== undefined) {
    return value[key];
  }
  for (const nested of Object.values(value)) {
    const result = findRecordValue(nested, key, depth + 1);
    if (result !== undefined) {
      return result;
    }
  }
  return undefined;
}

function findArrayValue(value: unknown, keys: string[], depth = 0): unknown[] {
  if (depth > 5) {
    return [];
  }
  if (Array.isArray(value)) {
    return value;
  }
  if (!isRecord(value)) {
    return [];
  }
  for (const key of keys) {
    const nested = value[key];
    if (Array.isArray(nested)) {
      return nested;
    }
  }
  for (const nested of Object.values(value)) {
    const result = findArrayValue(nested, keys, depth + 1);
    if (result.length > 0) {
      return result;
    }
  }
  return [];
}

function extractDocsFromNode(node?: TraceNode): TraceDocPreview[] {
  const outputData = node?.output?.data;
  const items = findArrayValue(outputData, ["items", "docs", "documents", "nodes"]);
  return items
    .filter(isRecord)
    .slice(0, 4)
    .map((item, index) => {
      const title =
        getStringField(item, ["file_name", "display_name", "docid", "document_id", "title"]) ||
        `Doc #${index + 1}`;
      const score = getNumberField(item, ["score", "similarity", "max_score"]);
      return {
        key: getStringField(item, ["docid", "id", "chunk_id"]) || `${title}-${index}`,
        title,
        text: getStringField(item, ["text", "content", "summary"]) || getDisplayText(item, 140) || "-",
        score,
        ref: getStringField(item, ["ref", "citation_index", "chunk_id"]),
      };
    });
}

function getInsightNode(rows: FlatTraceNode[]) {
  return (
    rows.find(({ node }) => ["failed", "error", "warning"].includes(node.status.toLowerCase()))?.node ||
    rows.find(({ node }) => extractDocsFromNode(node).length > 0)?.node ||
    rows.find(({ node }) => node.type === "tool")?.node ||
    rows.find(({ node }) => node.type === "retriever")?.node ||
    rows.find(({ node }) => node.type === "llm")?.node ||
    rows[0]?.node
  );
}

function getTraceConclusion(t: TFunction, detail: TraceDetailObservation, insightNode?: TraceNode) {
  const normalizedStatus = detail.status.toLowerCase();
  if (["failed", "error"].includes(normalizedStatus)) {
    return t("selfEvolutionRun.trace.conclusionFailed");
  }
  const docs = extractDocsFromNode(insightNode);
  if (detail.summary.retrievalCount && docs.length === 0) {
    return t("selfEvolutionRun.trace.conclusionRetrievalNoDoc");
  }
  if (detail.summary.toolCallCount && detail.summary.toolCallCount > 0) {
    return t("selfEvolutionRun.trace.conclusionWithTools");
  }
  return t("selfEvolutionRun.trace.conclusionDefault");
}

function getTypeStats(rows: FlatTraceNode[]) {
  const counts = rows.reduce<Record<string, number>>((acc, { node }) => {
    acc[node.type] = (acc[node.type] || 0) + 1;
    return acc;
  }, {});
  return Object.entries(counts).sort((a, b) => b[1] - a[1]);
}

function renderMetaTiles(t: TFunction, detail: TraceDetailObservation, rows: FlatTraceNode[]) {
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

function renderSummaryStrip(t: TFunction, detail: TraceDetailObservation) {
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

function renderPayloadSummary(label: string, payload?: TracePayloadPreview) {
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

function renderPayloadDetails(label: string, payload?: TracePayloadPreview) {
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

function renderMetadata(t: TFunction, metadata?: Record<string, unknown>) {
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

function renderFlowPanel(t: TFunction, detail: TraceDetailObservation, rows: FlatTraceNode[], compact = false) {
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

function renderDocList(t: TFunction, node?: TraceNode) {
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

function renderInspectorPanel(t: TFunction, detail: TraceDetailObservation, rows: FlatTraceNode[]) {
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

function TraceDetailPanel({ detail, label, compact = false }: { detail: TraceDetailObservation; label: string; compact?: boolean }) {
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

function TraceDetailWorkspace({ detail, title }: { detail: TraceDetailObservation; title: string }) {
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

function TraceComparePanel({ observation, title }: { observation: Extract<TraceObservation, { kind: "compare" }>; title: string }) {
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

export function TraceObservationView({ observation, title }: TraceObservationViewProps) {
  if (observation.kind === "compare") {
    return <TraceComparePanel observation={observation} title={title} />;
  }

  return <TraceDetailWorkspace detail={observation.detail} title={title} />;
}
