import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { $createParagraphNode, $createTextNode, $getRoot, type LexicalEditor } from 'lexical';
import i18n from '@/i18n';
import { MarkdownArtifactEditor } from './MarkdownArtifactEditor';
import { SlotEditingContext } from './slotEditingContext';
import readme from '../../../../../../README.CN.md?raw';

const capture = vi.hoisted(() => ({ editor: null as LexicalEditor | null, abortDiff: false, importError: '' }));
vi.mock('diff', async (importOriginal) => {
  const actual = await importOriginal<typeof import('diff')>();
  return { ...actual, diffLines: (...args: Parameters<typeof actual.diffLines>) =>
    capture.abortDiff ? undefined : actual.diffLines(...args) };
});
vi.mock('@mdxeditor/editor', async () => {
  const actual = await vi.importActual<typeof import('@mdxeditor/editor')>('@mdxeditor/editor');
  const React = await import('react');
  const capturePlugin = actual.realmPlugin({ init(realm) {
    realm.pub(actual.createRootEditorSubscription$, editor => { capture.editor = editor; return () => {}; });
  } });
  return { ...actual, MDXEditor: React.forwardRef<import('@mdxeditor/editor').MDXEditorMethods, import('@mdxeditor/editor').MDXEditorProps>(
    (props, ref) => <actual.MDXEditor {...props} ref={ref} plugins={[...(props.plugins ?? []), capturePlugin()]}
      onError={(error: { error: string; source: string }) => { capture.importError = error.error; props.onError?.(error); }} />,
  ) };
});

const source = [
  '# Project', '', '**[English](README.md)** | **中文**', '',
  '[![macOS](https://example.org/badge?style=flat&logo=apple)](desktop/README.md)', '',
  'Original paragraph.', '',
  ...Array.from({ length: 20 }, (_, i) => [
    `## Feature ${i}`, '', '- Desktop', '- Enterprise', '', '---', '',
    '| 场景 | 执行 |', '|------|------|', '| Writer | 编辑 |', '', '',
    `https://example.org/video/${i}`, '',
  ]).flat(),
  '## Quick start', '', '```bash', 'echo "A & B"', '```', '', '',
].join('\n');
const resolveImageUrl = async (url: string) => url;

const rectDescriptor = Object.getOwnPropertyDescriptor(Range.prototype, 'getBoundingClientRect');
const rectsDescriptor = Object.getOwnPropertyDescriptor(Range.prototype, 'getClientRects');
beforeEach(async () => {
  await i18n.changeLanguage('zh-CN');
  capture.abortDiff = false;
  capture.editor = null;
  capture.importError = '';
  Object.defineProperty(Range.prototype, 'getBoundingClientRect', { configurable: true, value: () => new DOMRect() });
  Object.defineProperty(Range.prototype, 'getClientRects', { configurable: true, value: () => [] });
});
afterEach(() => {
  cleanup();
  if (rectDescriptor) Object.defineProperty(Range.prototype, 'getBoundingClientRect', rectDescriptor);
  else Reflect.deleteProperty(Range.prototype, 'getBoundingClientRect');
  if (rectsDescriptor) Object.defineProperty(Range.prototype, 'getClientRects', rectsDescriptor);
  else Reflect.deleteProperty(Range.prototype, 'getClientRects');
});

async function replaceText(before: string, after: string) {
  await act(async () => capture.editor!.update(() => {
    const text = $getRoot().getAllTextNodes().find(node => node.getTextContent() === before);
    if (!text) throw new Error('Fixture text missing');
    text.setTextContent(after);
  }));
}

// The GitHub provider strips Writer's generated navigation anchors on publish.
// They must still exist locally for the outline and cross-references to work.
function publishedSource(markdown: string) {
  return markdown.replace(/^<a id="block-user-[^"]+"><\/a>\r?\n/gm, '');
}

