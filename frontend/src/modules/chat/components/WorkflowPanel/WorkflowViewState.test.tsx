import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { readdirSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { load } from 'js-yaml';
import { AgentAppsAuth, AUTH_USER_CHANGE_EVENT } from '@/components/auth';
import { hydrateWorkflowUI, type WorkflowSession } from '@/modules/chat/store/workflowPanel';
import { useWorkflowViewState } from './useWorkflowViewState';

const session: WorkflowSession = {
  session_id: 'run', conversation_id: 'chat', workflow_id: 'workflow', workflow_mode: 'auto',
  status: 'active', current_step_id: '', created_at: '', updated_at: '',
};
const builtinIds = readdirSync(resolve(process.cwd(), '../workflows'), { withFileTypes: true })
  .filter(entry => entry.isDirectory() && !entry.name.startsWith('.'))
  .filter(entry => {
    try { readFileSync(resolve(process.cwd(), `../workflows/${entry.name}/workflow.yaml`)); return true; } catch { return false; }
  }).map(entry => entry.name);
beforeEach(() => {
  localStorage.clear();
  vi.spyOn(AgentAppsAuth, 'getUserInfo').mockReturnValue({ userId: 'user-a', username: 'test-a', token: 'fake-test-token' });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe('workflow run presentation preferences', () => {
  it.each(builtinIds)('expands catalog builtin %s without requiring per-package expansion configuration', id => {
    const spec = load(readFileSync(resolve(process.cwd(), `../workflows/${id}/workflow.yaml`), 'utf8')) as object;
    const ui = hydrateWorkflowUI({ ...spec, source_type: 'builtin' });
    const { result } = renderHook(() => useWorkflowViewState({ ...session, workflow_id: id }, ui, true));
    expect(result.current.view.expanded).toBe(true);
    act(() => result.current.update({ expanded: false, collapsed: true }));
    expect(result.current.view.expanded).toBe(false);
    expect(result.current.view.collapsed).toBe(true);
  });
  it('does not persist incomplete defaults before catalog loading and does not expand custom workflows', () => {
    const { result, rerender } = renderHook(({ ready, ui }) => useWorkflowViewState(session, ui, ready),
      { initialProps: { ready: false, ui: {} as ReturnType<typeof hydrateWorkflowUI> } });
    expect(localStorage.getItem(result.current.key)).toBeNull();
    rerender({ ready: true, ui: { source_type: 'user' } });
    expect(result.current.view.expanded).toBe(false);
  });
  it('separates users, workflow runs and layout from browsing mode', () => {
    const ui = { source_type: 'builtin', follow_mode: { default: 'following' as const } };
    const { result, rerender } = renderHook(({ run }) => useWorkflowViewState(run, ui, true), { initialProps: { run: session } });
    act(() => result.current.update({ mode: 'free', tabId: 'plan' }));
    expect(result.current.view.expanded).toBe(true);
    act(() => result.current.update({ expanded: false }));
    expect(result.current.view.mode).toBe('free');
    vi.mocked(AgentAppsAuth.getUserInfo).mockReturnValue({ userId: 'user-b', username: 'test-b', token: 'fake-test-token' });
    act(() => window.dispatchEvent(new Event(AUTH_USER_CHANGE_EVENT)));
    expect(result.current.view).toMatchObject({ mode: 'following', expanded: true, tabId: '' });
    act(() => result.current.update({ expanded: false }));
    rerender({ run: { ...session, session_id: 'next-run' } });
    expect(result.current.view.expanded).toBe(true);
  });
  it('recovers from corrupt preferences and unavailable storage', () => {
    localStorage.setItem('lazymind:workflow-view:user-a:run', '{bad json');
    const { result, unmount } = renderHook(() => useWorkflowViewState(session, { source_type: 'builtin' }, true));
    expect(result.current.view.expanded).toBe(true);
    unmount();
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked'); });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('blocked'); });
    const next = renderHook(() => useWorkflowViewState(session, { source_type: 'builtin' }, true));
    act(() => next.result.current.update({ expanded: false }));
    expect(next.result.current.view.expanded).toBe(false);
  });
});
