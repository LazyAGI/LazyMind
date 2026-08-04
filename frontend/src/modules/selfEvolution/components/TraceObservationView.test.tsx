import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { TraceObservationView } from "./TraceObservationView";
import type { TraceDetailObservation, TraceObservation } from "./trace/types";

vi.mock("./trace/TraceComparePanel", () => ({
  TraceComparePanel: ({ title }: { title: string }) => <div>compare-panel:{title}</div>,
}));
vi.mock("./trace/TraceDetailPanel", () => ({
  TraceDetailWorkspace: ({ title }: { title: string }) => <div>detail-workspace:{title}</div>,
}));

function makeDetail(): TraceDetailObservation {
  return {
    traceId: "trace-1",
    query: "q",
    status: "success",
    summary: { status: "success", nodeCount: 1 },
    root: { id: "root", name: "root", type: "flow", status: "success", children: [] },
  };
}

describe("TraceObservationView", () => {
  it("renders the compare panel for a compare observation", () => {
    const observation: TraceObservation = { kind: "compare", query: "q", a: makeDetail(), b: makeDetail() };
    render(<TraceObservationView observation={observation} title="Compare" />);
    expect(screen.getByText("compare-panel:Compare")).toBeInTheDocument();
  });

  it("renders the detail workspace for a detail observation", () => {
    const observation: TraceObservation = { kind: "detail", detail: makeDetail() };
    render(<TraceObservationView observation={observation} title="Detail" />);
    expect(screen.getByText("detail-workspace:Detail")).toBeInTheDocument();
  });
});
