import type { SlotRevision, TabDef, WorkflowSession } from '@/modules/chat/store/workflowPanel';
import { activeExecutionTasks, type ExecutionActivity } from './useExecutionActivity';

/** A render-only overlay; never write provisional artifacts into the workflow store. */
export function executionPreview(session: WorkflowSession, tab: TabDef, activities: Record<string, ExecutionActivity>): WorkflowSession {
  const stepIds = tab.status_step_ids ?? [tab.step_id ?? tab.id];
  let slots = session.slots ?? [];
  for (const task of activeExecutionTasks(session).filter(step => stepIds.includes(step.step_id))) {
    const artifacts = activities[task.task_id]?.artifacts ?? [];
    for (const artifact of [...artifacts].sort((a, b) => a.seq - b.seq)) {
      const def = tab.slots.find(slot => slot.id === artifact.slot);
      if (!def) continue;
      const siblings = slots.filter(slot => slot.slot === artifact.slot && slot.selected);
      const explicitIndex = artifact.value?.list_index;
      const listIndex = def.cardinality === 'list'
        ? (Number.isInteger(explicitIndex) && explicitIndex >= 0 ? explicitIndex : Math.max(-1, ...siblings.map(slot => slot.list_index ?? -1)) + 1)
        : undefined;
      const existing = siblings.find(slot => slot.list_index === listIndex);
      const preview: SlotRevision = {
        slot_id: artifact.slot, slot: artifact.slot, step_id: task.step_id,
        revision: artifact.seq, selected: true, created_at: '',
        list_index: listIndex,
        sort_order: listIndex === undefined ? undefined : existing?.sort_order ?? Math.max(0, ...siblings.map(slot => slot.sort_order ?? 0)) + 1,
        content_type: artifact.content_type, artifact_value: typeof artifact.value === 'string' ? { text: artifact.value } : artifact.value,
        caption: artifact.value?.caption,
      };
      slots = [...slots.filter(slot => slot.slot !== artifact.slot || slot.list_index !== listIndex), preview];
    }
  }
  return slots === session.slots || !slots.length ? session : { ...session, slots };
}


/** Provisional Writer content has no durable revision to fetch from Core yet. */
export function executionPreviewTab(tab: TabDef, original: WorkflowSession, preview: WorkflowSession): TabDef {
  if (preview === original) return tab;
  return { ...tab, actions: [], slots: tab.slots.map(def => {
    const provisional = preview.slots?.some(slot => slot.slot === def.id && !original.slots?.includes(slot));
    return provisional && def.widget?.widgetType === 'writer-document'
      ? { ...def, widget: { ...def.widget, widgetType: 'text-markdown' } }
      : def;
  }) };
}
