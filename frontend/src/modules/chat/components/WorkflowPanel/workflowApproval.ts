import type { WorkflowSession } from '@/modules/chat/store/workflowPanel';

/**
 * A completed attempt is removed from projection.current, and the runtime may
 * clear session.current_step_id before entering `waiting`. Resolve the approval
 * checkpoint from the latest effective attempt instead of relying only on the
 * mutable current-step pointer.
 */
export function resolvePendingApprovalStep(
  session: Pick<WorkflowSession, 'status' | 'current_step_id' | 'steps' | 'projection'>,
  displayStatus: string,
): string | undefined {
  if (displayStatus !== 'waiting') return undefined;

  const isSucceededApproval = (stepId?: string) => Boolean(
    stepId
    && session.projection?.nodes?.[stepId]?.execution === 'succeeded'
    && session.projection.nodes[stepId].requires_approval,
  );
  if (isSucceededApproval(session.current_step_id)) {
    return session.current_step_id;
  }

  const latestEffectiveStep = [...(session.steps ?? [])]
    .filter((step) => step.validity !== 'stale')
    .sort((left, right) => {
      const timeDelta = Date.parse(right.created_at) - Date.parse(left.created_at);
      return timeDelta || right.attempt - left.attempt;
    })[0];
  if (latestEffectiveStep?.status !== 'succeeded') return undefined;
  return isSucceededApproval(latestEffectiveStep.step_id)
    ? latestEffectiveStep.step_id
    : undefined;
}
