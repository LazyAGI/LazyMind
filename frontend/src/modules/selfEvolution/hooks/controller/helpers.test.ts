import { describe, expect, it, vi } from "vitest";

vi.mock("@/components/request", () => ({
  BASE_URL: "https://example.com",
  axiosInstance: { get: vi.fn(), post: vi.fn() },
}));

import type { NormalizedThreadEvent, ThreadStepListState } from "../../shared";
import {
  buildAffectedBlockCountRows,
  buildAnalysisActionableCaseRows,
  buildAnalysisCategorySummaryRows,
  buildCheckpointPromptForCompletedStage,
  buildCompletedFlowCheckpointPrompt,
  buildDatasetCasePreviewRowFromArtifact,
  buildDatasetCasePreviewRows,
  buildDatasetQuestionTypeCounts,
  buildPxCaseDetailRows,
  buildStreamingAbtestCaseRows,
  buildStreamingAnalysisCaseRows,
  buildStreamingDatasetCaseRows,
  buildStreamingEvalCaseRows,
  buildThreadStepEventsStreamUrl,
  buildThreadStepStatusByStage,
  extractAnalysisSummaryContent,
  extractDatasetArtifactData,
  getAnalysisCategoryCount,
  getBooleanishField,
  getCheckpointWaitingStep,
  getDatasetTotalCaseCount,
  getDefaultThreadStep,
  getEvalReportBadCaseListRecords,
  getEvalReportBadCasesPayloadRecord,
  getEvalReportId,
  getEvalReportPayloadRecord,
  getEvalReportSourceRecord,
  getNextStepRunId,
  getSilentRestoreRequestConfig,
  getStreamingAbtestProgress,
  getStreamingAnalysisProgress,
  getStreamingDatasetProgress,
  getStreamingEvalProgress,
  humanizeFinalResultReason,
  isCheckpointContinueCommand,
  isCheckpointPromptSuperseded,
  isStepCheckpointWaiting,
  isThreadFlowRunning,
  isThreadStepRunning,
  mergeEvalReportBadCasesResponses,
  normalizeThreadStepListPayload,
  normalizeThreadStepStatus,
  resolveArtifactItemForThreadStep,
  resolveCaseArtifactId,
  resolveCheckpointAwareStepStatus,
  resolveContinueThreadStepId,
  resolveNextStepRunIdFromStepList,
  resolveStepListCheckpointPrompt,
  resolveSubscribeThreadStepId,
  resolveThreadStepViewStage,
  shouldUseEventTraceStream,
  sortThreadStepsByOrder,
  formatSignedFinalPercent,
  getFinalResultMetricLabel,
  getFinalResultMetricLabels,
  applyThreadStepListToWorkflowRuntimeState,
  markThreadStepStageCompleted,
  waitForSubscribableThreadStep,
} from "./helpers";
import { createInitialWorkflowRuntimeState } from "../../shared/runtimeState";

const t = (key: string, options?: Record<string, unknown>) => `${key}${options ? `:${JSON.stringify(options)}` : ""}`;

function makeEvent(overrides: Partial<NormalizedThreadEvent> = {}): NormalizedThreadEvent {
  return {
    key: "evt-1",
    type: "message",
    payload: {},
    ...overrides,
  };
}

function makeStepList(steps: ThreadStepListState["steps"], activeStepId?: string): ThreadStepListState {
  return { steps, activeStepId };
}

describe("extractDatasetArtifactData", () => {
  it("returns the record directly when it already looks like a dataset artifact", () => {
    const value = { cases: [{ case_id: "c1" }] };
    expect(extractDatasetArtifactData(value)).toBe(value);
  });

  it("unwraps a nested data field that looks like a dataset artifact", () => {
    const value = { data: { case_num: 3 } };
    expect(extractDatasetArtifactData(value)).toEqual({ case_num: 3 });
  });

  it("prefers the item whose artifact_id matches eval_dataset among result items", () => {
    const value = {
      items: [
        { artifact_id: "other", data: { case_num: 1 } },
        { artifact_id: "eval_dataset", data: { case_num: 5 } },
      ],
    };
    expect(extractDatasetArtifactData(value)).toEqual({ case_num: 5 });
  });

  it("returns undefined for non-record input", () => {
    expect(extractDatasetArtifactData("nope")).toBeUndefined();
  });
});

