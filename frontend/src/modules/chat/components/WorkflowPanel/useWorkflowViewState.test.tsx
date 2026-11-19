import { useLayoutEffect } from 'react';
import { renderHook } from '@testing-library/react';
import { beforeEach, expect, it } from 'vitest';
import type { WorkflowSession, WorkflowUI } from '@/modules/chat/store/workflowPanel';
import { useWorkflowViewState } from './useWorkflowViewState';

beforeEach(() => localStorage.clear());

it('preserves navigation made as the workflow UI becomes ready', () => {
  const session = { session_id: 'initial-navigation', conversation_id: 'chat', status: 'completed' } as WorkflowSession;
  const ui: WorkflowUI = { tabs: [{ id: 'skipped', label: 'Skipped', slots: [] }] };
  const { result, rerender } = renderHook(({ ready }) => {
    const state = useWorkflowViewState(session, ui, ready);
    // A tab can be used as soon as its DOM mounts, before passive initialization.
    useLayoutEffect(() => {
      if (ready) state.update({ mode: 'free', tabId: 'skipped' });
    }, [ready, state.update]);
    return state;
  }, { initialProps: { ready: false } });

  rerender({ ready: true });

  expect(result.current.view.mode).toBe('free');
  expect(result.current.view.tabId).toBe('skipped');
  expect(JSON.parse(localStorage.getItem(result.current.key)!)).toMatchObject({ mode: 'free', tabId: 'skipped' });
});
