import { AgentAppsAuth } from '@/components/auth';
import { BASE_URL } from '@/components/request';
import { SSE, type CustomEventType } from '@/modules/chat/utils/sse';
import type { WorkflowStreamEvent } from '@/modules/chat/store/workflowProjection';

const EVENT_TYPES = [
  'workflow.snapshot', 'snapshot', 'workflow.patch', 'step.patch', 'attempt.patch',
  'artifact.upsert', 'artifact.stale', 'workflow.waiting', 'workflow.completed', 'attempt.progress',
] as const;

export interface WorkflowEventStreamSubscription {
  close(): void;
  resync(): void;
}

function parseEvent(type: string, raw: unknown, id: string): WorkflowStreamEvent | null {
  try {
    const parsed = typeof raw === 'string' ? JSON.parse(raw) : raw;
    if (!parsed || typeof parsed !== 'object') return null;
    const record = parsed as Record<string, unknown>;
    // The current Go first packet is named `snapshot` and wraps the projection HTTP response.
    if (type === 'snapshot') {
      const body = record.data && typeof record.data === 'object'
        ? record.data as Record<string, unknown>
        : record;
      const projection = body.data && typeof body.data === 'object'
        ? body.data as Record<string, unknown>
        : body;
      return {
        contract_version: 'workflow.v1', cursor: Number(id || 0), type: 'workflow.snapshot',
        state_version: Number(projection.state_version ?? 0), payload: projection,
      };
    }
    return {
      ...(record as unknown as WorkflowStreamEvent),
      type,
      cursor: Number(record.cursor ?? id ?? 0),
      state_version: Number(record.state_version ?? 0),
      payload: (record.payload && typeof record.payload === 'object' ? record.payload : {}) as Record<string, unknown>,
    };
  } catch {
    return null;
  }
}

export function subscribeWorkflowEventStream(
  sessionId: string,
  lastCursor: number,
  onEvent: (event: WorkflowStreamEvent) => void,
  onResync: () => void,
): WorkflowEventStreamSubscription {
  let closed = false;
  let cursor = lastCursor;
  let stream: SSE | null = null;
  let retryTimer: ReturnType<typeof setTimeout> | null = null;

  const scheduleReconnect = (resetCursor = false) => {
    if (resetCursor) cursor = 0;
    stream?.close();
    stream = null;
    if (!closed && !retryTimer) {
      retryTimer = setTimeout(() => {
        retryTimer = null;
        connect();
      }, 1000);
    }
  };

  function connect() {
    if (closed) return;
    const headers: Record<string, string> = { Accept: 'text/event-stream', ...AgentAppsAuth.getAuthHeaders() };
    if (cursor > 0) headers['Last-Event-ID'] = String(cursor);
    stream = new SSE(`${BASE_URL}/api/core/workflow-sessions/${encodeURIComponent(sessionId)}/events`, {
      headers,
      timeout: 60_000,
    });
    for (const type of EVENT_TYPES) {
      stream.addEventListener(type, (raw: CustomEventType) => {
        const custom = raw as CustomEvent & { data?: unknown; id?: string };
        const event = parseEvent(type, custom.data, custom.id ?? '');
        if (event) {
          cursor = Math.max(cursor, event.cursor);
          onEvent(event);
        }
      });
    }
    stream.addEventListener('error', (raw: CustomEventType) => {
      const custom = raw as CustomEvent & { data?: unknown };
      let data = custom.data as { code?: string } | undefined;
      if (typeof custom.data === 'string') {
        try { data = JSON.parse(custom.data) as { code?: string }; } catch { data = undefined; }
      }
      if (data?.code === 'CURSOR_EXPIRED' || data?.code === 'EVENT_CURSOR_EXPIRED') {
        onResync();
        scheduleReconnect(true);
        return;
      }
      scheduleReconnect();
    });
  }
  connect();
  return {
    close: () => { closed = true; if (retryTimer) clearTimeout(retryTimer); stream?.close(); },
    resync: () => scheduleReconnect(true),
  };
}
