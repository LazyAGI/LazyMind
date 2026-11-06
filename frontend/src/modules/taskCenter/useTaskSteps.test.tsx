import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { taskStepProgress, useTaskSteps } from './useTaskSteps';
import type { Task } from './api';
import { useWorkflowStore } from '@/modules/chat/store/workflowPanel';

const mocks = vi.hoisted(() => ({ get: vi.fn(), language: 'zh-CN', developer: true }));
vi.mock('@/components/request', () => ({ axiosInstance: { get: mocks.get, defaults: {} }, BASE_URL: '' }));
vi.mock('@/utils/developerMode', () => ({ isDeveloperModeActive: () => mocks.developer, DEVELOPER_ACTIVE_EVENT: 'developer-mode' }));
vi.mock('react-i18next', () => ({ initReactI18next: { type: '3rdParty', init: vi.fn() }, useTranslation: () => ({ i18n: { language: mocks.language }, t: (key: string) => key }) }));

const ids = ['analysis', 'materials', 'outline', 'prompts', 'slides'];
const labels = ['分析', '素材', '大纲', '每页提示词', '幻灯片'];
const task: Task = { id: 'task', user_id: 'user', conversation_id: 'conversation', conversation_state: 'active', task_type: 'workflow_run', workflow_session_id: 'session', status: 'running', steps: [{ step_id: 'analysis', status: 'running' }], created_at: '2026-11-06T00:00:00Z', updated_at: '2026-11-06T00:00:00Z' };

function mockWorkflow() {
  const session = { session_id: 'session', workflow_id: 'ppt', pinned_revision_id: 'fixed-revision', current_step_id: 'analysis', steps: [{ step_id: 'analysis', attempt: 1, status: 'running' }], slots: [] as { slot: string; selected: boolean }[] };
  const projection = { projection: { nodes: Object.fromEntries(ids.map(id => [id, { execution: 'none', branch: 'active' }])) } };
  const ui = { tabs: ids.map((id, i) => ({ id, step_id: id, label: labels[i], slots: [{ id }] })), tab_visibility_ready_material: undefined as string | undefined };
  mocks.get.mockImplementation(async (url: string) => url.endsWith('/projection') ? { data: projection }
    : url.includes('/workflow-sessions/') ? { data: { session } } : { data: { ui } });
  return { session, projection, ui };
}

beforeEach(() => {
  mocks.get.mockReset();
  mocks.language = 'zh-CN';
  mocks.developer = true;
  useWorkflowStore.setState({ workflowUIByWorkflow: {} });
});
afterEach(cleanup);

