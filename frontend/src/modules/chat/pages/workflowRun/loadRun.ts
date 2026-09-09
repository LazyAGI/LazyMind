import { reconcileWorkflowSessionStatus } from '@/modules/chat/store/workflowStatus';
import type { WorkflowSession, WorkflowSessionStep } from '@/modules/chat/store/workflowPanel';
import { subscribeWorkflowEventStream } from '@/modules/chat/utils/workflowEventStream';
import type { WorkflowControlView } from '@/modules/chat/utils/workflowControl';

interface RunAPI {
  getControl?(id: string, options?: { signal?: AbortSignal }): Promise<{ data: { data: { session?: WorkflowSession; control?: WorkflowControlView; projection?: WorkflowSession['projection'] } } }>;
  getSession(id: string): Promise<{ data: { data: { session?: WorkflowSession } } }>;
  getProjection(id: string): Promise<{ data: { data: { projection?: WorkflowSession['projection'] } } }>;
}

export interface WorkflowRunSnapshot { session: WorkflowSession; control?: WorkflowControlView }

export async function loadWorkflowRunSnapshot(id: string, api: RunAPI, signal?: AbortSignal): Promise<WorkflowRunSnapshot> {
  if (api.getControl) {
    try {
      const response = await api.getControl(id, { signal });
      const { session, control, projection } = response.data.data;
      if (!session || session.session_id !== id || control?.protocol !== 'workflow.control.v1' || control.session_id !== id) throw new Error('Invalid workflow snapshot');
      const pending = control.reviews.find(review => review.status === 'pending');
      return { control, session: { ...session, projection,
        current_step_id: pending?.step_id ?? panelCurrentStep(session.current_step_id, projection, session.steps),
        status: control.continuation === 'completed' ? 'completed' : control.continuation === 'stopped' ? 'stopped' : control.continuation === 'failed' ? 'failed' : control.active_executions > 0 ? 'active' : 'waiting',
        steps: (session.steps ?? []).filter(step => step.step_id !== '__end__'),
      } };
    } catch (error) {
      const response = (error as { response?: { status?: number; data?: { error?: { code?: string } } } })?.response;
      if (response?.status !== 404 && response?.data?.error?.code !== 'CONTROL_PROTOCOL_REQUIRED') throw error;
    }
  }
  return { session: await loadWorkflowRun(id, api) };
}

/**
 * Panel tabs follow current_step_id. Hosted MCP runs do not update that
 * session column, so fill it from the same projection + attempts the query
 * already returned: running node while executing, else the latest attempt
 * (the step just finished, which is where review/pause should stay).
 */
function panelCurrentStep(
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
export async function loadWorkflowRun(id: string, api: RunAPI): Promise<WorkflowSession> {
  const detail = await api.getSession(id);
  const session = detail.data.data.session;
  if (!session || session.session_id !== id) throw new Error('Workflow run not found');
  const state = await api.getProjection(id);
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

/** The shared header describes execution facts, not a step's future human-review policy. */
export function controlStatusKey(control: WorkflowControlView): string {
  if (control.continuation === 'awaiting_user' || control.continuation === 'draining') return 'chat.workflowControlReviewStatus';
  if (control.continuation === 'binding_required') return 'chat.workflowControlBindingStatus';
  if (control.continuation === 'stopped') return 'chat.workflowStatusStopped';
  if (control.continuation === 'completed') return 'chat.workflowStatusDone';
  if (control.continuation === 'failed') return 'chat.workflowStatusFailed';
  return control.active_executions > 0 ? 'chat.workflowStatusRunning' : 'chat.workflowStatusReady';
}
