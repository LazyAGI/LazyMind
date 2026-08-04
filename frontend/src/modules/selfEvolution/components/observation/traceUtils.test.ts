import { describe, expect, it } from "vitest";
import {
  buildFlowRows,
  findArrayValue,
  findRecordValue,
  flattenTraceNodes,
  formatDeltaPercent,
  formatDeltaScore,
  formatDuration,
  formatOptionalPercent,
  formatPercent,
  getAbMaxScore,
  getAbReturnedDocs,
  getDetailRoundCount,
  getDisplayText,
  getNodeDataRecord,
  getNodeTitle,
  getPrimaryObservation,
  getSearchNode,
  getShortTraceId,
  getStatusColor,
  getTraceDocs,
  getTraceMode,
  isFiniteNumber,
} from "./traceUtils";
import type { TraceNode } from "../trace/types";
import type { TraceDetailObservation, TraceObservation } from "../TraceObservationView";

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

describe("isFiniteNumber / formatDuration", () => {
  it("formats sub-second durations as milliseconds and larger ones as seconds", () => {
    expect(formatDuration(120)).toBe("120ms");
    expect(formatDuration(1500)).toBe("1.50s");
  });

  it("returns a dash for non-finite input", () => {
    expect(formatDuration(undefined)).toBe("-");
  });
});

describe("getShortTraceId", () => {
  it("truncates long trace ids", () => {
    expect(getShortTraceId("abcdefghijklmnopqrstuvwxyz")).toBe("abcdef...uvwxyz");
  });

  it("returns short ids unchanged", () => {
    expect(getShortTraceId("short-id")).toBe("short-id");
  });
});

describe("getStatusColor", () => {
  it("maps known status values to color tokens", () => {
    expect(getStatusColor("SUCCESS")).toBe("success");
    expect(getStatusColor("failed")).toBe("error");
    expect(getStatusColor("weird")).toBe("default");
  });
});

describe("getDisplayText", () => {
  it("truncates long strings and returns a dash for empty ones", () => {
    expect(getDisplayText("a".repeat(200)).endsWith("...")).toBe(true);
    expect(getDisplayText("   ")).toBe("-");
  });

  it("describes arrays and records", () => {
    expect(getDisplayText([1, 2, 3])).toBe("3 items");
    expect(getDisplayText({ a: 1, b: 2 })).toBe("a / b");
  });
});

describe("flattenTraceNodes", () => {
  it("walks nested children depth-first", () => {
    const root = makeNode({ id: "root", children: [makeNode({ id: "child" })] });
    const rows = flattenTraceNodes(root);
    expect(rows.map((row) => row.node.id)).toEqual(["root", "child"]);
    expect(rows[1].depth).toBe(1);
  });
});

describe("findArrayValue", () => {
  it("finds an array under a matching key without recursing when allowDirectArray is true", () => {
    expect(findArrayValue([1, 2], ["items"])).toEqual([1, 2]);
    expect(findArrayValue({ items: [1, 2] }, ["items"])).toEqual([1, 2]);
  });

  it("recurses into nested records to find a matching array", () => {
    expect(findArrayValue({ data: { docs: [1] } }, ["docs"])).toEqual([1]);
  });
});

describe("getTraceDocs", () => {
  it("extracts up to three docs from a node's output data", () => {
    const node = makeNode({
      output: { data: { items: [{ file_name: "a.txt", score: 0.9 }, { file_name: "b.txt" }, { file_name: "c.txt" }, { file_name: "d.txt" }] } },
    });
    const docs = getTraceDocs(node);
    expect(docs).toHaveLength(3);
    expect(docs[0]).toMatchObject({ title: "a.txt", score: 0.9 });
  });

  it("returns an empty array when there is no matching output data", () => {
    expect(getTraceDocs(undefined)).toEqual([]);
  });
});

describe("getNodeDataRecord / findRecordValue", () => {
  it("returns the data record when it's a plain object", () => {
    expect(getNodeDataRecord({ data: { a: 1 } })).toEqual({ a: 1 });
    expect(getNodeDataRecord({ data: [1, 2] })).toBeUndefined();
  });

  it("finds a value by key recursively", () => {
    expect(findRecordValue({ a: { b: { c: 5 } } }, ["c"])).toBe(5);
    expect(findRecordValue({ a: 1 }, ["missing"])).toBeUndefined();
  });
});

