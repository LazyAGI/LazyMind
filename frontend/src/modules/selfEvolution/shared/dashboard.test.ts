import { describe, expect, it } from "vitest";
import { buildEvoProcessDashboard } from "./dashboard";
import { createInitialWorkflowRuntimeState } from "./runtimeState";
import type { NormalizedThreadEvent } from "./types";

function makeEvent(overrides: Partial<NormalizedThreadEvent>): NormalizedThreadEvent {
  return { key: "k", type: "x", ...overrides };
}

describe("buildEvoProcessDashboard", () => {
  it("builds an overview entry per workflow step with default statuses when there are no events", () => {
    const dashboard = buildEvoProcessDashboard([], createInitialWorkflowRuntimeState(), true);
    expect(dashboard.overview).toHaveLength(5);
    expect(dashboard.overview[0].stage).toBe("dataset");
    expect(dashboard.activeStage).toBe("dataset");
  });

  it("marks the dataset step running when includeFirstStep is set and no stage events exist yet", () => {
    const dashboard = buildEvoProcessDashboard([], createInitialWorkflowRuntimeState(), true);
    const datasetOverview = dashboard.overview.find((item) => item.stage === "dataset");
    expect(datasetOverview?.step.status).toBe("running");
  });

  it("marks every step done once a candidate cutover completion event is present", () => {
    const events: NormalizedThreadEvent[] = [
      makeEvent({
        stage: "abtest",
        action: "finish",
        payload: { data: { artifact_id: "candidate_algorithm_cutover" } },
      }),
    ];
    const dashboard = buildEvoProcessDashboard(events, createInitialWorkflowRuntimeState(), true);
    expect(dashboard.cutoverCompleted).toBe(true);
    expect(dashboard.overview.every((item) => item.step.status === "done")).toBe(true);
  });

  it("surfaces recent activities in reverse chronological order and tracks their total count", () => {
    const events: NormalizedThreadEvent[] = [
      makeEvent({ key: "e1", stage: "dataset", displayText: "first", sequence: 1 }),
      makeEvent({ key: "e2", stage: "dataset", displayText: "second", sequence: 2 }),
    ];
    const dashboard = buildEvoProcessDashboard(events, createInitialWorkflowRuntimeState(), true);
    expect(dashboard.recentActivityTotal).toBe(2);
    expect(dashboard.recentActivities[0].detail).toBe("second");
  });

  it("surfaces a pending checkpoint prompt when one is present in the events", () => {
    const events: NormalizedThreadEvent[] = [
      makeEvent({
        stage: "dataset",
        type: "checkpoint.wait",
        checkpointWait: { message: "waiting", command: "continue", completedStage: "dataset" },
      }),
    ];
    const dashboard = buildEvoProcessDashboard(events, createInitialWorkflowRuntimeState(), true);
    expect(dashboard.checkpoint?.message).toBe("waiting");
  });
});