it('saves only a new section and a later text edit, retaining untouched README source', async () => {
  let revision = 1;
  const onSave = vi.fn(async (markdown: string) => ({ markdown, revision: ++revision }));
  render(<MarkdownArtifactEditor markdown={source} sourceRevision={1} onSave={onSave} resolveImageUrl={resolveImageUrl} />);
  await waitFor(() => expect(capture.editor).not.toBeNull());
  expect(onSave).not.toHaveBeenCalled();
  await act(async () => capture.editor!.update(() => {
    const heading = $getRoot().getChildren().find(node => node.getTextContent() === 'Quick start');
    if (!heading) throw new Error('Fixture heading missing');
    heading.insertBefore($createParagraphNode().append($createTextNode('New security section.')));
  }));
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1), { timeout: 3000 });
  const saved = source.replace('## Quick start', 'New security section.\n\n## Quick start');
  expect(onSave.mock.calls[0][0]).toContain('<a id="block-user-');
  expect(publishedSource(onSave.mock.calls[0][0])).toBe(saved);
  await replaceText('Original paragraph.', 'Edited paragraph.');
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(2), { timeout: 3000 });
  expect(publishedSource(onSave.mock.calls[1][0])).toBe(saved.replace('Original paragraph.', 'Edited paragraph.'));
}, 15000);

it('retains an unsaved edit, blocks publication and allows retry after mapping aborts', async () => {
  let flush: (() => Promise<boolean>) | undefined;
  let finishSave: (() => void) | undefined;
  const onSave = vi.fn((markdown: string) => new Promise<{ markdown: string; revision: number }>(resolve => {
    finishSave = () => resolve({ markdown, revision: 2 });
  }));
  render(<SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFooterAction: () => () => {},
    registerFlush: (_key, callback) => { flush = callback; return () => {}; },
  }}><MarkdownArtifactEditor editingKey='readme' markdown={source} sourceRevision={1} onSave={onSave} resolveImageUrl={resolveImageUrl} /></SlotEditingContext.Provider>);
  await waitFor(() => expect(capture.editor).not.toBeNull());
  capture.abortDiff = true;
  await replaceText('Original paragraph.', 'Unsaved paragraph.');
  await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('保存失败'), { timeout: 3000 });
  expect(onSave).not.toHaveBeenCalled();
  await act(async () => expect(await flush!()).toBe(false));
  expect(capture.editor!.getRootElement()).toHaveTextContent('Unsaved paragraph.');
  // A read-only source view must not crash the editor or expose a silently
  // reformatted replacement when its mapping is unavailable.
  const menu = document.querySelector<HTMLDetailsElement>('.writer-document-options')!;
  act(() => { menu.open = true; fireEvent(menu, new Event('toggle')); });
  fireEvent.click(screen.getByRole('button', { name: '查看源码' }));
  expect(onSave).not.toHaveBeenCalled();
  expect(screen.queryByRole('textbox', { name: '查看源码' })).not.toBeInTheDocument();
  capture.abortDiff = false;
  fireEvent.click(screen.getByRole('button', { name: '重试' }));
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
  const savedMarkdown = onSave.mock.calls[0][0];
  const unsavedMarkdown = source.replace('Original paragraph.', 'Unsaved paragraph.');
  expect(publishedSource(savedMarkdown)).toBe(unsavedMarkdown);
  // Calling onSave starts persistence; its receipt updates the source preview.
  await waitFor(() => expect(screen.getByRole('textbox', { name: '查看源码' })).toHaveValue(unsavedMarkdown));
  await act(async () => { finishSave!(); });
  await waitFor(() => expect(screen.getByRole('textbox', { name: '查看源码' })).toHaveValue(savedMarkdown));
  expect(screen.getByRole('textbox', { name: '查看源码' })).toHaveAttribute('readonly');
}, 15000);

it('round-trips the repository README editor input with only the added content in the diff', async () => {
  // GitHub imports HTML image layouts as Markdown; their original HTML is
  // restored by the provider on publish. Feed the same supported layout here.
  const editorReadme = readme.replace(/<table>[\s\S]*?<\/table>/g, layout => {
    const images = [...layout.matchAll(/<img src="([^"]+)" alt="([^"]+)"/g)];
    const captions = [...layout.matchAll(/<sub>([^<]+)<\/sub>/g)];
    return images.map((image, index) => `![${image[2]}](${image[1]})\n\n*${captions[index][1]}*`).join('\n\n');
  });
  const onSave = vi.fn(async (markdown: string) => ({ markdown, revision: 2 }));
  render(<MarkdownArtifactEditor markdown={editorReadme} sourceRevision={1} onSave={onSave} resolveImageUrl={resolveImageUrl} />);
  expect(capture.importError).toBe('');
  await waitFor(() => expect(capture.editor).not.toBeNull());
  await act(async () => capture.editor!.update(() => {
    const heading = $getRoot().getChildren().find(node => node.getTextContent() === '快速开始');
    if (!heading) throw new Error('README Quick start heading missing');
    heading.insertBefore($createParagraphNode().append($createTextNode('新增安全合规说明。')));
  }));
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1), { timeout: 4000 });
  expect(publishedSource(onSave.mock.calls[0][0])).toBe(editorReadme.replace('## 快速开始', '新增安全合规说明。\n\n## 快速开始'));
}, 15000);

