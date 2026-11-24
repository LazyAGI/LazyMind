import { act, render, screen, cleanup } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { PaperAnalysisProgress } from '../PaperAnalysisProgress';
import { findWriterArtifactStream, shouldShowArtifactStream } from '../writerArtifactStream';
import { emptyWorkflowProjection, reduceWorkflowEvent } from '@/modules/chat/store/workflowProjection';
import { useWorkflowStore, workflowSnapshotSteps, type WorkflowSession, type WorkflowArtifactPreview } from '@/modules/chat/store/workflowPanel';

const network = vi.hoisted(() => ({ get: vi.fn(), streams: [] as any[] }));
vi.mock('@/modules/chat/utils/request', () => ({ WorkflowSessionApi: () => ({}), WorkflowInfoApi: () => ({}), TempUploadServiceApi: () => ({}) }));
vi.mock('@/components/auth', () => ({ AgentAppsAuth: { getAuthHeaders: () => ({}) } }));
vi.mock('@/components/request', () => ({ BASE_URL: '', axiosInstance: { get: network.get } }));
vi.mock('@/modules/chat/store/taskCenter', () => ({ useTaskCenterStore: (read: any) => read({ tasksByConversation: {} }) }));
vi.mock('@/modules/chat/utils/sse', () => ({ Method: { GET: 'GET' }, SSE: class {
  close = vi.fn();
  constructor(_url: string) { network.streams.push(this); }
} }));

afterEach(() => { cleanup(); vi.clearAllMocks(); network.streams.length = 0; });

function session(step = 'write_document'): WorkflowSession {
  return { session_id: 'run', conversation_id: 'chat', workflow_id: 'paper-writing', workflow_mode: 'auto',
    controller_host: 'external-agent', status: 'active', current_step_id: step, created_at: '', updated_at: '',
    steps: [{ id: 'attempt', session_id: 'run', step_id: step, attempt: 1, task_id: 'external-id',
      executor_host: 'external-agent', status: 'claimed', validity: 'effective', created_at: '', updated_at: '', fencing_generation: 1 }], slots: [] };
}
function preview(step = 'write_document'): WorkflowArtifactPreview {
  return { attempt_id: 'attempt', step_id: step, attempt: 1, slot: `${step}_document`, stream_id: 'attempt:1',
    seq: 1, content: '# First section', state: 'streaming' };
}

it.each(['literature_review', 'write_outline', 'write_document', 'revise_document'])(
  'routes %s previews from workflow events without persisting a slot or changing control', step => {
    const run = session(step), draft = preview(step);
    let state = reduceWorkflowEvent(emptyWorkflowProjection(), { type: 'workflow.snapshot', cursor: 10, state_version: 7,
      payload: { status: 'active', projection: { current: [step] } } });
    state = reduceWorkflowEvent(state, { type: 'artifact.preview', entity_id: 'attempt', cursor: 11, state_version: 7, payload: { ...draft } });
    const projected = { ...run, projection: state.projection } as WorkflowSession;
    expect(findWriterArtifactStream(projected, step, draft.slot, [])?.content).toBe(draft.content);
    state = reduceWorkflowEvent(state, { type: 'artifact.preview', entity_id: 'attempt', cursor: 12, state_version: 7,
      payload: { ...draft, seq: 2, content: `${draft.content}\n\nNext paragraph.` } });
    expect(findWriterArtifactStream({ ...run, projection: state.projection } as WorkflowSession, step, draft.slot, [])?.content).toContain('Next paragraph.');
    expect(state.stateVersion).toBe(7);
    expect(state.projection.current).toEqual([step]);
    expect(state.artifacts).toEqual({});
    expect(run.slots).toEqual([]);
    const store = useWorkflowStore.getState();
    useWorkflowStore.setState({ projectionBySession: {}, sessionByConversation: {} });
    store.setSession('chat', { ...run, state_version: 7 });
    store.applyWorkflowEvent('chat', 'run', { type: 'artifact.preview', entity_id: 'attempt', cursor: 11, state_version: 7, payload: { ...draft } });
    const displayed = useWorkflowStore.getState().sessionByConversation.chat!;
    expect(findWriterArtifactStream(displayed, step, draft.slot, [])?.content).toBe(draft.content);
    expect(displayed.slots).toEqual([]);
    expect(state.resyncRequired).toBe(false);
  },
);