describe("buildDatasetCasePreviewRows", () => {
  it("builds preview rows from a cases array", () => {
    const rows = buildDatasetCasePreviewRows({
      cases: [{ case_id: "c1", question: "q1", answer: "a1", question_type: "factual", difficulty: "easy" }],
    });
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ caseId: "c1", question: "q1", answer: "a1", questionType: "factual", difficulty: "easy" });
  });

  it("falls back to a generated case id and dash placeholders when data is undefined", () => {
    const rows = buildDatasetCasePreviewRows(undefined);
    expect(rows).toEqual([]);
  });
});

describe("buildDatasetQuestionTypeCounts", () => {
  it("reads legacy question_type_counts from stats when present", () => {
    const counts = buildDatasetQuestionTypeCounts({ stats: { question_type_counts: { factual: 3, comparison: 2 } } });
    expect(counts).toEqual({ factual: 3, comparison: 2 });
  });

  it("falls back to counting question_type across the cases array", () => {
    const counts = buildDatasetQuestionTypeCounts({
      cases: [{ question_type: "factual" }, { question_type: "factual" }, {}],
    });
    expect(counts).toEqual({ factual: 2, unknown: 1 });
  });
});

describe("getDatasetTotalCaseCount", () => {
  it("prefers the explicit case_num field", () => {
    expect(getDatasetTotalCaseCount({ case_num: 10 }, 3)).toBe(10);
  });

  it("falls back to the shown count when no total fields are present", () => {
    expect(getDatasetTotalCaseCount({}, 4)).toBe(4);
  });
});

describe("buildStreamingDatasetCaseRows", () => {
  it("tracks generate/prepare status per case across multiple events", () => {
    const rows = buildStreamingDatasetCaseRows([
      makeEvent({ stage: "dataset", payload: { event_type: "dataset.generate_case", case_id: "c1", action: "running" } }),
      makeEvent({ stage: "dataset", payload: { event_type: "dataset.generate_case", case_id: "c1", action: "completed" } }),
      makeEvent({ stage: "dataset", payload: { event_type: "dataset.prepare_case", case_id: "c1", action: "running" } }),
    ]);
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ caseId: "c1", generateStatus: "done", prepareStatus: "running" });
  });

  it("ignores events from other stages or without a case id", () => {
    const rows = buildStreamingDatasetCaseRows([
      makeEvent({ stage: "eval", payload: { event_type: "dataset.generate_case", case_id: "c1", action: "running" } }),
      makeEvent({ stage: "dataset", payload: { event_type: "dataset.generate_case", action: "running" } }),
    ]);
    expect(rows).toEqual([]);
  });
});

describe("getStreamingDatasetProgress", () => {
  it("computes the max current/total across dataset generate_case events", () => {
    const progress = getStreamingDatasetProgress([
      makeEvent({ stage: "dataset", payload: { event_type: "dataset.generate_case", progress: { current: 2, total: 10 } } }),
      makeEvent({ stage: "dataset", payload: { event_type: "dataset.generate_case", progress: { current: 5, total: 10 } } }),
    ]);
    expect(progress).toEqual({ current: 5, total: 10 });
  });

  it("returns zero progress when there are no matching events", () => {
    expect(getStreamingDatasetProgress([])).toEqual({ current: 0, total: 0 });
  });
});

describe("buildStreamingAnalysisCaseRows", () => {
  it("marks trace summary done once classify_case finishes", () => {
    const rows = buildStreamingAnalysisCaseRows([
      makeEvent({ stage: "analysis", payload: { event_type: "analysis.trace_summary", case_id: "c1", action: "running" } }),
      makeEvent({ stage: "analysis", payload: { event_type: "analysis.classify_case", case_id: "c1", action: "completed" } }),
    ]);
    expect(rows[0]).toMatchObject({ caseId: "c1", traceSummaryStatus: "done", classifyCaseStatus: "done" });
  });
});

