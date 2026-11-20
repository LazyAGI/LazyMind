import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { $getRoot, $getSelection, $isRangeSelection, UNDO_COMMAND, REDO_COMMAND, type LexicalEditor } from 'lexical';
import i18n from '@/i18n';
import { MarkdownArtifactEditor } from './MarkdownArtifactEditor';
import { SlotEditingContext } from './slotEditingContext';

const capture = vi.hoisted(() => ({ editor: null as LexicalEditor | null }));
vi.mock('@mdxeditor/editor', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@mdxeditor/editor')>();
  const React = await import('react');
  const capturePlugin = actual.realmPlugin({ init(realm) {
    realm.pub(actual.createRootEditorSubscription$, editor => { capture.editor = editor; return () => {}; });
  } });
  return { ...actual, MDXEditor: React.forwardRef<import('@mdxeditor/editor').MDXEditorMethods, import('@mdxeditor/editor').MDXEditorProps>(
    (props, ref) => <actual.MDXEditor {...props} ref={ref} plugins={[...(props.plugins ?? []), capturePlugin()]} />,
  ) };
});

beforeEach(async () => { capture.editor = null; await i18n.changeLanguage('zh-CN'); });

function renderEditor(markdown: string, readOnly = false) {
  let flush: (() => Promise<boolean>) | undefined;
  const onSave = vi.fn(async (value: string) => ({ markdown: value, revision: 2 }));
  const context = { setEditing: vi.fn(), registerFooterAction: () => () => {},
    registerFlush: (_key: string, callback: () => Promise<boolean>) => { flush = callback; return () => {}; } };
  const view = (value = markdown, disabled = readOnly) => <SlotEditingContext.Provider value={context}>
    <MarkdownArtifactEditor contentKind='prompt' editingKey='colors' markdown={value} sourceRevision={1} readOnly={disabled} onSave={onSave} />
  </SlotEditingContext.Provider>;
  return { ...render(view()), view, onSave, flush: async () => act(async () => { await flush!(); }) };
}

it('shows inline colors without saving or treating links, invalid colors and code blocks as color controls', async () => {
  const markdown = '主色 **#070A12**，透明色 #fff8，描边 rgba(0,229,255,0.06)。\n\n渐变 `linear-gradient(160deg, #070A12 0%, #0E1220 52%, #0A0E1A 100%)`。\n\n无效 #12345、#1234567、rgba(999,0,0,1)。\n\n[来源](#abcdef)\n\n```css\ncolor: #abcdef;\n```';
  const { onSave, container } = renderEditor(markdown);
  await screen.findAllByRole('button', { name: '修改颜色 #070A12' });
  expect(container.querySelectorAll('[data-writer-color]')).toHaveLength(6);
  expect(container.querySelector('[data-writer-color]')).toHaveTextContent('#070A12');
  expect(container.querySelector('a')).toHaveAttribute('href', '#abcdef');
  expect(onSave).not.toHaveBeenCalled();
});

it.each([
  ['RGBA label', '描边 rgba(0,229,255,0.33)。', 'rgba(0,229,255,0.33)', 190],
  ['inline code label', '渐变 `linear-gradient(160deg, #070A12 0%, #0E1220 52%)`。', '#0E1220', 145],
  ['wrapped color swatch', '描边 rgba(0,229,255,0.33)。', 'rgba(0,229,255,0.33)', 270],
])('opens the picker from the complete %s hit area', async (_kind, markdown, value, clientX) => {
  const editor = renderEditor(markdown as string);
  const color = await screen.findByRole('button', { name: `修改颜色 ${value}` });
  // The old hit test accepted only x <= left + 20. Real pointer coordinates
  // must work on the label and on a wrapped inline element's swatch as well.
  vi.spyOn(color, 'getBoundingClientRect').mockReturnValue(new DOMRect(100, 30, 180, 40));
  fireEvent.click(color, { clientX, clientY: 40 });
  expect(await screen.findByLabelText('颜色值')).toHaveValue(value);
  fireEvent.click(screen.getByRole('button', { name: /^取\s*消$/ }));
  await editor.flush();
  expect(editor.onSave).not.toHaveBeenCalled();
});

