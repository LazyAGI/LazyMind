import type {
  SlotRevision,
  TabDef,
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

/** Match a revision against a tab's declared producer scope. */
export function workflowSlotMatchesTabScope(
  tab: TabDef,
  steps: WorkflowSessionStep[] = [],
  slot: SlotRevision,
): boolean {
  const stepId = resolveWorkflowTabStepId(tab, steps);
  return stepId ? slot.step_id === stepId : Boolean(slot.selected);
}
