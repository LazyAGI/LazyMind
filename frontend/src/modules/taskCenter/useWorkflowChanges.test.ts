import { act, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useWorkflowChanges } from './useWorkflowChanges';
import { WORKFLOW_TASKS_CHANGED } from './events';

const fixtures = vi.hoisted(() => ({ streams: [] as Array<{ listeners: Map<string, (event: unknown) => void>; close: ReturnType<typeof vi.fn> }> }));
vi.mock('@/components/auth', () => ({ AgentAppsAuth: { getAuthHeaders: () => ({ Authorization: 'test' }) } }));
vi.mock('@/components/request', () => ({ BASE_URL: '' }));
vi.mock('@/modules/chat/utils/sse', () => ({ SSE: class {
  listeners = new Map<string, (event: unknown) => void>();
  close = vi.fn(() => this.listeners.get('readystatechange')?.({ readyState: 2 }));
  constructor() { fixtures.streams.push(this); }
  addEventListener(name: string, callback: (event: unknown) => void) { this.listeners.set(name, callback); }
} }));
afterEach(() => { vi.useRealTimers(); fixtures.streams = []; });

it('refreshes on state changes, reconnects once after disconnect, and releases the stream', () => {
  vi.useFakeTimers();
  const changed = vi.fn();
  window.addEventListener(WORKFLOW_TASKS_CHANGED, changed);
  const { unmount } = renderHook(useWorkflowChanges);
  const first = fixtures.streams[0];
  act(() => first.listeners.get('changed')?.({}));
  expect(changed).toHaveBeenCalledOnce();
  act(() => {
    first.listeners.get('error')?.({});
    first.listeners.get('readystatechange')?.({ readyState: 2 });
    vi.advanceTimersByTime(1000);
  });
  expect(fixtures.streams).toHaveLength(2);
  unmount();
  act(() => vi.advanceTimersByTime(2000));
  expect(fixtures.streams).toHaveLength(2);
  expect(fixtures.streams[1].close).toHaveBeenCalledOnce();
  window.removeEventListener(WORKFLOW_TASKS_CHANGED, changed);
});
