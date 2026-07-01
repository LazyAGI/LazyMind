import { getNumberField, getStringField, isRecord } from "../../shared";
import type { FlatTraceNode, MetricItem, TFunction, TraceDocPreview, TraceNode, TracePayloadPreview, TraceDetailObservation } from "./types";

export function getTraceTypeLabels(t: TFunction): Record<string, string> {
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

export function isFiniteNumber(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

export function getRecordField(payload: Record<string, unknown> | undefined, keys: string[]) {
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

export function getArrayField(payload: Record<string, unknown> | undefined, keys: string[]) {
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

export function getDisplayText(value: unknown, maxLength = 160): string | undefined {
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

export function flattenTraceNodes(root: TraceNode) {
  const rows: FlatTraceNode[] = [];
  const walk = (node: TraceNode, depth: number) => {
    rows.push({ node, depth });
    node.children.forEach((child) => walk(child, depth + 1));
  };
  walk(root, 0);
  return rows;
}

export function countTraceType(rows: FlatTraceNode[], type: string) {
  return rows.filter(({ node }) => node.type === type).length;
}

export function formatDuration(ms?: number) {
  if (!isFiniteNumber(ms)) {
    return "-";
  }
  if (ms >= 1000) {
    return `${(ms / 1000).toFixed(ms >= 10_000 ? 1 : 2)}s`;
  }
  return `${ms.toFixed(ms < 10 ? 1 : 0)}ms`;
}

export function formatCount(value?: number) {
  return isFiniteNumber(value) ? String(Math.round(value)) : "-";
}

export function formatNumberDelta(a?: number, b?: number) {
  if (!isFiniteNumber(a) || !isFiniteNumber(b)) {
    return "-";
  }
  const delta = b - a;
  return `${delta > 0 ? "+" : ""}${Number.isInteger(delta) ? delta : delta.toFixed(1)}`;
}

export function formatDurationDelta(a?: number, b?: number) {
  if (!isFiniteNumber(a) || !isFiniteNumber(b)) {
    return "-";
  }
  const delta = b - a;
  return `${delta > 0 ? "+" : delta < 0 ? "-" : ""}${formatDuration(Math.abs(delta))}`;
}

export function getStatusColor(status: string) {
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

export function getMetricItems(t: TFunction, detail: TraceDetailObservation): MetricItem[] {
  return [
    { key: "latency", label: t("selfEvolutionRun.trace.totalLatency"), value: formatDuration(detail.summary.latencyMs) },
    { key: "round", label: t("selfEvolutionRun.trace.roundCount"), value: formatCount(detail.summary.roundCount) },
    { key: "tool", label: t("selfEvolutionRun.trace.toolCallCount"), value: formatCount(detail.summary.toolCallCount) },
    { key: "retrieval", label: t("selfEvolutionRun.trace.retrievalCount"), value: formatCount(detail.summary.retrievalCount) },
    { key: "rerank", label: t("selfEvolutionRun.trace.rerankCount"), value: formatCount(detail.summary.rerankCount) },
    { key: "node", label: t("selfEvolutionRun.trace.nodeCount"), value: formatCount(detail.summary.nodeCount) },
  ];
}

export function getShortTraceId(traceId: string) {
  if (traceId.length <= 14) {
    return traceId;
  }
  return `${traceId.slice(0, 6)}...${traceId.slice(-6)}`;
}

export function getModeLabel(rows: FlatTraceNode[]) {
  return rows.some(({ node }) => node.type === "tool" || node.type === "retriever")
    ? "agentic_rag"
    : "rag";
}

export function getNodeDataRecord(payload?: TracePayloadPreview) {
  return isRecord(payload?.data) ? payload.data : undefined;
}

export function findRecordValue(value: unknown, key: string, depth = 0): unknown {
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

export function findArrayValue(value: unknown, keys: string[], depth = 0): unknown[] {
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

export function extractDocsFromNode(node?: TraceNode): TraceDocPreview[] {
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

export function getInsightNode(rows: FlatTraceNode[]) {
  return (
    rows.find(({ node }) => ["failed", "error", "warning"].includes(node.status.toLowerCase()))?.node ||
    rows.find(({ node }) => extractDocsFromNode(node).length > 0)?.node ||
    rows.find(({ node }) => node.type === "tool")?.node ||
    rows.find(({ node }) => node.type === "retriever")?.node ||
    rows.find(({ node }) => node.type === "llm")?.node ||
    rows[0]?.node
  );
}

export function getTraceConclusion(t: TFunction, detail: TraceDetailObservation, insightNode?: TraceNode) {
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

export function getTypeStats(rows: FlatTraceNode[]) {
  const counts = rows.reduce<Record<string, number>>((acc, { node }) => {
    acc[node.type] = (acc[node.type] || 0) + 1;
    return acc;
  }, {});
  return Object.entries(counts).sort((a, b) => b[1] - a[1]);
}
