import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import {
  renderDocList,
  renderFlowPanel,
  renderInspectorPanel,
  renderMetadata,
  renderMetaTiles,
  renderPayloadDetails,
  renderPayloadSummary,
  renderSummaryStrip,
} from "./renderHelpers";
import type { TraceDetailObservation, TraceNode } from "./types";

const t = (key: string, options?: Record<string, unknown>) =>
  `${key}${options ? `:${JSON.stringify(options)}` : ""}`;

function makeNode(overrides: Partial<TraceNode> = {}): TraceNode {
  return {
    id: "node-1",
    name: "Retriever",
    type: "retriever",
    status: "success",
    children: [],
    ...overrides,
  };
}

function makeDetail(overrides: Partial<TraceDetailObservation> = {}): TraceDetailObservation {
  return {
    traceId: "trace-abc-1234567890",
    query: "what is lazyllm",
    status: "success",
    summary: {
      status: "success",
      latencyMs: 1234,
      roundCount: 2,
      toolCallCount: 1,
      retrievalCount: 1,
      rerankCount: 0,
      nodeCount: 1,
    },
    root: makeNode(),
    ...overrides,
  };
}

describe("renderMetaTiles", () => {
  it("renders the trace id, status and latency tiles", () => {
    const detail = makeDetail();
    const { container } = render(<>{renderMetaTiles(t, detail, [{ node: detail.root, depth: 0 }])}</>);
    expect(container.querySelectorAll(".self-evolution-trace-meta-card")).toHaveLength(5);
    expect(container.textContent).toContain("1.23s");
  });
});

describe("renderSummaryStrip", () => {
  it("renders round/tool/retrieval/rerank counts", () => {
    const detail = makeDetail();
    const { container } = render(<>{renderSummaryStrip(t, detail)}</>);
    expect(container.textContent).toContain("2");
    expect(container.textContent).toContain("1");
    expect(container.textContent).toContain("success");
  });
});

describe("renderPayloadSummary", () => {
  it("returns null when there is no summary or kind", () => {
    expect(renderPayloadSummary("input", undefined)).toBeNull();
    expect(renderPayloadSummary("input", {})).toBeNull();
  });

  it("renders the kind and summary text when present", () => {
    const { container } = render(
      <>{renderPayloadSummary("input", { kind: "text", summary: "hello world" })}</>,
    );
    expect(container.textContent).toContain("text");
    expect(container.textContent).toContain("hello world");
  });
});

describe("renderPayloadDetails", () => {
  it("returns null when payload has no data", () => {
    expect(renderPayloadDetails("label", undefined)).toBeNull();
    expect(renderPayloadDetails("label", { summary: "x" })).toBeNull();
  });

  it("renders a details/summary block with stringified data", () => {
    const { container } = render(
      <>{renderPayloadDetails("Raw JSON", { data: { foo: "bar" } })}</>,
    );
    expect(container.querySelector("summary")?.textContent).toBe("Raw JSON");
    expect(container.querySelector("pre")?.textContent).toContain("foo");
  });
});

describe("renderMetadata", () => {
  it("returns null for empty or missing metadata", () => {
    expect(renderMetadata(t, undefined)).toBeNull();
    expect(renderMetadata(t, {})).toBeNull();
  });

  it("renders up to 8 metadata entries", () => {
    const metadata = Object.fromEntries(
      Array.from({ length: 10 }, (_, i) => [`key${i}`, `value${i}`]),
    );
    const { container } = render(<>{renderMetadata(t, metadata)}</>);
    expect(container.querySelectorAll(".self-evolution-trace-node-metadata span")).toHaveLength(8);
  });
});

describe("renderFlowPanel", () => {
  it("renders one row per flattened node", () => {
    const detail = makeDetail();
    const rows = [
      { node: makeNode({ id: "a" }), depth: 0 },
      { node: makeNode({ id: "b", type: "tool" }), depth: 1 },
    ];
    const { container } = render(<>{renderFlowPanel(t, detail, rows)}</>);
    expect(container.querySelectorAll(".self-evolution-trace-node")).toHaveLength(2);
  });

  it("limits compact mode to shallow nodes and at most 12 rows", () => {
    const detail = makeDetail();
    const rows = Array.from({ length: 20 }, (_, i) => ({
      node: makeNode({ id: `n${i}` }),
      depth: i % 5,
    }));
    const { container } = render(<>{renderFlowPanel(t, detail, rows, true)}</>);
    const rendered = container.querySelectorAll(".self-evolution-trace-node");
    expect(rendered.length).toBeLessThanOrEqual(12);
  });
});

describe("renderDocList", () => {
  it("renders an empty state when the node has no docs", () => {
    const { container } = render(<>{renderDocList(t, makeNode())}</>);
    expect(container.textContent).toContain("selfEvolutionRun.trace.noRetrievedDocs");
  });

  it("renders retrieved docs with score tags", () => {
    const node = makeNode({
      output: {
        data: {
          items: [{ id: "d1", title: "Doc A", text: "content", score: 0.87 }],
        },
      },
    });
    const { container } = render(<>{renderDocList(t, node)}</>);
    expect(container.textContent).toContain("Doc A");
    expect(container.textContent).toContain("0.87");
  });
});

describe("renderInspectorPanel", () => {
  it("renders the no-nodes state when rows are empty", () => {
    const detail = makeDetail();
    const { container } = render(<>{renderInspectorPanel(t, detail, [])}</>);
    expect(container.textContent).toContain("selfEvolutionRun.trace.noNodes");
  });

  it("renders the selected insight node's details", () => {
    const detail = makeDetail();
    const rows = [{ node: makeNode({ name: "Retriever" }), depth: 0 }];
    const { container } = render(<>{renderInspectorPanel(t, detail, rows)}</>);
    expect(container.textContent).toContain("Retriever");
  });
});
