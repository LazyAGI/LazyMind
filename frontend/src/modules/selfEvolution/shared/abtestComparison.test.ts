import { describe, expect, it } from "vitest";
import {
  buildAbCaseDetailItemFromComparisonCase,
  buildAbCaseTraceMapFromComparisonArtifact,
  buildAbSummaryFromComparisonArtifact,
  getAbtestVerdictTagColor,
  parseAbtestComparisonArtifact,
} from "./abtestComparison";

describe("parseAbtestComparisonArtifact", () => {
  it("parses a direct origin/candidate record into metric and case rows", () => {
    const artifact = parseAbtestComparisonArtifact({
      run_id: "run-1",
      algo_id: "algo-a",
      candidate_algo_id: "algo-b",
      status: "done",
      verdict: "pass",
      reasons: ["better"],
      origin: { avg_correctness: 0.5, cases: [{ case_id: "c1", overall: 0.6, correctness: 0.5, trace_id: "t-a" }] },
      candidate: { avg_correctness: 0.7, cases: [{ case_id: "c1", overall: 0.8, correctness: 0.7, trace_id: "t-b" }] },
    });
    expect(artifact?.runId).toBe("run-1");
    expect(artifact?.metricRows.find((row) => row.key === "avg_correctness")?.delta).toBeCloseTo(0.2);
    expect(artifact?.caseRows).toHaveLength(1);
    expect(artifact?.caseRows[0].deltaOverall).toBeCloseTo(0.2);
  });

  it("unwraps a nested comparison payload", () => {
    const artifact = parseAbtestComparisonArtifact({
      data: { origin: { avg_correctness: 0.4 }, candidate: { avg_correctness: 0.6 } },
    });
    expect(artifact).toBeDefined();
    expect(artifact?.metricRows.find((row) => row.key === "avg_correctness")?.origin).toBe(0.4);
  });

  it("returns undefined when origin/candidate can't be located", () => {
    expect(parseAbtestComparisonArtifact({ foo: "bar" })).toBeUndefined();
    expect(parseAbtestComparisonArtifact(null)).toBeUndefined();
  });
});

describe("getAbtestVerdictTagColor", () => {
  it("maps pass-like verdicts to success", () => {
    expect(getAbtestVerdictTagColor("accepted")).toBe("success");
  });

  it("maps fail-like verdicts to error", () => {
    expect(getAbtestVerdictTagColor("rejected")).toBe("error");
  });

  it("maps skipped to default and unknowns to warning", () => {
    expect(getAbtestVerdictTagColor("skipped")).toBe("default");
    expect(getAbtestVerdictTagColor("inconclusive")).toBe("warning");
    expect(getAbtestVerdictTagColor(undefined)).toBe("warning");
  });
});

describe("buildAbSummaryFromComparisonArtifact", () => {
  it("converts comparison metric/case rows into the AbSummaryReport shape", () => {
    const artifact = parseAbtestComparisonArtifact({
      origin: { avg_correctness: 0.5, cases: [{ case_id: "c1", overall: 0.6, correctness: 0.5 }] },
      candidate: { avg_correctness: 0.7, cases: [{ case_id: "c1", overall: 0.8, correctness: 0.7 }] },
    })!;
    const summary = buildAbSummaryFromComparisonArtifact(artifact);
    expect(summary.alignedCases).toBe(1);
    expect(summary.metricRows.find((row) => row.metric === "avg_correctness")?.meanB).toBe(0.7);
    expect(summary.topDiffRows[0].delta).toBeCloseTo(0.2);
  });
});

describe("buildAbCaseTraceMapFromComparisonArtifact", () => {
  it("maps case ids to origin/candidate trace ids", () => {
    const artifact = parseAbtestComparisonArtifact({
      origin: { cases: [{ case_id: "c1", trace_id: "t-a" }] },
      candidate: { cases: [{ case_id: "c1", trace_id: "t-b" }] },
    })!;
    const map = buildAbCaseTraceMapFromComparisonArtifact(artifact);
    expect(map.get("c1")).toEqual({ a: "t-a", b: "t-b" });
  });

  it("returns an empty map for an undefined artifact", () => {
    expect(buildAbCaseTraceMapFromComparisonArtifact(undefined).size).toBe(0);
  });
});

describe("buildAbCaseDetailItemFromComparisonCase", () => {
  it("builds the before/after and baseline/candidate trace shape", () => {
    const item = buildAbCaseDetailItemFromComparisonCase({
      key: "c1",
      caseId: "c1",
      originOverall: 0.6,
      candidateOverall: 0.8,
      deltaOverall: 0.2,
      originCorrectness: 0.5,
      candidateCorrectness: 0.7,
      originTraceId: "t-a",
      candidateTraceId: "t-b",
    });
    expect(item).toEqual({
      case_id: "c1",
      baseline: { trace_id: "t-a" },
      candidate: { trace_id: "t-b" },
      before: { trace_id: "t-a" },
      after: { trace_id: "t-b" },
    });
  });
});
