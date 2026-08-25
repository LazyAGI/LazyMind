import { describe, expect, it } from "vitest";
import type { SubAgentTask, TaskStatus } from "@/modules/chat/store/taskCenter";
import type { WorkflowSessionStep } from "@/modules/chat/store/workflowPanel";
import {
  buildOrdinaryTaskTimeline,
  taskCenterDisplayCount,
} from "./taskTimeline";

const BASE_TIME = Date.parse("2026-08-20T06:00:00.000Z");

function iso(offsetSeconds: number) {
  return new Date(BASE_TIME + offsetSeconds * 1000).toISOString();
}

function task(
  id: string,
  seq: number,
  status: TaskStatus,
  start: number,
  end: number,
  overrides: Partial<SubAgentTask> = {},
): SubAgentTask {
  return {
    task_id: id,
    conversation_id: "conversation-1",
    trigger_history_id: "history-1",
    seq_in_conversation: seq,
    created_at: iso(start),
    updated_at: iso(end),
    title: id,
    agent_type: "workflow_step",
    mode: "manual",
    status,
    progress_pct: status === "succeeded" ? 100 : 0,
    artifacts: [],
    sources: [],
    artifact_streams: [],
    execution_log: [],
    ...overrides,
  };
}

function step(
  taskId: string,
  stepId: string,
  attempt: number,
  validity: "effective" | "stale",
  start: number,
  end: number,
): WorkflowSessionStep {
  return {
    id: `${taskId}-attempt`,
    session_id: "session-1",
    step_id: stepId,
    attempt,
    task_id: taskId,
    status: validity === "effective" ? "succeeded" : "failed",
    validity,
    created_at: iso(start),
    updated_at: iso(end),
  };
}

