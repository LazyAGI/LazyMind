import { describe, expect, it } from "vitest";
import {
  buildAbCaseTraceIdMap,
  findAbCaseDetailItem,
  getAbtestVerdictColor,
  normalizeAbCaseRows,
  normalizeBadcaseRows,
  normalizeEvalReportSummary,
  normalizeObservationKind,
  resolveAbtestIdFromPayload,
  resolveCaseTraceIds,
  toAbMetricRows,
} from "./dataUtils";
import type { AbSummaryReport } from "../../shared";

const t = (key: string) => key;

describe("normalizeObservationKind", () => {
  it("maps known kind aliases to their canonical result kind", () => {
    expect(normalizeObservationKind("eval")).toBe("eval-reports");
    expect(normalizeObservationKind("abtest")).toBe("abtests");
  });

  it("returns undefined for unknown or missing kinds", () => {
    expect(normalizeObservationKind("weird")).toBeUndefined();
    expect(normalizeObservationKind(undefined)).toBeUndefined();
  });
});

describe("normalizeBadcaseRows", () => {
  it("extracts bad case rows from a bad_cases array with score-based failure tone", () => {
    const rows = normalizeBadcaseRows(t, {
      bad_cases: [{ case_id: "c1", query: "q1", metrics: { overall: 0.3 } }],
    });
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ caseId: "c1", query: "q1", score: 0.3, failureTone: "orange" });
  });

  it("falls back to a generated case id and default failure type text when fields are missing", () => {
    const rows = normalizeBadcaseRows(t, { cases: [{}] });
    expect(rows[0].caseId).toBe("case-001");
    expect(rows[0].failureType).toBe("selfEvolutionRun.observation.pendingAnalysis");
  });
});

describe("normalizeAbCaseRows", () => {
  it("uses the parsed comparison artifact's case rows when available", () => {
    const rows = normalizeAbCaseRows(t, {
      run_id: "run-1",
      origin: { cases: [{ case_id: "c1", overall: 0.5 }] },
      candidate: { cases: [{ case_id: "c1", overall: 0.7 }] },
    });
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ caseId: "c1", aScore: 0.5, bScore: 0.7, tone: "up" });
  });

  it("falls back to normalizing raw case records when there's no comparison artifact", () => {
    const rows = normalizeAbCaseRows(t, { cases: [{ case_id: "c1", a_score: 0.4, b_score: 0.6 }] });
    expect(rows[0]).toMatchObject({ caseId: "c1", aScore: 0.4, bScore: 0.6, tone: "up" });
  });
});

describe("toAbMetricRows", () => {
  it("maps summary metric rows into flattened ab metric rows", () => {
    const summary: AbSummaryReport = {
      id: "s1",
      reasons: [],
      metricRows: [
        { key: "m1", metric: "answer_correctness", metricLabel: "Correctness", meanA: 0.5, meanB: 0.6, deltaMean: 0.1, winRateB: 0.6, signP: 0.02 },
      ],
      topDiffRows: [],
      missingMetrics: [],
      guardMetrics: [],
    };
    const rows = toAbMetricRows(summary);
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ key: "answer_correctness", meanA: 0.5, meanB: 0.6 });
  });

  it("returns an empty array when the summary is undefined or has no metric rows", () => {
    expect(toAbMetricRows(undefined)).toEqual([]);
  });
});

describe("resolveAbtestIdFromPayload", () => {
  it("prefers the run id from a parsed comparison artifact", () => {
    expect(resolveAbtestIdFromPayload({ run_id: "run-42", origin: {}, candidate: {} })).toBe("run-42");
  });

  it("returns undefined for a synthetic fallback abtest id", () => {
    expect(resolveAbtestIdFromPayload({})).toBeUndefined();
  });
});

describe("getAbtestVerdictColor", () => {
  it("maps pass/fail verdict keywords to color tokens", () => {
    expect(getAbtestVerdictColor("pass")).toBe("success");
    expect(getAbtestVerdictColor("reject")).toBe("error");
    expect(getAbtestVerdictColor("undecided")).toBe("orange");
  });
});

describe("findAbCaseDetailItem", () => {
  it("finds a case item by case id within the items array", () => {
    const item = findAbCaseDetailItem({ items: [{ case_id: "c1" }, { case_id: "c2" }] }, "c2");
    expect(item).toEqual({ case_id: "c2" });
  });

  it("returns undefined when the data isn't a record or the case can't be found", () => {
    expect(findAbCaseDetailItem("nope", "c1")).toBeUndefined();
    expect(findAbCaseDetailItem({ items: [] }, "c1")).toBeUndefined();
  });
});

describe("buildAbCaseTraceIdMap / resolveCaseTraceIds", () => {
  it("builds a case -> {a,b} trace id map from origin/candidate result records", () => {
    const map = buildAbCaseTraceIdMap([
      { data: { id: "abtest.baseline_eval_summary", rows: [{ case_id: "c1", trace_id: "trace-a" }] } },
      { data: { id: "abtest.candidate_eval_summary", rows: [{ case_id: "c1", trace_id: "trace-b" }] } },
    ]);
    expect(map.get("c1")).toEqual({ a: "trace-a", b: "trace-b" });
  });

  it("resolveCaseTraceIds prefers case item fields over the map, falling back to the map", () => {
    const map = new Map([["c1", { a: "map-a", b: "map-b" }]]);
    expect(resolveCaseTraceIds(undefined, "c1", map)).toEqual({ a: "map-a", b: "map-b" });
    const result = resolveCaseTraceIds({ baseline_trace_id: "item-a" }, "c1", map);
    expect(result.a).toBe("item-a");
    expect(result.b).toBe("map-b");
  });
});

describe("normalizeEvalReportSummary", () => {
  it("normalizes summary fields from gate eval content", () => {
    const summary = normalizeEvalReportSummary({
      run_id: "run-1",
      algo_id: "algo-a",
      avg_correctness: 0.8,
      cases: [{ case_id: "c1" }],
    });
    expect(summary).toMatchObject({ reportId: "run-1", dataset: "algo-a", correctRate: 0.8, badCaseCount: 1 });
  });

  it("falls back to a generic eval report record shape when not gate eval content", () => {
    const summary = normalizeEvalReportSummary({
      report_id: "r1",
      data: { eval_dataset_ref: "ds1", metrics: { correct_rate: 0.6 } },
      bad_case_count: 3,
    });
    expect(summary).toMatchObject({ reportId: "r1", dataset: "ds1", correctRate: 0.6, badCaseCount: 3 });
  });
});
