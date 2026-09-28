import type { SubAgentTask } from "@/modules/chat/store/taskCenter";
import type { WorkflowSessionStep } from "@/modules/chat/store/workflowPanel";

import type { OrdinaryTaskView } from "@/modules/chat/types/ordinaryTask";

export type OrdinaryTaskState =
  | "complete"
  | "running"
  | "waiting"
  | "failed"
  | "canceled"
  | "interrupted"
  | "outdated";

export interface OrdinaryTaskItem {
  id: string;
  task?: SubAgentTask;
  ordinary?: OrdinaryTaskView;
  durationSeconds?: number;
  step?: WorkflowSessionStep;
  ordinal: number;
  retryCount: number;
  state: OrdinaryTaskState;
  validity?: "effective" | "stale";
  startedAt?: number;
  endedAt?: number;
  order: number;
}

export interface OrdinaryTaskGroup {
  id: string;
  mode: "serial" | "parallel";
  items: OrdinaryTaskItem[];
}

export interface OrdinaryTaskTimeline {
  items: OrdinaryTaskItem[];
  groups: OrdinaryTaskGroup[];
  totalCount: number;
  completedCount: number;
  failedCount: number;
  elapsedSeconds?: number;
  cumulativeExecutionSeconds?: number;
}

export function ordinaryTaskDurationSeconds(
  item: Pick<OrdinaryTaskItem, "startedAt" | "endedAt" | "durationSeconds">,
): number | undefined {
  if (item.durationSeconds !== undefined) return item.durationSeconds;
  if (item.startedAt === undefined || item.endedAt === undefined) return undefined;
  return Math.max(0, Math.round((item.endedAt - item.startedAt) / 1000));
}

function timestamp(value?: string | null): number | undefined {
  if (!value) return undefined;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : undefined;
}

function taskOrder(task: SubAgentTask, fallback: number): number {
  return task.ordinary?.order ?? task.seq_in_conversation ?? fallback;
}

function latestTask(tasks: SubAgentTask[]): SubAgentTask | undefined {
  return [...tasks].sort((a, b) => {
    const seqDelta = (b.seq_in_conversation ?? 0) - (a.seq_in_conversation ?? 0);
    if (seqDelta !== 0) return seqDelta;
    return (timestamp(b.created_at) ?? 0) - (timestamp(a.created_at) ?? 0);
  })[0];
}

function currentExecutionTasks(
  tasks: SubAgentTask[],
  workflowSteps: WorkflowSessionStep[],
): SubAgentTask[] {
  if (tasks.length === 0) return tasks;
  if (workflowSteps.length > 0) {
    const currentTaskIds = new Set(workflowSteps.map((step) => step.task_id));
    const matchingTasks = tasks.filter((task) => currentTaskIds.has(task.task_id));
    const scopeTrigger = latestTask(matchingTasks)?.trigger_history_id;
    return tasks.filter(
      (task) =>
        currentTaskIds.has(task.task_id) ||
        Boolean(scopeTrigger && task.trigger_history_id === scopeTrigger),
    );
  }
  const current = latestTask(tasks);
  if (current?.ordinary?.run_id) {
    return tasks.filter(task => task.ordinary?.run_id === current.ordinary?.run_id);
  }
  const scopeTrigger = current?.trigger_history_id;
  return scopeTrigger
    ? tasks.filter((task) => task.trigger_history_id === scopeTrigger)
    : tasks;
}

function taskState(
  task: SubAgentTask | undefined,
  step: WorkflowSessionStep | undefined,
  validity?: "effective" | "stale",
): OrdinaryTaskState {
  if (validity === "stale") return "outdated";
  const status = step?.ordinary?.status || step?.status || task?.ordinary?.status || task?.status;
  if (status === "succeeded") return "complete";
  if (status === "running") return "running";
  if (status === "interrupted") return "interrupted";
  if (status === "canceled" || status === "cancelled") return "canceled";
  if (status === "failed") return "failed";
  return "waiting";
}

function intervalFor(
  task: SubAgentTask | undefined,
  step: WorkflowSessionStep | undefined,
  now: number,
): Pick<OrdinaryTaskItem, "startedAt" | "endedAt" | "durationSeconds"> {
  const ordinary = step?.ordinary ?? task?.ordinary;
  if (!ordinary || ["pending", "queued", "waiting"].includes(ordinary.status)) return { startedAt: undefined, endedAt: undefined };
  const startedAt = timestamp(ordinary.timing.started_at);
  const finishedAt = timestamp(ordinary.timing.finished_at);
  const running = ordinary.status === "running";
  const elapsed = ordinary.timing.execution_elapsed_ms;
  const measured = timestamp(ordinary.timing.measured_at);
  const durationSeconds = elapsed != null && elapsed >= 0
    ? Math.round((elapsed + (running && measured !== undefined ? Math.max(0, now - measured) : 0)) / 1000)
    : undefined;
  return { startedAt, endedAt: finishedAt ?? (running ? now : undefined), durationSeconds };
}

function selectAttempt(attempts: WorkflowSessionStep[]) {
  const ordered = [...attempts].sort((a, b) => {
    if (a.attempt !== b.attempt) return a.attempt - b.attempt;
    return (timestamp(a.created_at) ?? 0) - (timestamp(b.created_at) ?? 0);
  });
  const effective = ordered.filter((attempt) => attempt.validity !== "stale");
  const candidates = effective.length > 0 ? effective : ordered;
  return candidates[candidates.length - 1];
}