describe("getStreamingAnalysisProgress", () => {
  it("counts completed cases from classify_case events when progress fields are absent", () => {
    const progress = getStreamingAnalysisProgress([
      makeEvent({ stage: "analysis", payload: { event_type: "analysis.classify_case", case_id: "c1", action: "completed" } }),
      makeEvent({ stage: "analysis", payload: { event_type: "analysis.classify_case", case_id: "c2", action: "completed" } }),
    ]);
    expect(progress.current).toBe(2);
  });
});

describe("buildStreamingEvalCaseRows / getStreamingEvalProgress", () => {
  it("tracks answer/judge status per case", () => {
    const rows = buildStreamingEvalCaseRows([
      makeEvent({ stage: "eval", payload: { event_type: "eval.answer", case_id: "c1", action: "completed" } }),
      makeEvent({ stage: "eval", payload: { event_type: "eval.judge", case_id: "c1", action: "running" } }),
    ]);
    expect(rows[0]).toMatchObject({ caseId: "c1", answerStatus: "done", judgeStatus: "running" });
  });

  it("prefers judge progress but falls back to answer progress", () => {
    const answerOnly = getStreamingEvalProgress([
      makeEvent({ stage: "eval", payload: { event_type: "eval.answer", case_id: "c1", action: "running" } }),
    ]);
    expect(answerOnly.total).toBeGreaterThan(0);
  });
});

describe("buildStreamingAbtestCaseRows / getStreamingAbtestProgress", () => {
  it("tracks answer/judge status for abtest candidate events", () => {
    const rows = buildStreamingAbtestCaseRows([
      makeEvent({ stage: "abtest", payload: { event_type: "abtest.candidate_answer", case_id: "c1", action: "completed" } }),
      makeEvent({ stage: "abtest", payload: { event_type: "abtest.candidate_judge", case_id: "c1", action: "running" } }),
    ]);
    expect(rows[0]).toMatchObject({ caseId: "c1", answerStatus: "done", judgeStatus: "running" });
  });

  it("falls back to answer progress when there's no judge progress", () => {
    const progress = getStreamingAbtestProgress([
      makeEvent({ stage: "abtest", payload: { event_type: "abtest.candidate_answer", case_id: "c1", action: "running" } }),
    ]);
    expect(progress.total).toBeGreaterThan(0);
  });
});

describe("buildDatasetCasePreviewRowFromArtifact", () => {
  it("builds a preview row keyed by the given case id when the artifact has no id", () => {
    const row = buildDatasetCasePreviewRowFromArtifact("c1", { question: "q1", answer: "a1" });
    expect(row).toMatchObject({ caseId: "c1", question: "q1", answer: "a1" });
  });

  it("returns just the case id when the value isn't a record", () => {
    expect(buildDatasetCasePreviewRowFromArtifact("c1", "nope")).toEqual({ caseId: "c1" });
  });
});

describe("resolveCaseArtifactId", () => {
  it("appends [caseId] before any @v version suffix", () => {
    expect(resolveCaseArtifactId("eval.report@v1", "c1")).toBe("eval.report[c1]@v1");
  });

  it("returns the artifact id unchanged when case id is empty", () => {
    expect(resolveCaseArtifactId("eval.report", "")).toBe("eval.report");
  });

  it("leaves already-bracketed artifact ids untouched", () => {
    expect(resolveCaseArtifactId("eval.report[c1]", "c2")).toBe("eval.report[c1]");
  });
});

describe("getFinalResultMetricLabel / getFinalResultMetricLabels", () => {
  it("returns the mapped label for a known metric key", () => {
    const labels = getFinalResultMetricLabels(t);
    expect(labels.answer_correctness).toContain("metricAnswerCorrectness");
  });

  it("strips _avg/_mean suffixes before matching a known metric", () => {
    expect(getFinalResultMetricLabel(t, "answer_correctness_avg")).toContain("metricAnswerCorrectness");
  });

  it("falls back to the generic overall label for unknown metrics without a usable fallback", () => {
    expect(getFinalResultMetricLabel(t, "totally_unknown_metric")).toContain("metricOverall");
  });
});

