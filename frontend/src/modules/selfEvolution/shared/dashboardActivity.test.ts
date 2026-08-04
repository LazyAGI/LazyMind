import { describe, expect, it } from "vitest";
import {
  buildEventActivity,
  eventActivityTitle,
  eventActivityTone,
  formatOperationRunId,
  getActivityArtifactKind,
  getActivityArtifactLabel,
  getStageLogicalTaskCount,
  isCutoverActivity,
  isCutoverCompletedEvent,
  operationFlowKindFromRef,
  shouldShowProcessActivity,
  stageProgressFromEvents,
} from "./dashboardActivity";
import type { EvoStageActivity, NormalizedThreadEvent } from "./types";

function makeEvent(overrides: Partial<NormalizedThreadEvent>): NormalizedThreadEvent {
  return { key: "k", type: "x", ...overrides };
}

describe("eventActivityTone", () => {
  it("classifies autooperator/checkpoint/message events distinctly", () => {
    expect(eventActivityTone(makeEvent({ type: "autooperator.step" }))).toBe("auto");
    expect(eventActivityTone(makeEvent({ type: "checkpoint.wait" }))).toBe("checkpoint");
    expect(eventActivityTone(makeEvent({ type: "message.user" }))).toBe("message");
  });

  it("classifies failed actions as error and progress events as progress", () => {
    expect(eventActivityTone(makeEvent({ type: "dataset.progress", action: "failed" }))).toBe("error");
    expect(eventActivityTone(makeEvent({ type: "dataset.progress", progress: { statusText: "x", percent: 10 } }))).toBe("progress");
  });
});

describe("eventActivityTitle", () => {
  it("returns fixed labels for checkpoint kinds", () => {
    expect(eventActivityTitle(makeEvent({ type: "checkpoint.wait" }))).toBeTruthy();
    expect(eventActivityTitle(makeEvent({ type: "checkpoint.wait" }))).not.toBe(
      eventActivityTitle(makeEvent({ type: "checkpoint.continue" })),
    );
  });

  it("falls back to a formatted operation run id when present", () => {
    const title = eventActivityTitle(
      makeEvent({ type: "dataset.progress", payload: { operation_run_id: "dataset.generate_case" } }),
    );
    expect(title).toContain("dataset");
  });
});

describe("formatOperationRunId", () => {
  it("inserts spaces around stage prefixes and underscores", () => {
    expect(formatOperationRunId("dataset.generate_case")).toBe("dataset · generate case");
  });

  it("normalizes a trailing case.<number> segment", () => {
    expect(formatOperationRunId("eval.case.3")).toContain("case 3");
  });
});

describe("getActivityArtifactKind", () => {
  it("returns the analysis-reports kind for repair analysis artifacts", () => {
    const kind = getActivityArtifactKind(
      makeEvent({ stage: "repair", payload: { data: { artifact_id: "repair_loop_plan" } } }),
    );
    expect(kind).toBe("analysis-reports");
  });

  it("returns undefined when there's no stage or the event is a checkpoint.created", () => {
    expect(getActivityArtifactKind(makeEvent({ type: "checkpoint.created", stage: "dataset" }))).toBeUndefined();
    expect(getActivityArtifactKind(makeEvent({}))).toBeUndefined();
  });
});

describe("getActivityArtifactLabel", () => {
  it("builds a label for a known artifact kind", () => {
    expect(getActivityArtifactLabel("datasets")).toBeTruthy();
  });

  it("returns undefined for no artifact kind", () => {
    expect(getActivityArtifactLabel(undefined)).toBeUndefined();
  });
});

describe("buildEventActivity", () => {
  it("builds an activity summary using the event's display text and stage", () => {
    const activity: EvoStageActivity = buildEventActivity(
      makeEvent({ stage: "dataset", displayText: "Generating cases", timestamp: "2026-01-01T00:00:00Z" }),
    );
    expect(activity.detail).toBe("Generating cases");
    expect(activity.stage).toBe("dataset");
  });

  it("falls back to the compacted payload or event type when there's no display text", () => {
    const activity = buildEventActivity(makeEvent({ type: "dataset.custom" }));
    expect(activity.detail).toBeTruthy();
  });
});

describe("stageProgressFromEvents", () => {
  it("returns the last progress snapshot for a given stage", () => {
    const events: NormalizedThreadEvent[] = [
      makeEvent({ stage: "dataset", progress: { statusText: "a", percent: 10 } }),
      makeEvent({ stage: "dataset", progress: { statusText: "b", percent: 50 } }),
    ];
    expect(stageProgressFromEvents(events, "dataset")?.percent).toBe(50);
  });

  it("returns undefined when there is no matching progress event", () => {
    expect(stageProgressFromEvents([], "dataset")).toBeUndefined();
  });
});

describe("shouldShowProcessActivity", () => {
  it("hides checkpoint.created and terminal events", () => {
    expect(shouldShowProcessActivity(makeEvent({ type: "checkpoint.created" }))).toBe(false);
    expect(shouldShowProcessActivity(makeEvent({ type: "done" }))).toBe(false);
  });

  it("shows events that have display text or progress", () => {
    expect(shouldShowProcessActivity(makeEvent({ displayText: "hi" }))).toBe(true);
    expect(shouldShowProcessActivity(makeEvent({}))).toBe(false);
  });
});

describe("isCutoverActivity / isCutoverCompletedEvent", () => {
  it("identifies the abtest candidate cutover artifact activity", () => {
    const activity: EvoStageActivity = {
      key: "k",
      stage: "abtest",
      title: "t",
      detail: "d",
      time: "",
      tone: "normal",
      artifactId: "candidate_algorithm_cutover",
    };
    expect(isCutoverActivity(activity)).toBe(true);
  });

  it("identifies a completed cutover event by flow kind or artifact id plus finish/100%", () => {
    const event = makeEvent({
      stage: "abtest",
      action: "finish",
      payload: { data: { artifact_id: "candidate_algorithm_cutover" } },
    });
    expect(isCutoverCompletedEvent(event)).toBe(true);
  });

  it("returns false for a non-abtest stage event", () => {
    expect(isCutoverCompletedEvent(makeEvent({ stage: "dataset" }))).toBe(false);
  });
});

describe("getStageLogicalTaskCount", () => {
  it("counts distinct operation run ids relevant to the stage", () => {
    const events: NormalizedThreadEvent[] = [
      makeEvent({ payload: { operation_run_id: "eval.rag.case1" } }),
      makeEvent({ payload: { operation_run_id: "eval.rag.case2" } }),
    ];
    expect(getStageLogicalTaskCount(events, "eval")).toBe(2);
  });

  it("falls back to the raw event count when no operation ids are found", () => {
    const events: NormalizedThreadEvent[] = [makeEvent({}), makeEvent({})];
    expect(getStageLogicalTaskCount(events, "dataset")).toBe(2);
  });
});

describe("operationFlowKindFromRef", () => {
  it("classifies rag/judge/aggregate refs for eval", () => {
    expect(operationFlowKindFromRef("eval.rag.case1")).toBe("eval.rag_answer");
    expect(operationFlowKindFromRef("eval.judge.case1")).toBe("eval.judge_answer");
    expect(operationFlowKindFromRef("eval.aggregate")).toBe("eval.aggregate");
  });

  it("returns an empty string for unrecognized refs", () => {
    expect(operationFlowKindFromRef("dataset.something")).toBe("");
  });
});
