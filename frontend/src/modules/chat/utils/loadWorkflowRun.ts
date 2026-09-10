import { reconcileWorkflowSessionStatus } from '@/modules/chat/store/workflowStatus';
import type { WorkflowSession, WorkflowSessionStep } from '@/modules/chat/store/workflowPanel';
import { subscribeWorkflowEventStream } from '@/modules/chat/utils/workflowEventStream';
import type { WorkflowControlView } from '@/modules/chat/utils/workflowControl';

export interface WorkflowRunRequestOptions {
  signal?: AbortSignal;
  silentError?: boolean;
}

export interface WorkflowRunAPI {
  getControl?(
    id: string,
    options?: WorkflowRunRequestOptions,
  ): Promise<{ data: { data: { session?: WorkflowSession; control?: WorkflowControlView; projection?: WorkflowSession['projection'] } } }>;
  getSession(
    id: string,
    options?: WorkflowRunRequestOptions,
  ): Promise<{ data: { data: { session?: WorkflowSession } } }>;
  getProjection(
    id: string,
    options?: WorkflowRunRequestOptions,
  ): Promise<{ data: { data: { projection?: WorkflowSession['projection'] } } }>;
}

export interface WorkflowRunSnapshot { session: WorkflowSession; control?: WorkflowControlView }

const sessionsWithoutControl = new Set<string>();

export function resetWorkflowRunControlCache(): void {
  sessionsWithoutControl.clear();
}

export async function loadWorkflowRunSnapshot(
  id: string,
  api: WorkflowRunAPI,
  options?: WorkflowRunRequestOptions,
): Promise<WorkflowRunSnapshot> {
  if (api.getControl && !sessionsWithoutControl.has(id)) {
    try {
      const response = await api.getControl(id, { ...options, silentError: true });
      const { session, control, projection } = response.data.data;
      if (!session || session.session_id !== id || control?.protocol !== 'workflow.control.v1' || control.session_id !== id) {
        throw new Error('Invalid workflow snapshot');
      }
      const pending = control.reviews.find((review) => review.status === 'pending');
      return {
        control,
        session: {
          ...session,
          projection,
          current_step_id: pending?.step_id ?? panelCurrentStep(session.current_step_id, projection, session.steps),
          status: control.continuation === 'completed'
            ? 'completed'
            : control.continuation === 'stopped'
              ? 'stopped'
              : control.continuation === 'failed'
                ? 'failed'
                : control.active_executions > 0 ? 'active' : 'waiting',
          steps: (session.steps ?? []).filter((step) => step.step_id !== '__end__'),
        },
      };
    } catch (error) {
      const response = (error as { response?: { status?: number; data?: { error?: { code?: string } } } })?.response;
      if (response?.data?.error?.code === 'CONTROL_PROTOCOL_REQUIRED') {
        sessionsWithoutControl.add(id);
      } else if (response?.status !== 404) {
        throw error;
      }
    }
  }
  return { session: await loadWorkflowRun(id, api, options) };
}

/**
 * Panel tabs follow current_step_id. Hosted MCP runs do not update that
 * session column, so fill it from the same projection + attempts the query
 * already returned: running node while executing, else the latest attempt
 * (the step just finished, which is where review/pause should stay).
 */
export function panelCurrentStep(
  recorded: string | undefined,
  projection: WorkflowSession['projection'] | undefined,
  steps: WorkflowSessionStep[] | undefined,
): string {
  const running = projection?.current?.find((id) => Boolean(id) && id !== '__end__');
  if (running) return running;
  for (let index = (steps?.length ?? 0) - 1; index >= 0; index -= 1) {
    const step = steps![index];
    if (step.validity !== 'stale' && step.step_id && step.step_id !== '__end__') {
      return step.step_id;
    }
  }
  return recorded ?? '';
}

/** Load one explicitly addressed run; a missing or mismatched run is an error. */
export async function loadWorkflowRun(
  id: string,
  api: WorkflowRunAPI,
  options?: WorkflowRunRequestOptions,
): Promise<WorkflowSession> {
  const detail = await api.getSession(id, options);
  const session = detail.data.data.session;
  if (!session || session.session_id !== id) throw new Error('Workflow run not found');
  const state = await api.getProjection(id, options);
  const projection = state.data.data.projection;
  const steps = (session.steps ?? []).filter((step) => step.step_id !== '__end__');
  return {
    ...session,
    current_step_id: panelCurrentStep(session.current_step_id, projection, steps),
    status: reconcileWorkflowSessionStatus(session.status, projection),
    projection,
    steps,
  };
}

/** SSE is only a change bell; the page reloads the same query the panel already understands. */
export function watchWorkflowRun(sessionId: string, onChange: () => void): () => void {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const ring = () => {
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => {
      timer = undefined;
      onChange();
    }, 100);
  };
  const subscription = subscribeWorkflowEventStream(sessionId, 0, ring, ring);
  return () => {
    if (timer) clearTimeout(timer);
    subscription.close();
  };
}
