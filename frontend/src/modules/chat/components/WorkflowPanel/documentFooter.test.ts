import { describe, expect, it, vi } from 'vitest';
import { buildDocumentFooterItems } from './documentFooter';
import type { SlotFooterAction } from './slotEditingContext';

const copy = (dedupKey?: string): SlotFooterAction => ({
  label: 'Copy Markdown', icon: 'copy', dedupKey, onClick: vi.fn(),
});

describe('document footer resource actions', () => {
  it('presents one copy action for aliases of a read-only paper and retains the remaining registration', () => {
    const first = copy('copy:paper.md');
    const second = copy('copy:paper.md');
    const actions = new Map([['final_paper:copy', first], ['final_paper_markdown:copy', second]]);
    expect(buildDocumentFooterItems(actions).actionItems).toHaveLength(1);
    actions.delete('final_paper:copy');
    expect(buildDocumentFooterItems(actions).actionItems).toEqual([
      expect.objectContaining({ key: 'final_paper_markdown:copy', action: second }),
    ]);
  });

  it('keeps the busy owner when hook updates change registration order', () => {
    const busy = { ...copy('copy:paper.md'), disabled: true };
    const result = buildDocumentFooterItems(new Map([
      ['b:copy', copy('copy:paper.md')], ['a:copy', busy],
    ]));
    expect(result.actionItems).toEqual([expect.objectContaining({ action: busy })]);
  });

  it('preserves different documents and independently editable snapshots', () => {
    expect(buildDocumentFooterItems(new Map([
      ['paper', copy('copy:paper.md')], ['appendix', copy('copy:appendix.md')],
      ['editor1', copy()], ['editor2', copy()],
    ])).actionItems).toHaveLength(4);
  });
});
