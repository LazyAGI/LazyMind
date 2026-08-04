import { describe, expect, it } from "vitest";
import { normalizeTraceObservation } from "./normalize";

describe("normalizeTraceObservation", () => {
  it("normalizes a single trace record into a 'detail' observation", () => {
    const observation = normalizeTraceObservation({
      trace_id: "t1",
      query: "hello",
      status: "success",
      root: { name: "root", type: "flow", status: "success", children: [] },
    });
    expect(observation?.kind).toBe("detail");
    expect(observation?.kind === "detail" && observation.detail.traceId).toBe("t1");
  });

  it("normalizes a/b records into a 'compare' observation", () => {
    const makeTrace = (id: string) => ({
      trace_id: id,
      status: "success",
      root: { name: "root", type: "flow", status: "success", children: [] },
    });
    const observation = normalizeTraceObservation({ a: makeTrace("a1"), b: makeTrace("b1") });
    expect(observation?.kind).toBe("compare");
    expect(observation?.kind === "compare" && observation.a.traceId).toBe("a1");
    expect(observation?.kind === "compare" && observation.b.traceId).toBe("b1");
  });

  it("recurses into nested observation keys like data/result/payload", () => {
    const observation = normalizeTraceObservation({
      data: {
        trace_id: "nested-1",
        root: { name: "root", type: "flow", status: "success", children: [] },
      },
    });
    expect(observation?.kind).toBe("detail");
    expect(observation?.kind === "detail" && observation.detail.traceId).toBe("nested-1");
  });

  it("returns undefined for empty, non-record, or overly deep input", () => {
    expect(normalizeTraceObservation(undefined)).toBeUndefined();
    expect(normalizeTraceObservation("not-a-record")).toBeUndefined();
    expect(normalizeTraceObservation({})).toBeUndefined();
  });

  it("picks the first valid observation when given an array", () => {
    const observation = normalizeTraceObservation([
      { not: "valid" },
      { trace_id: "t2", root: { name: "root", type: "flow", status: "success", children: [] } },
    ]);
    expect(observation?.kind === "detail" && observation.detail.traceId).toBe("t2");
  });

  it("computes latency, tool/retrieval/rerank counts, and node count from the tree", () => {
    const observation = normalizeTraceObservation({
      trace_id: "t3",
      root: {
        name: "root",
        type: "flow",
        status: "success",
        latency_ms: 500,
        children: [
          { name: "search", type: "retriever", status: "success", children: [] },
          { name: "call", type: "tool", status: "success", children: [] },
        ],
      },
    });
    expect(observation?.kind === "detail" && observation.detail.summary.nodeCount).toBe(3);
    expect(observation?.kind === "detail" && observation.detail.summary.retrievalCount).toBe(1);
    expect(observation?.kind === "detail" && observation.detail.summary.toolCallCount).toBe(1);
  });
});
