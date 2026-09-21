import type { WorkflowSession } from '@/modules/chat/store/workflowPanel';

/** Explain missing content using the displayed step, never another tab's execution. */
export function workflowEmptyStateKey(
  session: Pick<WorkflowSession, 'projection'>,
  stepId?: string,
): string {
  const node = stepId ? session.projection?.nodes?.[stepId] : undefined;
  if (!node || node.validity === 'stale') return 'chat.workflowEmptyUnknown';

  switch (node.execution) {
    case 'pending':
    case 'queued':
    case 'claimed':
    case 'running':
      return 'chat.workflowEmptyRunning';
    case 'failed':
      return 'chat.workflowEmptyFailed';
    case 'interrupted':
    case 'cancelled':
    case 'canceled':
      return 'chat.workflowEmptyInterrupted';
    case 'succeeded':
      return 'chat.workflowEmptyCompleted';
    case 'none':
      if (node.branch === 'pruned' || node.branch === 'bypassed') {
        return 'chat.workflowEmptySkipped';
      }
      return node.readiness === 'blocked'
        ? 'chat.workflowEmptyBlocked'
        : 'chat.workflowEmptyNotStarted';
    default:
      return 'chat.workflowEmptyUnknown';
  }
}
