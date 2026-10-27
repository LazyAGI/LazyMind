import type { WorkflowSession } from '@/modules/chat/store/workflowPanel';

/** Approval is a backend decision, never a synonym for internal waiting. */
export function resolvePendingApprovalStep(
  session: Pick<WorkflowSession, 'status' | 'current_step_id' | 'steps' | 'projection'>,
  displayStatus: string,
): string | undefined {
  if (displayStatus !== 'waiting' || session.projection?.completed) return undefined;
  return session.projection?.display?.status === 'waiting'
    ? session.projection.display.approval_step_id || undefined : undefined;
}
