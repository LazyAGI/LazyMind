import { describe, expect, it } from "vitest";
import {
  getAffectedBlockLabel,
  getAnalysisCategoryLabels,
  getAnalysisVerdictLabels,
  getCaseStepLabel,
  getCheckpointCommandText,
  getEventActionLabels,
  getEvalQuestionTypeLabel,
  getExistingEvalSetOptions,
  getPxMetricMeta,
  getQuestionTypeLabelMap,
  getStageLabels,
  getWorkflowResultLabels,
  getWorkflowStepDefinitions,
  t,
} from "./i18n";

describe("t", () => {
  it("resolves a known translation key", () => {
    expect(t("selfEvolutionRun.stageDataset")).toBe("数据集");
  });

  it("interpolates options into the translated string", () => {
    expect(t("selfEvolutionRun.categoryN", { n: 2 })).toBe("分类 2");
  });
});

describe("getWorkflowResultLabels", () => {
  it("returns a label for every workflow result kind", () => {
    const labels = getWorkflowResultLabels();
    expect(labels.datasets).toBe("数据集");
    expect(labels["eval-reports"]).toBe("评测报告");
    expect(labels.abtests).toBe("ABTest 报告");
  });
});

describe("getPxMetricMeta", () => {
  it("returns metadata entries for all four px metric keys", () => {
    const meta = getPxMetricMeta();
    expect(meta).toHaveLength(4);
    expect(meta.map((item) => item.key)).toEqual([
      "answer_correctness",
      "answer_score",
      "chunk_recall",
      "doc_recall",
    ]);
    expect(meta[0].label).toBe("答案正确性");
  });
});

describe("getStageLabels", () => {
  it("returns labels for every thread event stage", () => {
    const labels = getStageLabels();
    expect(labels.dataset).toBe("数据集");
    expect(labels.repair).toBe("代码优化");
  });
});

describe("getCheckpointCommandText", () => {
  it("returns a non-empty command text", () => {
    expect(getCheckpointCommandText().length).toBeGreaterThan(0);
  });
});

describe("getEventActionLabels", () => {
  it("maps known action keys to labels", () => {
    const labels = getEventActionLabels();
    expect(labels.start).toBe("开始");
    expect(labels["tool.used"]).toBe("工具调用");
  });
});

describe("getAnalysisCategoryLabels / getAnalysisVerdictLabels", () => {
  it("returns known category labels", () => {
    expect(getAnalysisCategoryLabels().retrieval_miss).toBe("召回缺失");
  });

  it("returns known verdict labels", () => {
    expect(getAnalysisVerdictLabels().confirmed).toBe("已确认");
  });
});

describe("getCaseStepLabel", () => {
  it("returns a localized label for a known step key", () => {
    expect(getCaseStepLabel("trace_summary")).toBe("trace_summary");
  });

  it("returns the raw key as fallback for unlisted step keys", () => {
    expect(getCaseStepLabel("unknown_step")).toBe("unknown_step");
  });

  it("returns unlocalized static labels for RAG/judge steps", () => {
    expect(getCaseStepLabel("rag")).toBe("RAG");
    expect(getCaseStepLabel("judge")).toBe("judge");
  });
});

describe("getWorkflowStepDefinitions", () => {
  it("returns five workflow steps in the expected order", () => {
    const steps = getWorkflowStepDefinitions();
    expect(steps.map((step) => step.id)).toEqual([
      "dataset",
      "px-report",
      "analysis",
      "code-optimize",
      "ab-test",
    ]);
    expect(steps[0].title).toBe("生成数据集");
  });
});

describe("getExistingEvalSetOptions", () => {
  it("returns the fixed none option", () => {
    expect(getExistingEvalSetOptions()).toEqual([{ label: "不使用已有评测集", value: "__none__" }]);
  });
});

describe("getQuestionTypeLabelMap", () => {
  it("maps numeric question types to labels", () => {
    const map = getQuestionTypeLabelMap();
    expect(map[1]).toBe("单跳问答");
    expect(map[5]).toBe("代码问答");
  });
});

describe("getEvalQuestionTypeLabel", () => {
  it("normalizes and maps a known question type string", () => {
    expect(getEvalQuestionTypeLabel("Single-Hop")).toBe("单跳问答");
  });

  it("falls back to underscore-replaced text for unknown types", () => {
    expect(getEvalQuestionTypeLabel("some_weird_type")).toBe("some weird type");
  });
});

describe("getAffectedBlockLabel", () => {
  it("returns the mapped label for a known block", () => {
    expect(getAffectedBlockLabel("retrieval")).toBe("检索");
  });

  it("normalizes case before matching", () => {
    expect(getAffectedBlockLabel("Tool_Orchestration")).toBe("工具编排");
  });

  it("falls back to underscore-replaced text for unknown blocks", () => {
    expect(getAffectedBlockLabel("some_other_block")).toBe("some other block");
  });
});
