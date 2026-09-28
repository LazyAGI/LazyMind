import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ordinary } from '../components/TaskCenter/ordinaryTestFixtures';
import { applyOrdinaryWorkflowSnapshot, useWorkflowStore, type WorkflowSession, type OrdinaryWorkflowSnapshot } from './workflowPanel';
import { emptyWorkflowProjection } from './workflowProjection';

const api = vi.hoisted(() => ({ getProjection: vi.fn() }));
vi.mock('@/modules/chat/utils/request', () => ({
  WorkflowSessionApi: () => api,
  WorkflowInfoApi: vi.fn(),
  TempUploadServiceApi: vi.fn(),
}));

function session(): WorkflowSession {
  return { session_id: 'session', conversation_id: 'conversation', workflow_id: 'writer', workflow_mode: 'auto',
    status: 'active', current_step_id: 'write', created_at: '', updated_at: '', state_version: 1,
    steps: [1, 2].map(attempt => ({ id: `attempt-${attempt}`, session_id: 'session', step_id: 'write',
      task_id: '', attempt, status: attempt === 1 ? 'failed' : 'running', created_at: '', updated_at: '' })) };
}

function snapshot(revision: number, attempt = 'attempt-2'): OrdinaryWorkflowSnapshot {
  return { schema_version: 1, session_id: 'session', revision, runs: [{ run_id: 'session', revision, final_output_refs: [] }],
    tasks: [ordinary('hosted', { display_key: `workflow:session:write:${attempt}`, task_id: null, session_id: 'session',
      workflow_step_id: 'write', attempt_id: attempt, execution_id: attempt, revision })] };
}

describe('ordinary workflow recovery', () => {
  beforeEach(() => {
    api.getProjection.mockReset();
    useWorkflowStore.setState({ sessionByConversation: {}, projectionBySession: {} });
  });

  it('binds hosted public details to the current attempt and never the failed attempt', () => {
    const restored = applyOrdinaryWorkflowSnapshot(session(), snapshot(4));
    expect(restored.steps?.[0].ordinary).toBeUndefined();
    expect(restored.steps?.[1].ordinary?.attempt_id).toBe('attempt-2');
    expect(applyOrdinaryWorkflowSnapshot(restored, snapshot(3))).toBe(restored);
    expect(applyOrdinaryWorkflowSnapshot(restored, { ...snapshot(5), session_id: 'different' })).toBe(restored);
  });

  it('keeps a loaded page when setSession receives the same revision', () => {
    const initial = applyOrdinaryWorkflowSnapshot(session(), snapshot(4));
    useWorkflowStore.getState().setSession('conversation', initial);
    const source = { source_id: 'page-2', platform: '网页', domain: 'example.com', title: 'More', kind: 'web' as const };
    const paged = { ...initial, ordinary_tasks: initial.ordinary_tasks!.map(task => ({ ...task, sources: [source] })) };
    useWorkflowStore.getState().setSession('conversation', paged);
    expect(useWorkflowStore.getState().sessionByConversation.conversation?.ordinary_tasks?.[0].sources).toEqual([source]);
    expect(useWorkflowStore.getState().sessionByConversation.conversation?.steps?.[1].ordinary?.sources).toEqual([source]);
    useWorkflowStore.getState().setSession('conversation', session());
    expect(useWorkflowStore.getState().sessionByConversation.conversation?.ordinary_tasks?.[0].sources).toEqual([source]);
  });

  it('restores saved plans into workflow attempts after reloading', async () => {
    const saved = snapshot(4);
    saved.tasks[0].plan_steps = ['Read requirements', 'Check constraints', 'Write brief'];
    api.getProjection.mockResolvedValue({ data: { data: saved } });
    useWorkflowStore.getState().setSession('conversation', session());
    await useWorkflowStore.getState().refreshOrdinarySession('conversation', 'session');
    const restored = useWorkflowStore.getState().sessionByConversation.conversation!;
    expect(restored.steps?.[0].ordinary).toBeUndefined();
    expect(restored.steps?.[1].ordinary?.plan_steps).toEqual(saved.tasks[0].plan_steps);
    expect(restored.steps?.[1].ordinary?.process_steps).toEqual([]);
  });

  it('does not let a slow REST response overwrite a newer public snapshot', async () => {
    let resolve!: (value: unknown) => void;
    api.getProjection.mockReturnValueOnce(new Promise(value => { resolve = value; }));
    useWorkflowStore.getState().setSession('conversation', session());
    const loading = useWorkflowStore.getState().refreshOrdinarySession('conversation', 'session');
    useWorkflowStore.getState().setSession('conversation', applyOrdinaryWorkflowSnapshot(session(), snapshot(8)));
    resolve({ data: { data: snapshot(6) } });
    await loading;
    expect(useWorkflowStore.getState().sessionByConversation.conversation?.ordinary_revision).toBe(8);
    expect(api.getProjection).toHaveBeenCalledWith('session', expect.objectContaining({ params: { view: 'ordinary' } }));
  });

  it('retains public details on reload failure', async () => {
    const initial = applyOrdinaryWorkflowSnapshot(session(), snapshot(4));
    useWorkflowStore.getState().setSession('conversation', initial);
    api.getProjection.mockRejectedValueOnce(new Error('offline'));
    await useWorkflowStore.getState().refreshOrdinarySession('conversation', 'session');
    const current = useWorkflowStore.getState().sessionByConversation.conversation!;
    expect(current.ordinary_tasks).toEqual(initial.ordinary_tasks);
    expect(current.ordinary_error).toBe(true);
  });

  it('consumes public invalidation cursors without treating them as graph errors', async () => {
    useWorkflowStore.getState().setSession('conversation', session());
    useWorkflowStore.setState({ projectionBySession: { session: emptyWorkflowProjection() } });
    api.getProjection.mockResolvedValue({ data: { data: snapshot(9) } });
    useWorkflowStore.getState().applyWorkflowEvent('conversation', 'session', {
      type: 'attempt.public_display', cursor: 9, state_version: 1, entity_id: 'attempt-2', payload: {},
    });
    await vi.waitFor(() => expect(useWorkflowStore.getState().sessionByConversation.conversation?.ordinary_revision).toBe(9));
    const projection = useWorkflowStore.getState().projectionBySession.session;
    expect(projection.cursor).toBe(9);
    expect(projection.resyncRequired).toBe(false);
  });
});
