import { describe, expect, it } from "vitest";
import {
  applyTerminalFlowStepStatus,
  applyThreadStepStatusToWorkflowSteps,
  applyThreadStreamTerminalToState,
  buildVisibleWorkflowSteps,
  buildWorkflowStepRuntimeFromEvents,
  createCheckpointRestoreWorkflowRuntimeState,
  createInitialWorkflowResultsState,
  createInitialWorkflowRuntimeState,
  createThreadRestoreWorkflowRuntimeState,
  createWorkflowRuntimeStateForMode,
  createWorkflowStepFromRuntime,
  getStepStatusLabel,
  getTerminalFlowStepStatus,
  getTerminalOverrideStepIndex,
  getWorkflowStepIndex,
  reduceWorkflowRuntimeState,
  reduceWorkflowRuntimeStateFromEvents,
} from "./runtimeState";
import type { NormalizedThreadEvent, WorkflowRuntimeState, WorkflowStep } from "./types";

function makeEvent(overrides: Partial<NormalizedThreadEvent>): NormalizedThreadEvent {
  return { key: "k", type: "x", ...overrides };
}

function makeStep(overrides: Partial<WorkflowStep>): WorkflowStep {
  return { id: "dataset", title: "Dataset", desc: "", status: "pending", ...overrides };
}

describe("createInitialWorkflowRuntimeState / createThreadRestoreWorkflowRuntimeState", () => {
  it("starts the dataset step running for a fresh workflow", () => {
    expect(createInitialWorkflowRuntimeState().dataset.status).toBe("running");
  });

  it("starts all steps pending for a restored thread", () => {
    const state = createThreadRestoreWorkflowRuntimeState();
    expect(Object.values(state).every((step) => step.status === "pending")).toBe(true);
  });
});

describe("createWorkflowRuntimeStateForMode", () => {
  it("uses the initial state for auto mode and the restore state otherwise", () => {
    expect(createWorkflowRuntimeStateForMode("auto").dataset.status).toBe("running");
    expect(createWorkflowRuntimeStateForMode("interactive").dataset.status).toBe("pending");
  });
});

describe("createCheckpointRestoreWorkflowRuntimeState", () => {
  it("marks steps before the checkpoint's stage as done and the checkpoint stage itself as done", () => {
    const state = createCheckpointRestoreWorkflowRuntimeState({
      message: "paused",
      command: "continue",
      completedStage: "eval",
    });
    expect(state.dataset.status).toBe("done");
    expect(state["px-report"].status).toBe("done");
    expect(state.analysis.status).toBe("pending");
  });

  it("returns the plain restore state when there is no checkpoint", () => {
    const state = createCheckpointRestoreWorkflowRuntimeState(undefined);
    expect(state.dataset.status).toBe("pending");
  });
});

describe("createInitialWorkflowResultsState", () => {
  it("initializes every result kind as not loading and not loaded", () => {
    const state = createInitialWorkflowResultsState();
    expect(state.datasets).toEqual({ loading: false, loaded: false });
    expect(state.abtests).toEqual({ loading: false, loaded: false });
  });
});

describe("getStepStatusLabel", () => {
  it("returns a distinct label for each status", () => {
    expect(getStepStatusLabel("running")).not.toBe(getStepStatusLabel("done"));
    expect(getStepStatusLabel("pending")).not.toBe(getStepStatusLabel("failed"));
  });
});

describe("getTerminalFlowStepStatus", () => {
  it("normalizes cancel/error/ended flow statuses to step statuses", () => {
    expect(getTerminalFlowStepStatus("cancelled")).toBe("canceled");
    expect(getTerminalFlowStepStatus("error")).toBe("failed");
    expect(getTerminalFlowStepStatus("ended")).toBe("done");
  });

  it("returns undefined for empty or unrecognized status", () => {
    expect(getTerminalFlowStepStatus(undefined)).toBeUndefined();
    expect(getTerminalFlowStepStatus("weird")).toBeUndefined();
  });
});

describe("getWorkflowStepIndex", () => {
  it("returns the position of a known step id", () => {
    expect(getWorkflowStepIndex("dataset")).toBe(0);
    expect(getWorkflowStepIndex("ab-test")).toBeGreaterThan(0);
  });

  it("returns -1 for an undefined step id", () => {
    expect(getWorkflowStepIndex(undefined)).toBe(-1);
  });
});

describe("createWorkflowStepFromRuntime", () => {
  it("merges runtime status/progress into the step definition", () => {
    const runtimeState = createInitialWorkflowRuntimeState();
    runtimeState.dataset.runtimeText = "in progress";
    const step = createWorkflowStepFromRuntime("dataset", runtimeState);
    expect(step.status).toBe("running");
    expect(step.runtimeText).toBe("in progress");
  });
});

describe("getTerminalOverrideStepIndex", () => {
  it("prefers the last active (running/paused/failed/canceled) step", () => {
    const steps = [makeStep({ status: "done" }), makeStep({ status: "running" }), makeStep({ status: "pending" })];
    expect(getTerminalOverrideStepIndex(steps)).toBe(1);
  });

  it("falls back to the first pending step, then the last step overall", () => {
    const pendingSteps = [makeStep({ status: "done" }), makeStep({ status: "pending" })];
    expect(getTerminalOverrideStepIndex(pendingSteps)).toBe(1);
    const doneSteps = [makeStep({ status: "done" })];
    expect(getTerminalOverrideStepIndex(doneSteps)).toBe(0);
  });
});

