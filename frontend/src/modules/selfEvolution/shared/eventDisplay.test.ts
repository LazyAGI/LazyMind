import { describe, expect, it } from "vitest";
import { t } from "./i18n";
import {
  buildAbtestEventDisplayText,
  buildAnalysisEventDisplayText,
  buildApplyEventDisplayText,
  buildDatasetEventDisplayText,
  buildEvalEventDisplayText,
  compactPayloadForDisplay,
} from "./eventDisplay";

describe("buildAnalysisEventDisplayText", () => {
  it("returns the started label on action start", () => {
    expect(buildAnalysisEventDisplayText("start", "run.start", {})).toBe(t("selfEvolutionRun.analysisStarted"));
  });

  it("summarizes indexer hypotheses when present", () => {
    const text = buildAnalysisEventDisplayText("progress", "run.indexer.result", {
      data: { result: { hypotheses: [{}, {}] } },
    });
    expect(text).toBe(t("selfEvolutionRun.analysisHypothesesGenerated", { count: 2 }));
  });

  it("reports convergence for the conductor result", () => {
    const text = buildAnalysisEventDisplayText("progress", "run.conductor.result", {
      data: { iteration: 3, result: { converged: true, total_actions: 5 } },
    });
    expect(text).toContain(t("selfEvolutionRun.analysisConvergedActions", { count: 5 }));
  });

  it("returns undefined when nothing matches", () => {
    expect(buildAnalysisEventDisplayText("progress", "run.other", {})).toBeUndefined();
  });
});

describe("buildApplyEventDisplayText", () => {
  it("reports a repair-loop attempt number while running", () => {
    const text = buildApplyEventDisplayText("progress", "apply.repair_loop", {
      data: { phase: "repair_loop", attempt: 2 },
    });
    expect(text).toBe(t("selfEvolutionRun.repairLoopAttempt", { attempt: 2 }));
  });

  it("reports the repair-loop decision on finish", () => {
    const text = buildApplyEventDisplayText("finish", "apply.repair_loop", {
      data: { phase: "repair_loop", detail: { decision: "accepted" } },
    });
    expect(text).toBe(t("selfEvolutionRun.repairLoopDone", { decision: "accepted" }));
  });

  it("summarizes a round diff with file/test info", () => {
    const text = buildApplyEventDisplayText("progress", "apply.round.diff", {
      data: { round: 1, files_changed: ["a.ts"], diff_summary: "3 tests passed" },
    });
    expect(text).toBe(
      t("selfEvolutionRun.roundDiffDone", {
        round: 1,
        fileText: t("selfEvolutionRun.filesChanged", { count: 1 }),
        testsText: t("selfEvolutionRun.testsPassed"),
      }),
    );
  });

  it("returns the generic repair-started label", () => {
    expect(buildApplyEventDisplayText("start", "apply.start", {})).toBe(t("selfEvolutionRun.repairStarted"));
  });
});

describe("buildDatasetEventDisplayText", () => {
  it("returns the started label for a start action", () => {
    expect(buildDatasetEventDisplayText("start", {})).toBe(t("selfEvolutionRun.datasetStarted"));
  });

  it("shows segment-done-waiting when the segment doesn't reach 100%", () => {
    const text = buildDatasetEventDisplayText("finish", { event_type: "load_corpus" });
    expect(text).toBe(t("selfEvolutionRun.segmentDoneWaiting", { label: t("selfEvolutionRun.segLoadCorpus") }));
  });

  it("shows running count text for a matched segment", () => {
    const text = buildDatasetEventDisplayText("progress", { event_type: "generate_case", data: { current: 2, total: 8 } });
    expect(text).toBe(
      t("selfEvolutionRun.segmentRunningCount", {
        label: t("selfEvolutionRun.segGenerateCase"),
        countText: t("selfEvolutionRun.progressCount", { current: 2, total: 8 }),
      }),
    );
  });

  it("falls back to the generic running label when there is no matched segment", () => {
    const text = buildDatasetEventDisplayText("progress", {});
    expect(text).toBe(t("selfEvolutionRun.datasetRunningCount", { countText: "" }));
  });
});

describe("buildEvalEventDisplayText", () => {
  it("returns a phase-specific started label", () => {
    expect(buildEvalEventDisplayText("start", "eval.rag", {})).toBe(t("selfEvolutionRun.evalRagStarted"));
  });

  it("returns a phase-specific done label", () => {
    expect(buildEvalEventDisplayText("eval.judge.finish", "eval.judge", {})).toBe(t("selfEvolutionRun.evalJudgeDone"));
  });

  it("returns undefined when there is no phase and action is a plain progress", () => {
    expect(buildEvalEventDisplayText("progress", "eval.other", {})).toBeUndefined();
  });
});

describe("buildAbtestEventDisplayText", () => {
  it("reports a candidate answer generating message with case counters", () => {
    const text = buildAbtestEventDisplayText("progress", {
      event_type: "abtest.candidate_rag_answer",
      data: { case_id: "c1", case_index: 1, total: 3 },
    });
    expect(text).toBe(t("selfEvolutionRun.abtestCandidateGenerating", { caseText: "，case 1/3" }));
  });

  it("reports pass/fail decision text", () => {
    const text = buildAbtestEventDisplayText("finish", { data: { detail: { decision_status: "accept" } } });
    expect(text).toBe(t("selfEvolutionRun.abtestDecisionPassed"));
  });

  it("falls back to the generic started/finished labels", () => {
    expect(buildAbtestEventDisplayText("start", {})).toBe(t("selfEvolutionRun.abtestStarted"));
    expect(buildAbtestEventDisplayText("finish", {})).toBe(t("selfEvolutionRun.abtestDone"));
  });
});

describe("compactPayloadForDisplay", () => {
  it("returns an empty string for an undefined payload", () => {
    expect(compactPayloadForDisplay(undefined)).toBe("");
  });

  it("builds a structured summary from status/phase/progress", () => {
    const text = compactPayloadForDisplay({ data: { phase: "repair_patch", status: "ok", current: 1, total: 2 } });
    expect(text).toContain("ok");
    expect(text).toContain(t("selfEvolutionRun.compactProgress", { current: 1, total: 2 }));
  });

  it("falls back to a generic key/value summary when there's no structured info", () => {
    const text = compactPayloadForDisplay({ custom_field: "hello" });
    expect(text).toContain("custom_field");
    expect(text).toContain("hello");
  });

  it("returns an empty string when only excluded keys are present", () => {
    expect(compactPayloadForDisplay({ seq: 1, event_id: "e1", created_at: "2026-01-01" })).toBe("");
  });
});
