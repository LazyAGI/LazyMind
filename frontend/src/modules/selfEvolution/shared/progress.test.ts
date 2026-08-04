import { describe, expect, it } from "vitest";
import { t } from "./i18n";
import {
  createEvalProgressPhaseSnapshot,
  createSegmentProgressSnapshot,
  getAbtestWorkflowProgressSnapshot,
  getCompletedEvalProgressPhases,
  getCompletedProgressSnapshot,
  getDatasetOperationSegments,
  getDatasetWorkflowProgressSnapshot,
  getDefaultEvalProgressPhases,
  getEvalOverallProgressSnapshot,
  getEvalPayloadPhase,
  getEvalPhaseLabel,
  getEvalPhasePayloadData,
  getEvalProgressStatusLabel,
  getRuntimeProgressStatusLabel,
  getWorkflowProgressSnapshot,
  isAbtestStageCompleteEvent,
  isActionKind,
  isIntentSidecarOperation,
  isStepFinishEvent,
  mergeProgressSnapshot,
  normalizePhaseText,
  updateEvalProgressPhases,
  updateProgressStatusText,
} from "./progress";

describe("getRuntimeProgressStatusLabel", () => {
  it("maps finish/cancel/pause actions to their status labels", () => {
    expect(getRuntimeProgressStatusLabel("finish")).toBe(t("selfEvolutionRun.statusDone"));
    expect(getRuntimeProgressStatusLabel("cancel")).toBe(t("selfEvolutionRun.statusCanceled"));
    expect(getRuntimeProgressStatusLabel("pause")).toBe(t("selfEvolutionRun.statusPaused"));
  });

  it("defaults to the running label for other/undefined actions", () => {
    expect(getRuntimeProgressStatusLabel(undefined)).toBe(t("selfEvolutionRun.statusRunning"));
  });
});

describe("normalizePhaseText / isActionKind", () => {
  it("trims and lowercases string values", () => {
    expect(normalizePhaseText(" Finish ")).toBe("finish");
    expect(normalizePhaseText(undefined)).toBe("");
  });

  it("matches an exact action or a dotted suffix", () => {
    expect(isActionKind("finish", "finish")).toBe(true);
    expect(isActionKind("eval.finish", "finish")).toBe(true);
    expect(isActionKind("start", "finish")).toBe(false);
  });
});

describe("createSegmentProgressSnapshot", () => {
  it("computes percent from current/total within the segment span", () => {
    const snapshot = createSegmentProgressSnapshot("label", 0, 40, "progress", 100, 5, 10);
    expect(snapshot.percent).toBe(20);
    expect(snapshot.rank).toBe(105);
  });

  it("jumps to 100% when action is finish and no current/total supplied", () => {
    const snapshot = createSegmentProgressSnapshot("label", 0, 40, "finish", 100);
    expect(snapshot.percent).toBe(40);
    expect(snapshot.statusText).toBe(t("selfEvolutionRun.segmentDone", { label: "label" }));
  });
});

describe("getAbtestWorkflowProgressSnapshot", () => {
  it("builds a candidate-answer segment snapshot when a case is in flight", () => {
    const snapshot = getAbtestWorkflowProgressSnapshot("progress", {
      event_type: "abtest.candidate_rag_answer",
      data: { case: { id: "c1" }, progress: { current: 1, total: 4 } },
    });
    expect(snapshot?.percent).toBeGreaterThanOrEqual(8);
  });

  it("returns an accept/reject decision snapshot", () => {
    const snapshot = getAbtestWorkflowProgressSnapshot("finish", {
      data: { detail: { decision_status: "accept" } },
    });
    expect(snapshot?.statusText).toBe(t("selfEvolutionRun.segCandidatePassedCutover"));
  });

  it("returns undefined when nothing matches", () => {
    expect(getAbtestWorkflowProgressSnapshot("progress", {})).toBeUndefined();
  });
});

describe("getDatasetWorkflowProgressSnapshot", () => {
  it("returns a base-segment snapshot without progress numbers when starting", () => {
    const snapshot = getDatasetWorkflowProgressSnapshot("start", {
      event_type: "load_corpus",
    });
    const segments = getDatasetOperationSegments();
    expect(snapshot?.percent).toBe(segments["dataset.load_corpus"].base);
  });

  it("computes percent within the segment span from current/total", () => {
    const snapshot = getDatasetWorkflowProgressSnapshot("progress", {
      event_type: "generate_case",
      data: { current: 9, total: 45 },
    });
    const segments = getDatasetOperationSegments();
    const seg = segments["dataset.generate_case"];
    expect(snapshot?.percent).toBe(seg.base + seg.span * 0.2);
  });

  it("returns the completed snapshot for a finish action outside any known segment", () => {
    const snapshot = getDatasetWorkflowProgressSnapshot("finish", { data: { stage: "dataset" } });
    expect(snapshot).toEqual(getCompletedProgressSnapshot());
  });

  it("returns undefined for an unrecognized operation with no finish action", () => {
    expect(getDatasetWorkflowProgressSnapshot("progress", {})).toBeUndefined();
  });
});