describe('definition-based task step progress', () => {
  it('shares one definition across 20 rows and does not refetch unchanged polling snapshots', async () => {
    const { session } = mockWorkflow();
    const values = Array.from({ length: 20 }, (_, i) => ({ ...task, id: `task-${i}`, workflow_session_id: `session-${i}` }));
    const views = values.map(value => renderHook(({ value }) => useTaskSteps(value), { initialProps: { value } }));
    await waitFor(() => { for (const view of views) expect(view.result.current.steps).toHaveLength(5); });
    expect(mocks.get).toHaveBeenCalledTimes(41);
    expect(mocks.get.mock.calls.filter(([url]) => url.includes('/workflows/'))).toHaveLength(1);

    await act(async () => {
      views.forEach((view, i) => view.rerender({ value: { ...values[i], steps: values[i].steps.map(step => ({ ...step })) } }));
    });
    expect(mocks.get).toHaveBeenCalledTimes(41);

    // A changed execution must still refresh even if its task timestamp is unchanged.
    session.steps[0].status = 'succeeded';
    views[0].rerender({ value: { ...values[0], steps: [{ step_id: 'analysis', status: 'succeeded' }] } });
    await waitFor(() => expect(views[0].result.current.steps[0].status).toBe('succeeded'));
    expect(mocks.get).toHaveBeenCalledTimes(43);
    expect(views[1].result.current.steps[0].status).toBe('running');
  });

  it('shares concurrent runtime reads between a row and its detail drawer', async () => {
    mockWorkflow();
    const first = renderHook(() => useTaskSteps(task));
    const second = renderHook(() => useTaskSteps({ ...task, steps: task.steps.map(step => ({ ...step })) }));
    await waitFor(() => {
      expect(first.result.current.steps).toHaveLength(5);
      expect(second.result.current.steps).toHaveLength(5);
    });
    expect(mocks.get).toHaveBeenCalledTimes(3);
  });

  it('reuses runtime data for language and audience changes but loads the matching definition language', async () => {
    mockWorkflow();
    const view = renderHook(() => useTaskSteps(task));
    await waitFor(() => expect(view.result.current.steps).toHaveLength(5));
    mocks.language = 'en-US';
    view.rerender();
    await waitFor(() => expect(mocks.get).toHaveBeenCalledTimes(4));
    expect(mocks.get.mock.calls[3][1]).toMatchObject({ headers: { 'Accept-Language': 'en-US' } });
    await act(async () => {
      mocks.developer = false;
      window.dispatchEvent(new Event('developer-mode'));
    });
    expect(mocks.get).toHaveBeenCalledTimes(4);
  });

  it('allows an explicit retry after a shared definition request fails', async () => {
    const { session, projection, ui } = mockWorkflow();
    let fail = true;
    mocks.get.mockImplementation(async (url: string) => {
      if (url.endsWith('/projection')) return { data: projection };
      if (url.includes('/workflow-sessions/')) return { data: { session } };
      if (fail) throw new Error('definition unavailable');
      return { data: { ui } };
    });
    const views = [0, 1].map(() => renderHook(({ retry }) => useTaskSteps(task, retry), { initialProps: { retry: 0 } }));
    await waitFor(() => { for (const view of views) expect(view.result.current.failed).toBe(true); });
    expect(mocks.get).toHaveBeenCalledTimes(3);
    fail = false;
    act(() => { views.forEach(view => view.rerender({ retry: 1 })); });
    await waitFor(() => { for (const view of views) {
      expect(view.result.current.failed).toBe(false);
      expect(view.result.current.steps).toHaveLength(5);
    } });
    expect(mocks.get).toHaveBeenCalledTimes(6);
  });

  it.each(['pending', 'running', 'paused', 'failed', 'succeeded', 'canceled'])('keeps the complete plan for task status %s even with one execution record', async status => {
    mockWorkflow();
    const { result } = renderHook(() => useTaskSteps({ ...task, status }));
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.steps.map(step => step.title)).toEqual(labels);
    expect(taskStepProgress(result.current.steps)).toEqual({ done: 0, total: 5, percent: 0 });
  });

  it('changes completed progress without growing the denominator and reuses the pinned definition', async () => {
    const { session } = mockWorkflow();
    const { result, rerender } = renderHook(({ value }) => useTaskSteps(value), { initialProps: { value: task } });
    await waitFor(() => expect(result.current.steps).toHaveLength(5));
    expect(taskStepProgress(result.current.steps)).toMatchObject({ done: 0, total: 5 });

    session.steps[0].status = 'succeeded';
    session.steps.push({ step_id: 'materials', attempt: 1, status: 'running' });
    rerender({ value: { ...task, updated_at: '2026-11-06T00:01:00Z', steps: session.steps } });
    await waitFor(() => expect(taskStepProgress(result.current.steps)).toEqual({ done: 1, total: 5, percent: 20 }));
    const packageReads = mocks.get.mock.calls.filter(([url]) => url.includes('/workflows/'));
    expect(packageReads).toHaveLength(1);
    expect(packageReads[0][1]).toMatchObject({ params: { revision_id: 'fixed-revision' } });
  });

  it('counts one logical step per tab and ignores stale or older retry attempts', async () => {
    const { session } = mockWorkflow();
    session.steps = [
      { step_id: 'analysis', attempt: 1, status: 'succeeded', validity: 'stale' },
      { step_id: 'analysis', attempt: 2, status: 'failed' },
      { step_id: 'analysis', attempt: 3, status: 'running' },
    ] as typeof session.steps;
    const { result } = renderHook(() => useTaskSteps({ ...task, steps: session.steps }));
    await waitFor(() => expect(result.current.steps).toHaveLength(5));
    expect(result.current.steps[0].status).toBe('running');
    expect(taskStepProgress(result.current.steps)).toEqual({ done: 0, total: 5, percent: 0 });
  });

  it('waits for a branch plan instead of publishing an incomplete total', async () => {
    const { session, ui } = mockWorkflow();
    ui.tab_visibility_ready_material = 'execution_plan';
    const { result, rerender } = renderHook(({ value }) => useTaskSteps(value), { initialProps: { value: task } });
    await waitFor(() => expect(result.current.planPending).toBe(true));
    expect(result.current.loading).toBe(true);
    expect(result.current.steps).toEqual([]);

    session.slots.push({ slot: 'execution_plan', selected: true });
    rerender({ value: { ...task, updated_at: '2026-11-06T00:01:00Z' } });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(taskStepProgress(result.current.steps).total).toBe(5);
    expect(result.current.planPending).toBe(false);
  });

  it.each(['pending', 'waiting', 'waiting_inputs', 'paused', 'failed', 'interrupted', 'canceled', 'succeeded', 'completed'])(
    'does not keep loading an ungenerated plan for a %s task', async status => {
      const { ui } = mockWorkflow();
      ui.tab_visibility_ready_material = 'execution_plan';
      const { result } = renderHook(() => useTaskSteps({ ...task, status }));
      // The initial request is still loading; missing plan data is determined by its response.
      expect(result.current.loading).toBe(true);
      await waitFor(() => expect(result.current.planPending).toBe(true));
      expect(result.current.loading).toBe(false);
      expect(result.current.failed).toBe(false);
      expect(result.current.steps).toEqual([]);
    },
  );

  it.each(['paused', 'failed', 'canceled'])('stops the planning spinner when a running task becomes %s', async status => {
    const { ui } = mockWorkflow();
    ui.tab_visibility_ready_material = 'execution_plan';
    const view = renderHook(({ value }) => useTaskSteps(value), { initialProps: { value: task } });
    await waitFor(() => expect(view.result.current.planPending).toBe(true));
    expect(view.result.current.loading).toBe(true);
    view.rerender({ value: { ...task, status } });
    expect(view.result.current.loading).toBe(false);
    await act(async () => { await Promise.resolve(); });
    expect(view.result.current).toMatchObject({ planPending: true, loading: false, failed: false, steps: [] });
  });

  it('waits again after resuming and shows only the complete plan once it is generated', async () => {
    const { session, ui } = mockWorkflow();
    ui.tab_visibility_ready_material = 'execution_plan';
    const view = renderHook(({ value }) => useTaskSteps(value), { initialProps: { value: { ...task, status: 'paused' } } });
    await waitFor(() => expect(view.result.current.planPending).toBe(true));
    expect(view.result.current.loading).toBe(false);
    view.rerender({ value: task });
    expect(view.result.current.loading).toBe(true);
    await act(async () => { await Promise.resolve(); });
    expect(view.result.current.steps).toEqual([]);

    session.slots.push({ slot: 'execution_plan', selected: true });
    view.rerender({ value: { ...task, updated_at: '2026-11-06T00:01:00Z' } });
    await waitFor(() => expect(view.result.current.steps).toHaveLength(5));
    expect(view.result.current).toMatchObject({ planPending: false, loading: false, failed: false });
  });

  it('reports a definition lookup error without substituting partial execution history', async () => {
    const { session, projection } = mockWorkflow();
    mocks.get.mockImplementation(async (url: string) => {
      if (url.endsWith('/projection')) return { data: projection };
      if (url.includes('/workflow-sessions/')) return { data: { session } };
      throw new Error('definition unavailable');
    });
    const { result } = renderHook(() => useTaskSteps(task));
    await waitFor(() => expect(result.current.failed).toBe(true));
    expect(result.current.steps).toEqual([]);
    expect(result.current.planPending).toBe(false);
    expect(result.current.loading).toBe(false);
  });
});
