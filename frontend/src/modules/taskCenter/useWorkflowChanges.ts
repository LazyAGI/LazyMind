import { useEffect } from 'react';
import { AgentAppsAuth } from '@/components/auth';
import { BASE_URL } from '@/components/request';
import { SSE } from '@/modules/chat/utils/sse';

import { WORKFLOW_TASKS_CHANGED } from './events';

// One owner-scoped connection for the task center, including paused/completed
// workflows that may be resumed from a different browser tab.
export function useWorkflowChanges() {
  useEffect(() => {
    let closed = false;
    let stream: SSE;
    let timer: ReturnType<typeof setTimeout> | undefined;
    function reconnect() {
      if (closed || timer) return;
      timer = setTimeout(() => { timer = undefined; connect(); }, 1000);
      stream.close();
    }
    function connect() {
      if (closed) return;
      stream = new SSE(`${BASE_URL}/api/core/task-center/workflow-events`, {
        headers: { Accept: 'text/event-stream', ...AgentAppsAuth.getAuthHeaders() },
        timeout: 60_000,
      });
      stream.addEventListener('changed', () => window.dispatchEvent(new Event(WORKFLOW_TASKS_CHANGED)));
      stream.addEventListener('error', reconnect);
      stream.addEventListener('readystatechange', (event) => {
        if ((event as Event & { readyState: number }).readyState === 2) reconnect();
      });
    }
    connect();
    return () => { closed = true; clearTimeout(timer); stream?.close(); };
  }, []);
}
