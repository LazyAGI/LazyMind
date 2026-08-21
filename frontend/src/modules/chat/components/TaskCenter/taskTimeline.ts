import type { SubAgentTask, TaskStatus } from "@/modules/chat/store/taskCenter";
import type { WorkflowSessionStep } from "@/modules/chat/store/workflowPanel";

export type OrdinaryTaskState =
  | "complete"
  | "running"
  | "waiting"
  | "failed"
  | "outdated";

export interface OrdinaryTaskItem {
  id: string;
  task?: SubAgentTask;
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
  completedCount: number;
  failedCount: number;
  elapsedSeconds?: number;
}

const TERMINAL_STATUSES = new Set<TaskStatus>([
  "succeeded",
  "failed",
  "interrupted",
  "canceled",
]);
const TERMINAL_STEP_STATUSES = new Set([
  "succeeded",
  "failed",
  "interrupted",
  "canceled",
  "cancelled",
]);
const PARALLEL_LAUNCH_WINDOW_MS = 2_000;

function timestamp(value?: string): number | undefined {
  if (!value) return undefined;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : undefined;
}

function taskOrder(task: SubAgentTask, fallback: number): number {
  return task.seq_in_conversation ?? fallback;
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
  const scopeTrigger = latestTask(tasks)?.trigger_history_id;
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
  const status = step?.status || task?.status;
  if (status === "succeeded") return "complete";
  if (status === "running") return "running";
  if (
    status === "failed" ||
    status === "interrupted" ||
    status === "canceled" ||
    status === "cancelled"
  ) {
    return "failed";
  }
  return "waiting";
}

function intervalFor(
  task: SubAgentTask | undefined,
  step: WorkflowSessionStep | undefined,
  now: number,
): Pick<OrdinaryTaskItem, "startedAt" | "endedAt"> {
  const startedAt = timestamp(step?.created_at) ?? timestamp(task?.created_at);
  const status = step?.status || task?.status;
  if (!status || status === "pending" || status === "queued" || status === "waiting") {
    return { startedAt: undefined, endedAt: undefined };
  }
  const terminal = step
    ? TERMINAL_STEP_STATUSES.has(step.status)
    : Boolean(task && TERMINAL_STATUSES.has(task.status));
  const endedAt = terminal
    ? timestamp(step?.updated_at) ?? timestamp(task?.updated_at)
    : status === "running"
      ? now
      : undefined;
  return { startedAt, endedAt };
}

function selectAttempt(attempts: WorkflowSessionStep[]) {
  const ordered = [...attempts].sort((a, b) => {
    if (a.attempt !== b.attempt) return a.attempt - b.attempt;
    return (timestamp(a.created_at) ?? 0) - (timestamp(b.created_at) ?? 0);
  });
  const effective = ordered.filter((attempt) => attempt.validity !== "stale");
  return (effective.length > 0 ? effective : ordered).at(-1);
}

function canShareParallelGroup(
  item: OrdinaryTaskItem,
  group: OrdinaryTaskItem[],
): boolean {
  if (
    !item.task?.trigger_history_id ||
    item.state === "waiting" ||
    group.length === 0
  ) {
    return false;
  }
  if (
    group.some(
      (candidate) =>
        !candidate.task ||
        candidate.state === "waiting" ||
        candidate.task.trigger_history_id !== item.task?.trigger_history_id,
    )
  ) {
    return false;
  }
  const starts = [...group.map((candidate) => candidate.startedAt), item.startedAt];
  const ends = [...group.map((candidate) => candidate.endedAt), item.endedAt];
  if (starts.some((value) => value === undefined) || ends.some((value) => value === undefined)) {
    return false;
  }
  const latestStart = Math.max(...(starts as number[]));
  const earliestStart = Math.min(...(starts as number[]));
  const earliestEnd = Math.min(...(ends as number[]));
  // Task records are created at dispatch time in both ordinary SubAgent and
  // workflow batch launch paths. Keep the window narrow so a long-running task
  // cannot absorb a later, serial dispatch merely because their lifetimes overlap.
  return (
    latestStart - earliestStart <= PARALLEL_LAUNCH_WINDOW_MS &&
    latestStart < earliestEnd
  );
}

function groupConcurrentItems(items: OrdinaryTaskItem[]): OrdinaryTaskGroup[] {
  const groups: OrdinaryTaskGroup[] = [];
  let current: OrdinaryTaskItem[] = [];

  const flush = () => {
    if (current.length === 0) return;
    groups.push({
      id: current.map((item) => item.id).join(":"),
      mode: current.length > 1 ? "parallel" : "serial",
      items: current,
    });
    current = [];
  };

  for (const item of items) {
    if (current.length === 0 || canShareParallelGroup(item, current)) {
      current.push(item);
    } else {
      flush();
      current.push(item);
    }
  }
  flush();
  return groups;
}

export function buildOrdinaryTaskTimeline(
  tasks: SubAgentTask[],
  workflowSteps: WorkflowSessionStep[] = [],
  now = Date.now(),
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
      id: `workflow:${stepId}`,
      task: selectedTask,
      step: selectedAttempt,
      ordinal: 0,
      retryCount: Math.max(0, attempts.length - 1),
      state: taskState(selectedTask, selectedAttempt, selectedAttempt.validity),
      validity: selectedAttempt.validity,
      order,
      ...intervalFor(selectedTask, selectedAttempt, now),
    });
  }

  scopedTasks.forEach((task, index) => {
    if (claimedTaskIds.has(task.task_id)) return;
    items.push({
      id: `task:${task.task_id}`,
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

  const starts = items
    .map((item) => item.startedAt)
    .filter((value): value is number => value !== undefined);
  const ends = items
    .map((item) => item.endedAt)
    .filter((value): value is number => value !== undefined);
  const elapsedSeconds = starts.length > 0 && ends.length > 0
    ? Math.max(0, Math.round((Math.max(...ends) - Math.min(...starts)) / 1000))
    : undefined;

  return {
    items,
    groups: groupConcurrentItems(items),
    completedCount: items.filter((item) => item.state === "complete").length,
    failedCount: items.filter((item) => item.state === "failed").length,
    elapsedSeconds,
  };
}

export function taskCenterDisplayCount(
  tasks: SubAgentTask[],
  workflowSteps: WorkflowSessionStep[] | undefined,
  developerMode: boolean,
): number {
  return developerMode
    ? tasks.length
    : buildOrdinaryTaskTimeline(tasks, workflowSteps).items.length;
}