describe("formatSignedFinalPercent", () => {
  it("adds a plus sign for positive values and formats as a percentage", () => {
    expect(formatSignedFinalPercent(0.123)).toBe("+12.3%");
  });

  it("does not add a plus sign for negative or zero values", () => {
    expect(formatSignedFinalPercent(-0.05)).toBe("-5.0%");
    expect(formatSignedFinalPercent(0)).toBe("0.0%");
  });
});

describe("humanizeFinalResultReason", () => {
  it("formats a primary metric delta below target reason", () => {
    const result = humanizeFinalResultReason(t, "primary metric delta -0.05 < target 0.0", "正确性");
    expect(result).toContain("reasonPrimaryBelowTarget");
  });

  it("formats a goodcase regression ratio reason", () => {
    const result = humanizeFinalResultReason(t, "goodcase regression ratio 0.2 <= limit 0.1", "正确性");
    expect(result).toContain("reasonRegressionExceeds");
  });

  it("falls back to generic text substitution for unmatched reasons", () => {
    const result = humanizeFinalResultReason(t, "primary metric is below target", "正确性");
    expect(result).toContain("正确性");
  });
});

describe("getBooleanishField", () => {
  it("parses boolean-ish strings and numbers", () => {
    expect(getBooleanishField({ active: "true" }, ["active"])).toBe(true);
    expect(getBooleanishField({ active: 0 }, ["active"])).toBe(false);
  });

  it("returns false when no key matches a boolean-ish value", () => {
    expect(getBooleanishField({}, ["active"])).toBe(false);
  });
});

describe("normalizeThreadStepStatus", () => {
  it("maps varied raw status strings to canonical StepStatus values", () => {
    expect(normalizeThreadStepStatus("进行中")).toBe("running");
    expect(normalizeThreadStepStatus("已完成")).toBe("done");
    expect(normalizeThreadStepStatus("失败")).toBe("failed");
  });

  it("returns undefined for unrecognized or missing status", () => {
    expect(normalizeThreadStepStatus(undefined)).toBeUndefined();
    expect(normalizeThreadStepStatus("weird")).toBeUndefined();
  });
});

describe("isThreadStepRunning / isThreadFlowRunning", () => {
  it("derives running state from the normalized status when available", () => {
    expect(isThreadStepRunning({ stepId: "s1", active: false, status: "running" })).toBe(true);
  });

  it("falls back to the active flag when status can't be normalized", () => {
    expect(isThreadStepRunning({ stepId: "s1", active: true, status: "weird" })).toBe(true);
  });

  it("isThreadFlowRunning only reports true for a running flow status", () => {
    expect(isThreadFlowRunning("running")).toBe(true);
    expect(isThreadFlowRunning("paused")).toBe(false);
  });
});

describe("getSilentRestoreRequestConfig", () => {
  it("returns a config carrying the signal and silentError flag", () => {
    const controller = new AbortController();
    const config = getSilentRestoreRequestConfig(controller.signal);
    expect(config).toMatchObject({ signal: controller.signal, silentError: true });
  });
});

describe("shouldUseEventTraceStream / buildThreadStepEventsStreamUrl", () => {
  it("uses the event-trace stream only for the repair stage", () => {
    expect(shouldUseEventTraceStream({ stepId: "s1", active: false, stage: "apply" })).toBe(true);
    expect(shouldUseEventTraceStream({ stepId: "s1", active: false, stage: "dataset" })).toBe(false);
  });

  it("builds a URL using the event-trace segment for repair steps", () => {
    const url = buildThreadStepEventsStreamUrl("thread-1", "step-1", { stepId: "step-1", active: false, stage: "repair" });
    expect(url).toContain("event-trace:stream");
    expect(url).toContain("step_id=step-1");
  });

  it("builds a URL using the plain events segment when no step or a non-repair stage is given", () => {
    const url = buildThreadStepEventsStreamUrl("thread-1", "step-1");
    expect(url).toContain("events:stream");
  });
});

