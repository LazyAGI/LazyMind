import { describe, expect, it } from "vitest";
import {
  countTraceType,
  extractDocsFromNode,
  findArrayValue,
  findRecordValue,
  flattenTraceNodes,
  formatCount,
  formatDuration,
  formatDurationDelta,
  formatNumberDelta,
  formatTraceStatusLabel,
  getArrayField,
  getDisplayText,
  getInsightNode,
  getMetricItems,
  getModeLabel,
  getNodeDataRecord,
  getRecordField,
  getShortTraceId,
  getStatusColor,
  getTraceConclusion,
  getTraceTypeLabels,
  getTypeStats,
  isFiniteNumber,
} from "./utils";
import type { FlatTraceNode, TraceDetailObservation, TraceNode } from "./types";

const t = (key: string) => key;

function makeNode(overrides: Partial<TraceNode> = {}): TraceNode {
  return {
    id: "n1",
    name: "node",
    type: "module",
    status: "success",
    children: [],
    ...overrides,
  };
}

function makeDetail(overrides: Partial<TraceDetailObservation> = {}): TraceDetailObservation {
  return {
    traceId: "t1",
    query: "q",
    status: "success",
    summary: { status: "success", nodeCount: 1 },
    root: makeNode(),
    ...overrides,
  };
}

describe("getTraceTypeLabels", () => {
  it("returns fixed English labels alongside translated ones", () => {
    const labels = getTraceTypeLabels(t);
    expect(labels.flow).toBe("Flow");
    expect(labels.retriever).toBe("selfEvolutionRun.trace.retriever");
  });
});

describe("isFiniteNumber / formatDuration", () => {
  it("formats sub-second durations as milliseconds and larger ones as seconds", () => {
    expect(formatDuration(120)).toBe("120ms");
    expect(formatDuration(1500)).toBe("1.50s");
  });

  it("returns a dash for non-finite input", () => {
    expect(formatDuration(undefined)).toBe("-");
  });
});

describe("getRecordField / getArrayField", () => {
  it("returns the first matching record/array field", () => {
    expect(getRecordField({ meta: { a: 1 } }, ["metadata", "meta"])).toEqual({ a: 1 });
    expect(getArrayField({ items: [1, 2] }, ["docs", "items"])).toEqual([1, 2]);
  });

  it("returns undefined/empty when payload is missing or no key matches", () => {
    expect(getRecordField(undefined, ["meta"])).toBeUndefined();
    expect(getArrayField({ a: 1 }, ["items"])).toEqual([]);
  });
});

describe("getDisplayText", () => {
  it("truncates long strings and stringifies primitives", () => {
    expect(getDisplayText("a".repeat(200))?.endsWith("...")).toBe(true);
    expect(getDisplayText(42)).toBe("42");
    expect(getDisplayText("   ")).toBeUndefined();
  });

  it("summarizes arrays and records", () => {
    expect(getDisplayText([1, 2])).toBe("2 items");
    expect(getDisplayText({})).toBe("0 fields");
  });
});

describe("flattenTraceNodes / countTraceType", () => {
  it("flattens nested nodes and counts by type", () => {
    const root = makeNode({ children: [makeNode({ type: "tool" }), makeNode({ type: "tool" })] });
    const rows = flattenTraceNodes(root);
    expect(rows).toHaveLength(3);
    expect(countTraceType(rows, "tool")).toBe(2);
  });
});

describe("formatCount / formatNumberDelta / formatDurationDelta", () => {
  it("formats counts and numeric deltas", () => {
    expect(formatCount(5)).toBe("5");
    expect(formatCount(undefined)).toBe("-");
    expect(formatNumberDelta(1, 3)).toBe("+2");
    expect(formatNumberDelta(undefined, 3)).toBe("-");
  });

  it("formats duration deltas with a sign", () => {
    expect(formatDurationDelta(100, 300)).toBe("+200ms");
    expect(formatDurationDelta(300, 100)).toBe("-200ms");
  });
});

