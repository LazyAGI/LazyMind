export type WorkflowSessionStatus = 'active' | 'completed' | 'failed' | 'waiting' | 'stopped';

interface RuntimeProjectionStatus {
  completed?: boolean;
  status?: string;
  current?: string[];
  ready?: string[];
  blocked?: string[];
  nodes?: Record<string, {
    requires_approval?: boolean;
  }>;
}

/**
 * Reconcile the persisted session status with the runtime projection.
 *
 * The projection is computed from attempts and graph reachability, so it can
 * already be quiescent while a delayed session-status write still says active.
 * In that case the UI must allow the user to continue instead of presenting a
 * permanently busy workflow.
 */
export function reconcileWorkflowSessionStatus(
  status: WorkflowSessionStatus,
  projection?: RuntimeProjectionStatus,
): WorkflowSessionStatus {
  if (!projection) return status;

  // A durable Workflow Session cannot leave or change a terminal state.
  if (status === 'completed' || status === 'failed' || status === 'stopped') return status;
  if (projection.completed) return 'completed';

  const streamStatus = projection.status === 'running' ? 'active' : projection.status;
  const projectedStatus: WorkflowSessionStatus | undefined =
    streamStatus === 'active' || streamStatus === 'completed'
      || streamStatus === 'failed' || streamStatus === 'waiting' || streamStatus === 'stopped'
      ? streamStatus
      : undefined;

  const effectiveStatus = projectedStatus ?? status;
  if (
    effectiveStatus === 'active'
    && (projection.current?.length ?? 0) === 0
    && ((projection.ready?.length ?? 0) > 0 || (projection.blocked?.length ?? 0) > 0)
  ) {
    return 'waiting';
  }
  return effectiveStatus;
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
