export type WorkflowSessionStatus = 'active' | 'pausing' | 'completed' | 'failed' | 'waiting' | 'stopped';

export interface WorkflowDisplayState {
  status: 'running' | 'waiting' | 'paused' | 'failed' | 'completed';
  reason: string;
  approval_step_id: string;
  /** Native Chat turn owning cancellation, including after workflow hand-off. */
  chat_history_id?: string;
}

const displayLabels: Record<WorkflowDisplayState['status'], string> = {
  running: 'chat.workflowStatusRunning', waiting: 'chat.workflowStatusWaiting',
  paused: 'chat.workflowStatusPaused', failed: 'chat.workflowStatusFailed',
  completed: 'chat.workflowStatusDone',
};
export const workflowDisplayLabel = (status?: WorkflowDisplayState['status']): string => status ? displayLabels[status] : 'common.loading';

export interface RuntimeProjectionStatus {
  completed?: boolean;
  status?: string;
  current?: string[];
  ready?: string[];
  blocked?: string[];
  nodes?: Record<string, {
    requires_approval?: boolean;
    execution?: string;
  }>;
}

/** Resolve display status from a snapshot accepted by the versioned store. */
export function reconcileWorkflowSessionStatus(
  status: WorkflowSessionStatus,
  projection?: RuntimeProjectionStatus,
): WorkflowSessionStatus {
  if (!projection) return status;

  // Ordering is enforced at the store/reducer boundary. A newer projection
  // may legitimately reopen a failed/completed session after retry or rewind.
  const projectedStatus = projection.status === 'running' ? 'active' : projection.status;
  if (projectedStatus === 'pausing') return 'pausing';
  if (projectedStatus === 'stopped') return 'stopped';
  if (projection.completed) return 'completed';
  const executions = (projection.current ?? []).map((id) => projection.nodes?.[id]?.execution);
  if (executions.some((execution) => ['pending', 'queued', 'claimed', 'running'].includes(execution ?? ''))) {
    return 'active';
  }
  if (executions.includes('failed')) return 'failed';
  if (executions.some((execution) => ['interrupted', 'cancelled', 'canceled', 'waiting'].includes(execution ?? ''))) {
    return 'waiting';
  }
  if (['active', 'completed', 'failed', 'waiting', 'stopped'].includes(projectedStatus ?? '')) {
    return projectedStatus as WorkflowSessionStatus;
  }
  if ((projection.current?.length ?? 0) === 0
    && ((projection.ready?.length ?? 0) > 0 || (projection.blocked?.length ?? 0) > 0)) return 'waiting';
  return status;
}

export interface NativeWorkflowControl {
  session_id: string;
  kind: 'approve' | 'resume' | 'retry' | 'rewind';
  expected_state_version: number;
  step_id?: string;
  preference_scope?: 'step' | 'following';
}
