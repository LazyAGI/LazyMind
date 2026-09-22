import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { MarkdownArtifactEditor } from './MarkdownArtifactEditor';
import { documentRewritePreview } from './documentRewritePreview';
import type { MarkdownSelection } from './artifactRewriteSelection';

const source = '<a id="block-heading"></a>\n## Heading\n\n3. First\n   - Child\n4. Last\n\n- [x] Done\n- [ ] Todo\n\nKeep';
const rect = { x: 20, y: 100, left: 20, top: 100, right: 220, bottom: 120, width: 200, height: 20, toJSON: () => ({}) };
const originalRect = Object.getOwnPropertyDescriptor(Range.prototype, 'getBoundingClientRect');
const originalRects = Object.getOwnPropertyDescriptor(Range.prototype, 'getClientRects');
beforeEach(async () => {
  await i18n.changeLanguage('zh-CN');
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
    ...rect, x: 0, y: 0, left: 0, top: 0, right: 800, bottom: 600, width: 800, height: 600,
  });
  Object.defineProperty(Range.prototype, 'getBoundingClientRect', { configurable: true, value: () => rect });
  Object.defineProperty(Range.prototype, 'getClientRects', { configurable: true, value: () => [rect] });
});
afterEach(() => {
  window.getSelection()?.removeAllRanges(); vi.restoreAllMocks(); vi.unstubAllGlobals();
  if (originalRect) Object.defineProperty(Range.prototype, 'getBoundingClientRect', originalRect);
  else Reflect.deleteProperty(Range.prototype, 'getBoundingClientRect');
  if (originalRects) Object.defineProperty(Range.prototype, 'getClientRects', originalRects);
  else Reflect.deleteProperty(Range.prototype, 'getClientRects');
});

it.each([['heading', 'Heading'], ['list item', 'Child'], ['checked item', 'Done'], ['unchecked item', 'Todo']])(
'submits a real %s selection with exact source offsets', async (kind, selectedText) => {
  const onRewrite = vi.fn<(selection: MarkdownSelection) => void>();
  const onSave = vi.fn(async (markdown: string, revision: number) => ({ markdown, revision: revision + 1 }));
  const props = { markdown: source, sourceRevision: 3, onSave, onRewriteSelection: onRewrite, allowMultipleParagraphs: true };
  const view = render(<MarkdownArtifactEditor {...props} />);
  const editable = view.container.querySelector('.mdxeditor-root-contenteditable')!;
  const target = (kind === 'heading' ? editable.querySelector('h2')
    : Array.from(editable.querySelectorAll('li')).find(item => item.textContent === selectedText)) as HTMLElement;
  const range = document.createRange(); range.selectNodeContents(target);
  window.getSelection()?.addRange(range);
  fireEvent.mouseUp(target);
  const polish = await screen.findByRole('button', { name: 'AI 润色', exact: true });
  expect(polish).toBeEnabled();
  fireEvent.click(polish);
  await waitFor(() => expect(onRewrite).toHaveBeenCalledTimes(1));
  const selected = onRewrite.mock.calls[0][0];
  const quote = selected.sourceRange!;
  expect(quote.selected_text).toBe(selectedText);
  expect(Array.from(source).slice(quote.start, quote.end).join('')).toBe(quote.selected_text);
});

it('submits and applies each heading/list block once while preserving Markdown structure', async () => {
  const onRewrite = vi.fn<(selection: MarkdownSelection) => void>();
  const onSave = vi.fn(async (markdown: string, revision: number) => ({ markdown, revision: revision + 1 }));
  const props = { markdown: source, sourceRevision: 3, onSave, onRewriteSelection: onRewrite, allowMultipleParagraphs: true };
  const view = render(<MarkdownArtifactEditor {...props} />);
  const editable = view.container.querySelector('.mdxeditor-root-contenteditable')!;
  expect(editable.querySelector('ol')).toHaveAttribute('start', '3');
  expect(Array.from(editable.querySelectorAll('ol > li')).map(item => item.getAttribute('value'))).toEqual(['3', '4', '4']);
  const heading = editable.querySelector('h2')!, last = Array.from(editable.querySelectorAll('li')).find(item => item.textContent === 'Todo')!;
  const range = document.createRange(); range.setStartBefore(heading); range.setEndAfter(last);
  window.getSelection()?.addRange(range); fireEvent.mouseUp(last);
  fireEvent.click(await screen.findByRole('button', { name: 'AI 润色', exact: true }));
  await waitFor(() => expect(onRewrite).toHaveBeenCalledTimes(1));
  const ranges = onRewrite.mock.calls[0][0].sourceRanges!;
  expect(ranges.map(item => item.selected_text)).toEqual(['Heading', 'First', 'Child', 'Last', 'Done', 'Todo']);
  expect(ranges.every(item => Array.from(source).slice(item.start, item.end).join('') === item.selected_text)).toBe(true);
  const revised = source.replace('Heading', 'Revised heading').replace('First', 'Revised first').replace('Child', 'Revised child').replace('Last', 'Revised last').replace('Done', 'Revised done').replace('Todo', 'Revised todo');
  const preview = documentRewritePreview({
    representation: 'markdown', results: ranges.map((item, index) => ({
      target: { type: 'block', block_type: index === 0 ? 'heading' : 'list_item', target_start: item.start, target_end: item.end },
      preview: { old_text: item.selected_text, new_text: `Revised ${item.selected_text.toLowerCase()}` },
      patch: { type: 'string_replace_set', payload: {} },
    })), artifact: { content_type: 'text', value: revised }, commit: { token: '00000000000000000000000000000001' },
  }, 3);
  view.rerender(<MarkdownArtifactEditor {...props} rewritePreview={{
    paragraph: heading as HTMLElement, sourceMarkdown: source, sessionId: 'fixture', slotId: 'fixture', listIndex: -1, preview,
  }} onRewritePreviewRejected={() => {}} />);
  fireEvent.click(await screen.findByRole('button', { name: '全部接受' }));
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
  expect(onSave.mock.calls[0][0]).toBe(revised);
  expect(editable.querySelector('h2')).toHaveTextContent('Revised heading');
  expect(Array.from(editable.querySelectorAll('ol > li')).map(item => item.getAttribute('value'))).toEqual(['3', '4', '4']);
  expect(editable.querySelector('ol')).toHaveAttribute('start', '3');
  expect(editable.querySelector('ol > li ul li')).toHaveTextContent('Revised child');
  expect(editable.querySelector('li[aria-checked="true"]')).toHaveTextContent('Revised done');
  expect(editable.querySelector('li[aria-checked="false"]')).toHaveTextContent('Revised todo');
});
