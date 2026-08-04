import { describe, expect, it } from "vitest";
import { taskStatusDescription } from "./taskStatusDescription";
import type { Task } from "./api";

const t = ((key: string, params?: Record<string, unknown>) => {
  if (!params) return key;
  const paramsStr = Object.entries(params)
    .map(([k, v]) => `${k}=${v}`)
    .join(",");
  return `${key}(${paramsStr})`;
}) as unknown as (key: string, params?: Record<string, unknown>) => string;

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: "task1",
    user_id: "u1",
    conversation_id: "c1",
    task_type: "background_chat",
    status: "pending",
    steps: [],
    created_at: "2026-01-01",
    updated_at: "2026-01-01",
    ...overrides,
  };
}

describe("taskStatusDescription", () => {
  it("describes a failed task with the failing step and reason", () => {
    const task = makeTask({
      status: "failed",
      steps: [
        { step_id: "s1", status: "completed" },
        { step_id: "s2", status: "failed", current_phase: "解析文档", summary: "解析失败：格式错误" },
      ],
    });
    const result = taskStatusDescription(task, t);
    expect(result).toContain("taskCenter.taskStateFailed");
    expect(result).toContain("task=解析文档");
    expect(result).toContain("解析失败：格式错误");
  });

  it("falls back to the failure reason unavailable message when nothing else is present", () => {
    const task = makeTask({ status: "failed" });
    const result = taskStatusDescription(task, t);
    expect(result).toContain("taskCenter.failureReasonUnavailable");
  });

  it("describes a canceled task using the schedule name as subject", () => {
    const task = makeTask({ status: "canceled", schedule_name: "每日巡检" });
    const result = taskStatusDescription(task, t);
    expect(result).toBe("taskCenter.taskStateCanceled(task=每日巡检)");
  });

  it("describes an interrupted task using the last interrupted step", () => {
    const task = makeTask({
      status: "interrupted",
      steps: [{ step_id: "s1", status: "interrupted", title: "生成报告" }],
    });
    const result = taskStatusDescription(task, t);
    expect(result).toBe("taskCenter.taskStateInterrupted(task=生成报告)");
  });

  it("describes succeeded/completed tasks the same way", () => {
    const completed = makeTask({ status: "completed", title: "任务A" });
    const succeeded = makeTask({ status: "succeeded", title: "任务A" });
    expect(taskStatusDescription(completed, t)).toBe(
      taskStatusDescription(succeeded, t),
    );
    expect(taskStatusDescription(completed, t)).toContain("taskCenter.taskStateSucceeded");
  });

  it("describes a running task using the currently running step", () => {
    const task = makeTask({
      status: "running",
      steps: [{ step_id: "s1", status: "running", current_phase: "检索中" }],
    });
    expect(taskStatusDescription(task, t)).toBe("taskCenter.taskStateRunning(task=检索中)");
  });

  it("naturalizes ratio patterns in waiting_inputs reason text", () => {
    const task = makeTask({ status: "waiting_inputs", waiting_reason: "已完成 3/10 项" });
    const result = taskStatusDescription(task, t);
    expect(result).toContain("taskCenter.progressItemsReady(done=3,total=10)");
  });

  it("falls back to the generic waiting_inputs message when no reason is set", () => {
    const task = makeTask({ status: "waiting_inputs", title: "任务B" });
    const result = taskStatusDescription(task, t);
    expect(result).toBe("taskCenter.taskStateWaitingInputs(task=任务B)");
  });

  it("describes a plain waiting task similarly to waiting_inputs", () => {
    const task = makeTask({ status: "waiting", title: "任务C" });
    expect(taskStatusDescription(task, t)).toBe("taskCenter.taskStateWaiting(task=任务C)");
  });

  it("describes a pending task with fallback subject from task_type", () => {
    const task = makeTask({ status: "pending", task_type: "plugin_run" });
    expect(taskStatusDescription(task, t)).toBe(
      "taskCenter.taskStatePending(task=taskCenter.taskFallbackPlugin)",
    );
  });

  it("falls back to a generic task type message for unknown task_type", () => {
    const task = makeTask({ status: "pending", task_type: "something_else" });
    expect(taskStatusDescription(task, t)).toBe(
      "taskCenter.taskStatePending(task=taskCenter.taskFallbackGeneric)",
    );
  });

  it("strips a leading 'Scheduled:' prefix from the schedule name", () => {
    const task = makeTask({ status: "pending", schedule_name: "Scheduled: 每日巡检" });
    expect(taskStatusDescription(task, t)).toBe("taskCenter.taskStatePending(task=每日巡检)");
  });

  it("describes an unknown status using the status i18n key", () => {
    const task = makeTask({ status: "some_new_status", title: "任务D" });
    const result = taskStatusDescription(task, t);
    expect(result).toContain("taskCenter.taskStateUnknown");
    expect(result).toContain("task=任务D");
  });

  it("prefers conversation_title over generic fallback when schedule_name is absent", () => {
    const task = makeTask({ status: "pending", conversation_title: "对话标题" });
    expect(taskStatusDescription(task, t)).toBe("taskCenter.taskStatePending(task=对话标题)");
  });
});