it('changes only the selected gradient stop and preserves source through save, undo, redo and reopen', async () => {
  const original = '<!-- 保留元数据 -->\n\n底层 `linear-gradient(160deg, #070A12 0%, #070A12 52%, #0A0E1A 100%)`。\n\n**主色** #070A12。\n';
  const editor = renderEditor(original);
  const colors = await screen.findAllByRole('button', { name: '修改颜色 #070A12' });
  fireEvent.click(colors[1]);
  fireEvent.change(await screen.findByLabelText('选择颜色'), { target: { value: '#ff2288' } });
  fireEvent.click(screen.getByRole('button', { name: /^确\s*认$/ }));
  const expected = original.replace('#070A12 52%', '#FF2288 52%');
  await screen.findByRole('button', { name: '修改颜色 #FF2288' });
  await editor.flush();
  expect(editor.onSave.mock.calls[0][0]).toBe(expected);
  await act(async () => { capture.editor!.dispatchCommand(UNDO_COMMAND, undefined); });
  await waitFor(() => expect(screen.getAllByRole('button', { name: '修改颜色 #070A12' })).toHaveLength(3));
  await act(async () => { capture.editor!.dispatchCommand(REDO_COMMAND, undefined); });
  await screen.findByRole('button', { name: '修改颜色 #FF2288' });
  editor.unmount();
  const reopened = renderEditor(expected);
  await screen.findByRole('button', { name: '修改颜色 #FF2288' });
  expect(reopened.onSave).not.toHaveBeenCalled();
});

it.each([
  ['rgba(0,229,255,0.06)', 'rgba(17,34,51,0.06)'],
  ['#abcd', '#112233dd'],
])('preserves the opacity of %s when choosing a new color', async (original, expected) => {
  const editor = renderEditor(`描边 ${original}。`);
  fireEvent.click(await screen.findByRole('button', { name: `修改颜色 ${original}` }));
  fireEvent.change(await screen.findByLabelText('选择颜色'), { target: { value: '#112233' } });
  fireEvent.click(screen.getByRole('button', { name: /^确\s*认$/ }));
  await screen.findByRole('button', { name: `修改颜色 ${expected}` });
  await editor.flush();
  expect(editor.onSave.mock.calls[0][0]).toBe(`描边 ${expected}。`);
});

it('validates manual values and cancels without changing the document', async () => {
  const editor = renderEditor('颜色 #123456。');
  fireEvent.keyDown(await screen.findByRole('button', { name: '修改颜色 #123456' }), { key: 'Enter' });
  fireEvent.change(await screen.findByLabelText('颜色值'), { target: { value: 'url(https://example.com)' } });
  expect(screen.getByRole('button', { name: /^确\s*认$/ })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: /^取\s*消$/ }));
  await editor.flush();
  expect(editor.onSave).not.toHaveBeenCalled();
});

it('shows read-only swatches and rejects an open picker after permission is revoked', async () => {
  const editor = renderEditor('颜色 #123456。', true);
  await waitFor(() => expect(editor.container.querySelector('[data-writer-color]')).toBeInTheDocument());
  expect(screen.queryByRole('button', { name: '修改颜色 #123456' })).not.toBeInTheDocument();
  editor.rerender(editor.view('颜色 #123456。', false));
  fireEvent.click(await screen.findByRole('button', { name: '修改颜色 #123456' }));
  await screen.findByLabelText('选择颜色');
  editor.rerender(editor.view('颜色 #123456。', true));
  await waitFor(() => expect(screen.queryByLabelText('选择颜色')).not.toBeInTheDocument());
  expect(editor.onSave).not.toHaveBeenCalled();
});

