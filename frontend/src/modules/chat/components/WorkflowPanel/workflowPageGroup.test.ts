import { describe, expect, it } from 'vitest';
import { buildPageOrderChange, linkedPageTabs } from './workflowPageGroup';
import type { SlotRevision, TabDef } from '../../store/workflowPanel';

describe('linked composite pages', () => {
  const tab = (id: string, group?: string) => ({ id, label: id, layout: 'composite', page_group: group, slots: [] }) as TabDef;
  it('links only explicitly declared groups', () => {
    const tabs = [tab('prompts', 'deck'), tab('slides', 'deck'), tab('unrelated', 'other'), tab('single')];
    expect(linkedPageTabs(tabs[0], tabs).map(t => t.id)).toEqual(['prompts', 'slides']);
    expect(linkedPageTabs(tabs[3], tabs)).toEqual([tabs[3]]);
  });
  it('maps visual positions to stable identities after earlier moves and deletions', () => {
    const revisions = [
      { selected: true, list_index: 8, sort_order: 1, order_version: 3 },
      { selected: true, list_index: 2, sort_order: 2, order_version: 3 },
      { selected: false, list_index: 1, sort_order: 3 },
    ] as SlotRevision[];
    expect(buildPageOrderChange('notes', revisions, [2, 1])).toEqual({slot_id: 'notes', order: [2, 8], version: 3});
    expect(() => buildPageOrderChange('notes', revisions, [3, 1])).toThrow();
    expect(() => buildPageOrderChange('notes', revisions, [1, 2, 3])).toThrow();
    expect(buildPageOrderChange('empty', [], [2, 1])).toBeNull();
  });
});