describe("getEvalPayloadPhase", () => {
  it("detects the judge phase from action/type/payload keywords", () => {
    expect(getEvalPayloadPhase("eval.judge", undefined, undefined)).toBe("judge");
    expect(getEvalPayloadPhase(undefined, undefined, { judge: true })).toBe("judge");
  });

  it("detects the rag phase from a rag-flavored candidate", () => {
    expect(getEvalPayloadPhase("eval.rag", undefined, undefined)).toBe("rag");
  });

  it("returns undefined when no phase keyword is present", () => {
    expect(getEvalPayloadPhase("progress", "eval.other", undefined)).toBeUndefined();
  });
});

describe("getEvalPhasePayloadData / getEvalPhaseLabel", () => {
  it("returns the nested phase record when present", () => {
    const data = getEvalPhasePayloadData({ data: { judge: { current: 1 } } }, "judge");
    expect(data).toEqual({ current: 1 });
  });

  it("falls back to the event data when the phase key is absent", () => {
    const data = getEvalPhasePayloadData({ data: { current: 1 } }, "judge");
    expect(data).toEqual({ current: 1 });
  });

  it("labels judge/rag/default phases distinctly", () => {
    expect(getEvalPhaseLabel("judge")).toBe(t("selfEvolutionRun.evalPhaseJudge"));
    expect(getEvalPhaseLabel("rag")).toBe(t("selfEvolutionRun.evalPhaseRag"));
    expect(getEvalPhaseLabel(undefined)).toBe(t("selfEvolutionRun.evalPhaseDefault"));
  });
});

describe("getEvalProgressStatusLabel", () => {
  it("returns done/canceled/paused labels based on the action", () => {
    expect(getEvalProgressStatusLabel("finish", "judge")).toBe(t("selfEvolutionRun.segmentDone", { label: t("selfEvolutionRun.evalPhaseJudge") }));
    expect(getEvalProgressStatusLabel("cancel", "rag")).toBe(t("selfEvolutionRun.segmentCanceled", { label: t("selfEvolutionRun.evalPhaseRag") }));
  });

  it("defaults to the active label otherwise", () => {
    expect(getEvalProgressStatusLabel("progress", "rag")).toBe(t("selfEvolutionRun.segmentActive", { label: t("selfEvolutionRun.evalPhaseRag") }));
  });
});

describe("updateProgressStatusText / mergeProgressSnapshot", () => {
  it("replaces only the status text, keeping other fields", () => {
    const updated = updateProgressStatusText({ statusText: "old", percent: 50 }, "new");
    expect(updated).toEqual({ statusText: "new", percent: 50 });
  });

  it("returns undefined unchanged when there is no snapshot", () => {
    expect(updateProgressStatusText(undefined, "new")).toBeUndefined();
  });

  it("prefers the higher-ranked snapshot", () => {
    const current = { statusText: "a", percent: 50, rank: 10 };
    const next = { statusText: "b", percent: 40, rank: 5 };
    expect(mergeProgressSnapshot(current, next)).toBe(current);
  });

  it("keeps current progress if next regresses percent with the same status text", () => {
    const current = { statusText: "same", percent: 50 };
    const next = { statusText: "same", percent: 40 };
    expect(mergeProgressSnapshot(current, next)).toBe(current);
  });

  it("returns whichever snapshot is defined when the other is missing", () => {
    const next = { statusText: "b", percent: 40 };
    expect(mergeProgressSnapshot(undefined, next)).toBe(next);
  });
});

describe("createEvalProgressPhaseSnapshot / getDefaultEvalProgressPhases / getCompletedEvalProgressPhases", () => {
  it("uses the provided progress or falls back to waiting-to-start", () => {
    const withProgress = createEvalProgressPhaseSnapshot("rag", { statusText: "running", percent: 30 });
    expect(withProgress.percent).toBe(30);
    const withoutProgress = createEvalProgressPhaseSnapshot("judge");
    expect(withoutProgress.statusText).toBe(t("selfEvolutionRun.waitingToStart"));
  });

  it("builds two default phases both at 0%", () => {
    const phases = getDefaultEvalProgressPhases();
    expect(phases).toHaveLength(2);
    expect(phases.every((phase) => phase.percent === 0)).toBe(true);
  });

  it("builds two completed phases both at 100%", () => {
    const phases = getCompletedEvalProgressPhases();
    expect(phases.every((phase) => phase.percent === 100)).toBe(true);
  });
});

