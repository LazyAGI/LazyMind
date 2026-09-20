import { act, cleanup, render, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { $getRoot, $getSelection, $isElementNode, $isRangeSelection, $isTextNode, type LexicalEditor } from 'lexical';
import i18n from '@/i18n';
import { MarkdownArtifactEditor } from './MarkdownArtifactEditor';

const capture = vi.hoisted(() => ({ editor: null as LexicalEditor | null }));
vi.mock('@mdxeditor/editor', async () => {
  const actual = await vi.importActual<typeof import('@mdxeditor/editor')>('@mdxeditor/editor');
  const React = await import('react');
  const capturePlugin = actual.realmPlugin({ init(realm) {
    realm.pub(actual.createRootEditorSubscription$, editor => { capture.editor = editor; return () => {}; });
  } });
  return { ...actual, MDXEditor: React.forwardRef<import('@mdxeditor/editor').MDXEditorMethods, import('@mdxeditor/editor').MDXEditorProps>(
    (props, ref) => <actual.MDXEditor {...props} ref={ref} plugins={[...(props.plugins ?? []), capturePlugin()]} />,
  ) };
});

const originalRect = Object.getOwnPropertyDescriptor(Range.prototype, 'getBoundingClientRect');
const originalRects = Object.getOwnPropertyDescriptor(Range.prototype, 'getClientRects');
beforeEach(async () => {
  await i18n.changeLanguage('zh-CN');
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  const rect = { x: 0, y: 0, top: 0, left: 0, right: 100, bottom: 20, width: 100, height: 20, toJSON: () => ({}) };
  Object.defineProperty(Range.prototype, 'getBoundingClientRect', { configurable: true, value: () => rect });
  Object.defineProperty(Range.prototype, 'getClientRects', { configurable: true, value: () => [rect] });
});
afterEach(() => {
  cleanup(); window.getSelection()?.removeAllRanges(); vi.unstubAllGlobals();
  if (originalRect) Object.defineProperty(Range.prototype, 'getBoundingClientRect', originalRect);
  else Reflect.deleteProperty(Range.prototype, 'getBoundingClientRect');
  if (originalRects) Object.defineProperty(Range.prototype, 'getClientRects', originalRects);
  else Reflect.deleteProperty(Range.prototype, 'getClientRects');
});

const source = '<a id="block-title"></a>\n# Title\n\nAlpha beta\n\n3. First item\n4. Last item\n';
function caret() {
  return capture.editor!.getEditorState().read(() => {
    const selection = $getSelection();
    if (!$isRangeSelection(selection)) return null;
    return { key: selection.anchor.key, offset: selection.anchor.offset, type: selection.anchor.type,
      collapsed: selection.isCollapsed() };
  });
}
async function enterParagraph(empty: boolean) {
  await act(async () => capture.editor!.update(() => {
    const paragraph = $getRoot().getChildren().find(node => node.getTextContent() === 'Alpha beta');
    if (!$isElementNode(paragraph)) throw new Error('fixture paragraph missing');
    const text = paragraph.getFirstChild();
    if (!$isTextNode(text)) throw new Error('fixture text missing');
    text.select(5, 5);
    const selection = $getSelection();
    if (!$isRangeSelection(selection)) throw new Error('fixture selection missing');
    selection.insertParagraph();
    if (empty) selection.insertParagraph();
    else selection.insertText('New ');
  }));
}

it.each([false, true])('keeps the actual caret and paragraph after autosave (empty paragraph: %s)', async empty => {
  let resolveSave!: (result: { markdown: string; revision: number }) => void;
  const onSave = vi.fn((_markdown: string, _revision: number) => new Promise<{ markdown: string; revision: number }>(resolve => { resolveSave = resolve; }));
  const view = render(<MarkdownArtifactEditor markdown={source} sourceRevision={3} onSave={onSave} />);
  capture.editor!.getRootElement()!.focus();
  await enterParagraph(empty);
  if (empty) await act(async () => capture.editor!.update(() => {
    const paragraph = $getRoot().getChildren().find(node => node.getType() === 'paragraph' && !node.getTextContent());
    if (!$isElementNode(paragraph)) throw new Error('empty paragraph missing');
    paragraph.selectStart();
  }));
  const before = caret();
  const node = capture.editor!.getElementByKey(before!.key);
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1), { timeout: 2500 });
  const saved = onSave.mock.calls[0][0];
  await act(async () => { resolveSave({ markdown: saved, revision: 4 }); });
  view.rerender(<MarkdownArtifactEditor markdown={saved} sourceRevision={4} onSave={onSave} />);
  await act(async () => { await new Promise(resolve => requestAnimationFrame(resolve)); });
  expect(caret()).toEqual(before);
  expect(capture.editor!.getElementByKey(before!.key)).toBe(node);
  expect(view.container.querySelector('ol')).toHaveAttribute('start', '3');
  await act(async () => capture.editor!.update(() => {
    const selection = $getSelection();
    if (!$isRangeSelection(selection)) throw new Error('selection missing after save');
    selection.insertText('Continue');
  }));
  const paragraphs = Array.from(view.container.querySelectorAll('.mdxeditor-root-contenteditable p')).map(node => node.textContent);
  expect(paragraphs).toContain(empty ? 'Continue' : 'New Continue beta');
});

it('keeps newer input and the latest caret while an earlier save is pending', async () => {
  let resolveSave!: (result: { markdown: string; revision: number }) => void;
  const onSave = vi.fn(async (markdown: string, revision: number) => ({ markdown, revision: revision + 1 }))
    .mockImplementationOnce(() => new Promise(resolve => { resolveSave = resolve; }));
  const view = render(<MarkdownArtifactEditor markdown={source} sourceRevision={3} onSave={onSave} />);
  capture.editor!.getRootElement()!.focus();
  await enterParagraph(false);
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1), { timeout: 2500 });
  await act(async () => capture.editor!.update(() => {
    const selection = $getSelection();
    if (!$isRangeSelection(selection)) throw new Error('fixture selection missing');
    selection.insertText('Newer ');
  }));
  const before = caret();
  const saved = onSave.mock.calls[0][0];
  await act(async () => { resolveSave({ markdown: saved, revision: 4 }); });
  view.rerender(<MarkdownArtifactEditor markdown={saved} sourceRevision={4} onSave={onSave} />);
  expect(caret()).toEqual(before);
  expect(view.container).toHaveTextContent('New Newer beta');
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(2), { timeout: 2500 });
  expect(onSave.mock.calls[1][0]).toContain('New Newer  beta');
  expect(onSave.mock.calls[1][1]).toBe(4);
  expect(caret()).toEqual(before);
});
