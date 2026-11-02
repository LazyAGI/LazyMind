import type { SlotRevision, TabDef } from '../../store/workflowPanel';

/** Only explicitly linked composites participate; equal page counts imply no relation. */
export function linkedPageTabs(tab: TabDef, tabs: TabDef[]): TabDef[] {
  return tab.page_group
    ? tabs.filter(candidate => candidate.layout === 'composite' && candidate.page_group === tab.page_group)
    : [tab];
}

export function buildPageOrderChange(slotId: string, revisions: SlotRevision[], pages: number[]) {
  const selected = revisions.filter(item => item.selected && item.list_index !== undefined);
  if (!selected.length) return null;
  const byPosition = new Map(selected.map(item => [item.sort_order, item.list_index!]));
  if (selected.length !== pages.length || byPosition.size !== pages.length
    || pages.some(page => !byPosition.has(page))) {
    throw new Error('关联页面尚未对齐，请等待生成完成后再排序。');
  }
  return { slot_id: slotId, order: pages.map(page => byPosition.get(page)!), version: selected[0].order_version ?? 0 };
}