it.each(['ref-001', 'SRC-001', 'KB-001', 'source.v2_甲'])('preserves and navigates citation %s across real editor saves', async (id) => {
  const manuscript = '# 论文\n\nOriginal paragraph.\n\n依据[\\[1\\]](#paper-ref-ref-001)。\n\n'
    + '<a id="block-chart-001" data-paper-figure-label="图1"></a>\n![图1 比较](https://example.org/chart.svg)\n\n'
    + '<a id="paper-references-start"></a>\n\n**参考文献**\n\n'
    + '<a id="paper-ref-ref-001"></a>\n\n[1] 作者. 标题. 2026.\n\n'
    + '<a id="paper-references-end"></a>';
  const source = manuscript.replaceAll('paper-ref-ref-001', `paper-ref-${id}`);
  const onSave = vi.fn(async (markdown: string) => ({ markdown, revision: 2 }));
  render(<MarkdownArtifactEditor markdown={source} sourceRevision={1} onSave={onSave} resolveImageUrl={resolveImageUrl} />);
  await waitFor(() => expect(capture.editor).not.toBeNull());
  expect(capture.importError).toBe('');
  await replaceText('Original paragraph.', 'Edited paragraph.');
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1), { timeout: 3000 });
  expect(publishedSource(onSave.mock.calls[0][0])).toBe(source.replace('Original paragraph.', 'Edited paragraph.'));
  const target = document.getElementById(`paper-ref-${id}`)!;
  expect(target).toHaveClass('writer-markdown-editor__system-anchor');
  const surface = document.querySelector<HTMLElement>('.writer-markdown-editor__surface')!;
  // JSDOM does not lay out the document; model an overflowing editor viewport.
  surface.style.overflowY = 'auto';
  Object.defineProperties(surface, {
    clientHeight: { value: 300 },
    scrollHeight: { value: 1800 },
    clientTop: { value: 1 },
  });
  surface.scrollTop = 100;
  vi.spyOn(surface, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, 200, 600, 300));
  vi.spyOn(target, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, 900, 600, 20));
  surface.scrollTo = vi.fn();
  const link = document.querySelector<HTMLAnchorElement>(`a[href="#paper-ref-${id}"]`)!;
  expect(link.textContent).toBe('[1]');
  fireEvent.click(link);
  expect(surface.scrollTo).toHaveBeenCalledTimes(1);
  expect(surface.scrollTo).toHaveBeenCalledWith({ top: 791, behavior: 'smooth' });
}, 15000);

it('selects a bibliography entry and saves a canonical citation through the real editor', async () => {
  Object.defineProperty(Range.prototype, 'getBoundingClientRect', { configurable: true, value: () => new DOMRect(20, 40, 160, 20) });
  const source = '# 论文\n\nOriginal paragraph.\n\n'
    + '<a id="paper-references-start"></a>\n\n**参考文献**\n\n'
    + '<a id="paper-ref-SRC-甲"></a>\n\n[1] Author. Study.\n\n'
    + '<a id="paper-references-end"></a>';
  const onSave = vi.fn(async (markdown: string) => ({ markdown, revision: 2 }));
  const { container } = render(<MarkdownArtifactEditor markdown={source} sourceRevision={1} onSave={onSave} />);
  await waitFor(() => expect(capture.editor).not.toBeNull());
  const paragraph = Array.from(container.querySelectorAll('p')).find(p => p.textContent === 'Original paragraph.')!;
  const range = document.createRange();
  range.selectNodeContents(paragraph);
  window.getSelection()!.removeAllRanges();
  window.getSelection()!.addRange(range);
  fireEvent.mouseUp(paragraph);
  const trigger = await screen.findByTitle(i18n.t('chat.writerIR.crossReference'));
  fireEvent.mouseDown(trigger);
  fireEvent.click(trigger);
  fireEvent.click(await screen.findByTitle('[1] Author. Study.'));
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1), { timeout: 3000 });
  expect(publishedSource(onSave.mock.calls[0][0])).toBe(source.replace(
    'Original paragraph.', 'Original paragraph.[\\[1\\]](#paper-ref-SRC-甲)',
  ));
  expect(container.querySelector('a[href="#paper-ref-SRC-甲"]')).toHaveTextContent('[1]');
}, 15000);
