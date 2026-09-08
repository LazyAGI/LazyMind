import { reconcileWorkflowSessionStatus } from '@/modules/chat/store/workflowStatus';
import type { WorkflowSession, WorkflowSessionStep } from '@/modules/chat/store/workflowPanel';
import { subscribeWorkflowEventStream } from '@/modules/chat/utils/workflowEventStream';

interface RunAPI {
  getSession(id: string): Promise<{ data: { data: { session?: WorkflowSession } } }>;
  getProjection(id: string): Promise<{ data: { data: { projection?: WorkflowSession['projection'] } } }>;
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
