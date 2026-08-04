import { describe, expect, it } from "vitest";
import { buildAbCategoryComparisons, buildAbSummaryReports, getAbtestResultRecords } from "./abtest";
import type { AbSummaryReport } from "./types";

describe("getAbtestResultRecords", () => {
  it("filters out empty objects from an array payload", () => {
    const records = getAbtestResultRecords([{ id: 1 }, {}, { id: 2 }]);
    expect(records).toHaveLength(2);
  });

  it("unwraps nested result items from a record payload", () => {
    const records = getAbtestResultRecords({ items: [{ id: 1 }, { id: 2 }] });
    expect(records).toHaveLength(2);
  });

  it("wraps a plain record as a single-item array when no nested items exist", () => {
    expect(getAbtestResultRecords({ id: 1 })).toEqual([{ id: 1 }]);
  });

  it("returns an empty array for non-record, non-array values", () => {
    expect(getAbtestResultRecords("nope")).toEqual([]);
  });
});

describe("buildAbSummaryReports", () => {
  it("builds a report with metric rows from baseline/candidate metrics", () => {
    const reports = buildAbSummaryReports({
      data: {
        summary: {
          metrics: {
            baseline: { answer_correctness: 0.5 },
            candidate: { answer_correctness: 0.7 },
          },
          verdict: "pass",
          case_deltas: [{ outcome: "improved" }],
        },
        abtest_id: "ab-1",
      },
    });
    expect(reports).toHaveLength(1);
    const metricRow = reports[0].metricRows.find((row) => row.key === "answer_correctness");
    expect(metricRow?.meanA).toBe(0.5);
    expect(metricRow?.meanB).toBe(0.7);
    expect(reports[0].verdict).toBe("pass");
    expect(reports[0].id).toBe("ab-1");
  });

  it("falls back to raw metric entries when baseline/candidate metrics are absent", () => {
    const reports = buildAbSummaryReports({
      data: {
        summary: {
          metrics: {
            custom_metric: { mean_a: 0.3, mean_b: 0.4, delta_mean: 0.1, win_rate_b: 0.6, sign_p: 0.02, n: 20 },
          },
        },
      },
    });
    expect(reports[0].metricRows[0]).toMatchObject({ key: "custom_metric", meanA: 0.3, meanB: 0.4, n: 20 });
  });

  it("skips records with no summary and returns an empty report list", () => {
    expect(buildAbSummaryReports({ data: { unrelated: true } })).toEqual([]);
  });
});

describe("buildAbCategoryComparisons", () => {
  it("clamps and derives baseline/experiment/delta scores per px metric", () => {
    const report: AbSummaryReport = {
      id: "r1",
      reasons: [],
      metricRows: [
        { key: "answer_correctness", metric: "answer_correctness", metricLabel: "x", meanA: 0.5, meanB: 0.8, deltaMean: 0.3, winRateB: 0.5, n: 10 },
      ],
      topDiffRows: [],
      missingMetrics: [],
      guardMetrics: [],
    };
    const [comparison] = buildAbCategoryComparisons([report]);
    expect(comparison.baseline.answer_correctness).toBe(0.5);
    expect(comparison.experiment.answer_correctness).toBe(0.8);
    expect(comparison.delta.answer_correctness).toBeCloseTo(0.3);
  });

  it("skips reports without any metric rows", () => {
    const report: AbSummaryReport = { id: "r1", reasons: [], metricRows: [], topDiffRows: [], missingMetrics: [], guardMetrics: [] };
    expect(buildAbCategoryComparisons([report])).toEqual([]);
  });
});
