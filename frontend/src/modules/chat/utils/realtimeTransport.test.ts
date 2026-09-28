import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SSE, Method } from './sse';
import { realtimeURL } from './realtimeTransport';

class Socket {
  static OPEN = 1;
  static instances: Socket[] = [];
  readyState = 0;
  sent: Record<string, string>[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  onclose: (() => void) | null = null;
  constructor(public url: string, public protocol: string) { Socket.instances.push(this); }
  send(value: string) { this.sent.push(JSON.parse(value)); }
  close() { this.readyState = 3; }
  connected() { this.readyState = 1; this.onopen?.(); }
  frame(value: Record<string, unknown>) { this.onmessage?.({ data: JSON.stringify(value) }); }
}

const streams: SSE[] = [];
function stream(path: string, options: ConstructorParameters<typeof SSE>[1] = {}) {
  const value = new SSE(`http://localhost/api/core${path}`, {
    headers: { Authorization: 'Bearer test' }, timeout: 60_000, ...options,
  });
  streams.push(value);
  return value;
}
function data(bytes: Uint8Array) { return btoa(String.fromCharCode(...bytes)); }

beforeEach(() => { Socket.instances = []; vi.stubGlobal('WebSocket', Socket); });
afterEach(() => { streams.splice(0).forEach(s => s.close()); vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

describe('multiplexed realtime transport', () => {
  it('carries more than six subscriptions without opening HTTP streams', () => {
    const xhr = vi.spyOn(XMLHttpRequest.prototype, 'open');
    const messages = Array.from({ length: 12 }, () => vi.fn());
    messages.forEach((message, i) => stream(`/conversations/c${i}/events`, { callbacks: { message } }));
    expect(Socket.instances).toHaveLength(1);
    const socket = Socket.instances[0];
    socket.connected();
    expect(socket.sent).toHaveLength(12);
    socket.sent.forEach(({ id }) => {
      socket.frame({ type: 'headers', id, status: 200 });
      socket.frame({ type: 'data', id, data: data(new TextEncoder().encode('data: updated\n\n')) });
    });
    messages.forEach(message => expect(message).toHaveBeenCalledWith(expect.objectContaining({ data: 'updated' })));
    expect(xhr).not.toHaveBeenCalled();
  });

  it('preserves UTF-8 split across frames, SSE names, ids, and resume cursors', () => {
    const patch = vi.fn();
    stream('/workflow-sessions/w1/events', { headers: { Authorization: 'Bearer test', 'Last-Event-ID': '7' }, callbacks: { 'workflow.patch': patch } });
    const socket = Socket.instances[0]; socket.connected();
    const { id } = socket.sent[0];
    expect(socket.sent[0].last_event_id).toBe('7');
    socket.frame({ type: 'headers', id, status: 200 });
    const prefix = new TextEncoder().encode('event: workflow.patch\nid: 8\ndata: ');
    const bytes = new TextEncoder().encode('event: workflow.patch\nid: 8\ndata: 中文\n\n');
    socket.frame({ type: 'data', id, data: data(bytes.slice(0, prefix.length + 1)) });
    expect(patch).not.toHaveBeenCalled();
    socket.frame({ type: 'data', id, data: data(bytes.slice(prefix.length + 1)) });
    expect(patch).toHaveBeenCalledWith(expect.objectContaining({ data: '中文', id: '8' }));
  });

  it('cancels one subscription without interrupting another, then releases the socket', () => {
    const first = stream('/conversations/c1/events');
    const second = stream('/tasks/t1:stream');
    const socket = Socket.instances[0]; socket.connected();
    first.close();
    expect(socket.sent[2]).toEqual({ type: 'cancel', id: socket.sent[0].id });
    expect(socket.readyState).toBe(1);
    second.close();
    expect(socket.readyState).toBe(3);
  });

  it('never replays a chat POST or falls back to HTTP after a socket loss', async () => {
    const error = vi.fn();
    const xhr = vi.spyOn(XMLHttpRequest.prototype, 'open');
    stream('/conversations:chat', { method: Method.POST, payload: '{"conversation_id":"c1"}', callbacks: { error } });
    const socket = Socket.instances[0]; socket.connected(); socket.onerror?.();
    await Promise.resolve();
    expect(error).toHaveBeenCalledOnce();
    expect(socket.sent).toHaveLength(1);
    expect(Socket.instances).toHaveLength(1);
    expect(xhr).not.toHaveBeenCalled();
  });

  it('preserves non-200 status and response body', () => {
    const error = vi.fn();
    stream('/tasks/t1:stream', { callbacks: { error } });
    const socket = Socket.instances[0]; socket.connected(); const { id } = socket.sent[0];
    socket.frame({ type: 'headers', id, status: 403 });
    socket.frame({ type: 'data', id, data: data(new TextEncoder().encode('{"code":"FORBIDDEN"}')) });
    socket.frame({ type: 'end', id });
    expect(error).toHaveBeenCalledWith(expect.objectContaining({ status: 403, data: '{"code":"FORBIDDEN"}' }));
  });

  it('uses separate connections for changed credentials and never puts tokens in URLs', () => {
    stream('/tasks/t1:stream');
    stream('/tasks/t2:stream', { headers: { Authorization: 'Bearer second' } });
    expect(Socket.instances).toHaveLength(2);
    Socket.instances.forEach(socket => expect(socket.url).toBe('ws://localhost/api/core/realtime/connect'));
  });

  it('supports TLS and prefixes, and leaves unrelated endpoints on their existing transport', () => {
    expect(realtimeURL('https://example.com/app/api/core/tasks/t1:stream?view=ordinary', 'GET')).toEqual({
      socket: 'wss://example.com/app/api/core/realtime/connect', path: '/tasks/t1:stream?view=ordinary',
    });
    expect(realtimeURL('/api/core/internal/subagent/tasks/t1/events', 'GET')).toBeNull();
    expect(realtimeURL('/api/core/conversations:stopChatGeneration', 'POST')).toBeNull();
  });

  it('keeps idle read subscriptions alive without masking chat timeouts', () => {
    vi.useFakeTimers();
    const readTimeout = vi.fn(); const chatTimeout = vi.fn();
    stream('/conversations/c1/events', { timeout: 1000, callbacks: { timeout: readTimeout } });
    stream('/conversations:chat', { method: Method.POST, timeout: 1000, callbacks: { timeout: chatTimeout } });
    const socket = Socket.instances[0]; socket.connected();
    vi.advanceTimersByTime(900);
    socket.frame({ type: 'heartbeat' });
    vi.advanceTimersByTime(200);
    expect(readTimeout).not.toHaveBeenCalled();
    expect(chatTimeout).toHaveBeenCalledOnce();
  });
});
