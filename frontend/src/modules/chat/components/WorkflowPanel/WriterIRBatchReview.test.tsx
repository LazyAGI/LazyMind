import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { documentRewritePreview } from './documentRewritePreview';
import { WriterIRControl } from './WriterIRControl';
import type { WriterDocument } from './writerIR';

const source: WriterDocument = { document_id: 'review-test', title: 'Document', stage: 'draft', blocks: [
  { node_id: 'first', type: 'paragraph', content: 'First' },
  { node_id: 'middle', type: 'paragraph', content: 'Keep' },
  { node_id: 'last', type: 'paragraph', content: 'Last' },
] };
const preview = documentRewritePreview({ representation: 'ir', results: ['first', 'last'].map((id, index) => ({
  target: { type: 'block', block_type: 'paragraph', node_id: id },
  preview: { old_text: index ? 'Last' : 'First', new_text: index ? 'Better' : 'Clear' },
  patch: { type: 'writer_ir_patch', payload: {} },
})), artifact: { content_type: 'json', value: { ...source, blocks: source.blocks.map(block => ({ ...block, content: block.node_id === 'first' ? 'Clear' : block.node_id === 'last' ? 'Better' : 'Keep' })) } },
commit: { token: '00000000000000000000000000000001' } }, 3);

type Save = NonNullable<React.ComponentProps<typeof WriterIRControl>['onSave']>;
function Review({ onSave }: { onSave: Save }) {
  const [reviewing, setReviewing] = useState(true);
  return <WriterIRControl document={source} sourceRevision={3} onSave={onSave}
    rewritePreview={reviewing ? { nodeId: 'first', sourceDocument: source, sessionId: '', slotId: '', listIndex: 0, preview } : null}
    onRewritePreviewApplied={() => setReviewing(false)} onRewritePreviewRejected={() => setReviewing(false)} />;
}
function editMiddle(container: HTMLElement, text: string) {
  const paragraph = container.querySelector<HTMLElement>('[data-node-id="middle"] [data-writer-block-content]')!;
  paragraph.textContent = text;
  fireEvent.input(paragraph);
}
beforeEach(async () => {
  await i18n.changeLanguage('zh-CN');
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
});
afterEach(() => vi.unstubAllGlobals());

it('reviews in the editable document and preserves typing before and during checkpoint save', async () => {
  let finish!: (result: { document: WriterDocument; sourceRevision: number }) => void;
  const save = vi.fn<Save>().mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }))
    .mockImplementation(async (_, document, revision) => ({ document, sourceRevision: Number(revision) + 1 }));
  const { container } = render(<Review onSave={save} />);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(screen.getByRole('textbox')).toHaveAttribute('contenteditable', 'true');
  await waitFor(() => expect(container.querySelector('[data-node-id="first"] [data-writer-block-content]')).toHaveAttribute('contenteditable', 'false'));
  editMiddle(container, 'Edited while reviewing');
  fireEvent.click(screen.getByRole('button', { name: '全部接受' }));
  await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
  expect(save.mock.calls[0][1].blocks.map(block => block.content)).toEqual(['Clear', 'Edited while reviewing', 'Better']);
  expect(save.mock.calls[0][3]).toBe('checkpoint');
  editMiddle(container, 'Typing during save');
  await act(async () => finish({ document: save.mock.calls[0][1], sourceRevision: 4 }));
  await waitFor(() => expect(save).toHaveBeenCalledTimes(2), { timeout: 2500 });
  expect(save.mock.calls[1][1].blocks.map(block => block.content)).toEqual(['Clear', 'Typing during save', 'Better']);
});

it('rejects the proposal without reverting other edits', async () => {
  const save = vi.fn<Save>(async (_, document, revision) => ({ document, sourceRevision: Number(revision) + 1 }));
  const { container } = render(<Review onSave={save} />);
  editMiddle(container, 'Kept after rejection');
  fireEvent.click(screen.getByRole('button', { name: '全部拒绝' }));
  await waitFor(() => expect(save).toHaveBeenCalled(), { timeout: 2500 });
  expect(save.mock.calls[0][1].blocks.map(block => block.content)).toEqual(['First', 'Kept after rejection', 'Last']);
  expect(container.querySelector('[data-node-id="first"] [data-writer-block-content]')).not.toHaveAttribute('contenteditable', 'false');
});

it('keeps an accepted paragraph when the remaining proposal is rejected', async () => {
  const save = vi.fn<Save>(async (_, document, revision) => ({ document, sourceRevision: Number(revision) + 1 }));
  const { container } = render(<Review onSave={save} />);
  fireEvent.click(within(screen.getByRole('group', { name: '第 1 段' })).getByRole('button', { name: '接受' }));
  await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
  expect(save.mock.calls[0][1].blocks.map(block => block.content)).toEqual(['Clear', 'Keep', 'Last']);
  await waitFor(() => expect(screen.queryByRole('group', { name: '第 1 段' })).not.toBeInTheDocument());
  expect(container.querySelector('[data-node-id="last"] [data-writer-block-content]')).toHaveAttribute('contenteditable', 'false');
  fireEvent.click(screen.getByRole('button', { name: '全部拒绝' }));
  expect(container.querySelector('[data-node-id="first"] [data-writer-block-content]')).toHaveTextContent('Clear');
  expect(container.querySelector('[data-node-id="last"] [data-writer-block-content]')).toHaveTextContent('Last');
  expect(save).toHaveBeenCalledTimes(1);
});
