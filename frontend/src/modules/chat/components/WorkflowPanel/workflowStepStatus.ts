import type { WorkflowDisplayState } from '../../store/workflowStatus';
import type { TabDef, WorkflowSession, WorkflowSessionStep } from '../../store/workflowPanel';

/** Execution facts take precedence over branch labels, including completed retries. */
export function workflowNodeStatus(node?: { execution?: string; branch?: string; validity?: string }, display?: Pick<WorkflowDisplayState, 'status' | 'input_step_id'>, stepId?: string): string {
  if (display?.status === 'waiting_input' && stepId && display.input_step_id === stepId) return 'waiting_input';
  if (!node || node.validity === 'stale') return 'pending';
  if (node.execution && node.execution !== 'none') return node.execution;
  return node.execution === 'none' && ['pruned', 'bypassed'].includes(node.branch ?? '') ? 'skipped' : 'pending';
}

/** Select the effective attempt represented by a workflow UI tab. */
export function resolveWorkflowTabStep(
  tab: Pick<TabDef, 'id' | 'step_id' | 'status_step_ids'>,
  steps: WorkflowSessionStep[] = [],
  currentStepId = '',
): WorkflowSessionStep | undefined {
  const stepIds = tab.status_step_ids ?? [tab.step_id ?? tab.id];
  return steps
    .filter(step => stepIds.includes(step.step_id) && step.validity !== 'stale')
    .sort((a, b) => (
      Number(b.step_id === currentStepId)
      - Number(a.step_id === currentStepId)
      || b.attempt - a.attempt
    ))[0];
}

/** A skipped step has no attempt. Read its persisted branch decision instead. */
export function workflowTabStatus(
  tab: Pick<TabDef, 'id' | 'step_id' | 'status_step_ids'>,
  session: Pick<WorkflowSession, 'steps' | 'current_step_id' | 'projection'>,
): string {
  const inputStepId = session.projection?.display?.input_step_id;
  if (session.projection?.display?.status === 'waiting_input' && inputStepId
    && (tab.status_step_ids ?? [tab.step_id ?? tab.id]).includes(inputStepId)) return 'waiting_input';
  const attempt = resolveWorkflowTabStep(tab, session.steps, session.current_step_id);
  if (attempt) return attempt.status;
  const ids = tab.status_step_ids ?? [tab.step_id ?? tab.id];
  const ordered = ids.includes(session.current_step_id)
    ? [session.current_step_id, ...ids.filter(id => id !== session.current_step_id)] : ids;
  for (const id of ordered) {
    const node = session.projection?.nodes?.[id];
    if (!node || node.validity === 'stale') continue;
    const status = workflowNodeStatus(node);
    if (status !== 'pending' || (node.execution && node.execution !== 'none')) return status;
  }
  return 'pending';
}
