import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { WriterIRControl, type WriterIRControlProps } from './WriterIRControl';
import type { WriterDocument } from './writerIR';

const original: WriterDocument = {
  document_id: 'source-test', stage: 'final', title: '标题', ui_editable: true,
  blocks: [{ node_id: 'p1', type: 'paragraph', content: '正文' }],
  metadata: { extension: 'preserve' },
};
type Save = NonNullable<WriterIRControlProps['onSave']>;
function toggleSource() {
  const menu = document.querySelector<HTMLDetailsElement>('.writer-document-options')!;
  menu.open = true;
  fireEvent.click(screen.getByRole('button', { name: /^(源码|返回正文)$/ }));
  expect(menu.open).toBe(false);
}
const source = () => screen.getByRole('textbox', { name: '源码' }) as HTMLTextAreaElement;
const autosave = () => act(async () => { await vi.advanceTimersByTimeAsync(1_100); });

beforeEach(async () => { await i18n.changeLanguage('zh-CN'); vi.useFakeTimers(); });
afterEach(() => { cleanup(); vi.useRealTimers(); });

it.each([true, false])('keeps inline source read-only regardless of document editability (readOnly=%s)', async (readOnly) => {
  const save = vi.fn<Save>();
  render(<WriterIRControl document={original} readOnly={readOnly} onSave={save} />);
  toggleSource();
  expect(source()).toHaveValue(JSON.stringify(original, null, 2));
  expect(source()).toHaveAttribute('readonly');
  expect(source()).not.toBeDisabled(); // The source can still be selected and copied.
  expect(source().closest('.writer-ir__main')).not.toBeNull();
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  fireEvent.change(source(), { target: { value: '{"title":"attempted edit"}' } });
  toggleSource();
  expect(screen.queryByRole('textbox', { name: '源码' })).not.toBeInTheDocument();
  expect(screen.getByText('正文')).toBeInTheDocument();
  expect(screen.getByRole('heading', { name: '标题' })).toBeInTheDocument();
  await autosave();
  expect(save).not.toHaveBeenCalled();
});

it('shows pending rich edits without letting source shortcuts mutate or force-save them', async () => {
  const save = vi.fn<Save>(async (_base, next) => ({ document: next, sourceRevision: 2 }));
  const { container } = render(<WriterIRControl document={original} sourceRevision={1} onSave={save} />);
  const paragraph = container.querySelector('[data-node-id="p1"] [data-writer-block-content]')!;
  paragraph.textContent = '未保存的正文';
  fireEvent.input(paragraph);
  toggleSource();
  expect(JSON.parse(source().value).blocks[0].content).toBe('未保存的正文');
  fireEvent.keyDown(source(), { key: 'z', ctrlKey: true });
  fireEvent.keyDown(source(), { key: 's', ctrlKey: true });
  expect(save).not.toHaveBeenCalled();
  expect(JSON.parse(source().value).blocks[0].content).toBe('未保存的正文');
  await autosave();
  expect(save).toHaveBeenCalledTimes(1);
  expect(save.mock.calls[0][1].blocks[0].content).toBe('未保存的正文');
  toggleSource();
  const editor = screen.getByRole('textbox', { name: '结构化文档' });
  expect(editor).toHaveAttribute('contenteditable', 'true');
  const nextParagraph = container.querySelector('[data-node-id="p1"] [data-writer-block-content]')!;
  nextParagraph.textContent = '继续编辑正文';
  fireEvent.input(nextParagraph);
  await autosave();
  expect(save).toHaveBeenCalledTimes(2);
  expect(save.mock.calls[1][1].blocks[0].content).toBe('继续编辑正文');
});

it('updates the read-only source when the displayed document changes', () => {
  const save = vi.fn<Save>();
  const { rerender } = render(<WriterIRControl document={original} sourceRevision={1} onSave={save} />);
  toggleSource();
  const next = { ...original, title: '新版本' };
  rerender(<WriterIRControl document={next} sourceRevision={2} onSave={save} />);
  expect(source()).toHaveValue(JSON.stringify(next, null, 2));
  expect(source()).toHaveAttribute('readonly');
  expect(save).not.toHaveBeenCalled();
});
