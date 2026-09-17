export type WorkflowSessionStatus = 'active' | 'completed' | 'failed' | 'waiting' | 'stopped';

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

/** A prepared session can be waiting for its first dispatch without awaiting approval. */
export function isWorkflowReadyToStart(
  status: WorkflowSessionStatus,
  projection?: RuntimeProjectionStatus,
  recordedStepCount = 0,
): boolean {
  const ready = projection?.ready ?? [];
  return status === 'waiting'
    && recordedStepCount === 0
    && (projection?.current?.length ?? 0) === 0
    && ready.length > 0
    && ready.every((stepId) => projection?.nodes?.[stepId]?.requires_approval === false);
}