it('hydrates previews on reopening and retains explicit execution hosts and fencing generations', () => {
  const draft = preview();
  const state = reduceWorkflowEvent(emptyWorkflowProjection(), { type: 'workflow.snapshot', cursor: 20, state_version: 7,
    payload: { projection: {}, artifact_previews: { attempt: draft } } });
  expect(state.projection.artifact_previews).toEqual({ attempt: draft });
  const steps = workflowSnapshotSteps('run', { write_document: [{ attempt: 1, attempt_id: 'attempt', task_id: 'native',
    executor_host: 'lazymind', fencing_generation: 2, status: 'running', validity: 'effective', started_at: '' }] });
  expect(steps?.[0]).toMatchObject({ executor_host: 'lazymind', fencing_generation: 2 });
});

it('keeps aborts, failed attempts, retries, rotated leases and manual editing from replaying a preview', () => {
  const draft = preview();
  const run = { ...session(), projection: { artifact_previews: { attempt: draft } } };
  const find = (value: WorkflowSession) => findWriterArtifactStream(value, 'write_document', draft.slot, []);
  expect(find(run)).toBeDefined();
  expect(findWriterArtifactStream(run, 'write_document', draft.slot, [], false)).toBeUndefined();
  for (const status of ['failed', 'interrupted', 'cancelled', 'succeeded']) {
    expect(find({ ...run, steps: [{ ...run.steps![0], status }] })).toBeUndefined();
  }
  expect(find({ ...run, status: 'stopped' })).toBeUndefined();
  expect(find({ ...run, steps: [{ ...run.steps![0], fencing_generation: 2 }] })).toBeUndefined();
  expect(find({ ...run, steps: [...run.steps!, { ...run.steps![0], id: 'retry', attempt: 2 }] })).toBeUndefined();
  const aborted = { ...draft, seq: 2, state: 'aborted' as const, content: '' };
  const first = reduceWorkflowEvent(emptyWorkflowProjection(), { type: 'artifact.preview', entity_id: 'attempt', cursor: 1, state_version: 0, payload: { ...aborted } });
  const late = reduceWorkflowEvent(first, { type: 'artifact.preview', entity_id: 'attempt', cursor: 2, state_version: 0, payload: { ...draft } });
  expect(late.projection.artifact_previews).toEqual({ attempt: aborted });
  expect(find({ ...run, projection: { artifact_previews: { attempt: aborted } } })).toBeUndefined();
  expect(shouldShowArtifactStream(find(run), [{ selected: true, producer_attempt_id: 'attempt' } as any])).toBe(false);
  expect(find({ ...run, workflow_id: 'ppt-workflow' })).toBeUndefined();
});

it('does not connect external data analysis to the native F4 progress API, but preserves native and mixed-host connections', async () => {
  network.get.mockResolvedValue({ data: {} });
  const run = session('data_analysis');
  const view = render(<PaperAnalysisProgress session={run} />);
  expect(network.get).not.toHaveBeenCalled();
  expect(network.streams).toHaveLength(0);
  expect(screen.queryByLabelText('数据分析执行进度')).toBeNull();
  view.rerender(<PaperAnalysisProgress session={{ ...run, steps: [{ ...run.steps![0], executor_host: 'lazymind' }] }} />);
  await act(async () => {});
  expect(network.get).toHaveBeenCalledTimes(1);
  expect(network.streams).toHaveLength(1);
  view.rerender(<PaperAnalysisProgress session={run} />);
  expect(network.streams[0].close).toHaveBeenCalled();
  view.unmount();
  render(<PaperAnalysisProgress session={{ ...run, controller_host: 'lazymind', steps: [{ ...run.steps![0], executor_host: undefined }] }} />);
  await act(async () => {});
  expect(network.get).toHaveBeenCalledTimes(2);
});