it.each([
  ['颜色 #123456。', '颜色 前#123456后。'],
  ['颜色 `rgba(242,246,255,0.03)`。', '颜色 前`rgba(242,246,255,0.03)`后。'],
])('keeps %s non-editable while allowing text before and after it', async (markdown, expected) => {
  const value = markdown.includes('rgba') ? 'rgba(242,246,255,0.03)' : '#123456';
  const editor = renderEditor(markdown);
  const color = await screen.findByRole('button', { name: `修改颜色 ${value}` });
  expect(color).toHaveAttribute('contenteditable', 'false');
  expect(color.closest('.writer-markdown-editor__content')).toHaveAttribute('contenteditable', 'true');
  for (const [offset, text] of [[0, '前'], [value.length, '后']] as const) {
    await act(async () => capture.editor!.update(() => {
      const node = $getRoot().getAllTextNodes().find(node => node.getTextContent() === value)!;
      node.select(offset, offset);
      const selection = $getSelection();
      if (!$isRangeSelection(selection)) throw new Error('Missing caret selection');
      selection.insertText(text);
    }));
  }
  await editor.flush();
  expect(editor.onSave.mock.calls[0][0]).toBe(expected);
  expect(screen.getByRole('button', { name: `修改颜色 ${value}` })).toHaveAttribute('contenteditable', 'false');
});

it('does not place a caret inside the nested inline code label on mouse down', async () => {
  const editor = renderEditor('颜色 `rgba(242,246,255,0.03)`。');
  const color = await screen.findByRole('button', { name: '修改颜色 rgba(242,246,255,0.03)' });
  const label = color.querySelector('span')!;
  expect(fireEvent.mouseDown(label, { button: 0 })).toBe(false);
  fireEvent.click(label);
  expect(await screen.findByLabelText('颜色值')).toHaveValue('rgba(242,246,255,0.03)');
  fireEvent.click(screen.getByRole('button', { name: /^取\s*消$/ }));
  await editor.flush();
  expect(editor.onSave).not.toHaveBeenCalled();
});

it.each(['x', 'Backspace', 'Delete'])('does not send %s from a focused color to the text editor', async key => {
  const editor = renderEditor('前文 `rgba(242,246,255,0.03)` 后文。');
  const color = await screen.findByRole('button', { name: '修改颜色 rgba(242,246,255,0.03)' });
  expect(fireEvent.keyDown(color, { key })).toBe(false);
  await editor.flush();
  expect(editor.onSave).not.toHaveBeenCalled();
});

it.each(['beforeinput', 'paste', 'cut'])('prevents %s from changing the focused color', async type => {
  const editor = renderEditor('前文 #123456 后文。');
  const color = await screen.findByRole('button', { name: '修改颜色 #123456' });
  const event = new Event(type, { bubbles: true, cancelable: true });
  expect(fireEvent(color, event)).toBe(false);
  await editor.flush();
  expect(editor.onSave).not.toHaveBeenCalled();
});

it('exports color text without display decorations in rich clipboard content', async () => {
  renderEditor('**#123456**。');
  await screen.findByRole('button', { name: '修改颜色 #123456' });
  capture.editor!.getEditorState().read(() => {
    const node = $getRoot().getAllTextNodes().find(node => node.getTextContent() === '#123456')!;
    const element = node.exportDOM(capture.editor!).element as HTMLElement;
    expect(element.textContent).toBe('#123456');
    expect(element.outerHTML).not.toContain('writer-color');
    expect(element.outerHTML).not.toContain('contenteditable');
    expect(element.querySelector('b, strong') ?? (element.matches('b, strong') ? element : null)).not.toBeNull();
  });
});

it('does not apply a stale picker to a newer document with the same color', async () => {
  const editor = renderEditor('原始 #123456。');
  fireEvent.click(await screen.findByRole('button', { name: '修改颜色 #123456' }));
  fireEvent.change(await screen.findByLabelText('颜色值'), { target: { value: '#abcdef' } });
  editor.rerender(editor.view('远端版本 #123456。'));
  await waitFor(() => expect(editor.container).toHaveTextContent('远端版本'));
  fireEvent.click(screen.getByRole('button', { name: /^确\s*认$/ }));
  await editor.flush();
  expect(editor.container).toHaveTextContent('远端版本 #123456。');
  expect(editor.onSave).not.toHaveBeenCalled();
});