describe("normalizeThreadStepListPayload", () => {
  it("parses steps from a payload and marks the active step based on active_step_id", () => {
    const state = normalizeThreadStepListPayload({
      active_step_id: "s2",
      steps: [
        { step_id: "s1", stage: "dataset", status: "done" },
        { step_id: "s2", stage: "eval", status: "running" },
      ],
    });
    expect(state.activeStepId).toBe("s2");
    expect(state.steps).toHaveLength(2);
    expect(state.steps[1].active).toBe(true);
    expect(state.steps[0].active).toBe(false);
  });

  it("skips step records without a usable step id", () => {
    const state = normalizeThreadStepListPayload({ steps: [{ stage: "dataset" }] });
    expect(state.steps).toEqual([]);
  });
});

describe("buildThreadStepStatusByStage", () => {
  it("maps each recognized step's stage to its normalized status", () => {
    const result = buildThreadStepStatusByStage(
      makeStepList([{ stepId: "s1", active: false, stage: "dataset", status: "done" }]),
    );
    expect(result.dataset).toBe("done");
  });
});

describe("applyThreadStepListToWorkflowRuntimeState", () => {
  it("returns the previous state unchanged when there are no steps", () => {
    const prev = createInitialWorkflowRuntimeState();
    expect(applyThreadStepListToWorkflowRuntimeState(prev, makeStepList([]))).toBe(prev);
  });

  it("applies done status and a completed progress snapshot for finished steps", () => {
    const prev = createInitialWorkflowRuntimeState();
    const next = applyThreadStepListToWorkflowRuntimeState(
      prev,
      makeStepList([{ stepId: "s1", active: false, stage: "dataset", status: "done" }]),
    );
    expect(next.dataset.status).toBe("done");
    expect(next.dataset.progress?.percent).toBe(100);
  });
});

describe("getDefaultThreadStep", () => {
  it("prefers the active step matched by activeStepId", () => {
    const stepList = makeStepList(
      [{ stepId: "s1", active: false }, { stepId: "s2", active: false }],
      "s2",
    );
    expect(getDefaultThreadStep(stepList)?.stepId).toBe("s2");
  });

  it("falls back to the last step in the list when there's no active step match", () => {
    const stepList = makeStepList([{ stepId: "s1", active: false }, { stepId: "s2", active: false }]);
    expect(getDefaultThreadStep(stepList)?.stepId).toBe("s2");
  });
});

describe("resolveNextStepRunIdFromStepList / resolveContinueThreadStepId", () => {
  it("finds the most recent non-running step with a next_step_run_id", () => {
    const stepList = makeStepList([
      { stepId: "s1", active: false, status: "done", nextStepRunId: "run-1" },
      { stepId: "s2", active: false, status: "running" },
    ]);
    expect(resolveNextStepRunIdFromStepList(stepList)).toBe("run-1");
  });

  it("resolveContinueThreadStepId prefers the checkpoint waiting step's nextStepRunId", () => {
    const stepList = makeStepList([
      { stepId: "s1", active: false, stage: "dataset", status: "done", nextStepRunId: "run-1" },
    ]);
    expect(resolveContinueThreadStepId(stepList)).toBe("run-1");
  });
});

describe("resolveArtifactItemForThreadStep", () => {
  it("matches an artifact item by the kind mapped from the step's stage", () => {
    const items = [{ kind: "eval-reports" }, { kind: "datasets" }];
    const match = resolveArtifactItemForThreadStep(
      { stepId: "s1", active: false, stage: "dataset" },
      1,
      items,
      { dataset: "datasets", eval: "eval-reports" },
    );
    expect(match).toEqual({ kind: "datasets" });
  });

  it("falls back to the item at fallbackIndex when there's no stage-based match", () => {
    const items = [{ kind: "a" }, { kind: "b" }];
    expect(resolveArtifactItemForThreadStep({ stepId: "s1", active: false }, 1, items, {})).toEqual({ kind: "b" });
  });
});

describe("resolveThreadStepViewStage", () => {
  it("prefers the stage derived directly from the step", () => {
    expect(resolveThreadStepViewStage({ stepId: "s1", active: false, stage: "eval" })).toBe("eval");
  });

  it("falls back to the workflow step id map when the step has no usable stage", () => {
    const stage = resolveThreadStepViewStage(
      { stepId: "s1", active: false },
      "px-report",
      { "px-report": "eval" },
    );
    expect(stage).toBe("eval");
  });
});

