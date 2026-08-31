import type { TabDef, WorkflowSession } from '@/modules/chat/store/workflowPanel';

export function resolveCompletedContinueStep(
  session: Pick<WorkflowSession, 'status'>,
  activeTab?: TabDef,
): string | undefined {
  if (session.status !== 'completed') return undefined;
  const stepId = activeTab?.completed_continue_step?.trim();
  return stepId || undefined;
}
