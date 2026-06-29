import { getNumberField, getStringField, isRecord } from "../../shared";
import type { TraceDetailObservation, TraceObservation } from "../TraceObservationView";
import type { FlowRow, TFunction, TraceDocRow, TraceNode } from "./types";

export function isFiniteNumber(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
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

export function getShortTraceId(traceId: string) {
  return traceId.length > 14 ? `${traceId.slice(0, 6)}...${traceId.slice(-6)}` : traceId;
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

export function getDisplayText(value: unknown, maxLength = 120): string {
  if (typeof value === "string") {
    const trimmed = value.trim();
    if (!trimmed) {
      return "-";
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
    return keys.length ? `${keys.slice(0, 4).join(" / ")}${keys.length > 4 ? "..." : ""}` : "0 fields";
  }
  return "-";
}

export function flattenTraceNodes(root: TraceNode) {
  const rows: Array<{ node: TraceNode; depth: number }> = [];
  const walk = (node: TraceNode, depth: number) => {
    rows.push({ node, depth });
    node.children.forEach((child) => walk(child, depth + 1));
  };
  walk(root, 0);
  return rows;
}

export function findArrayValue(value: unknown, keys: string[], depth = 0, allowDirectArray = true): unknown[] {
  if (depth > 5) {
    return [];
  }
  if (Array.isArray(value)) {
    if (allowDirectArray) {
      return value;
    }
    for (const item of value) {
      const result = findArrayValue(item, keys, depth + 1, false);
      if (result.length > 0) {
        return result;
      }
    }
    return [];
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
    const result = findArrayValue(nested, keys, depth + 1, false);
    if (result.length > 0) {
      return result;
    }
  }
  return [];
}

export function getTraceDocs(node?: TraceNode): TraceDocRow[] {
  const docs = findArrayValue(node?.output?.data, ["items", "docs", "documents", "nodes"]);
  return docs.filter(isRecord).slice(0, 3).map((item, index) => {
    const title =
      getStringField(item, ["file_name", "display_name", "docid", "document_id", "title"]) ||
      `Doc #${index + 1}`;
    const score = getNumberField(item, ["score", "similarity", "max_score"]);
    return {
      key: getStringField(item, ["docid", "id", "chunk_id"]) || `${title}-${index}`,
      title,
      ref: getStringField(item, ["ref", "citation_index", "chunk_id"]) || `chunk-${index + 1}`,
      score,
      text: getStringField(item, ["text", "content", "summary"]) || getDisplayText(item, 180),
    };
  });
}

export function getNodeDataRecord(payload?: TraceNode["input"]) {
  return isRecord(payload?.data) ? payload.data : undefined;
}

export function findRecordValue(value: unknown, keys: string[], depth = 0): unknown {
  if (depth > 5) {
    return undefined;
  }
  if (Array.isArray(value)) {
    for (const item of value) {
      const result = findRecordValue(item, keys, depth + 1);
      if (result !== undefined) {
        return result;
      }
    }
    return undefined;
  }
  if (!isRecord(value)) {
    return undefined;
  }
  for (const key of keys) {
    if (value[key] !== undefined) {
      return value[key];
    }
  }
  for (const nested of Object.values(value)) {
    const result = findRecordValue(nested, keys, depth + 1);
    if (result !== undefined) {
      return result;
    }
  }
  return undefined;
}

export function getNodeTitle(node: TraceNode) {
  if (node.type === "tool") {
    return `Tool Call: ${node.name}`;
  }
  if (node.type === "llm") {
    return "LLM Generate";
  }
  return node.name;
}

export function buildFlowRows(t: TFunction, detail: TraceDetailObservation): FlowRow[] {
  const rootChildren = detail.root.children.length ? detail.root.children : [detail.root];
  return rootChildren.flatMap((roundNode, roundIndex) => {
    const descendants = flattenTraceNodes(roundNode)
      .filter(({ node }) => !["_build_history", "Pipeline", "FunctionCall"].includes(node.name))
      .filter(({ node }) => node.type === "llm" || node.type === "tool" || node.type === "retriever" || node.type === "rerank" || node.children.length === 0)
      .slice(0, 5);
    const rows = descendants.length ? descendants : [{ node: roundNode, depth: 0 }];
    return rows.map(({ node }, index) => ({
      key: `${roundIndex}-${node.id}-${index}`,
      round: roundIndex + 1,
      title: getNodeTitle(node),
      desc: node.output?.summary || node.input?.summary || t("selfEvolutionRun.observation.noSummary"),
      duration: formatDuration(node.latencyMs),
      tone: getTraceDocs(node).length > 0 || node.status === "warning" ? "warning" : node.status === "success" ? "success" : "normal",
      node,
    }));
  });
}

export function formatPercent(value: number) {
  return `${(value * 100).toFixed(1)}%`;
}

export function formatOptionalPercent(value?: number) {
  if (!isFiniteNumber(value)) {
    return "-";
  }
  return formatPercent(value);
}

export function formatDeltaScore(value: number) {
  return `${value > 0 ? "+" : ""}${value.toFixed(2)}`;
}

export function formatDeltaPercent(a: number, b: number) {
  const delta = b - a;
  return `${delta > 0 ? "+" : ""}${Math.round(delta * 100)}%`;
}

export function getDetailRoundCount(detail: TraceDetailObservation) {
  return Math.max(detail.root.children.length, detail.summary.roundCount || 0);
}

export function getTraceMode(detail: TraceDetailObservation) {
  const rows = flattenTraceNodes(detail.root);
  return rows.some(({ node }) => node.type === "tool" || node.type === "retriever") ? "Agentic RAG" : "RAG";
}

export function getSearchNode(detail: TraceDetailObservation) {
  const rows = flattenTraceNodes(detail.root);
  return (
    rows.find(({ node }) => node.name.includes("kb_search"))?.node ||
    rows.find(({ node }) => getTraceDocs(node).length > 0)?.node ||
    rows.find(({ node }) => node.type === "retriever")?.node ||
    rows.find(({ node }) => node.type === "tool")?.node ||
    rows[0]?.node
  );
}

export function getAbReturnedDocs(node?: TraceNode) {
  const docs = getTraceDocs(node);
  const outputData = getNodeDataRecord(node?.output);
  return docs.length || Number(findRecordValue(outputData, ["total", "returned_docs", "node_count"])) || 0;
}

export function getAbMaxScore(node?: TraceNode) {
  const docs = getTraceDocs(node);
  const outputData = getNodeDataRecord(node?.output);
  const score = docs[0]?.score ?? getNumberField(outputData, ["max_score", "score"]);
  return isFiniteNumber(score) ? score : undefined;
}

export function getPrimaryObservation(observation?: TraceObservation) {
  if (!observation) {
    return undefined;
  }
  return observation.kind === "detail" ? observation.detail : observation.a;
}