describe("sortThreadStepsByOrder", () => {
  it("sorts steps by orderIndex ascending, with undefined order last", () => {
    const sorted = sortThreadStepsByOrder([
      { stepId: "b", active: false, orderIndex: 2 },
      { stepId: "a", active: false, orderIndex: 1 },
      { stepId: "c", active: false },
    ]);
    expect(sorted.map((step) => step.stepId)).toEqual(["a", "b", "c"]);
  });
});

describe("isStepCheckpointWaiting", () => {
  it("is true for a done step that still has a next_step_run_id and is not running", () => {
    expect(isStepCheckpointWaiting({ stepId: "s1", active: false, status: "done", nextStepRunId: "run-1" })).toBe(true);
  });

  it("is false when the step is currently running", () => {
    expect(isStepCheckpointWaiting({ stepId: "s1", active: false, status: "running", nextStepRunId: "run-1" })).toBe(false);
  });
});

describe("resolveCheckpointAwareStepStatus", () => {
  it("keeps non-paused statuses unchanged", () => {
    expect(resolveCheckpointAwareStepStatus("done")).toBe("done");
  });

  it("resolves a paused status to done when the flow status is a checkpoint gate", () => {
    expect(resolveCheckpointAwareStepStatus("paused", { flowStatus: "paused" })).toBe("done");
  });

  it("keeps paused when none of the checkpoint conditions apply", () => {
    expect(resolveCheckpointAwareStepStatus("paused", {})).toBe("paused");
  });
});

describe("getCheckpointWaitingStep", () => {
  it("returns the last non-abtest step that is done and awaiting continuation", () => {
    const stepList = makeStepList([
      { stepId: "s1", active: false, stage: "dataset", status: "done", nextStepRunId: "run-1" },
    ]);
    expect(getCheckpointWaitingStep(stepList)?.stepId).toBe("s1");
  });

  it("returns undefined once a subsequent step has already started", () => {
    const stepList = makeStepList([
      { stepId: "s1", active: false, stage: "dataset", status: "done", nextStepRunId: "run-1", orderIndex: 0 },
      { stepId: "s2", active: false, stage: "eval", status: "running", orderIndex: 1 },
    ]);
    expect(getCheckpointWaitingStep(stepList)).toBeUndefined();
  });
});

describe("buildCheckpointPromptForCompletedStage", () => {
  it("builds a prompt pointing to the next stage in the pipeline", () => {
    const prompt = buildCheckpointPromptForCompletedStage("dataset");
    expect(prompt?.nextStage).toBe("eval");
    expect(prompt?.completedStage).toBe("dataset");
  });

  it("returns undefined for the last stage in the pipeline", () => {
    expect(buildCheckpointPromptForCompletedStage("abtest")).toBeUndefined();
  });
});

describe("isCheckpointPromptSuperseded / resolveStepListCheckpointPrompt", () => {
  it("treats failure prompts as never superseded", () => {
    const prompt = { message: "m", kind: "failure" as const, command: "c" };
    expect(isCheckpointPromptSuperseded(prompt, makeStepList([]))).toBe(false);
  });

  it("marks a checkpoint prompt superseded once its next stage has already started", () => {
    const prompt = { message: "m", command: "c", nextStage: "eval" as const };
    const stepList = makeStepList([{ stepId: "s1", active: false, stage: "eval", status: "running" }]);
    expect(isCheckpointPromptSuperseded(prompt, stepList)).toBe(true);
  });

  it("resolveStepListCheckpointPrompt returns a prompt for a completed non-terminal stage", () => {
    const stepList = makeStepList([
      { stepId: "s1", active: false, stage: "dataset", status: "done", nextStepRunId: "run-1" },
    ]);
    const prompt = resolveStepListCheckpointPrompt(stepList);
    expect(prompt?.completedStage).toBe("dataset");
  });

  it("resolveStepListCheckpointPrompt returns undefined when there's no waiting step and flow isn't paused", () => {
    const stepList = makeStepList([{ stepId: "s1", active: false, stage: "dataset", status: "running" }]);
    expect(resolveStepListCheckpointPrompt(stepList, "running")).toBeUndefined();
  });
});

