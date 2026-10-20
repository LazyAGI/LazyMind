import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { WriterIRControl } from './WriterIRControl';
import { SlotEditingContext } from './slotEditingContext';
import type { WriterDocument } from './writerIR';

vi.mock('./WriterIRDocumentEditor', () => ({
  WriterIRDocumentEditor: ({ document, onChange }: { document: WriterDocument; onChange: (document: WriterDocument) => void }) =>
    <textarea aria-label='IR draft' value={String(document.blocks[0].content)} onChange={event => onChange({
      ...document, blocks: [{ node_id: 'p', type: 'paragraph', content: event.target.value }],
    })} />,
}));
vi.mock('@/modules/knowledge/utils/imageUrl', () => ({ resolveMarkdownImageUrlAsync: async (value: string) => value }));

beforeEach(async () => { await i18n.changeLanguage('zh-CN'); });

it.each(['transport', 'unexpected'])('hides %s errors while preserving the edited draft and retry action', async kind => {
  const failure = new Error('raw-private-error request-private diagnostic-private');
  if (kind === 'transport') Object.assign(failure, { isAxiosError: true, response: { status: 502, data: {
    message: failure.message, data: { operation_id: 'op-private', request_id: 'request-private', diagnostic_id: 'diagnostic-private' },
  } } });
  const onSave = vi.fn().mockRejectedValue(failure);
  let flush: (() => Promise<boolean>) | undefined;
  render(<SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFooterAction: () => () => {}, registerFlush: (_key, next) => { flush = next; return () => {}; } }}>
    <WriterIRControl editingKey='safe-save' sourceRevision={1} onSave={onSave} document={{ document_id: 'fixture', stage: 'draft', title: 'Draft',
      blocks: [{ node_id: 'p', type: 'paragraph', content: 'Original' }] }} />
  </SlotEditingContext.Provider>);
  fireEvent.change(await screen.findByRole('textbox', { name: 'IR draft' }), { target: { value: 'Retained edits' } });
  await waitFor(() => expect(flush).toBeTypeOf('function'));
  await act(async () => { expect(await flush!()).toBe(false); });
  expect(onSave).toHaveBeenCalledTimes(1);
  expect(await screen.findByRole('alert')).toHaveTextContent('保存失败');
  for (const text of ['raw-private-error', 'request-private', 'diagnostic-private', 'op-private']) {
    expect(screen.queryByText(text, { exact: false })).not.toBeInTheDocument();
  }
  expect(screen.getByRole('textbox', { name: 'IR draft' })).toHaveValue('Retained edits');
  expect(screen.getByRole('button', { name: '重试' })).toBeEnabled();
});
