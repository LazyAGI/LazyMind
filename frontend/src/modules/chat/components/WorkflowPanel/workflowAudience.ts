import type { CompositePanelNode, CompositeLayoutNode, WorkflowUI } from '../../store/workflowPanel';

export function hiddenUserSlots(ui: WorkflowUI): Set<string> {
  return new Set([
    ...(ui.slotDefinitions ?? []),
    ...(ui.tabs ?? []).flatMap(tab => tab.slots),
  ].filter(slot => slot.user_visible === false).map(slot => slot.id));
}

/** Prune both current layout trees and legacy nested columns/tabs. */
function pruneLayout(value: unknown, hidden: Set<string>): unknown {
  if (typeof value === 'string') return hidden.has(value) ? undefined : value;
  if (Array.isArray(value)) {
    const children = value.map(child => pruneLayout(child, hidden)).filter(child => child !== undefined);
    return children.length ? children : undefined;
  }
  if (!value || typeof value !== 'object') return value;
  const result = { ...value } as Record<string, unknown>;
  for (const key of ['slot', 'tabs', 'children']) {
    if (!(key in result)) continue;
    const child = pruneLayout(result[key], hidden);
    if (child === undefined) return undefined;
    result[key] = child;
  }
  return result;
}

export function workflowUIForAudience(ui: WorkflowUI, developer: boolean): WorkflowUI {
  const hidden = hiddenUserSlots(ui);
  if (developer || !hidden.size) return ui;
  return {
    ...ui,
    tabs: ui.tabs?.map(tab => ({
      ...tab,
      slots: tab.slots.filter(slot => !hidden.has(slot.id)),
      composite_layout: pruneLayout(tab.composite_layout, hidden) as CompositePanelNode | CompositeLayoutNode[] | undefined,
      generation_process: tab.generation_process && !hidden.has(tab.generation_process.result_slot)
        ? { ...tab.generation_process, slots: tab.generation_process.slots.filter(slot => !hidden.has(slot)) }
        : undefined,
    })).filter(tab => tab.slots.length > 0),
  };
}