describe("buildCompletedFlowCheckpointPrompt", () => {
  it("builds a prompt when the event payload status is completed and the stage isn't terminal", () => {
    const prompt = buildCompletedFlowCheckpointPrompt({ stage: "eval", payload: { status: "completed" } });
    expect(prompt?.nextStage).toBe("analysis");
  });

  it("returns undefined when the flow status isn't completed", () => {
    expect(buildCompletedFlowCheckpointPrompt({ stage: "eval", payload: { status: "running" } })).toBeUndefined();
  });
});

describe("markThreadStepStageCompleted", () => {
  it("marks steps matching the completed stage as done and clears activeStepId", () => {
    const stepList = makeStepList(
      [{ stepId: "s1", active: true, stage: "dataset", status: "running" }],
      "s1",
    );
    const next = markThreadStepStageCompleted(stepList, "dataset", "completed");
    expect(next.activeStepId).toBeUndefined();
    expect(next.steps[0]).toMatchObject({ status: "done", active: false });
  });
});

describe("isCheckpointContinueCommand", () => {
  it("matches when the text equals the checkpoint prompt's command", () => {
    const prompt = { message: "m", command: "继续本步骤" };
    expect(isCheckpointContinueCommand("继续本步骤", prompt, "继续执行", "继续")).toBe(true);
  });

  it("returns false for blank input", () => {
    expect(isCheckpointContinueCommand("   ", undefined, "继续执行", "继续")).toBe(false);
  });
});

describe("getNextStepRunId", () => {
  it("reads next_step_run_id directly from the event payload", () => {
    expect(getNextStepRunId(makeEvent({ payload: { next_step_run_id: "run-1" } }))).toBe("run-1");
  });

  it("falls back to nested payload.data.next_step_run_id", () => {
    expect(
      getNextStepRunId(makeEvent({ payload: { payload: { data: { next_step_run_id: "run-2" } } } })),
    ).toBe("run-2");
  });
});

describe("resolveSubscribeThreadStepId", () => {
  it("returns undefined while a checkpoint waiting step exists", () => {
    const stepList = makeStepList([
      { stepId: "s1", active: false, stage: "dataset", status: "done", nextStepRunId: "run-1" },
    ]);
    expect(resolveSubscribeThreadStepId(stepList)).toBeUndefined();
  });

  it("prefers the active step id when set", () => {
    const stepList = makeStepList([{ stepId: "s1", active: false }], "s1");
    expect(resolveSubscribeThreadStepId(stepList)).toBe("s1");
  });

  it("falls back to resolveFallbackStepId when there are no steps", () => {
    const result = resolveSubscribeThreadStepId(makeStepList([]), "thread-1", (id) => `fallback-${id}`);
    expect(result).toBe("fallback-thread-1");
  });
});

describe("waitForSubscribableThreadStep", () => {
  it("returns as soon as an active or running step is found", async () => {
    const fetchStepList = vi.fn().mockResolvedValue(
      makeStepList([{ stepId: "s1", active: true, status: "running" }]),
    );
    const result = await waitForSubscribableThreadStep(fetchStepList, { maxAttempts: 1, intervalMs: 0 });
    expect(result?.steps[0].stepId).toBe("s1");
    expect(fetchStepList).toHaveBeenCalledTimes(1);
  });

  it("returns undefined immediately when the signal is already aborted", async () => {
    const controller = new AbortController();
    controller.abort();
    const fetchStepList = vi.fn().mockResolvedValue(makeStepList([]));
    const result = await waitForSubscribableThreadStep(fetchStepList, { signal: controller.signal, maxAttempts: 3 });
    expect(result).toBeUndefined();
    expect(fetchStepList).not.toHaveBeenCalled();
  });
});

