import type { TabDef, WorkflowSession, WorkflowSessionStep } from '@/modules/chat/store/workflowPanel';
import { resolvePendingApprovalStep } from './workflowApproval';

export type WorkflowFollowMode = 'following' | 'free';

export function workflowTabMatchesStep(tab: TabDef, stepId: string): boolean {
  return (tab.step_id ?? tab.id) === stepId || Boolean(tab.status_step_ids?.includes(stepId));
}

export function resolveWorkflowActiveTabIndex(tabs: TabDef[], activeTabId?: string): number {
  return Math.max(0, tabs.findIndex(tab => tab.id === activeTabId));
}

/** Runtime position is independent of the tab the user chose to read. */
export function resolveWorkflowRealTabIndex(
  session: Pick<WorkflowSession, 'status' | 'current_step_id' | 'steps' | 'projection' | 'slots'>,
  tabs: TabDef[],
  preferredTabId?: string,
): number {
  const projection = session.projection;
  const effective = (id: string) => projection?.nodes?.[id]?.validity !== 'stale'
    && !projection?.stale?.includes(id) && !projection?.pruned?.includes(id)
    && !projection?.bypassed?.includes(id);
  const find = (id?: string) => id && effective(id) ? tabs.findIndex(tab => workflowTabMatchesStep(tab, id)) : -1;
  const latest = (predicate: (step: WorkflowSessionStep) => boolean) => [...(session.steps ?? [])]
    .filter(step => step.validity !== 'stale' && effective(step.step_id) && predicate(step))
    .sort((a, b) => (Date.parse(b.updated_at) || Date.parse(b.created_at) || 0)
      - (Date.parse(a.updated_at) || Date.parse(a.created_at) || 0) || b.attempt - a.attempt
      || Number(b.step_id === session.current_step_id) - Number(a.step_id === session.current_step_id))
    .map(step => find(step.step_id)).find(index => index !== -1) ?? -1;

  const failed = (projection?.current ?? []).filter(id => projection?.nodes?.[id]?.execution === 'failed')
    .map(find).find(index => index !== -1);
  if (failed !== undefined) return failed;
  if (session.status === 'failed') {
    const index = latest(step => step.status === 'failed');
    if (index !== -1) return index;
  }
  const terminal = session.status === 'completed' || session.status === 'stopped' || projection?.completed;
  const current = find(session.current_step_id);
  // A resumed producer patch may arrive before the session leaves completed.
  // Do not hide its process page behind the previous successful output.
  if (terminal && current !== -1 && (session.steps ?? []).some(step => step.step_id === session.current_step_id
    && step.validity !== 'stale' && ['pending', 'queued', 'claimed', 'running', 'waiting'].includes(step.status))) return current;
  if (terminal) {
    const index = latest(step => session.status === 'stopped' || step.status === 'succeeded');
    if (index !== -1) return index;
  } else {
    const running = (projection?.current ?? []).filter(id =>
      ['pending', 'queued', 'claimed', 'running', 'waiting'].includes(projection?.nodes?.[id]?.execution ?? ''))
      .map(find).filter(index => index !== -1);
    // Keep the same running branch on repeated/parallel updates.
    const preferred = running.find(index => tabs[index].id === preferredTabId);
    if (preferred !== undefined) return preferred;
    if (running.length) return running[0];
    const approval = find(resolvePendingApprovalStep(session, session.status));
    if (approval !== -1) return approval;
  }
  if (current !== -1 && projection?.nodes?.[session.current_step_id]?.execution !== 'none') return current;
  const last = latest(() => true);
  if (last !== -1) return last;
  // A just-published artifact may arrive before its step patch.
  return [...(session.slots ?? [])].filter(slot => slot.selected && Boolean(slot.step_id) && effective(slot.step_id!))
    .sort((a, b) => (Date.parse(b.created_at ?? '') || 0) - (Date.parse(a.created_at ?? '') || 0) || b.revision - a.revision)
    .map(slot => find(slot.step_id)).find(index => index !== -1) ?? -1;
}