describe("getStatusColor / formatTraceStatusLabel", () => {
  it("maps status strings to color tokens", () => {
    expect(getStatusColor("SUCCESS")).toBe("success");
    expect(getStatusColor("weird")).toBe("default");
  });

  it("translates a status label falling back to the normalized status", () => {
    expect(formatTraceStatusLabel((key, opts) => (opts as { defaultValue?: string })?.defaultValue || key, "Running")).toBe("running");
  });
});

describe("getMetricItems", () => {
  it("builds the fixed metric item list from a trace detail summary", () => {
    const detail = makeDetail({ summary: { status: "success", nodeCount: 4, roundCount: 2, toolCallCount: 1 } });
    const items = getMetricItems(t, detail);
    expect(items.find((item) => item.key === "node")?.value).toBe("4");
    expect(items.find((item) => item.key === "round")?.value).toBe("2");
  });
});

describe("getShortTraceId", () => {
  it("truncates long trace ids and keeps short ones unchanged", () => {
    expect(getShortTraceId("abcdefghijklmnopqrstuvwxyz")).toBe("abcdef...uvwxyz");
    expect(getShortTraceId("short")).toBe("short");
  });
});

describe("getModeLabel", () => {
  it("detects agentic_rag when a tool or retriever node exists", () => {
    const rows: FlatTraceNode[] = [{ node: makeNode({ type: "tool" }), depth: 0 }];
    expect(getModeLabel(rows)).toBe("agentic_rag");
    expect(getModeLabel([{ node: makeNode(), depth: 0 }])).toBe("rag");
  });
});

describe("getNodeDataRecord / findRecordValue / findArrayValue", () => {
  it("extracts the data record and finds nested values", () => {
    expect(getNodeDataRecord({ data: { a: 1 } })).toEqual({ a: 1 });
    expect(findRecordValue({ a: { b: 2 } }, "b")).toBe(2);
    expect(findArrayValue({ a: { docs: [1] } }, ["docs"])).toEqual([1]);
  });
});

describe("extractDocsFromNode", () => {
  it("extracts up to four docs from a node's output", () => {
    const node = makeNode({ output: { data: { items: [{ file_name: "a" }, { file_name: "b" }] } } });
    const docs = extractDocsFromNode(node);
    expect(docs).toHaveLength(2);
    expect(docs[0].title).toBe("a");
  });

  it("returns an empty array when there is no node", () => {
    expect(extractDocsFromNode(undefined)).toEqual([]);
  });
});

describe("getInsightNode", () => {
  it("prefers a failed/error/warning node over others", () => {
    const failedNode = makeNode({ id: "bad", status: "failed" });
    const rows: FlatTraceNode[] = [{ node: makeNode(), depth: 0 }, { node: failedNode, depth: 0 }];
    expect(getInsightNode(rows)?.id).toBe("bad");
  });

  it("falls back to the first row when nothing else matches", () => {
    const rows: FlatTraceNode[] = [{ node: makeNode({ id: "first" }), depth: 0 }];
    expect(getInsightNode(rows)?.id).toBe("first");
  });
});

describe("getTraceConclusion", () => {
  it("reports a failed conclusion for failed status", () => {
    const detail = makeDetail({ status: "failed" });
    expect(getTraceConclusion(t, detail)).toBe("selfEvolutionRun.trace.conclusionFailed");
  });

  it("reports a default conclusion when nothing special is detected", () => {
    expect(getTraceConclusion(t, makeDetail())).toBe("selfEvolutionRun.trace.conclusionDefault");
  });
});

describe("getTypeStats", () => {
  it("counts nodes by type sorted descending", () => {
    const rows: FlatTraceNode[] = [
      { node: makeNode({ type: "tool" }), depth: 0 },
      { node: makeNode({ type: "tool" }), depth: 0 },
      { node: makeNode({ type: "llm" }), depth: 0 },
    ];
    expect(getTypeStats(rows)).toEqual([["tool", 2], ["llm", 1]]);
  });
});
