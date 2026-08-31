export const WORKFLOW_CONTRACT_MAJOR = 1;

export type WorkflowStreamEventType =
  | 'workflow.snapshot'
  | 'workflow.patch'
  | 'step.patch'
  | 'attempt.patch'
  | 'artifact.upsert'
  | 'artifact.stale'
  | 'workflow.waiting'
  | 'workflow.completed'
  | 'attempt.progress';

export interface WorkflowStreamEvent {
  contract_version?: string;
  session_id?: string;
  cursor: number;
  type: WorkflowStreamEventType | string;
  entity_id?: string;
  state_version: number;
  payload: Record<string, unknown>;
}

export interface WorkflowProjectionState {
  contractVersion: string;
  cursor: number;
  stateVersion: number;
  projection: Record<string, unknown>;
  steps: Record<string, Record<string, unknown>>;
  attempts: Record<string, Record<string, unknown>>;
  artifacts: Record<string, Record<string, unknown>>;
  progress: Record<string, Record<string, unknown>>;
  resyncRequired: boolean;
  errorCode?: 'UNSUPPORTED_CONTRACT_VERSION' | 'CURSOR_GAP' | 'STATE_VERSION_GAP' | 'UNKNOWN_BREAKING_EVENT';
}

export const emptyWorkflowProjection = (): WorkflowProjectionState => ({
  contractVersion: 'workflow.v1',
  cursor: 0,
  stateVersion: 0,
  projection: {},
  steps: {},
  attempts: {},
  artifacts: {},
  progress: {},
  resyncRequired: false,
});

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

function deepMerge(
  current: Record<string, unknown>,
  patch: Record<string, unknown>,
): Record<string, unknown> {
  const next = { ...current };
  for (const [key, value] of Object.entries(patch)) {
    next[key] = isRecord(value) && isRecord(current[key])
      ? deepMerge(current[key], value)
      : value;
  }
  return next;
}

function majorOf(version: string): number | null {
  const match = /^workflow\.v(\d+)(?:\.|$)/.exec(version);
  return match ? Number(match[1]) : null;
}

function fail(
  state: WorkflowProjectionState,
  errorCode: NonNullable<WorkflowProjectionState['errorCode']>,
): WorkflowProjectionState {
  return { ...state, resyncRequired: true, errorCode };
}

function payloadProjection(payload: Record<string, unknown>): Record<string, unknown> {
  if (isRecord(payload.projection)) return payload.projection;
  const data = isRecord(payload.data) ? payload.data : undefined;
  if (data && isRecord(data.projection)) return data.projection;
  return payload;
}

/** Pure reducer shared by every Workflow surface. It never performs a refetch. */
export function reduceWorkflowEvent(
  state: WorkflowProjectionState,
  event: WorkflowStreamEvent,
): WorkflowProjectionState {
  const contractVersion = event.contract_version ?? state.contractVersion;
  if (majorOf(contractVersion) !== WORKFLOW_CONTRACT_MAJOR) {
    return fail(state, 'UNSUPPORTED_CONTRACT_VERSION');
  }

  if (event.type === 'workflow.snapshot') {
    return {
      ...emptyWorkflowProjection(),
      contractVersion,
      cursor: event.cursor,
      stateVersion: event.state_version,
      projection: payloadProjection(event.payload),
    };
  }

  // Duplicate replay is idempotent; a skipped durable cursor is not.
  if (event.cursor > 0 && event.cursor <= state.cursor) return state;
  if (state.cursor > 0 && event.cursor > state.cursor + 1) return fail(state, 'CURSOR_GAP');

  const isProgress = event.type === 'attempt.progress';
  if (!isProgress && event.state_version > state.stateVersion + 1) {
    return fail(state, 'STATE_VERSION_GAP');
  }
  if (!isProgress && event.state_version < state.stateVersion) return state;

  const next: WorkflowProjectionState = {
    ...state,
    contractVersion,
    cursor: event.cursor || state.cursor,
    stateVersion: isProgress ? state.stateVersion : Math.max(state.stateVersion, event.state_version),
    errorCode: undefined,
  };
  const entityId = event.entity_id ?? String(event.payload.id ?? event.payload.attempt_id ?? '');

  switch (event.type) {
    case 'workflow.patch':
      return { ...next, projection: deepMerge(state.projection, payloadProjection(event.payload)) };
    case 'workflow.waiting':
      return { ...next, projection: deepMerge(state.projection, { ...payloadProjection(event.payload), status: 'waiting' }) };
    case 'workflow.completed':
      return { ...next, projection: deepMerge(state.projection, { ...payloadProjection(event.payload), status: 'completed' }) };
    case 'step.patch':
      return entityId ? { ...next, steps: { ...state.steps, [entityId]: deepMerge(state.steps[entityId] ?? {}, event.payload) } } : next;
    case 'attempt.patch':
      return entityId ? { ...next, attempts: { ...state.attempts, [entityId]: deepMerge(state.attempts[entityId] ?? {}, event.payload) } } : next;
    case 'attempt.progress':
      return entityId ? { ...next, progress: { ...state.progress, [entityId]: deepMerge(state.progress[entityId] ?? {}, event.payload) } } : next;
    case 'artifact.upsert':
    case 'artifact.stale':
      return entityId ? { ...next, artifacts: { ...state.artifacts, [entityId]: deepMerge(state.artifacts[entityId] ?? {}, event.payload) } } : next;
    default:
      return fail(state, 'UNKNOWN_BREAKING_EVENT');
  }
}

export function markWorkflowResyncRequired(state: WorkflowProjectionState): WorkflowProjectionState {
  return { ...state, resyncRequired: true };
}