describe("getNodeTitle", () => {
  it("prefixes tool nodes and renames llm nodes", () => {
    expect(getNodeTitle(makeNode({ type: "tool", name: "kb_search" }))).toBe("Tool Call: kb_search");
    expect(getNodeTitle(makeNode({ type: "llm" }))).toBe("LLM Generate");
    expect(getNodeTitle(makeNode({ type: "module", name: "custom" }))).toBe("custom");
  });
});

describe("buildFlowRows", () => {
  it("builds one row per round using the round's leaf/interesting descendants", () => {
    const child1 = makeNode({ id: "c1", type: "llm" });
    const child2 = makeNode({ id: "c2", type: "tool" });
    const round = makeNode({ id: "round1", children: [child1, child2] });
    const detail = makeDetail({ root: makeNode({ children: [round] }) });
    const rows = buildFlowRows(t, detail);
    expect(rows.length).toBeGreaterThan(0);
    expect(rows.every((row) => row.round === 1)).toBe(true);
  });
});

describe("formatPercent / formatOptionalPercent / formatDeltaScore / formatDeltaPercent", () => {
  it("formats ratios and deltas", () => {
    expect(formatPercent(0.856)).toBe("85.6%");
    expect(formatOptionalPercent(undefined)).toBe("-");
    expect(formatDeltaScore(0.25)).toBe("+0.25");
    expect(formatDeltaScore(-0.1)).toBe("-0.10");
    expect(formatDeltaPercent(0.5, 0.6)).toBe("+10%");
  });
});

describe("getDetailRoundCount", () => {
  it("uses the larger of root children count and summary round count", () => {
    const detail = makeDetail({
      root: makeNode({ children: [makeNode(), makeNode()] }),
      summary: { status: "success", nodeCount: 3, roundCount: 1 },
    });
    expect(getDetailRoundCount(detail)).toBe(2);
  });
});

describe("getTraceMode", () => {
  it("detects agentic rag mode when a tool or retriever node is present", () => {
    const detail = makeDetail({ root: makeNode({ children: [makeNode({ type: "tool" })] }) });
    expect(getTraceMode(detail)).toBe("Agentic RAG");
  });

  it("falls back to plain RAG mode otherwise", () => {
    expect(getTraceMode(makeDetail())).toBe("RAG");
  });
});

describe("getSearchNode", () => {
  it("prefers a node named with kb_search", () => {
    const searchNode = makeNode({ id: "search", name: "kb_search_tool" });
    const detail = makeDetail({ root: makeNode({ children: [searchNode] }) });
    expect(getSearchNode(detail)?.id).toBe("search");
  });

  it("falls back to the first row when nothing matches", () => {
    const detail = makeDetail();
    expect(getSearchNode(detail)?.id).toBe(detail.root.id);
  });
});

describe("getAbReturnedDocs / getAbMaxScore", () => {
  it("counts returned docs from the doc list or fallback total field", () => {
    const node = makeNode({ output: { data: { items: [{ file_name: "a" }] } } });
    expect(getAbReturnedDocs(node)).toBe(1);
    const emptyNode = makeNode({ output: { data: { total: 4 } } });
    expect(getAbReturnedDocs(emptyNode)).toBe(4);
  });

  it("returns the top doc score or a fallback max_score field", () => {
    const node = makeNode({ output: { data: { items: [{ file_name: "a", score: 0.7 }] } } });
    expect(getAbMaxScore(node)).toBe(0.7);
    expect(getAbMaxScore(undefined)).toBeUndefined();
  });
});

describe("getPrimaryObservation", () => {
  it("returns the detail for a 'detail' kind observation", () => {
    const detail = makeDetail();
    const observation: TraceObservation = { kind: "detail", detail };
    expect(getPrimaryObservation(observation)).toBe(detail);
  });

  it("returns side a for a 'compare' kind observation, and undefined for no observation", () => {
    const a = makeDetail({ traceId: "a" });
    const b = makeDetail({ traceId: "b" });
    const observation: TraceObservation = { kind: "compare", query: "q", a, b };
    expect(getPrimaryObservation(observation)).toBe(a);
    expect(getPrimaryObservation(undefined)).toBeUndefined();
  });
});
