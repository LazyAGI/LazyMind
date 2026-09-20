import { beforeEach, describe, expect, it, vi } from 'vitest';
import { emptyWorkflowProjection, reduceWorkflowEvent } from './workflowProjection';
import { useWorkflowStore, type WorkflowSession } from './workflowPanel';
import { reconcileWorkflowTasks } from '../utils/workflowTaskStatus';
import { isCurrentCapabilityFailure } from '../utils/mediaCapabilityDependency';

const api = vi.hoisted(() => ({ getLatestSession: vi.fn(), getProjection: vi.fn(), getDismissedSessions: vi.fn() }));
vi.mock('@/modules/chat/utils/request', () => ({
  WorkflowSessionApi: () => api, WorkflowInfoApi: () => ({}), TempUploadServiceApi: () => ({}),
}));
const session = (version = 1): WorkflowSession => ({
  session_id: 's', conversation_id: 'c', workflow_id: 'image-workflow', workflow_mode: 'auto',
  state_version: version, status: 'waiting', current_step_id: '', created_at: '', updated_at: '',
});
const snapshot = (version: number, cursor: number, status: string, completed = false) => ({
  type: 'workflow.snapshot', state_version: version, cursor,
  payload: { status, projection: { completed, current: completed ? [] : ['edit'],
    nodes: { edit: { execution: completed ? 'succeeded' : 'running' } } } },
});

beforeEach(() => {
  vi.clearAllMocks();
  useWorkflowStore.setState({ sessionByConversation: {}, projectionBySession: {}, autoRunningByConversation: {} });
});

describe('versioned workflow synchronization', () => {
  it('accepts global cursor gaps and legacy unversioned attempt events', () => {
    let state = reduceWorkflowEvent(emptyWorkflowProjection(), snapshot(8, 100, 'active'));
    state = reduceWorkflowEvent(state, { type: 'attempt.patch', state_version: 0, cursor: 105,
      entity_id: 'a', payload: { status: 'succeeded' } });
    expect(state.resyncRequired).toBe(false);
    expect(state.cursor).toBe(105);
    expect(state.stateVersion).toBe(8);
    expect(state.attempts.a.status).toBe('succeeded');
    state = reduceWorkflowEvent(state, snapshot(12, 109, 'completed', true));
    expect(state.projection.status).toBe('completed');
    expect(state.resyncRequired).toBe(false);
  });

  it('consumes old event cursors without regressing a completed projection', () => {
    const completed = reduceWorkflowEvent(emptyWorkflowProjection(), snapshot(12, 109, 'completed', true));
    const old = reduceWorkflowEvent(completed, { type: 'workflow.patch', state_version: 8, cursor: 110,
      payload: { status: 'waiting' } });
    expect(old.cursor).toBe(110);
    expect(old.projection.status).toBe('completed');
    expect(reduceWorkflowEvent(old, snapshot(8, 105, 'waiting'))).toBe(old);
  });

  it('reopens only on a newer snapshot and clears a stale automatic-running flag at completion', () => {
    const store = useWorkflowStore.getState();
    store.setSession('c', session());
    store.setAutoRunning('c', true);
    store.applyWorkflowEvent('c', 's', snapshot(12, 109, 'completed', true));
    expect(useWorkflowStore.getState().sessionByConversation.c?.status).toBe('completed');
    expect(useWorkflowStore.getState().autoRunningByConversation.c).toBe(false);
    store.setAutoRunning('c', true);
    expect(useWorkflowStore.getState().autoRunningByConversation.c).toBe(false);
    store.setSession('c', { ...session(8), status: 'waiting' });
    expect(useWorkflowStore.getState().sessionByConversation.c?.status).toBe('completed');
    store.applyWorkflowEvent('c', 's', snapshot(13, 111, 'active'));
    expect(useWorkflowStore.getState().sessionByConversation.c?.status).toBe('active');
  });

  it('does not let a REST load started before completion overwrite the live snapshot', async () => {
    let resolve!: (value: unknown) => void;
    api.getLatestSession.mockReturnValueOnce(new Promise((done) => { resolve = done; }))
      .mockResolvedValue({ data: { data: { session: session(12) } } });
    api.getProjection.mockResolvedValueOnce({ data: { data: { state_version: 8, status: 'waiting', projection: {}, attempt_history: {} } } })
      .mockResolvedValue({ data: { data: { state_version: 12, status: 'completed',
      projection: { completed: true }, attempt_history: {} } } });
    const store = useWorkflowStore.getState();
    store.setSession('c', session(8));
    const loading = store.loadActiveSession('c');
    store.applyWorkflowEvent('c', 's', snapshot(12, 109, 'completed', true));
    resolve({ data: { data: { session: session(8) } } });
    await loading;
    expect(useWorkflowStore.getState().sessionByConversation.c?.status).toBe('completed');
    expect(useWorkflowStore.getState().sessionByConversation.c?.state_version).toBe(12);
  });

  it('keeps a REST completion when a subsequent attempt event advances the SSE cache', () => {
    const store = useWorkflowStore.getState();
    store.setSession('c', session(8));
    store.applyWorkflowEvent('c', 's', snapshot(8, 100, 'active'));
    store.setSession('c', { ...session(12), status: 'completed', projection: { completed: true, current: [] } });
    store.applyWorkflowEvent('c', 's', { type: 'attempt.progress', state_version: 12, cursor: 105,
      entity_id: 'a', payload: { percent: 100 } });
    expect(useWorkflowStore.getState().sessionByConversation.c?.status).toBe('completed');
    expect(useWorkflowStore.getState().projectionBySession.s.projection.completed).toBe(true);
  });
});

it('removes configuration notices for stale failures, successful retries and completed sessions', () => {
  const failed = { id: 'a', session_id: 's', step_id: 'analyze', attempt: 1, task_id: 'a',
    status: 'failed', validity: 'effective' as const, created_at: '', updated_at: '' };
  const current = { ...session(), status: 'failed' as const, steps: [failed] };
  expect(isCurrentCapabilityFailure(current, 'a')).toBe(true);
  expect(isCurrentCapabilityFailure({ ...current, steps: [{ ...failed, validity: 'stale' }] }, 'a')).toBe(false);
  expect(isCurrentCapabilityFailure({ ...current, steps: [failed, { ...failed, attempt: 2, task_id: 'b', status: 'running' }] }, 'a')).toBe(false);
  expect(isCurrentCapabilityFailure({ ...current, status: 'completed' }, 'a')).toBe(false);
});


it('renders workflow task status from the same attempt snapshot as the main panel', () => {
  const tasks = [{ task_id: 'a', status: 'running' as const, agent_type: 'workflow_step' },
    { task_id: 'ordinary', status: 'running' as const, agent_type: 'general' }];
  const steps = [{ id: 'a', session_id: 's', step_id: 'edit', attempt: 1, task_id: 'a',
    status: 'succeeded', validity: 'effective' as const, created_at: '', updated_at: '' }];
  expect(reconcileWorkflowTasks(tasks, steps).map((task) => task.status)).toEqual(['succeeded', 'running']);
});
