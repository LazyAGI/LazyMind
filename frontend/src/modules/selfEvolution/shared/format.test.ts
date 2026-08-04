import { describe, expect, it } from "vitest";
import {
  buildPxCategoryMetricAveragesFromReport,
  clampPercent,
  clampScore,
  formatAbMetricLabel,
  formatAnalysisAgentName,
  formatAnalysisCategory,
  formatAnalysisVerdict,
  formatConfidencePercent,
  formatMaybePValue,
  formatMetricDelta,
  formatMetricPercent,
  formatMetricSummary,
  formatPercent,
  formatQuestionType,
  formatThreadListTime,
  formatThreadTime,
  getMetricFieldNumber,
  getQuestionTypeDisplayName,
  getShortLabel,
  getThreadTimeSortValue,
  getTimeLabel,
  toFiniteNumber,
} from "./format";

describe("getMetricFieldNumber", () => {
  it("resolves a value via known field aliases and clamps it", () => {
    expect(getMetricFieldNumber({ answer_correctness_avg: 0.8 }, "answer_correctness")).toBe(0.8);
  });

  it("returns the fallback clamped when no alias matches", () => {
    expect(getMetricFieldNumber({}, "answer_correctness", 2)).toBe(1);
  });
});

describe("formatQuestionType", () => {
  it("maps a known question type number to its label", () => {
    expect(formatQuestionType(1)).toBe("单跳问答");
  });

  it("falls back to the stringified number for unknown types", () => {
    expect(formatQuestionType(999)).toBe("999");
  });
});

describe("clampScore", () => {
  it("clamps values into [0,1]", () => {
    expect(clampScore(1.5)).toBe(1);
    expect(clampScore(-0.5)).toBe(0);
    expect(clampScore(0.5)).toBe(0.5);
  });

  it("returns 0 for non-finite values", () => {
    expect(clampScore(NaN)).toBe(0);
    expect(clampScore(Infinity)).toBe(0);
  });
});

describe("formatPercent", () => {
  it("formats a ratio as a percentage string with one decimal", () => {
    expect(formatPercent(0.856)).toBe("85.6%");
    expect(formatPercent(0)).toBe("0.0%");
  });
});

describe("getQuestionTypeDisplayName", () => {
  it("prefers question_type_name when present", () => {
    expect(
      getQuestionTypeDisplayName({ question_type_name: "  自定义类型  " } as any, 0),
    ).toBe("自定义类型");
  });

  it("falls back to question_type_key when name absent", () => {
    expect(getQuestionTypeDisplayName({ question_type_key: "key1" } as any, 0)).toBe("key1");
  });

  it("falls back to numeric question_type label", () => {
    expect(getQuestionTypeDisplayName({ question_type: 2 } as any, 0)).toBe("多跳问答");
  });

  it("falls back to a generated category label using the index", () => {
    expect(getQuestionTypeDisplayName({} as any, 2)).toBe("分类 3");
  });
});

describe("buildPxCategoryMetricAveragesFromReport", () => {
  it("returns question-type breakdown from case_details_summary when present", () => {
    const payload = {
      case_details_summary: {
        question_types: [
          {
            question_type_name: "单跳",
            count: 5,
            averages: { answer_correctness: 0.9, answer_score: 0.8, chunk_recall: 0.7, doc_recall: 0.6 },
          },
        ],
      },
    };
    const result = buildPxCategoryMetricAveragesFromReport(payload);
    expect(result).toHaveLength(1);
    expect(result[0].category).toBe("单跳");
    expect(result[0].caseCount).toBe(5);
    expect(result[0].metrics.answer_correctness).toBe(0.9);
  });

  it("falls back to overall metrics record when no question types present", () => {
    const payload = { metrics: { answer_correctness: 0.5, answer_score: 0.4, chunk_recall: 0.3, doc_recall: 0.2 }, total_cases: 10 };
    const result = buildPxCategoryMetricAveragesFromReport(payload);
    expect(result).toHaveLength(1);
    expect(result[0].category).toBe("总体");
    expect(result[0].caseCount).toBe(10);
  });

  it("falls back to by_question_type breakdown as the last resort", () => {
    const payload = {
      by_question_type: {
        single_hop: { count: 3, answer_correctness: 0.6, answer_score: 0.5, chunk_recall: 0.4, doc_recall: 0.3 },
      },
    };
    const result = buildPxCategoryMetricAveragesFromReport(payload);
    expect(result).toHaveLength(1);
    expect(result[0].category).toBe("single_hop");
    expect(result[0].caseCount).toBe(3);
  });

  it("returns an empty array when nothing matches", () => {
    expect(buildPxCategoryMetricAveragesFromReport({})).toEqual([]);
  });
});

describe("getTimeLabel", () => {
  it("returns a HH:mm formatted time string", () => {
    expect(getTimeLabel()).toMatch(/^\d{2}:\d{2}$/);
  });
});

describe("formatThreadTime", () => {
  it("formats a valid ISO date string as HH:mm", () => {
    expect(formatThreadTime("2026-01-01T12:30:00Z")).toMatch(/^\d{2}:\d{2}$/);
  });

  it("formats a unix-seconds timestamp", () => {
    expect(formatThreadTime(1700000000)).toMatch(/^\d{2}:\d{2}$/);
  });

  it("returns the trimmed original string for an unparsable non-empty string", () => {
    expect(formatThreadTime("not-a-date")).toBe("not-a-date");
  });

  it("falls back to current time label for other types", () => {
    expect(formatThreadTime(null)).toMatch(/^\d{2}:\d{2}$/);
  });
});

