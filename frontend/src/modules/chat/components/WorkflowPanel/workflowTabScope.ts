import type {
  SlotRevision,
  TabDef,
  WorkflowSession,
  WorkflowSessionStep,
} from '@/modules/chat/store/workflowPanel';

/** Resolve the producer step used by a tab, unless it explicitly spans selected artifacts. */
export function resolveWorkflowTabStepId(
  tab: TabDef,
  steps: WorkflowSessionStep[] = [],
): string | undefined {
  if (tab.slot_scope === 'selected') return undefined;
  if (tab.step_id) return tab.step_id;
  return steps.some((step) => step.step_id === tab.id) ? tab.id : undefined;
}

/** Recovery may target the current step; approval must stay within the visible tab. */
export function resolveWorkflowControlScope(
  tab: TabDef | undefined,
  session: Pick<WorkflowSession, 'steps' | 'slots' | 'current_step_id' | 'projection'>,
): { stepId: string; stepIds: string[] } {
  const tabStepId = tab ? resolveWorkflowTabStepId(tab, session.steps) : undefined;
  const current = session.projection?.current ?? [];
  const stepId = tabStepId || session.current_step_id || (current.length === 1 ? current[0] : '');
  const visibleProducers = (session.slots ?? []).filter(slot => tab
    && tab.slots.some(item => item.id === slot.slot)
    && workflowSlotMatchesTabScope(tab, session.steps, slot)).map(slot => slot.step_id);
  return { stepId, stepIds: tab?.status_step_ids ?? [...new Set([tabStepId, ...visibleProducers]
    .filter((id): id is string => Boolean(id)))] };
}

/** Match a revision against a tab's declared producer scope. */
export function workflowSlotMatchesTabScope(
  tab: TabDef,
  steps: WorkflowSessionStep[] = [],
  slot: SlotRevision,
): boolean {
  const stepId = resolveWorkflowTabStepId(tab, steps);
  return stepId ? slot.step_id === stepId : Boolean(slot.selected);
}
