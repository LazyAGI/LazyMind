export const TASKS_REMOVED_EVENT = 'lazymind:tasks-removed';
export const WORKFLOW_TASKS_CHANGED = 'workflow-tasks-changed';
export interface TasksRemovedDetail {
  removed_task_ids: string[];
  trashed_conversation_ids: string[];
  cancellation_pending: boolean;
}
