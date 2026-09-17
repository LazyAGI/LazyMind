import type { TaskStatus } from '../store/taskCenter';
import type { WorkflowSessionStep } from '../store/workflowPanel';

/** Workflow attempt status is shared with the main panel; logs and artifacts remain task-owned. */
export function reconcileWorkflowTasks<T extends { task_id: string; status: TaskStatus; agent_type?: string }>(
  tasks: T[], steps?: WorkflowSessionStep[],
): T[] {
  if (!steps?.length) return tasks;
  const attempts = new Map(steps.map((step) => [step.task_id, step]));
  return tasks.map((task) => {
    if (task.agent_type !== 'workflow_step') return task;
    const attempt = attempts.get(task.task_id);
    if (!attempt) return task;
    const normalized = attempt.status === 'queued' || attempt.status === 'claimed' ? 'pending'
      : attempt.status === 'cancelled' ? 'canceled' : attempt.status;
    if (!['pending', 'running', 'succeeded', 'failed', 'interrupted', 'canceled'].includes(normalized)) return task;
    return normalized === task.status ? task : { ...task, status: normalized as TaskStatus };
  });
}
