export const TASKS_REMOVED_EVENT = 'lazymind:tasks-removed';
export interface TasksRemovedDetail {
  removed_task_ids: string[];
  trashed_conversation_ids: string[];
  cancellation_pending: boolean;
}