function groupConcurrentItems(items: OrdinaryTaskItem[]): OrdinaryTaskGroup[] {
  const groups: OrdinaryTaskGroup[] = [];
  const explicitGroups = new Map<string, OrdinaryTaskGroup>();
  for (const item of items) {
    const parallel = item.ordinary?.parallel_group_id;
    const key = parallel ? `${item.ordinary?.run_id}:${parallel}` : undefined;
    const existing = key ? explicitGroups.get(key) : undefined;
    if (existing) {
      existing.items.push(item);
      existing.mode = "parallel";
    } else {
      const group: OrdinaryTaskGroup = { id: key ?? item.id, mode: "serial", items: [item] };
      groups.push(group);
      if (key) explicitGroups.set(key, group);
    }
  }
  return groups;
}

export function buildOrdinaryTaskTimeline(
  tasks: SubAgentTask[],
  workflowSteps: WorkflowSessionStep[] = [],
  now = Date.now(),
  plannedCount?: number,
): OrdinaryTaskTimeline {
  const scopedTasks = currentExecutionTasks(tasks, workflowSteps);
  const taskById = new Map(scopedTasks.map((task) => [task.task_id, task]));
  const claimedTaskIds = new Set<string>();
  const items: OrdinaryTaskItem[] = [];
  const attemptsByStep = new Map<string, WorkflowSessionStep[]>();

  for (const step of workflowSteps) {
    const attempts = attemptsByStep.get(step.step_id) ?? [];
    attempts.push(step);
    attemptsByStep.set(step.step_id, attempts);
  }

  let stepOnlyOrder = scopedTasks.length;
  for (const [stepId, attempts] of attemptsByStep) {
    for (const attempt of attempts) claimedTaskIds.add(attempt.task_id);
    const selectedAttempt = selectAttempt(attempts);
    const selectedTask = selectedAttempt
      ? taskById.get(selectedAttempt.task_id)
      : undefined;
    if (!selectedAttempt) continue;
    const attemptTasks = attempts
      .map((attempt) => taskById.get(attempt.task_id))
      .filter((task): task is SubAgentTask => Boolean(task));
    const order = attemptTasks.length > 0
      ? Math.min(
          ...attemptTasks.map((task, index) => taskOrder(task, scopedTasks.length + index)),
        )
      : ++stepOnlyOrder;
    items.push({
      id: selectedAttempt.ordinary?.display_key ?? selectedTask?.ordinary?.display_key ?? `workflow:${selectedAttempt.session_id}:${stepId}:${selectedAttempt.id}`,
      ordinary: selectedAttempt.ordinary ?? selectedTask?.ordinary,
      task: selectedTask,
      step: selectedAttempt,
      ordinal: 0,
      retryCount: Math.max(0, attempts.length - 1),
      state: taskState(selectedTask, selectedAttempt, selectedAttempt.validity),
      validity: selectedAttempt.validity,
      order: selectedAttempt.ordinary?.order ?? order,
      ...intervalFor(selectedTask, selectedAttempt, now),
    });
  }

  scopedTasks.forEach((task, index) => {
    if (claimedTaskIds.has(task.task_id)) return;
    items.push({
      id: task.ordinary?.display_key ?? `task:${task.task_id}`,
      ordinary: task.ordinary,
      task,
      ordinal: 0,
      retryCount: 0,
      state: taskState(task, undefined),
      order: taskOrder(task, scopedTasks.length + index),
      ...intervalFor(task, undefined, now),
    });
  });

  items.sort((a, b) => a.order - b.order || a.id.localeCompare(b.id));
  items.forEach((item, index) => {
    item.ordinal = index + 1;
  });

  const timedItems = items.filter(
    (item): item is OrdinaryTaskItem & { startedAt: number; endedAt: number } =>
      item.startedAt !== undefined && item.endedAt !== undefined,
  );
  const elapsedSeconds = timedItems.length > 0
    ? Math.max(0, Math.round((
        Math.max(...timedItems.map((item) => item.endedAt)) -
        Math.min(...timedItems.map((item) => item.startedAt))
      ) / 1000))
    : undefined;
  const durationItems = items.filter(item => ordinaryTaskDurationSeconds(item) !== undefined);
  const cumulativeExecutionSeconds = durationItems.length > 0
    ? durationItems.reduce(
        (total, item) => total + (ordinaryTaskDurationSeconds(item) ?? 0),
        0,
      )
    : undefined;

  return {
    items,
    groups: groupConcurrentItems(items),
    // Future workflow milestones do not have task records yet. Count them in
    // the summary after execution starts without fabricating task cards.
    totalCount: items.length > 0
      ? Math.max(items.length, plannedCount ?? 0)
      : 0,
    completedCount: items.filter((item) => item.state === "complete").length,
    failedCount: items.filter((item) => ["failed", "canceled", "interrupted"].includes(item.state)).length,
    elapsedSeconds,
    cumulativeExecutionSeconds,
  };
}

export function taskCenterDisplayCount(
  tasks: SubAgentTask[],
  workflowSteps: WorkflowSessionStep[] | undefined,
  developerMode: boolean,
  plannedCount?: number,
): number {
  return developerMode
    ? tasks.length
    : buildOrdinaryTaskTimeline(
        tasks,
        workflowSteps,
        Date.now(),
        plannedCount,
      ).totalCount;
}