describe("getEvalReportSourceRecord / getEvalReportPayloadRecord / getEvalReportId", () => {
  it("resolves the report id from a wrapped result item", () => {
    const resultData = { items: [{ report_id: "r1", data: { id: "eval.report" } }] };
    expect(getEvalReportId(resultData)).toBe("r1");
  });

  it("returns undefined when no report id can be found anywhere", () => {
    expect(getEvalReportId({})).toBeUndefined();
  });

  it("getEvalReportPayloadRecord falls back to the source record when there's no nested data", () => {
    const source = getEvalReportSourceRecord({ report_id: "r1" });
    expect(getEvalReportPayloadRecord(source)).toEqual({ report_id: "r1" });
  });
});

describe("getEvalReportBadCaseListRecords / getEvalReportBadCasesPayloadRecord / mergeEvalReportBadCasesResponses", () => {
  it("extracts bad case items nested under data.items", () => {
    const records = getEvalReportBadCaseListRecords({ data: { items: [{ case_id: "c1" }] } });
    expect(records).toEqual([{ case_id: "c1" }]);
  });

  it("merges bad case items across multiple paginated responses", () => {
    const merged = mergeEvalReportBadCasesResponses([
      { data: { items: [{ case_id: "c1" }] } },
      { data: { items: [{ case_id: "c2" }] } },
    ]);
    expect(merged.totalSize).toBe(2);
    expect(merged.data.data.items).toHaveLength(2);
  });
});

describe("buildPxCaseDetailRows", () => {
  it("dedupes by case id and formats the score to two decimals", () => {
    const rows = buildPxCaseDetailRows([
      { case_id: "c1", query: "q1", metrics: { overall: 0.5 } },
      { case_id: "c1", query: "dup" },
    ]);
    expect(rows).toHaveLength(1);
    expect(rows[0].score).toBe("0.50");
  });
});

describe("getAnalysisCategoryCount", () => {
  it("reads a numeric count from a record value", () => {
    expect(getAnalysisCategoryCount({ count: 5 })).toBe(5);
  });

  it("parses a numeric string directly", () => {
    expect(getAnalysisCategoryCount("7")).toBe(7);
  });

  it("returns undefined for values that can't be interpreted as a count", () => {
    expect(getAnalysisCategoryCount(undefined)).toBeUndefined();
  });
});

describe("extractAnalysisSummaryContent", () => {
  it("returns the record directly when it already looks like an analysis summary", () => {
    const value = { actionable_cases: [] };
    expect(extractAnalysisSummaryContent(value)).toBe(value);
  });

  it("recurses into a nested content field", () => {
    const value = { content: { affected_block_counts: { a: 1 } } };
    expect(extractAnalysisSummaryContent(value)).toEqual({ affected_block_counts: { a: 1 } });
  });

  it("returns undefined when nothing matches", () => {
    expect(extractAnalysisSummaryContent({ unrelated: true })).toBeUndefined();
  });
});

describe("buildAnalysisActionableCaseRows", () => {
  it("maps actionable case records into display rows", () => {
    const rows = buildAnalysisActionableCaseRows({
      actionable_cases: [{ case_id: "c1", issue_type: "hallucination", confidence: "high" }],
    });
    expect(rows[0]).toMatchObject({ caseId: "c1", issueType: "hallucination", confidence: "high" });
  });

  it("returns an empty array when there are no actionable cases", () => {
    expect(buildAnalysisActionableCaseRows(undefined)).toEqual([]);
  });
});

describe("buildAffectedBlockCountRows / buildAnalysisCategorySummaryRows", () => {
  it("builds sorted, ratio-annotated rows from affected_block_counts", () => {
    const rows = buildAffectedBlockCountRows({ affected_block_counts: { retrieval: 3, generation: 1 } });
    expect(rows[0]).toMatchObject({ category: "retrieval", count: 3 });
    expect(rows[1]).toMatchObject({ category: "generation", count: 1 });
  });

  it("falls back to issue_category_counts when affected_block_counts is empty", () => {
    const rows = buildAffectedBlockCountRows({ issue_category_counts: { format: 2 } });
    expect(rows).toEqual([expect.objectContaining({ category: "format", count: 2 })]);
  });

  it("buildAnalysisCategorySummaryRows reads coarse_category_counts", () => {
    const rows = buildAnalysisCategorySummaryRows({ coarse_category_counts: { retrieval: 4 } });
    expect(rows[0]).toMatchObject({ category: "retrieval", count: 4 });
  });
});
