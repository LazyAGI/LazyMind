import { getNumberField, getStringField, isRecord } from "../../shared";
import type { TraceDetailObservation, TraceNode, TracePayloadPreview, TraceObservation } from "./types";
import {
  countTraceType,
  flattenTraceNodes,
  getArrayField,
  getDisplayText,
  getRecordField,
  isFiniteNumber,
} from "./utils";

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
