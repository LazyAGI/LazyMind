import type { TaskStatus } from '../store/taskCenter';
import type { WorkflowSessionStep } from '../store/workflowPanel';

/** The backend display state confirms a user stop; other interruptions are failures.
 * Callers must only pass the owning workflow state for its current, effective attempt.
 * Keep persisted execution statuses unchanged so retries and history retain their meaning.
 */
export function workflowTaskDisplayStatus(status: string, workflowStatus?: string): string {
  if (status !== 'interrupted') return status;
  return workflowStatus === 'paused' ? 'paused' : 'failed';
}

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

/** Apply a pause only to tasks owned by the current workflow session. */
export function taskBelongsToWorkflow(
  task: { task_id: string; ordinary?: { session_id?: string | null } },
  session?: { session_id: string; steps?: WorkflowSessionStep[] },
): boolean {
  if (!session) return false;
  if (!session.steps) return task.ordinary?.session_id === session.session_id;
  const attempt = session.steps.find(step => step.task_id === task.task_id && step.session_id === session.session_id);
  return Boolean(attempt && attempt.validity !== 'stale' && !session.steps.some(step =>
    step.step_id === attempt.step_id && step.validity !== 'stale' && step.attempt > attempt.attempt));
}
