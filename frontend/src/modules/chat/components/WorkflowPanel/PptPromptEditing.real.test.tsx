import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { $getRoot, type LexicalEditor } from 'lexical';
import i18n from '@/i18n';
import { useWorkflowStore, type SlotRevision } from '@/modules/chat/store/workflowPanel';
import { SlotRenderer } from './SlotComponents';
import { SlotEditingContext } from './slotEditingContext';

const capture = vi.hoisted(() => ({ editor: null as LexicalEditor | null, importError: '' }));
vi.mock('./FilePreviewDrawer', () => ({ FilePreviewDrawer: () => null }));
vi.mock('@mdxeditor/editor', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@mdxeditor/editor')>();
  const React = await import('react');
  const capturePlugin = actual.realmPlugin({ init(realm) {
    realm.pub(actual.createRootEditorSubscription$, editor => { capture.editor = editor; return () => {}; });
  } });
  return { ...actual, MDXEditor: React.forwardRef<import('@mdxeditor/editor').MDXEditorMethods, import('@mdxeditor/editor').MDXEditorProps>(
    (props, ref) => <actual.MDXEditor {...props} ref={ref} plugins={[...(props.plugins ?? []), capturePlugin()]}
      onError={(error: { error: string; source: string }) => { capture.importError = error.error; props.onError?.(error); }} />,
  ) };
});

const prompt = [
  '<!-- ppt-page-prompt:v1 -->', '',
  '## 风格', '浅色背景', '',
  '## 布局', 'HTML 示例 <i>/<sub>/<sup>，参数 {"color":"#FFFFFF"}。', '',
  '```html', '<!-- ppt-material-images: ["example"] -->', '<div style="color: red">示例</div>', '```', '',
  '## 页面正文', '**标题**：原始标题', '',
  '## 页面素材', '<!-- ppt-material-images: ["image-revision-1"] -->', '',
  '保留素材说明。',
].join('\n');
const slot: SlotRevision = {
  slot_id: 'slide_outline', slot: 'slide_outline', selected: true,
  revision: 3, draft_version: 2, list_index: 1, content_type: 'text',
  created_at: '', artifact_value: { text: prompt },
};

beforeEach(async () => {
  await i18n.changeLanguage('zh-CN');
  capture.editor = null;
  capture.importError = '';
});
afterEach(cleanup);

it('edits a PPT design in the real Markdown editor and saves its source metadata and HTML intact', async () => {
  const patchSlotItemValue = vi.fn().mockResolvedValue(3);
  useWorkflowStore.setState({ patchSlotItemValue });
  let flush: (() => Promise<boolean>) | undefined;
  const view = render(<SlotEditingContext.Provider value={{ setEditing: vi.fn(), registerFooterAction: () => () => {},
    registerFlush: (_key, callback) => { flush = callback; return () => {}; },
  }}><SlotRenderer slot={slot} sessionId='ppt-editor' widget={{ widgetType: 'ppt-prompt', maxHeight: 420 }} /></SlotEditingContext.Provider>);

  fireEvent.click(view.container.querySelector('summary')!);
  expect(capture.editor).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: '编辑文本' }));
  expect(capture.importError).toBe('');
  await waitFor(() => expect(view.container.querySelector('[contenteditable="true"]')).toBeInTheDocument());
  expect(view.container.querySelector('.workflow-slot__text-editor')).toBeNull();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  expect(patchSlotItemValue).not.toHaveBeenCalled();

  await act(async () => capture.editor!.update(() => {
    const node = $getRoot().getAllTextNodes().find(node => node.getTextContent() === '浅色背景');
    if (!node) throw new Error('Editable prompt paragraph missing');
    node.setTextContent('深色背景');
  }));
  await act(async () => { expect(await flush!()).toBe(true); });
  expect(patchSlotItemValue).toHaveBeenCalledTimes(1);
  const saved = patchSlotItemValue.mock.calls[0][3].text as string;
  expect(saved).toBe(prompt.replace('浅色背景', '深色背景'));
  expect(patchSlotItemValue).toHaveBeenCalledWith('ppt-editor', 'slide_outline', 1, { text: saved }, 'text', 'checkpoint', 3, 2);
  await act(async () => capture.editor!.update(() => {
    const node = $getRoot().getAllTextNodes().find(node => node.getTextContent() === '深色背景');
    if (!node) throw new Error('Saved prompt paragraph missing');
    node.setTextContent('渐变背景');
  }));
  await act(async () => { expect(await flush!()).toBe(true); });
  expect(patchSlotItemValue.mock.calls[1][3].text).toBe(prompt.replace('浅色背景', '渐变背景'));
});

it('closes editing when the prompt becomes read-only', async () => {
  const view = render(<SlotRenderer slot={slot} sessionId='ppt-editor' widget={{ widgetType: 'ppt-prompt' }} />);
  fireEvent.click(screen.getByRole('button', { name: '编辑文本' }));
  expect(await screen.findByRole('textbox', { name: 'editable markdown' })).toBeInTheDocument();
  view.rerender(<SlotRenderer slot={slot} sessionId='ppt-editor' readOnly widget={{ widgetType: 'ppt-prompt' }} />);
  expect(view.container.querySelector('[contenteditable="true"], .workflow-slot__text-editor')).toBeNull();
  expect(screen.queryByRole('button', { name: '编辑文本' })).not.toBeInTheDocument();
});

it.each(['panel', 'widget', 'unselected', 'descriptor'] as const)('keeps %s read-only PPT prompts out of editing', (reason) => {
  const current = { ...slot, selected: reason !== 'unselected', ...(reason === 'descriptor' ? {
    artifact_id: 'historical-prompt', document: { representation: 'markdown', editable: false, capabilities: [] },
  } : {}) } as SlotRevision;
  const { container } = render(<SlotRenderer slot={current} sessionId='ppt-editor' readOnly={reason === 'panel'}
    widget={{ widgetType: 'ppt-prompt', readOnly: reason === 'widget' }} />);
  expect(screen.queryByRole('button', { name: '编辑文本' })).not.toBeInTheDocument();
  expect(container.querySelector('[contenteditable="true"], .workflow-slot__text-editor')).toBeNull();
});