describe("applyTerminalFlowStepStatus", () => {
  it("overrides the terminal step's status and runtime text", () => {
    const steps = [makeStep({ status: "running" })];
    const result = applyTerminalFlowStepStatus(steps, "failed");
    expect(result[0].status).toBe("failed");
    expect(result[0].runtimeText).toBeTruthy();
  });

  it("returns the steps unchanged when there is no terminal status", () => {
    const steps = [makeStep({ status: "running" })];
    expect(applyTerminalFlowStepStatus(steps, undefined)).toBe(steps);
  });
});

describe("buildWorkflowStepRuntimeFromEvents", () => {
  it("marks a step done once a finishing event arrives", () => {
    const events: NormalizedThreadEvent[] = [
      makeEvent({ stage: "dataset", action: "start" }),
      makeEvent({ type: "done", stage: "dataset", payload: { status: "done" } }),
    ];
    const snapshot = buildWorkflowStepRuntimeFromEvents(events, false);
    expect(snapshot.status).toBe("done");
  });

  it("marks a superseded running step as done", () => {
    const events: NormalizedThreadEvent[] = [makeEvent({ stage: "dataset", action: "progress" })];
    const snapshot = buildWorkflowStepRuntimeFromEvents(events, true);
    expect(snapshot.status).toBe("done");
  });

  it("propagates cancel/failed actions to the step status", () => {
    const canceled = buildWorkflowStepRuntimeFromEvents([makeEvent({ stage: "dataset", action: "cancel" })], false);
    expect(canceled.status).toBe("canceled");
    const failed = buildWorkflowStepRuntimeFromEvents([makeEvent({ stage: "dataset", action: "failed" })], false);
    expect(failed.status).toBe("failed");
  });
});

describe("buildVisibleWorkflowSteps", () => {
  it("returns a single fallback dataset step when there are no stage events and includeFirstStep is true", () => {
    const steps = buildVisibleWorkflowSteps([], createInitialWorkflowRuntimeState(), true);
    expect(steps).toHaveLength(1);
    expect(steps[0].id).toBe("dataset");
  });

  it("groups consecutive events by stage into separate steps", () => {
    const events: NormalizedThreadEvent[] = [
      makeEvent({ key: "e1", stage: "dataset", action: "progress", sequence: 1 }),
      makeEvent({ key: "e2", stage: "eval", action: "progress", sequence: 2 }),
    ];
    const steps = buildVisibleWorkflowSteps(events, createInitialWorkflowRuntimeState(), true);
    expect(steps.map((step) => step.id)).toEqual(["dataset", "px-report"]);
  });
});

describe("applyThreadStepStatusToWorkflowSteps", () => {
  it("overrides a step's status based on the thread step status map", () => {
    const steps = [makeStep({ id: "dataset", status: "running" })];
    const result = applyThreadStepStatusToWorkflowSteps(steps, { dataset: "done" });
    expect(result[0].status).toBe("done");
  });

  it("returns the steps unchanged when there is nothing to override", () => {
    const steps = [makeStep({ id: "dataset", status: "running" })];
    expect(applyThreadStepStatusToWorkflowSteps(steps, {})).toBe(steps);
  });
});

describe("applyThreadStreamTerminalToState", () => {
  it("marks the completed stage's step done and earlier steps done too", () => {
    const prev = createInitialWorkflowRuntimeState();
    const event = makeEvent({ stage: "eval", payload: { status: "completed" } });
    const next = applyThreadStreamTerminalToState(prev, event);
    expect(next.dataset.status).toBe("done");
    expect(next["px-report"].status).toBe("done");
  });

  it("returns the previous state unchanged when there is no resolvable stage", () => {
    const prev = createInitialWorkflowRuntimeState();
    const event = makeEvent({ payload: {} });
    expect(applyThreadStreamTerminalToState(prev, event)).toBe(prev);
  });
});

describe("reduceWorkflowRuntimeState / reduceWorkflowRuntimeStateFromEvents", () => {
  it("advances the current stage's step status based on an event's action", () => {
    const prev = createInitialWorkflowRuntimeState();
    const next = reduceWorkflowRuntimeState(prev, makeEvent({ stage: "dataset", action: "finish", payload: {} }));
    expect(next.dataset.status).toBe("done");
  });

  it("returns the previous state unchanged for a stage-less event", () => {
    const prev = createInitialWorkflowRuntimeState();
    expect(reduceWorkflowRuntimeState(prev, makeEvent({}))).toBe(prev);
  });

  it("reduces a batch of events in order into a final runtime state", () => {
    const events: NormalizedThreadEvent[] = [
      makeEvent({ key: "e1", stage: "dataset", action: "progress", sequence: 1 }),
      makeEvent({ key: "e2", stage: "dataset", action: "finish", sequence: 2, payload: {} }),
    ];
    const result: WorkflowRuntimeState = reduceWorkflowRuntimeStateFromEvents(
      createInitialWorkflowRuntimeState(),
      events,
    );
    expect(result.dataset.status).toBe("done");
  });
});