describe("getThreadTimeSortValue", () => {
  it("returns the timestamp for a valid date string", () => {
    const value = getThreadTimeSortValue("2026-01-01T00:00:00Z");
    expect(value).toBeGreaterThan(0);
  });

  it("returns the timestamp for a numeric unix-seconds value", () => {
    expect(getThreadTimeSortValue(1700000000)).toBe(1700000000 * 1000);
  });

  it("returns 0 for unparsable input", () => {
    expect(getThreadTimeSortValue("nonsense")).toBe(0);
    expect(getThreadTimeSortValue(null)).toBe(0);
  });
});

describe("formatThreadListTime", () => {
  it("formats a valid date string with month/day/hour/minute", () => {
    expect(formatThreadListTime("2026-01-02T03:04:00Z")).toMatch(/\d{2}\/\d{2}/);
  });

  it("falls back to justNow label for unparsable input", () => {
    expect(formatThreadListTime(null)).toBe("刚刚");
  });
});

describe("clampPercent", () => {
  it("clamps and rounds values into [0,100]", () => {
    expect(clampPercent(150)).toBe(100);
    expect(clampPercent(-10)).toBe(0);
    expect(clampPercent(55.6)).toBe(56);
  });

  it("returns 0 for non-finite values", () => {
    expect(clampPercent(NaN)).toBe(0);
  });
});

describe("formatAnalysisVerdict", () => {
  it("returns the mapped label for a known verdict", () => {
    expect(formatAnalysisVerdict("confirmed")).toBe("已确认");
  });

  it("returns the investigating fallback when verdict is undefined", () => {
    expect(formatAnalysisVerdict(undefined)).toBe("分析中");
  });

  it("returns the raw verdict when unmapped", () => {
    expect(formatAnalysisVerdict("weird_verdict")).toBe("weird_verdict");
  });
});

describe("formatAnalysisCategory", () => {
  it("returns the mapped label for a known category", () => {
    expect(formatAnalysisCategory("retrieval_miss")).toBe("召回缺失");
  });

  it("returns the uncategorized fallback when category is undefined", () => {
    expect(formatAnalysisCategory(undefined)).toBe("未分类");
  });

  it("returns the raw category when unmapped", () => {
    expect(formatAnalysisCategory("weird_category")).toBe("weird_category");
  });
});

describe("formatConfidencePercent", () => {
  it("scales a fraction value to a percent string", () => {
    expect(formatConfidencePercent(0.75)).toBe("75%");
  });

  it("keeps an already-percent value as is", () => {
    expect(formatConfidencePercent(75)).toBe("75%");
  });

  it("returns undefined for non-numeric input", () => {
    expect(formatConfidencePercent(undefined)).toBeUndefined();
    expect(formatConfidencePercent(NaN)).toBeUndefined();
  });
});

describe("formatAnalysisAgentName", () => {
  it("returns the fallback label when agent is undefined", () => {
    expect(formatAnalysisAgentName(undefined)).toBe("研究子代理");
  });

  it("formats a researcher-prefixed agent id", () => {
    expect(formatAnalysisAgentName("researcher:3")).toBe("研究员 3");
  });

  it("returns the raw agent name when not researcher-prefixed", () => {
    expect(formatAnalysisAgentName("conductor")).toBe("conductor");
  });
});

describe("getShortLabel", () => {
  it("returns text unchanged when within max length", () => {
    expect(getShortLabel("short", 6)).toBe("short");
  });

  it("truncates and appends an ellipsis when over max length", () => {
    expect(getShortLabel("this is long", 4)).toBe("this…");
  });
});

describe("formatMetricPercent", () => {
  it("rounds a ratio to a whole percent", () => {
    expect(formatMetricPercent(0.856)).toBe("86%");
  });
});

describe("formatMetricDelta", () => {
  it("adds a plus sign for positive deltas", () => {
    expect(formatMetricDelta(0.05)).toBe("+5%");
  });

  it("keeps a negative sign for negative deltas", () => {
    expect(formatMetricDelta(-0.05)).toBe("-5%");
  });

  it("has no sign prefix for a zero delta", () => {
    expect(formatMetricDelta(0)).toBe("0%");
  });
});

describe("formatMetricSummary", () => {
  it("joins all four metric summaries with a separator", () => {
    const summary = formatMetricSummary({
      answer_correctness: 0.9,
      answer_score: 0.8,
      chunk_recall: 0.7,
      doc_recall: 0.6,
    });
    expect(summary).toContain("正确性 90%");
    expect(summary).toContain("综合得分 80%");
    expect(summary.split(" / ")).toHaveLength(4);
  });
});

describe("toFiniteNumber", () => {
  it("returns a finite number unchanged", () => {
    expect(toFiniteNumber(42)).toBe(42);
  });

  it("parses a numeric string", () => {
    expect(toFiniteNumber("3.14")).toBe(3.14);
  });

  it("returns the fallback for non-numeric input", () => {
    expect(toFiniteNumber("abc", -1)).toBe(-1);
    expect(toFiniteNumber(undefined, 7)).toBe(7);
  });
});

describe("formatAbMetricLabel", () => {
  it("returns the localized label for a known metric key", () => {
    expect(formatAbMetricLabel("answer_correctness")).toBe("答案正确性");
  });

  it("returns the raw metric string when unknown", () => {
    expect(formatAbMetricLabel("unknown_metric")).toBe("unknown_metric");
  });
});

describe("formatMaybePValue", () => {
  it("formats a normal p-value to three decimals", () => {
    expect(formatMaybePValue(0.045)).toBe("0.045");
  });

  it("uses the small-value marker below 0.001", () => {
    expect(formatMaybePValue(0.0001)).toBe("<0.001");
  });

  it("returns a dash for null/undefined/non-finite values", () => {
    expect(formatMaybePValue(null)).toBe("-");
    expect(formatMaybePValue(undefined)).toBe("-");
    expect(formatMaybePValue(NaN)).toBe("-");
  });
});
