import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { axiosInstance, BASE_URL } from '@/components/request';
import { loadWorkflowUI, workflowSnapshotSteps, type WorkflowSession, type WorkflowUI } from '@/modules/chat/store/workflowPanel';
import { workflowTabsForAudience } from '@/modules/chat/components/WorkflowPanel/workflowAudience';
import { workflowNodeStatus, workflowTabStatus } from '@/modules/chat/components/WorkflowPanel/workflowStepStatus';
import { DEVELOPER_ACTIVE_EVENT, isDeveloperModeActive } from '@/utils/developerMode';
import type { Task } from './api';

export type TaskStep = { step_id: string; title?: string; status: string; current?: boolean };
export const isTaskStepDone = (status: string) => ['completed', 'succeeded'].includes(status);

type Projection = {
  display?: import('@/modules/chat/store/workflowStatus').WorkflowDisplayState;
  current_step_id?: string;
  graph?: { static_order?: string[]; nodes?: Record<string, { label?: string }> };
  projection?: WorkflowSession['projection'];
  attempt_history?: NonNullable<WorkflowSession['projection']>['attempt_history'];
};

type StepRuntime = { payload: Projection; session?: WorkflowSession };
type RuntimeLoad = { request: Promise<StepRuntime>; session: Promise<WorkflowSession | undefined>; readers: number };
// Retain a snapshot while its card/row/drawer is mounted, not just while it is
// in flight. The versioned key prevents reuse after execution facts change.
const runtimeLoads = new Map<string, RuntimeLoad>();

function retainStepRuntime(sessionID: string, key: string, previous?: RuntimeLoad): RuntimeLoad {
  const shared = runtimeLoads.get(key);
  if (shared) {
    shared.readers += 1;
    return shared;
  }
  const encodedID = encodeURIComponent(sessionID);
  const session = previous?.session ?? axiosInstance.get(`${BASE_URL}/api/core/workflow-sessions/${encodedID}`, { silentError: true } as never)
    .then(response => (response.data?.data ?? response.data)?.session as WorkflowSession | undefined);
  const request = previous?.request ?? Promise.all([
    axiosInstance.get(`${BASE_URL}/api/core/workflow-sessions/${encodedID}/projection`, { silentError: true } as never),
    session,
  ]).then(([projectionResponse, session]) => ({
    payload: projectionResponse.data?.data ?? projectionResponse.data,
    session,
  }));
  const entry = { request, session, readers: 1 };
  runtimeLoads.set(key, entry);
  // A failed read must not prevent a newly opened drawer from trying again.
  void request.catch(() => { if (runtimeLoads.get(key) === entry) runtimeLoads.delete(key); });
  return entry;
}

export function workflowPlannedSteps(ui: WorkflowUI, session: WorkflowSession, payload: Projection, developer: boolean): TaskStep[] | null {
  // Conditional presentation is not a complete plan until launch planning has
  // decided which optional branches apply. Never show an interim denominator.
  if (ui.tab_visibility_ready_material && !session.slots?.some(slot => slot.selected && slot.slot === ui.tab_visibility_ready_material)) return null;
  return workflowTabsForAudience(ui, session, developer).map(tab => {
    const ids = tab.status_step_ids ?? [tab.step_id ?? tab.id];
    return {
      step_id: tab.id,
      title: tab.label,
      status: workflowTabStatus(tab, session),
      current: ids.includes(session.current_step_id) || ids.some(id => payload.projection?.current?.includes(id)),
    };
  });
}

/** Shared definition-based steps for cards, list rows and the detail drawer. */
export function useTaskSteps(task: Task | null, refreshVersion = 0) {
  const { i18n } = useTranslation();
  const [developer, setDeveloper] = useState(isDeveloperModeActive);
  const [snapshot, setSnapshot] = useState<{ sessionID: string; steps: TaskStep[] | null; failed: boolean }>();
  const runtimeCache = useRef<{ key: string; entry: RuntimeLoad }>();
  const sessionID = task?.workflow_session_id;
  // Polling creates new arrays even when execution facts have not changed.
  const runtimeKey = JSON.stringify([task?.user_id, sessionID, task?.updated_at, task?.status,
    task?.steps.map(step => [step.step_id, step.status]), refreshVersion]);
  const language = i18n?.language ?? '';
  useEffect(() => {
    const update = () => setDeveloper(isDeveloperModeActive());
    window.addEventListener(DEVELOPER_ACTIVE_EVENT, update);
    return () => window.removeEventListener(DEVELOPER_ACTIVE_EVENT, update);
  }, []);

  useEffect(() => {
    if (!sessionID) return;
    let active = true;
    const entry = retainStepRuntime(sessionID, runtimeKey,
      runtimeCache.current?.key === runtimeKey ? runtimeCache.current.entry : undefined);
    runtimeCache.current = { key: runtimeKey, entry };
    // The immutable definition depends only on the session, not its projection.
    const definition = entry.session.then(session => session?.workflow_id
      ? loadWorkflowUI(session.workflow_id, session.pinned_revision_id, { language, silentError: true }) : undefined);
    void Promise.all([entry.request, definition]).then(([{ payload, session }, ui]) => {
      if (!active) return;
      let steps: TaskStep[] | null;
      if (session?.workflow_id && ui) {
        const runtime: WorkflowSession = { ...session,
          current_step_id: payload.current_step_id ?? session.current_step_id,
          steps: workflowSnapshotSteps(session.session_id, payload.attempt_history) ?? session.steps,
          projection: { ...payload.projection, display: payload.display },
        };
        steps = workflowPlannedSteps(ui, runtime, payload, developer);
      } else {
        const order = payload.graph?.static_order ?? Object.keys(payload.graph?.nodes ?? {});
        steps = order.filter(id => id !== '__start__' && id !== '__end__').map(id => {
          const node = payload.projection?.nodes?.[id];
          return { step_id: id, title: payload.graph?.nodes?.[id]?.label,
            current: payload.current_step_id === id || payload.projection?.current?.includes(id),
            status: workflowNodeStatus(node, payload.display, id) };
        });
      }
      if (active) setSnapshot({ sessionID, steps, failed: false });
    }).catch(() => { if (active) setSnapshot(previous => ({ sessionID, steps: previous?.sessionID === sessionID ? previous.steps : null, failed: true })); });
    return () => {
      active = false;
      entry.readers -= 1;
      if (!entry.readers && runtimeLoads.get(runtimeKey) === entry) runtimeLoads.delete(runtimeKey);
    };
  }, [sessionID, runtimeKey, language, developer]);

  if (sessionID) {
    const current = snapshot?.sessionID === sessionID ? snapshot : undefined;
    // A successful read can still lack a plan. Only an executing task can be
    // actively generating it; pauses and terminal states must not keep spinning.
    const planPending = Boolean(current && !current.failed && current.steps === null);
    return { steps: current?.steps ?? [], loading: !current || (planPending && task?.status === 'running'),
      failed: current?.failed ?? false, planPending };
  }
  const steps: TaskStep[] = task?.steps.length ? task.steps : task?.task_type === 'background_chat'
    ? [{ step_id: task.id, title: task.title || task.conversation_title, status: task.status }] : [];
  return { steps, loading: false, failed: false, planPending: false };
}

export function taskStepProgress(steps: TaskStep[]) {
  const done = steps.filter(step => isTaskStepDone(step.status)).length;
  return { done, total: steps.length, percent: steps.length ? Math.round(done / steps.length * 100) : 0 };
}