describe("getEvalOverallProgressSnapshot", () => {
  it("returns undefined for an empty phase list", () => {
    expect(getEvalOverallProgressSnapshot([])).toBeUndefined();
  });

  it("averages the phase percentages and marks done when all are complete", () => {
    const phases = getCompletedEvalProgressPhases();
    const snapshot = getEvalOverallProgressSnapshot(phases);
    expect(snapshot?.percent).toBe(100);
    expect(snapshot?.statusText).toBe(t("selfEvolutionRun.statusDone"));
  });

  it("reports the active phase status while in progress", () => {
    const phases = getDefaultEvalProgressPhases().map((phase, index) =>
      index === 0 ? { ...phase, percent: 40, statusText: "running-rag" } : phase,
    );
    const snapshot = getEvalOverallProgressSnapshot(phases);
    expect(snapshot?.statusText).toBe("running-rag");
  });
});

describe("updateEvalProgressPhases", () => {
  it("marks both phases complete on a non-operation-scoped judge finish", () => {
    const phases = updateEvalProgressPhases(undefined, "judge", undefined, "finish", false);
    expect(phases.every((phase) => phase.percent === 100)).toBe(true);
  });

  it("updates only the targeted phase and completes rag when moving to judge", () => {
    const initial = getDefaultEvalProgressPhases();
    const phases = updateEvalProgressPhases(initial, "judge", { statusText: "judging", percent: 20 }, "progress");
    const ragPhase = phases.find((phase) => phase.id === "rag");
    const judgePhase = phases.find((phase) => phase.id === "judge");
    expect(ragPhase?.percent).toBe(100);
    expect(judgePhase?.percent).toBe(20);
  });

  it("applies an overall progress snapshot to every phase when no phase is given", () => {
    const phases = updateEvalProgressPhases(undefined, undefined, { statusText: "x", percent: 55 }, "progress");
    expect(phases.every((phase) => phase.percent === 55)).toBe(true);
  });
});

describe("getWorkflowProgressSnapshot", () => {
  it("returns undefined for a stage without progress tracking", () => {
    expect(getWorkflowProgressSnapshot("repair", "progress", {})).toBeUndefined();
  });

  it("delegates to the dataset snapshot builder for the dataset stage", () => {
    const snapshot = getWorkflowProgressSnapshot("dataset", "finish", { data: { stage: "dataset" } });
    expect(snapshot).toEqual(getCompletedProgressSnapshot());
  });

  it("computes an eval-stage snapshot from current/total counters", () => {
    const snapshot = getWorkflowProgressSnapshot("eval", "progress", { data: { current: 3, total: 6 } }, "eval.progress");
    expect(snapshot?.percent).toBe(50);
  });
});

describe("isAbtestStageCompleteEvent", () => {
  it("returns true for a finished abtest cutover artifact", () => {
    expect(
      isAbtestStageCompleteEvent({ stage: "abtest", action: "finish", payload: { data: { artifact_id: "candidate_algorithm_cutover" } } }),
    ).toBe(true);
  });

  it("returns false for a non-abtest stage", () => {
    expect(isAbtestStageCompleteEvent({ stage: "eval", action: "finish", payload: {} })).toBe(false);
  });
});

describe("isIntentSidecarOperation", () => {
  it("detects intent.* and dataset.assemble.intervention.* operation ids", () => {
    expect(isIntentSidecarOperation({ payload: { data: { operation_run_id: "intent.suggest" } } })).toBe(true);
    expect(isIntentSidecarOperation({ payload: { operation_run_id: "dataset.assemble.intervention.x" } })).toBe(true);
  });

  it("returns false for unrelated operation ids", () => {
    expect(isIntentSidecarOperation({ payload: { operation_run_id: "dataset.load_corpus" } })).toBe(false);
  });
});

describe("isStepFinishEvent", () => {
  it("returns false when the action is not a finish action", () => {
    expect(isStepFinishEvent({ action: "progress", progress: undefined, progressPhase: undefined, payload: {}, stage: "dataset" })).toBe(false);
  });

  it("returns false when scoped to an in-flight operation run id", () => {
    expect(
      isStepFinishEvent({
        action: "finish",
        progress: undefined,
        progressPhase: undefined,
        payload: { operation_run_id: "dataset.generate_case" },
        stage: "dataset",
      }),
    ).toBe(false);
  });

  it("returns true for an eval finish once the judge phase completes", () => {
    expect(
      isStepFinishEvent({ action: "finish", progress: undefined, progressPhase: "judge", payload: {}, stage: "eval" }),
    ).toBe(true);
  });

  it("returns false for an eval finish still in the rag phase", () => {
    expect(
      isStepFinishEvent({ action: "finish", progress: undefined, progressPhase: "rag", payload: {}, stage: "eval" }),
    ).toBe(false);
  });
});
