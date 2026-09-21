import { act, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { WorkflowSession } from '@/modules/chat/store/workflowPanel';
import { activeExecutionTasks, reduceActivity, useExecutionActivity } from './useExecutionActivity';

const streams = vi.hoisted(() => [] as any[]);
vi.mock('@/components/auth', () => ({ AgentAppsAuth: { getAuthHeaders: () => ({}) } }));
vi.mock('@/modules/chat/utils/request', () => ({ taskStreamUrl: (id: string) => id }));
vi.mock('@/modules/chat/utils/sse', () => ({ Method: { GET: 'GET' }, SSE: class {
  close = vi.fn();
  constructor(id: string, options: any) { streams.push({ id, ...options, close: this.close }); }
} }));
const session = (task = 'one', status = 'running') => ({ session_id: 'run', status: 'active', steps: [
  { step_id: 'slides', task_id: task, attempt: 1, status, validity: 'effective' },
] }) as WorkflowSession;
afterEach(() => { streams.length = 0; vi.useRealTimers(); });

it('retains only safe display metadata and clamps progress without regressing', () => {
  const tool = reduceActivity({ progress: 60 }, { type: 'tool_calls', tool_calls: [{ name: 'search', args: { secret: 'x' } }] });
  expect(tool).toEqual({ kind: 'tool', tool: 'search', progress: 60 });
  expect(reduceActivity(tool, { type: 'progress', progress: 20 })).toEqual(tool);
  expect(reduceActivity(tool, { type: 'progress', progress: 120 }).progress).toBe(100);
  expect(reduceActivity(tool, { type: 'think', think: 'private reasoning' })).toEqual({ kind: 'thinking', tool: undefined, progress: 60 });
  expect(reduceActivity(tool, { type: 'done' })).toEqual({ finished: true });
  expect(reduceActivity({ finished: true }, { type: 'think' })).toEqual({ finished: true });
});

it('ignores finished, reviewed, stale and superseded attempts', () => {
  expect(activeExecutionTasks(session('one', 'succeeded'))).toEqual([]);
  expect(activeExecutionTasks({ ...session(), status: 'stopped' })).toEqual([]);
  expect(activeExecutionTasks({ ...session(), projection: { nodes: { slides: { execution: 'succeeded' } as any } } })).toEqual([]);
  const run = session();
  run.steps!.push({ ...run.steps![0], task_id: 'two', attempt: 2, status: 'succeeded' });
  expect(activeExecutionTasks(run)).toEqual([]);
});

it('resets on retry and closes the previous stream; ignores late messages', () => {
  const hook = renderHook(({ run }) => useExecutionActivity(run), { initialProps: { run: session() } });
  act(() => streams[0].callbacks.message({ data: JSON.stringify({ type: 'progress', progress: 80 }) }));
  expect(hook.result.current.one.progress).toBe(80);
  hook.rerender({ run: session('two') });
  expect(streams[0].close).toHaveBeenCalled();
  expect(hook.result.current.one).toBeUndefined();
  expect(hook.result.current.two.progress).toBeUndefined();
  act(() => streams[0].callbacks.message({ data: JSON.stringify({ type: 'think' }) }));
  expect(hook.result.current.one).toBeUndefined();
  hook.rerender({ run: session('two', 'succeeded') });
  expect(hook.result.current).toEqual({});
  expect(streams[1].close).toHaveBeenCalled();
  hook.unmount();
});

it('reconnects after transport errors and cancels retries on unmount', () => {
  vi.useFakeTimers();
  const hook = renderHook(() => useExecutionActivity(session()));
  act(() => streams[0].callbacks.error());
  expect(hook.result.current.one.kind).toBe('reconnecting');
  act(() => vi.advanceTimersByTime(1500));
  expect(streams).toHaveLength(2);
  act(() => streams[0].callbacks.message({ data: JSON.stringify({ type: 'done' }) }));
  expect(hook.result.current.one.finished).toBeUndefined();
  act(() => streams[1].callbacks.error());
  hook.unmount();
  act(() => vi.advanceTimersByTime(1500));
  expect(streams).toHaveLength(2);
});

it('streams published pages, retains them across reconnect, and clears them when the step settles', () => {
  vi.useFakeTimers();
  const hook = renderHook(({ run }) => useExecutionActivity(run), { initialProps: { run: session() } });
  const page = { type: 'artifact', slot: 'html', content_type: 'text', seq: 1, value: { text: 'page 1' } };
  act(() => streams[0].callbacks.message({ data: JSON.stringify(page) }));
  expect(hook.result.current.one.artifacts).toHaveLength(1);
  act(() => streams[0].callbacks.error());
  expect(hook.result.current.one.artifacts).toHaveLength(1);
  act(() => vi.advanceTimersByTime(1500));
  expect(hook.result.current.one.artifacts).toHaveLength(1);
  act(() => streams[1].callbacks.message({ data: JSON.stringify(page) }));
  expect(hook.result.current.one.artifacts).toHaveLength(1);
  act(() => streams[1].callbacks.message({ data: JSON.stringify({ type: 'done' }) }));
  expect(hook.result.current.one.artifacts).toHaveLength(1);
  expect(streams[1].close).toHaveBeenCalled();
  hook.rerender({ run: session('one', 'succeeded') });
  expect(hook.result.current).toEqual({});
  hook.unmount();
});


it('does not subscribe without an external run and closes subscriptions when it is removed', () => {
  const hook = renderHook(({ run }: { run: WorkflowSession | undefined }) => useExecutionActivity(run), {
    initialProps: { run: undefined as WorkflowSession | undefined },
  });
  expect(streams).toHaveLength(0);
  hook.rerender({ run: session() });
  expect(streams).toHaveLength(1);
  hook.rerender({ run: undefined });
  expect(streams[0].close).toHaveBeenCalled();
  expect(hook.result.current).toEqual({});
  hook.unmount();
});

it('keeps real progress across reconnect and ignores replayed lower values', () => {
  vi.useFakeTimers();
  const hook = renderHook(() => useExecutionActivity(session()));
  act(() => streams[0].callbacks.message({ data: JSON.stringify({ type: 'progress', progress: 65 }) }));
  act(() => streams[0].callbacks.error());
  expect(hook.result.current.one.progress).toBe(65);
  act(() => vi.advanceTimersByTime(1500));
  act(() => streams[1].callbacks.message({ data: JSON.stringify({ type: 'progress', progress: 20 }) }));
  expect(hook.result.current.one.progress).toBe(65);
  hook.unmount();
});
