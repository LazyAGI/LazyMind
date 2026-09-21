import { afterEach, expect, it, vi } from 'vitest';
import { subscribeWorkflowEventStream } from './workflowEventStream';
import { watchWorkflowRun } from './loadWorkflowRun';
const streams = vi.hoisted(() => [] as any[]);
vi.mock('@/components/auth', () => ({ AgentAppsAuth: { getAuthHeaders: () => ({}) } }));
vi.mock('@/components/request', () => ({ BASE_URL: '' }));
vi.mock('@/modules/chat/utils/sse', () => ({ SSE: class {
 listeners: Record<string, Function> = {};
 close = vi.fn();
 constructor(public url: string, public options: any) { streams.push(this); }
 addEventListener(name: string, callback: Function) { this.listeners[name] = callback; }
} }));
afterEach(() => { streams.length = 0; vi.useRealTimers(); });
it('keeps external notifications opt-in for native subscriptions', () => {
 const subscription = subscribeWorkflowEventStream('native', 0, vi.fn(), vi.fn());
 expect(streams[0].listeners['control.changed']).toBeUndefined();
 expect(streams[0].listeners['workflow.patch']).toBeTypeOf('function');
 subscription.close();
});
it('refreshes for every external control notification and after reconnect', () => {
 vi.useFakeTimers();
 const reload = vi.fn();
 const close = watchWorkflowRun('external', reload);
 for (const type of ['control.changed', 'binding.changed', 'delivery.changed', 'review.changed', 'execution.settled', 'artifact.delete']) {
  streams[0].listeners[type]({ id: '12', data: JSON.stringify({ state_version: 12, payload: {} }) });
  vi.advanceTimersByTime(100);
 }
 expect(reload).toHaveBeenCalledTimes(6);
 streams[0].listeners.error({});
 vi.advanceTimersByTime(1000);
 expect(streams[1].options.headers['Last-Event-ID']).toBe('12');
 streams[1].listeners.open({});
 vi.advanceTimersByTime(100);
 expect(reload).toHaveBeenCalledTimes(7);
 streams[1].listeners['control.changed']({ data: '{}' });
 close();
 vi.advanceTimersByTime(100);
 expect(reload).toHaveBeenCalledTimes(7);
 expect(streams[1].close).toHaveBeenCalled();
});
