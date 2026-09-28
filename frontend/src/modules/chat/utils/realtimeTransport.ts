// One socket per API origin and credential in this renderer. Business SSE
// reducers stay unchanged; only the byte transport is multiplexed.
export interface RealtimeFrame {
  type: 'headers' | 'data' | 'end' | 'error' | 'heartbeat';
  id: string;
  status?: number;
  data?: string;
}

interface StreamRequest {
  method: string;
  url: string;
  headers: Record<string, string>;
  payload: string;
}

const subscriptions = /^\/(conversations\/[A-Za-z0-9_-]+\/events|tasks\/[A-Za-z0-9_-]+:stream|workflow-sessions\/[A-Za-z0-9_-]+\/events)$/;
const connections = new Map<string, RealtimeConnection>();
let nextID = 0;

export function realtimeURL(url: string, method: string): { socket: string; path: string } | null {
  const parsed = new URL(url, window.location.href);
  const marker = '/api/core';
  const index = parsed.pathname.indexOf(`${marker}/`);
  if (index < 0 || !['http:', 'https:'].includes(parsed.protocol)) return null;
  const path = parsed.pathname.slice(index + marker.length);
  if (!(method === 'GET' && subscriptions.test(path)) &&
      !(method === 'POST' && ['/conversations:chat', '/conversations:resumeChat'].includes(path))) return null;
  const requestPath = path + parsed.search;
  parsed.pathname = parsed.pathname.slice(0, index) + '/api/core/realtime/connect';
  parsed.search = '';
  parsed.hash = '';
  parsed.protocol = parsed.protocol === 'https:' ? 'wss:' : 'ws:';
  return { socket: parsed.toString(), path: requestPath };
}

export function openRealtimeStream(request: StreamRequest, receive: (frame: RealtimeFrame) => void): (() => void) | null {
  const target = realtimeURL(request.url, request.method);
  if (!target) return null;
  const headers = new Headers(request.headers);
  const authorization = headers.get('Authorization') || '';
  const key = JSON.stringify([target.socket, authorization]);
  let connection = connections.get(key);
  if (!connection) {
    try {
      connection = new RealtimeConnection(target.socket, () => {
        if (connections.get(key) === connection) connections.delete(key);
      });
    } catch {
      let cancelled = false;
      queueMicrotask(() => { if (!cancelled) receive({ type: 'error', id: '', status: 0 }); });
      return () => { cancelled = true; };
    }
    connections.set(key, connection);
  }
  return connection.open({
    type: 'open', id: String(++nextID), method: request.method, path: target.path,
    authorization, payload: request.payload,
    language: headers.get('Accept-Language') || '',
    last_event_id: headers.get('Last-Event-ID') || '',
  }, receive);
}

class RealtimeConnection {
  private socket: WebSocket;
  private streams = new Map<string, { message: { id: string; [key: string]: unknown }; receive: (frame: RealtimeFrame) => void }>();
  private timer: ReturnType<typeof setTimeout>;
  private closed = false;

  constructor(url: string, private dispose: () => void) {
    this.socket = new WebSocket(url, 'lazymind.realtime.v1');
    this.timer = setTimeout(() => this.fail(), 10_000);
    this.socket.onopen = () => {
      clearTimeout(this.timer);
      for (const stream of this.streams.values()) this.send(stream.message);
    };
    this.socket.onmessage = (event) => {
      let frame: RealtimeFrame;
      try { frame = JSON.parse(event.data as string) as RealtimeFrame; }
      catch { this.fail(); return; }
      if (frame.type === 'heartbeat') {
        for (const [id, stream] of this.streams) {
          if (stream.message.method === 'GET') stream.receive({ type: 'heartbeat', id });
        }
        return;
      }
      const stream = this.streams.get(frame.id);
      if (!stream) return;
      if (frame.type === 'end' || frame.type === 'error') this.streams.delete(frame.id);
      stream.receive(frame);
      if (this.streams.size === 0) this.close();
    };
    this.socket.onerror = () => this.fail();
    this.socket.onclose = () => this.fail();
  }

  open(message: { id: string; [key: string]: unknown }, receive: (frame: RealtimeFrame) => void): () => void {
    this.streams.set(message.id, { message, receive });
    if (this.socket.readyState === WebSocket.OPEN) this.send(message);
    return () => {
      if (!this.streams.delete(message.id)) return;
      if (this.socket.readyState === WebSocket.OPEN) this.send({ type: 'cancel', id: message.id });
      if (this.streams.size === 0) this.close();
    };
  }

  private send(message: unknown) {
    try { this.socket.send(JSON.stringify(message)); }
    catch { this.fail(); }
  }

  private fail() {
    if (this.closed) return;
    const pending = [...this.streams.entries()];
    this.streams.clear();
    this.close();
    // Never replay POST requests or silently fall back to HTTP after a loss.
    // Existing subscription owners decide whether and how to resume reads.
    queueMicrotask(() => pending.forEach(([id, stream]) => stream.receive({ type: 'error', id, status: 0 })));
  }

  private close() {
    if (this.closed) return;
    this.closed = true;
    clearTimeout(this.timer);
    this.dispose();
    this.socket.onopen = this.socket.onmessage = this.socket.onerror = this.socket.onclose = null;
    this.socket.close();
  }
}