describe("buildOrdinaryTaskTimeline", () => {
  it("collapses workflow retries into one effective step", () => {
    const tasks = [
      task("analyze", 1, "succeeded", 0, 10),
      task("collect-1", 2, "failed", 10, 20),
      task("collect-2", 3, "failed", 20, 30),
      task("collect-3", 4, "succeeded", 30, 40),
      task("optimize", 5, "succeeded", 40, 50),
    ];
    const steps = [
      step("analyze", "analyze_subject", 1, "effective", 0, 10),
      step("collect-1", "collect_materials", 1, "stale", 10, 20),
      step("collect-2", "collect_materials", 2, "stale", 20, 30),
      step("collect-3", "collect_materials", 3, "effective", 30, 40),
      step("optimize", "optimize_prompt", 1, "effective", 40, 50),
    ];

    const timeline = buildOrdinaryTaskTimeline(tasks, steps, BASE_TIME + 60_000);

    expect(timeline.items).toHaveLength(3);
    expect(timeline.items.map((item) => item.task?.task_id)).toEqual([
      "analyze",
      "collect-3",
      "optimize",
    ]);
    expect(timeline.items[1]).toMatchObject({
      retryCount: 2,
      state: "complete",
      ordinal: 2,
    });
    expect(timeline.groups.map((group) => group.mode)).toEqual([
      "serial",
      "serial",
      "serial",
    ]);
  });

  it("uses overlapping task intervals to form a parallel tab group", () => {
    const tasks = [
      task("research-a", 1, "succeeded", 0, 12, {
        agent_type: "research",
      }),
      task("research-b", 2, "succeeded", 2, 10, {
        agent_type: "research",
      }),
      task("report", 3, "succeeded", 13, 20, {
        agent_type: "writer",
      }),
    ];

    const timeline = buildOrdinaryTaskTimeline(tasks, [], BASE_TIME + 30_000);

    expect(timeline.groups).toHaveLength(2);
    expect(timeline.groups[0].mode).toBe("parallel");
    expect(timeline.groups[0].items.map((item) => item.task.task_id)).toEqual([
      "research-a",
      "research-b",
    ]);
    expect(timeline.groups[1].mode).toBe("serial");
  });

  it("shows only the latest execution instead of merging conversation turns", () => {
    const tasks = [
      task("first", 1, "succeeded", 0, 12, { agent_type: "research" }),
      task("second", 2, "succeeded", 2, 10, {
        agent_type: "research",
        trigger_history_id: "history-2",
      }),
    ];

    const timeline = buildOrdinaryTaskTimeline(tasks, [], BASE_TIME + 30_000);

    expect(timeline.groups.map((group) => group.mode)).toEqual(["serial"]);
    expect(timeline.items[0].task?.task_id).toBe("second");
  });

  it("does not treat a later overlapping dispatch or pending work as parallel", () => {
    const laterDispatch = buildOrdinaryTaskTimeline([
      task("long-running", 1, "succeeded", 0, 20, { agent_type: "research" }),
      task("later", 2, "succeeded", 5, 10, { agent_type: "research" }),
    ], [], BASE_TIME + 30_000);
    const pendingDispatch = buildOrdinaryTaskTimeline([
      task("active", 1, "running", 0, 20, { agent_type: "research" }),
      task("queued", 2, "pending", 1, 10, { agent_type: "research" }),
    ], [], BASE_TIME + 30_000);

    expect(laterDispatch.groups.map((group) => group.mode)).toEqual([
      "serial",
      "serial",
    ]);
    expect(pendingDispatch.groups.map((group) => group.mode)).toEqual([
      "serial",
      "serial",
    ]);
    expect(pendingDispatch.items[1]).toMatchObject({
      startedAt: undefined,
      endedAt: undefined,
    });
  });

  it("uses workflow attempt state and keeps hosted attempts without task rows", () => {
    const pendingTask = task("workflow-task", 1, "pending", 0, 10);
    const authoritativeStep = {
      ...step("workflow-task", "workflow_step", 1, "effective", 0, 10),
      status: "succeeded",
    };
    const hostedStep = {
      ...step("hosted-task", "hosted_step", 1, "effective", 11, 20),
      status: "running",
    };

    const timeline = buildOrdinaryTaskTimeline(
      [pendingTask],
      [authoritativeStep, hostedStep],
      BASE_TIME + 30_000,
    );

    expect(timeline.items).toHaveLength(2);
    expect(timeline.items[0]).toMatchObject({ state: "complete" });
    expect(timeline.items[1]).toMatchObject({ state: "running" });
    expect(timeline.items[1].task).toBeUndefined();
  });

  it("keeps an all-stale workflow step visible but marks it outdated", () => {
    const tasks = [task("stale-task", 1, "succeeded", 0, 10)];
    const steps = [step("stale-task", "old_branch", 1, "stale", 0, 10)];

    const timeline = buildOrdinaryTaskTimeline(tasks, steps, BASE_TIME + 20_000);

    expect(timeline.items[0]).toMatchObject({
      state: "outdated",
      validity: "stale",
    });
  });

  it("uses raw attempts for developer count and logical steps for ordinary count", () => {
    const tasks = [
      task("collect-1", 1, "failed", 0, 10),
      task("collect-2", 2, "succeeded", 10, 20),
    ];
    const steps = [
      step("collect-1", "collect", 1, "stale", 0, 10),
      step("collect-2", "collect", 2, "effective", 10, 20),
    ];

    expect(taskCenterDisplayCount(tasks, steps, true)).toBe(2);
    expect(taskCenterDisplayCount(tasks, steps, false)).toBe(1);
  });

  it("keeps the ordinary timeline scoped to the current execution turn", () => {
    const tasks = [
      task("old", 1, "succeeded", 0, 5, {
        trigger_history_id: "history-old",
      }),
      task("current-a", 2, "succeeded", 10, 15, {
        trigger_history_id: "history-current",
      }),
      task("current-b", 3, "succeeded", 15, 20, {
        trigger_history_id: "history-current",
      }),
    ];

    const plainTimeline = buildOrdinaryTaskTimeline(tasks, [], BASE_TIME + 30_000);
    const workflowTimeline = buildOrdinaryTaskTimeline(tasks, [
      step("current-a", "current-step", 1, "effective", 10, 15),
    ], BASE_TIME + 30_000);

    expect(plainTimeline.items.map((item) => item.task?.task_id)).toEqual([
      "current-a",
      "current-b",
    ]);
    expect(workflowTimeline.items.map((item) => item.task?.task_id)).toEqual([
      "current-a",
      "current-b",
    ]);
  });
});
