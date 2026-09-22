import { useEffect, useState } from 'react';
import { AgentAppsAuth } from '@/components/auth';
import type { TaskArtifact } from '@/modules/chat/store/taskCenter';
import type { WorkflowSession } from '@/modules/chat/store/workflowPanel';
import { taskStreamUrl } from '@/modules/chat/utils/request';
import { Method, SSE } from '@/modules/chat/utils/sse';

export interface ExecutionActivity {
  kind?: 'thinking' | 'tool' | 'working' | 'reconnecting';
  tool?: string;
  progress?: number;
  finished?: boolean;
  artifacts?: TaskArtifact[];
}

/** Keep display metadata and published artifacts, never tool arguments or thought text. */
export function reduceActivity(previous: ExecutionActivity, event: Record<string, unknown>): ExecutionActivity {
  if (previous.finished) return previous;
  switch (event.type) {
    case 'think': return { ...previous, kind: 'thinking', tool: undefined };
    case 'text':
    case 'tool_results': return { ...previous, kind: 'working', tool: undefined };
    case 'tool_calls': {
      const calls = event.tool_calls as Array<{ name?: string; function?: { name?: string } }> | undefined;
      const tool = calls?.[0]?.name ?? calls?.[0]?.function?.name;
      return tool ? { ...previous, kind: 'tool', tool } : previous;
    }
    case 'progress': {
      const progress = event.progress;
      return typeof progress === 'number' && Number.isFinite(progress)
        ? { ...previous, progress: Math.max(previous.progress ?? 0, Math.min(100, Math.max(0, progress))) }
        : previous;
    }
    case 'artifact': {
      if (typeof event.slot !== 'string' || typeof event.content_type !== 'string'
        || !Number.isInteger(event.seq) || Number(event.seq) < 1 || event.value == null) return previous;
      const artifact: TaskArtifact = { slot: event.slot, content_type: event.content_type, seq: Number(event.seq), value: event.value };
      const artifacts = (previous.artifacts ?? []).filter(item => item.slot !== artifact.slot || item.seq !== artifact.seq);
      return { ...previous, artifacts: [...artifacts, artifact] };
    }
    case 'done': return { finished: true, ...(previous.artifacts ? { artifacts: previous.artifacts } : {}) };
    case 'error': return { finished: true };
    default: return previous;
  }
}

export function activeExecutionTasks(session?: WorkflowSession | null) {
  if (!session || ['completed', 'failed', 'stopped'].includes(session.status)) return [];
  const latest = new Map<string, NonNullable<WorkflowSession['steps']>[number]>();
  for (const step of session?.steps ?? []) {
    if (step.validity === 'stale') continue;
    if (!latest.has(step.step_id) || latest.get(step.step_id)!.attempt < step.attempt) latest.set(step.step_id, step);
  }
  return [...latest.values()].filter(step => step.task_id
    && ['pending', 'queued', 'claimed', 'running'].includes(step.status)
    && ['pending', 'queued', 'claimed', 'running'].includes(session?.projection?.nodes?.[step.step_id]?.execution ?? step.status));
}

export function useExecutionActivity(session?: WorkflowSession | null): Record<string, ExecutionActivity> {
  const taskKey = JSON.stringify(activeExecutionTasks(session).map(step => step.task_id).sort());
  const [snapshot, setSnapshot] = useState<{ key: string; values: Record<string, ExecutionActivity> }>({ key: '', values: {} });
  useEffect(() => {
    let closed = false;
    const disposers = (JSON.parse(taskKey) as string[]).map(taskId => {
      let stream: SSE | undefined;
      let retry: ReturnType<typeof setTimeout> | undefined;
      let activity: ExecutionActivity = {};
      let generation = 0;
      const publish = () => {
        if (!closed) setSnapshot(previous => ({ key: taskKey, values: {
          ...(previous.key === taskKey ? previous.values : {}), [taskId]: activity,
        } }));
      };
      const connect = () => {
        if (closed) return;
        const current = ++generation;
        activity = { ...activity, kind: activity.kind === 'reconnecting' ? 'working' : activity.kind };
        publish();
        stream = new SSE(taskStreamUrl(taskId), {
          method: Method.GET,
          headers: { Accept: 'text/event-stream', ...AgentAppsAuth.getAuthHeaders() },
          timeout: 3600000,
          callbacks: {
            message: (raw: CustomEvent) => {
              if (closed || current !== generation) return;
              const data = (raw as CustomEvent & { data?: string }).data;
              if (!data || data === '[DONE]') return;
              let event: Record<string, unknown>;
              try { event = JSON.parse(data); } catch { return; }
              if (!event || typeof event !== 'object') return;
              const next = reduceActivity(activity, event);
              if (JSON.stringify(next) !== JSON.stringify(activity)) { activity = next; publish(); }
              if (activity.finished) stream?.close();
            },
            error: () => {
              if (closed || current !== generation || activity.finished || retry) return;
              generation += 1;
              stream?.close();
              activity = { ...activity, kind: 'reconnecting', tool: undefined };
              publish();
              retry = setTimeout(() => { retry = undefined; connect(); }, 1500);
            },
          },
        });
      };
      connect();
      return () => { if (retry) clearTimeout(retry); stream?.close(); };
    });
    return () => { closed = true; disposers.forEach(dispose => dispose()); };
  }, [taskKey]);
  return snapshot.key === taskKey ? snapshot.values : {};
}
