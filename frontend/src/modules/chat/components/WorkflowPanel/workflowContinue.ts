import type { TabDef, WorkflowSession } from '@/modules/chat/store/workflowPanel';
import { resolvePendingApprovalStep } from './workflowApproval';

export type WorkflowContinueAction = { kind: 'approval' | 'resume' | 'completed'; stepId: string };

/** Waiting alone is not a checkpoint: automatic dispatch also passes through it. */
export function resolveWorkflowContinueAction(
  session: Pick<WorkflowSession, 'status' | 'current_step_id' | 'steps' | 'projection'>,
  displayStatus: string,
  activeTab?: TabDef,
): WorkflowContinueAction | undefined {
  if (displayStatus === 'active') return undefined;
  const completedStep = resolveCompletedContinueStep(session, activeTab);
  if (completedStep) return { kind: 'completed', stepId: completedStep };
  const approvalStep = resolvePendingApprovalStep(session, displayStatus);
  if (approvalStep) return { kind: 'approval', stepId: approvalStep };
  if (displayStatus !== 'waiting' || session.projection?.completed) return undefined;
  const nodes = session.projection?.nodes ?? {};
  if (Object.values(nodes).some((node) => node.validity !== 'stale'
    && ['pending', 'queued', 'claimed', 'running', 'failed'].includes(node.execution))) return undefined;
  const interruptedStep = (session.projection?.current ?? []).find((id) =>
    nodes[id]?.validity !== 'stale' && ['interrupted', 'cancelled', 'canceled'].includes(nodes[id]?.execution),
  );
  return interruptedStep ? { kind: 'resume', stepId: interruptedStep } : undefined;
}

export function resolveCompletedContinueStep(
  session: Pick<WorkflowSession, 'status'>,
  activeTab?: TabDef,
): string | undefined {
  if (session.status !== 'completed') return undefined;
  const stepId = activeTab?.completed_continue_step?.trim();
  return stepId || undefined;
}
