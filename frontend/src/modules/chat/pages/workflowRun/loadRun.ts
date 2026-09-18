import type { WorkflowControlView } from '@/modules/chat/utils/workflowControl';

export {
  loadWorkflowRun,
  loadWorkflowRunSnapshot,
  panelCurrentStep,
  watchWorkflowRun,
  type WorkflowRunSnapshot,
} from '@/modules/chat/utils/loadWorkflowRun';

/** The shared header describes execution facts, not a step's future human-review policy. */
export function controlStatusKey(control: WorkflowControlView): string {
  if (control.continuation === 'awaiting_user' || control.continuation === 'draining') return 'chat.workflowControlReviewStatus';
  if (control.continuation === 'binding_required') return 'chat.workflowControlBindingStatus';
  if (control.continuation === 'stopped') return 'chat.workflowStatusStopped';
  if (control.continuation === 'completed') return 'chat.workflowStatusDone';
  if (control.continuation === 'failed') return 'chat.workflowStatusFailed';
  return control.active_executions > 0 ? 'chat.workflowStatusRunning' : 'chat.workflowStatusReady';
}
