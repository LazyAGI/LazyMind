import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { useWorkflowStore } from './workflowPanel';
import { workflowUICacheKey } from '@/modules/chat/utils/workflowUIIdentity';

const api = vi.hoisted(() => ({ getWorkflow: vi.fn() }));
vi.mock('@/modules/chat/utils/request', () => ({
  WorkflowInfoApi: () => api,
  WorkflowSessionApi: () => ({}),
  TempUploadServiceApi: () => ({}),
}));

function response(...ids: string[]) {
  return { data: { ui: { tabs: ids.map(id => ({ id, label: id, slots: [] })) } } };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}

beforeEach(async () => {
  api.getWorkflow.mockReset();
  useWorkflowStore.setState({ workflowUIByWorkflow: {} });
  await i18n.changeLanguage('zh-CN');
});

describe('revision-scoped workflow UI cache', () => {
  it('reads and reuses only the session-pinned revision, not an earlier workflow cache', async () => {
    useWorkflowStore.setState({ workflowUIByWorkflow: { 'smoke:zh-CN': { tabs: [{ id: 'stale', slots: [] }] } } });
    api.getWorkflow.mockResolvedValueOnce(response('result', 'prepare'))
      .mockResolvedValueOnce(response('prepare', 'review', 'result'));
    const { fetchWorkflowUI } = useWorkflowStore.getState();
    const oldUI = await fetchWorkflowUI('smoke', 'v1');
    const newUI = await fetchWorkflowUI('smoke', 'v2');
    expect(oldUI.tabs?.map(tab => tab.id)).toEqual(['result', 'prepare']);
    expect(newUI.tabs?.map(tab => tab.id)).toEqual(['prepare', 'review', 'result']);
    expect(await fetchWorkflowUI('smoke', 'v1')).toBe(oldUI);
    expect(await fetchWorkflowUI('smoke', 'v2')).toBe(newUI);
    expect(api.getWorkflow.mock.calls).toEqual([
      ['smoke', { params: { revision_id: 'v1' }, headers: { 'Accept-Language': 'zh-CN' } }],
      ['smoke', { params: { revision_id: 'v2' }, headers: { 'Accept-Language': 'zh-CN' } }],
    ]);
  });

  it('does not let a late older-revision response overwrite the newer revision', async () => {
    const oldRequest = deferred<ReturnType<typeof response>>();
    const newRequest = deferred<ReturnType<typeof response>>();
    api.getWorkflow.mockReturnValueOnce(oldRequest.promise).mockReturnValueOnce(newRequest.promise);
    const { fetchWorkflowUI } = useWorkflowStore.getState();
    const oldUI = fetchWorkflowUI('smoke', 'v1');
    const newUI = fetchWorkflowUI('smoke', 'v2');
    newRequest.resolve(response('prepare', 'review', 'result'));
    const newConfig = await newUI;
    oldRequest.resolve(response('result', 'prepare'));
    const oldConfig = await oldUI;
    const cache = useWorkflowStore.getState().workflowUIByWorkflow;
    expect(cache[workflowUICacheKey('smoke', 'v2', 'zh-CN')]).toBe(newConfig);
    expect(cache[workflowUICacheKey('smoke', 'v1', 'zh-CN')]).toBe(oldConfig);
  });

  it('isolates both workflow identity and language within the same revision', async () => {
    api.getWorkflow.mockResolvedValueOnce(response('中文')).mockResolvedValueOnce(response('English'))
      .mockResolvedValueOnce(response('another'));
    const { fetchWorkflowUI } = useWorkflowStore.getState();
    const chinese = await fetchWorkflowUI('smoke', 'v1');
    await i18n.changeLanguage('en-US');
    const english = await fetchWorkflowUI('smoke', 'v1');
    const otherWorkflow = await fetchWorkflowUI('another', 'v1');
    expect(english.tabs?.[0].label).toBe('English');
    expect(otherWorkflow.tabs?.[0].id).toBe('another');
    await i18n.changeLanguage('zh-CN');
    expect(await fetchWorkflowUI('smoke', 'v1')).toBe(chinese);
    expect(api.getWorkflow).toHaveBeenCalledTimes(3);
  });

  it('never substitutes head or another version when a pinned revision fails, and allows retry', async () => {
    api.getWorkflow.mockResolvedValueOnce(response('head')).mockRejectedValueOnce(new Error('not found'))
      .mockResolvedValueOnce(response('pinned'));
    const { fetchWorkflowUI } = useWorkflowStore.getState();
    await fetchWorkflowUI('smoke');
    expect(await fetchWorkflowUI('smoke', 'v1')).toEqual({});
    expect(useWorkflowStore.getState().workflowUIByWorkflow[workflowUICacheKey('smoke', 'v1', 'zh-CN')]).toBeUndefined();
    expect((await fetchWorkflowUI('smoke', 'v1')).tabs?.[0].id).toBe('pinned');
    expect(api.getWorkflow.mock.calls.slice(1).map(call => call[1].params)).toEqual([{ revision_id: 'v1' }, { revision_id: 'v1' }]);
  });

  it('refetches mutable head reads for existing callers without a pinned revision', async () => {
    api.getWorkflow.mockResolvedValueOnce(response('old-head')).mockResolvedValueOnce(response('new-head'));
    const { fetchWorkflowUI } = useWorkflowStore.getState();
    expect((await fetchWorkflowUI('smoke')).tabs?.[0].id).toBe('old-head');
    expect((await fetchWorkflowUI('smoke')).tabs?.[0].id).toBe('new-head');
    expect(api.getWorkflow).toHaveBeenCalledTimes(2);
    expect(api.getWorkflow.mock.calls[0][1].params).toBeUndefined();
  });
});
