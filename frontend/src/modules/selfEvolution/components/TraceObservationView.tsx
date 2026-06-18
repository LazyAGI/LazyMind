import { type CSSProperties } from "react";
import { Tag, Typography } from "antd";
import {
  getNumberField,
  getStringField,
  isRecord,
  stringifyResultPayload,
} from "../shared";

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

const traceTypeLabels: Record<string, string> = {
  flow: "Flow",
  workflow_control: "控制流",
  callable: "Callable",
  llm: "LLM",
  tool: "Tool",
  retriever: "检索",
  rerank: "重排",
  module: "模块",
};

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

function getMetricItems(detail: TraceDetailObservation): MetricItem[] {
  return [
    { key: "latency", label: "总耗时", value: formatDuration(detail.summary.latencyMs) },
    { key: "round", label: "轮次", value: formatCount(detail.summary.roundCount) },
    { key: "tool", label: "工具调用", value: formatCount(detail.summary.toolCallCount) },
    { key: "retrieval", label: "检索", value: formatCount(detail.summary.retrievalCount) },
    { key: "rerank", label: "重排", value: formatCount(detail.summary.rerankCount) },
    { key: "node", label: "节点", value: formatCount(detail.summary.nodeCount) },
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

function getTraceConclusion(detail: TraceDetailObservation, insightNode?: TraceNode) {
  const normalizedStatus = detail.status.toLowerCase();
  if (["failed", "error"].includes(normalizedStatus)) {
    return "Trace 执行失败，请优先查看失败节点的输入、输出和错误信息。";
  }
  const docs = extractDocsFromNode(insightNode);
  if (detail.summary.retrievalCount && docs.length === 0) {
    return "本次链路发生了检索，但当前节点未返回可展示文档，建议检查召回结果与筛选策略。";
  }
  if (detail.summary.toolCallCount && detail.summary.toolCallCount > 0) {
    return "本次链路完成工具调用与检索编排，可展开节点查看输入、输出、召回文档和 metadata。";
  }
  return "本次链路已完成，可结合节点耗时与输入输出摘要定位效果问题。";
}

function getTypeStats(rows: FlatTraceNode[]) {
  const counts = rows.reduce<Record<string, number>>((acc, { node }) => {
    acc[node.type] = (acc[node.type] || 0) + 1;
    return acc;
  }, {});
  return Object.entries(counts).sort((a, b) => b[1] - a[1]);
}

function renderMetaTiles(detail: TraceDetailObservation, rows: FlatTraceNode[]) {
  const tiles = [
    { key: "trace_id", label: "trace_id", value: getShortTraceId(detail.traceId) },
    { key: "scene", label: "scene", value: "eval" },
    { key: "execution_mode", label: "execution_mode", value: getModeLabel(rows) },
    { key: "status", label: "status", value: detail.status, status: true },
    { key: "latency", label: "latency", value: formatDuration(detail.summary.latencyMs) },
  ];

  return (
    <div className="self-evolution-trace-meta-grid" aria-label="Trace 元信息">
      {tiles.map((tile) => (
        <div key={tile.key} className={`self-evolution-trace-meta-card${tile.status ? " is-status" : ""}`}>
          <span>{tile.label}</span>
          {tile.status ? <Tag color={getStatusColor(detail.status)}>{tile.value}</Tag> : <strong>{tile.value}</strong>}
        </div>
      ))}
    </div>
  );
}

function renderSummaryStrip(detail: TraceDetailObservation) {
  return (
    <div className="self-evolution-trace-summary-strip" aria-label="Trace 汇总指标">
      <span>
        <strong>{formatCount(detail.summary.roundCount)}</strong>
        <em>轮决策</em>
      </span>
      <span>
        <strong>{formatCount(detail.summary.toolCallCount)}</strong>
        <em>次工具调用</em>
      </span>
      <span>
        <strong>{formatCount(detail.summary.retrievalCount)}</strong>
        <em>次知识库检索</em>
      </span>
      <span>
        <strong>{formatCount(detail.summary.rerankCount)}</strong>
        <em>次重排</em>
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

function renderMetadata(metadata?: Record<string, unknown>) {
  if (!metadata || Object.keys(metadata).length === 0) {
    return null;
  }

  return (
    <div className="self-evolution-trace-node-metadata" aria-label="节点元数据">
      {Object.entries(metadata).slice(0, 8).map(([key, value]) => (
        <span key={key}>
          <strong>{key}</strong>
          <em>{getDisplayText(value, 80) || "-"}</em>
        </span>
      ))}
    </div>
  );
}

function renderNodeRow(row: FlatTraceNode, index: number, maxLatencyMs: number, compact = false) {
  const { node, depth } = row;
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
              {node.input?.summary || node.output?.summary || "暂无输入输出摘要"}
            </span>
          </span>
          <span className="self-evolution-trace-node-duration">
            <span>{formatDuration(node.latencyMs)}</span>
            <i style={{ width: `${latencyRatio}%` }} />
          </span>
        </summary>
        <div className="self-evolution-trace-node-detail">
          <div className="self-evolution-trace-node-io">
            {renderPayloadSummary("输入", node.input)}
            {renderPayloadSummary("输出", node.output)}
          </div>
          {renderMetadata(node.metadata)}
          {renderPayloadDetails("输入 JSON", node.input)}
          {renderPayloadDetails("输出 JSON", node.output)}
        </div>
      </details>
    </li>
  );
}

function renderFlowPanel(detail: TraceDetailObservation, rows: FlatTraceNode[], compact = false) {
  const maxLatencyMs = rows.reduce((max, { node }) => Math.max(max, node.latencyMs || 0), 0);
  const visibleRows = compact ? rows.filter(({ depth }) => depth <= 3).slice(0, 12) : rows;

  return (
    <section className="self-evolution-trace-flow-panel" aria-label="智能执行流程">
      <div className="self-evolution-trace-panel-head">
        <Text>智能执行流程</Text>
        <span>{formatDuration(detail.summary.latencyMs)}</span>
      </div>
      <ol className="self-evolution-trace-node-list">
        {visibleRows.map((row, index) => renderNodeRow(row, index, maxLatencyMs, compact))}
      </ol>
    </section>
  );
}

function renderNodeInfoGrid(node: TraceNode) {
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

function renderDocList(node?: TraceNode) {
  const docs = extractDocsFromNode(node);
  if (docs.length === 0) {
    return <Paragraph className="self-evolution-trace-empty">当前节点没有可展示召回文档。</Paragraph>;
  }

  return (
    <div className="self-evolution-trace-doc-list">
      {docs.map((doc, index) => (
        <article key={doc.key} className="self-evolution-trace-doc-card">
          <div>
            <Text strong>{`Doc #${index + 1} ${doc.title}`}</Text>
            <span>{doc.ref || "chunk"}</span>
            {isFiniteNumber(doc.score) && <Tag color="blue">{`相关度 ${doc.score.toFixed(2)}`}</Tag>}
          </div>
          <p>{doc.text}</p>
        </article>
      ))}
    </div>
  );
}

function renderInspectorPanel(detail: TraceDetailObservation, rows: FlatTraceNode[]) {
  const node = getInsightNode(rows);
  if (!node) {
    return (
      <section className="self-evolution-trace-inspector" aria-label="节点详情">
        <div className="self-evolution-trace-panel-head">
          <Text>节点详情</Text>
        </div>
        <Paragraph className="self-evolution-trace-empty">暂无可展示节点。</Paragraph>
      </section>
    );
  }

  return (
    <section className="self-evolution-trace-inspector" aria-label="节点详情">
      <div className="self-evolution-trace-panel-head">
        <Text>{`节点详情：${node.name}`}</Text>
        <Tag color={getStatusColor(node.status)}>{node.status}</Tag>
      </div>
      <div className="self-evolution-trace-inspector-section">
        <h4>1. 节点信息</h4>
        {renderNodeInfoGrid(node)}
      </div>
      <div className="self-evolution-trace-inspector-section">
        <h4>2. 输入</h4>
        {renderPayloadSummary("input", node.input) || <Paragraph className="self-evolution-trace-empty">暂无输入摘要。</Paragraph>}
      </div>
      <div className="self-evolution-trace-inspector-section">
        <h4>3. 输出</h4>
        {renderPayloadSummary("output", node.output) || <Paragraph className="self-evolution-trace-empty">暂无输出摘要。</Paragraph>}
      </div>
      <div className="self-evolution-trace-inspector-section">
        <h4>4. 召回文档</h4>
        {renderDocList(node)}
      </div>
      <div className="self-evolution-trace-inspector-section">
        <h4>5. Metadata</h4>
        {renderMetadata(node.metadata) || <Paragraph className="self-evolution-trace-empty">暂无 metadata。</Paragraph>}
      </div>
      <div className="self-evolution-trace-inspector-section is-judgement">
        <h4>6. 观测判断</h4>
        <p>{getTraceConclusion(detail, node)}</p>
      </div>
      {renderPayloadDetails("查看节点输入 JSON", node.input)}
      {renderPayloadDetails("查看节点输出 JSON", node.output)}
    </section>
  );
}

function TraceDetailPanel({ detail, label, compact = false }: { detail: TraceDetailObservation; label: string; compact?: boolean }) {
  const rows = flattenTraceNodes(detail.root);
  const typeStats = getTypeStats(rows);

  return (
    <section className={`self-evolution-trace-detail-card${compact ? " is-compact" : ""}`} aria-label={`${label} Trace 详情`}>
      <div className="self-evolution-trace-detail-head">
        <div>
          <Text strong>{label}</Text>
          <span>{detail.traceId}</span>
        </div>
        <Tag color={getStatusColor(detail.status)}>{detail.status}</Tag>
      </div>
      {detail.query && <Paragraph className="self-evolution-trace-query">{detail.query}</Paragraph>}
      <div className="self-evolution-trace-metrics" aria-label={`${label} Trace 摘要指标`}>
        {getMetricItems(detail).map((metric) => (
          <span key={metric.key}>
            <em>{metric.label}</em>
            <strong>{metric.value}</strong>
          </span>
        ))}
      </div>
      <div className="self-evolution-trace-type-strip" aria-label={`${label} 节点类型统计`}>
        {typeStats.map(([type, count]) => (
          <span key={type}>{`${traceTypeLabels[type] || type} ${count}`}</span>
        ))}
      </div>
      {renderFlowPanel(detail, rows, compact)}
    </section>
  );
}

function TraceDetailWorkspace({ detail, title }: { detail: TraceDetailObservation; title: string }) {
  const rows = flattenTraceNodes(detail.root);

  return (
    <section className="self-evolution-trace-observation" aria-label="Agentic RAG 观测详情">
      <div className="self-evolution-trace-observation-head">
        <div>
          <Text strong>{title}</Text>
          <span>{`Agentic RAG 观测详情 · ${getShortTraceId(detail.traceId)}`}</span>
        </div>
      </div>
      {renderMetaTiles(detail, rows)}
      {renderSummaryStrip(detail)}
      <div className="self-evolution-trace-workspace">
        {renderFlowPanel(detail, rows)}
        {renderInspectorPanel(detail, rows)}
      </div>
    </section>
  );
}

function TraceComparePanel({ observation, title }: { observation: Extract<TraceObservation, { kind: "compare" }>; title: string }) {
  const compareRows = [
    {
      key: "latency",
      label: "总耗时",
      a: formatDuration(observation.a.summary.latencyMs),
      b: formatDuration(observation.b.summary.latencyMs),
      delta: formatDurationDelta(observation.a.summary.latencyMs, observation.b.summary.latencyMs),
    },
    {
      key: "tool",
      label: "工具调用",
      a: formatCount(observation.a.summary.toolCallCount),
      b: formatCount(observation.b.summary.toolCallCount),
      delta: formatNumberDelta(observation.a.summary.toolCallCount, observation.b.summary.toolCallCount),
    },
    {
      key: "retrieval",
      label: "检索",
      a: formatCount(observation.a.summary.retrievalCount),
      b: formatCount(observation.b.summary.retrievalCount),
      delta: formatNumberDelta(observation.a.summary.retrievalCount, observation.b.summary.retrievalCount),
    },
    {
      key: "rerank",
      label: "重排",
      a: formatCount(observation.a.summary.rerankCount),
      b: formatCount(observation.b.summary.rerankCount),
      delta: formatNumberDelta(observation.a.summary.rerankCount, observation.b.summary.rerankCount),
    },
    {
      key: "node",
      label: "节点数",
      a: formatCount(observation.a.summary.nodeCount),
      b: formatCount(observation.b.summary.nodeCount),
      delta: formatNumberDelta(observation.a.summary.nodeCount, observation.b.summary.nodeCount),
    },
  ];

  return (
    <section className="self-evolution-trace-observation is-compare" aria-label="A/B 观测 Trace 对照">
      <div className="self-evolution-trace-observation-head">
        <div>
          <Text strong>{title}</Text>
          <span>Case A/B Trace 对比</span>
        </div>
      </div>
      {observation.query && <Paragraph className="self-evolution-trace-query is-main">{observation.query}</Paragraph>}
      <div className="self-evolution-trace-ab-summary">
        <article>
          <Text strong>A 基线算法</Text>
          <span>{`Trace ID ${getShortTraceId(observation.a.traceId)}`}</span>
          <span>{`Latency ${formatDuration(observation.a.summary.latencyMs)}`}</span>
          <Tag color={getStatusColor(observation.a.status)}>{observation.a.status}</Tag>
        </article>
        <article>
          <Text strong>B 优化后算法</Text>
          <span>{`Trace ID ${getShortTraceId(observation.b.traceId)}`}</span>
          <span>{`Latency ${formatDuration(observation.b.summary.latencyMs)}`}</span>
          <Tag color={getStatusColor(observation.b.status)}>{observation.b.status}</Tag>
        </article>
      </div>
      <div className="self-evolution-trace-compare-grid" aria-label="A/B Trace 摘要对照">
        <div className="self-evolution-trace-compare-row is-head">
          <span>指标</span>
          <span>A 基线</span>
          <span>B 候选</span>
          <span>变化</span>
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
        <TraceDetailPanel detail={observation.a} label="A 基线 Trace" compact />
        <TraceDetailPanel detail={observation.b} label="B 候选 Trace" compact />
      </div>
      <div className="self-evolution-trace-ab-docs">
        <div className="self-evolution-trace-panel-head">
          <Text>召回文档对比</Text>
        </div>
        <div className="self-evolution-trace-ab-doc-columns">
          <section>
            <Text strong>A 基线算法</Text>
            {renderDocList(getInsightNode(flattenTraceNodes(observation.a.root)))}
          </section>
          <section>
            <Text strong>B 优化后算法</Text>
            {renderDocList(getInsightNode(flattenTraceNodes(observation.b.root)))}
          </section>
        </div>
      </div>
      <div className="self-evolution-trace-ab-conclusion">
        <Text strong>观测结论：</Text>
        <span>{`B 相比 A 耗时 ${formatDurationDelta(observation.a.summary.latencyMs, observation.b.summary.latencyMs)}，工具调用变化 ${formatNumberDelta(observation.a.summary.toolCallCount, observation.b.summary.toolCallCount)} 次。请结合召回文档与节点详情判断是否需要继续优化检索策略。`}</span>
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
