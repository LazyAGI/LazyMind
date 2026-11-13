import { act, fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { WriterDocumentOptions } from './WriterDocumentOptions';
import { WriterIRControl } from './WriterIRControl';
import { useWriterPageWidth } from './useWriterPageWidth';

beforeEach(async () => {
  localStorage.clear();
  await i18n.changeLanguage('zh-CN');
});
function setup() {
  function Harness() {
    const [width, setWidth] = useWriterPageWidth();
    return <><div role='button' tabIndex={0} aria-label='内容项'><WriterDocumentOptions width={width} onWidth={setWidth} /></div><button>菜单外</button><p>菜单外空白区</p></>;
  }
  const rendered = render(<Harness />);
  const menu = rendered.container.querySelector('details')!;
  const summary = menu.querySelector('summary')!;
  act(() => { menu.open = true; fireEvent(menu, new Event('toggle')); });
  act(() => summary.focus());
  return { ...rendered, menu, summary };
}

it('keeps the menu mounted through the temporary focus gap when clicking a width label', () => {
  const { menu, summary } = setup();
  const radio = screen.getByRole('radio', { name: '变窄' });
  expect(menu.contains(radio)).toBe(false);
  expect(document.body.contains(radio)).toBe(true);
  // A label click can blur the summary before the browser focuses its radio.
  fireEvent.blur(summary, { relatedTarget: null });
  expect(menu.open).toBe(true);
  act(() => radio.focus());
  fireEvent.click(screen.getByText('变窄', { exact: true }));
  expect(radio).toBeChecked();
  expect(screen.getByText('变窄', { exact: true })).toHaveAttribute('title', '适合公众号阅读体验');
  expect(radio).toHaveFocus();
  expect(menu.open).toBe(true);
});

it('closes after focus settles outside, without stealing focus back', () => {
  const { menu, summary } = setup();
  const outside = screen.getByRole('button', { name: '菜单外' });
  fireEvent.blur(summary, { relatedTarget: outside });
  expect(menu.open).toBe(true);
  act(() => outside.focus());
  expect(menu.open).toBe(false);
  expect(outside).toHaveFocus();
});

it('keeps the menu open when a label press temporarily focuses the containing content item', () => {
  const { menu } = setup();
  const label = screen.getByText('变窄', { exact: true });
  fireEvent.pointerDown(label);
  act(() => screen.getByRole('button', { name: '内容项' }).focus());
  expect(menu.open).toBe(true);
  fireEvent.pointerUp(label);
  fireEvent.click(label);
  expect(screen.getByRole('radio', { name: '变窄' })).toBeChecked();
  expect(menu.open).toBe(true);
  act(() => screen.getByRole('button', { name: '菜单外' }).focus());
  expect(menu.open).toBe(false);
});

it('keeps Escape immediate and returns focus to the menu trigger', () => {
  const { menu, summary } = setup();
  const radio = screen.getByRole('radio', { name: '默认' });
  act(() => radio.focus());
  fireEvent.keyDown(radio, { key: 'Escape' });
  expect(menu.open).toBe(false);
  expect(summary).toHaveFocus();
});

it('closes when clicking a non-focusable area outside the menu', () => {
  const { menu } = setup();
  fireEvent.pointerDown(screen.getByText('菜单外空白区'));
  expect(menu.open).toBe(false);
});

it('switches all three IR widths without rewriting the document or saving it', () => {
  const onSave = vi.fn(), onDocumentChange = vi.fn();
  const { container } = render(<WriterIRControl document={{ document_id: 'width-test', stage: 'final', title: '草稿',
    blocks: [{ node_id: 'paragraph', type: 'paragraph', content: '这段正文在宽度切换前后保持不变。' }],
  }} onSave={onSave} onDocumentChange={onDocumentChange} />);
  const details = container.querySelector('details.writer-document-options')!;
  details.setAttribute('open', '');
  fireEvent(details, new Event('toggle'));
  const editor = screen.getByRole('textbox', { name: '结构化文档' });
  const markup = editor.innerHTML;
  onDocumentChange.mockClear();
  for (const [label, width] of [['变窄', 'reading'], ['加宽', 'wide'], ['默认', 'default']] as const) {
    fireEvent.click(screen.getByRole('radio', { name: label }));
    expect(container.querySelector('.writer-ir')).toHaveClass(`writer-ir--width-${width}`);
    expect(editor.innerHTML).toBe(markup);
  }
  expect(onDocumentChange).not.toHaveBeenCalled();
  expect(onSave).not.toHaveBeenCalled();
});

it('restores the chosen width when opening another document', () => {
  const first = setup();
  fireEvent.click(screen.getByRole('radio', { name: '变窄' }));
  expect(first.menu.open).toBe(true);
  first.unmount();
  const next = render(<WriterIRControl document={{ document_id: 'another-document', stage: 'final', title: '草稿', blocks: [] }} />);
  expect(next.container.querySelector('.writer-ir')).toHaveClass('writer-ir--width-reading');
  const details = next.container.querySelector('details.writer-document-options')!;
  details.setAttribute('open', '');
  fireEvent(details, new Event('toggle'));
  expect(screen.getByRole('radio', { name: '变窄' })).toBeChecked();
  expect(screen.getByRole('group', { name: '正文宽度' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '查看源码' })).toBeInTheDocument();
});
