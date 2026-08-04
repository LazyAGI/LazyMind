import { describe, expect, it } from "vitest";
import {
  buildPxCategoryMetricAveragesFromGateEval,
  getGateEvalCaseCount,
  getGateEvalCaseRecords,
  getGateEvalMetrics,
  getGateEvalQuestionTypeSummaries,
  hasEmbeddedGateEvalCases,
  isGateEvalContent,
  unwrapGateEvalContent,
} from "./evalGateContent";

describe("isGateEvalContent", () => {
  it("returns true when a known aggregate metric field is present", () => {
    expect(isGateEvalContent({ avg_correctness: 0.8 })).toBe(true);
  });

  it("returns true when a cases array is present", () => {
    expect(isGateEvalContent({ cases: [] })).toBe(true);
  });

  it("returns false when neither is present", () => {
    expect(isGateEvalContent({ foo: "bar" })).toBe(false);
  });
});

describe("unwrapGateEvalContent", () => {
  it("returns the record itself when it already looks like gate eval content", () => {
    const record = { avg_correctness: 0.5, cases: [] };
    expect(unwrapGateEvalContent(record)).toBe(record);
  });

  it("unwraps nested content/data/result records", () => {
    const nested = { avg_correctness: 0.5 };
    expect(unwrapGateEvalContent({ content: nested })).toBe(nested);
    expect(unwrapGateEvalContent({ data: nested })).toBe(nested);
  });

  it("returns undefined when nothing looks like gate eval content", () => {
    expect(unwrapGateEvalContent({ foo: "bar" })).toBeUndefined();
    expect(unwrapGateEvalContent("nope")).toBeUndefined();
  });
});

describe("getGateEvalCaseRecords / getGateEvalCaseCount", () => {
  it("extracts case records from the cases array", () => {
    const payload = { avg_correctness: 0.5, cases: [{ case_id: "c1" }, { case_id: "c2" }] };
    expect(getGateEvalCaseRecords(payload)).toHaveLength(2);
  });

  it("prefers an explicit case_num field over counting records", () => {
    const payload = { avg_correctness: 0.5, case_num: 10, cases: [{ case_id: "c1" }] };
    expect(getGateEvalCaseCount(payload)).toBe(10);
  });

  it("falls back to counting case records when no explicit count field exists", () => {
    const payload = { avg_correctness: 0.5, cases: [{ case_id: "c1" }, { case_id: "c2" }] };
    expect(getGateEvalCaseCount(payload)).toBe(2);
  });

  it("returns 0/empty when the payload isn't recognized as gate eval content", () => {
    expect(getGateEvalCaseCount({ foo: "bar" })).toBe(0);
    expect(getGateEvalCaseRecords({ foo: "bar" })).toEqual([]);
  });
});

describe("getGateEvalMetrics", () => {
  it("clamps and normalizes known metric keys from a nested metrics record", () => {
    const payload = { avg_correctness: 0.5, metrics: { correctness: 1.5, relevance: -0.2, overall: 0.7 } };
    const metrics = getGateEvalMetrics(payload);
    expect(metrics?.correctness).toBe(1);
    expect(metrics?.relevance).toBe(0);
    expect(metrics?.overall).toBe(0.7);
  });

  it("returns undefined when there's no metrics record", () => {
    expect(getGateEvalMetrics({ avg_correctness: 0.5 })).toBeUndefined();
  });
});

describe("getGateEvalQuestionTypeSummaries", () => {
  it("normalizes question type summaries with defaults for missing fields", () => {
    const payload = {
      avg_correctness: 0.5,
      question_type_summaries: [
        { question_type: "factual", case_num: 3, scored_case_num: 2, metrics: { overall: 0.6 } },
        { case_num: 1 },
      ],
    };
    const summaries = getGateEvalQuestionTypeSummaries(payload);
    expect(summaries).toHaveLength(2);
    expect(summaries[0]).toMatchObject({ questionType: "factual", caseCount: 3, scoredCaseCount: 2 });
    expect(summaries[1].scoredCaseCount).toBe(0);
  });
});

describe("buildPxCategoryMetricAveragesFromGateEval", () => {
  it("builds a single 'overall' category average when aggregate metrics exist", () => {
    const payload = { avg_correctness: 0.8, avg_overall: 0.7, avg_retrieval_quality: 0.6, avg_groundedness: 0.5 };
    const averages = buildPxCategoryMetricAveragesFromGateEval(payload);
    expect(averages).toHaveLength(1);
    expect(averages[0].metrics.answer_correctness).toBe(0.8);
  });

  it("returns an empty array when there are no aggregate metrics and no cases", () => {
    expect(buildPxCategoryMetricAveragesFromGateEval({ foo: "bar" })).toEqual([]);
  });
});

describe("hasEmbeddedGateEvalCases", () => {
  it("returns true when case records are present", () => {
    expect(hasEmbeddedGateEvalCases({ avg_correctness: 0.5, cases: [{ case_id: "c1" }] })).toBe(true);
  });

  it("returns false when there are no case records", () => {
    expect(hasEmbeddedGateEvalCases({ foo: "bar" })).toBe(false);
  });
});
