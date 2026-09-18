import { beforeEach, describe, expect, it, vi } from 'vitest';

const harness = vi.hoisted(() => ({
  latest: vi.fn(), snapshot: vi.fn(), watches: new Map<string, () => void>(),
}));
vi.mock('@/modules/chat/utils/request', () => ({
  WorkflowInfoApi: vi.fn(), TempUploadServiceApi: vi.fn(),
  WorkflowSessionApi: () => ({
    getLatestSession: harness.latest,
    listDismissedSessions: async () => ({ data: { data: { sessions: [] } } }),
  }),
}));
vi.mock('@/modules/chat/utils/loadWorkflowRun', () => ({
  loadWorkflowRunSnapshot: harness.snapshot,
  watchWorkflowRun: (id: string, callback: () => void) => {
    harness.watches.set(id, callback);
    return () => harness.watches.delete(id);
  },
}));
import { useWorkflowStore, type WorkflowSession } from './workflowPanel';

const session = (status: WorkflowSession['status'], version?: number): WorkflowSession => ({
  session_id: 'run', conversation_id: 'conv', workflow_id: 'workflow', workflow_mode: 'auto',
  status, state_version: version, current_step_id: '', created_at: '', updated_at: '',
});

describe('workflow refresh consistency', () => {
  beforeEach(() => {
    harness.latest.mockReset().mockResolvedValue({ data: { data: { session: session('active') } } });
    harness.snapshot.mockReset().mockResolvedValue({ session: session('active') });
    useWorkflowStore.setState({ sessionByConversation: {} });
  });

  it('rejects older snapshots but accepts a newer retry and a different run', () => {
    const store = useWorkflowStore.getState();
    store.setSession('conv', session('completed', 3));
    store.setSession('conv', session('active', 2));
    expect(useWorkflowStore.getState().sessionByConversation.conv?.status).toBe('completed');
    store.setSession('conv', session('active', 4));
    expect(useWorkflowStore.getState().sessionByConversation.conv?.status).toBe('active');
    store.setSession('conv', { ...session('waiting', 1), session_id: 'new-run' });
    expect(useWorkflowStore.getState().sessionByConversation.conv?.session_id).toBe('new-run');
  });

  it('serializes legacy event refreshes with manual loads and follows up on a bell', async () => {
    await useWorkflowStore.getState().loadActiveSession('conv');
    let resolve!: (value: { session: WorkflowSession }) => void;
    harness.snapshot.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
    const loading = useWorkflowStore.getState().loadActiveSession('conv');
    await vi.waitFor(() => expect(resolve).toBeDefined());
    const calls = harness.snapshot.mock.calls.length;
    harness.watches.get('run')!();
    harness.watches.get('run')!();
    await Promise.resolve();
    expect(harness.snapshot).toHaveBeenCalledTimes(calls);
    harness.snapshot.mockResolvedValue({ session: session('completed') });
    resolve({ session: session('active') });
    await loading;
    expect(harness.snapshot).toHaveBeenCalledTimes(calls + 1);
    expect(useWorkflowStore.getState().sessionByConversation.conv?.status).toBe('completed');
  });
  it('discards an in-flight snapshot after a different run is selected', async () => {
    await useWorkflowStore.getState().loadActiveSession('conv');
    let resolve!: (value: { session: WorkflowSession }) => void;
    harness.snapshot.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
    const loading = useWorkflowStore.getState().loadActiveSession('conv');
    await vi.waitFor(() => expect(resolve).toBeDefined());
    useWorkflowStore.getState().setSession('conv', { ...session('waiting'), session_id: 'new-run' });
    resolve({ session: session('completed') });
    await loading;
    expect(useWorkflowStore.getState().sessionByConversation.conv?.session_id).toBe('new-run');
  });

});
