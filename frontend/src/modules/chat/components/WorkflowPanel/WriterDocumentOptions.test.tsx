import { useState } from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { WriterDocumentOptions } from './WriterDocumentOptions';
import { WriterIRControl } from './WriterIRControl';

beforeEach(async () => {
  await i18n.changeLanguage('zh-CN');
});
function setup() {
  function Harness() {
    const [width, setWidth] = useState<'default' | 'wide' | 'reading'>('default');
    return <><div role='button' tabIndex={0} aria-label='内容项'><WriterDocumentOptions width={width} onWidth={setWidth} /></div><button>菜单外</button><p>菜单外空白区</p></>;
  }
  const rendered = render(<Harness />);
  const menu = rendered.container.querySelector('details')!;
  const summary = menu.querySelector('summary')!;
  menu.open = true;
  act(() => summary.focus());
  return { ...rendered, menu, summary };
}

it('keeps the menu mounted through the temporary focus gap when clicking a width label', () => {
  const { menu, summary } = setup();
  const radio = screen.getByRole('radio', { name: '公众号阅读宽度' });
  // A label click can blur the summary before the browser focuses its radio.
  fireEvent.blur(summary, { relatedTarget: null });
  expect(menu.open).toBe(true);
  act(() => radio.focus());
  fireEvent.click(screen.getByText('公众号阅读宽度', { exact: true }));
  expect(radio).toBeChecked();
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
  const label = screen.getByText('公众号阅读宽度', { exact: true });
  fireEvent.pointerDown(label);
  act(() => screen.getByRole('button', { name: '内容项' }).focus());
  expect(menu.open).toBe(true);
  fireEvent.pointerUp(label);
  fireEvent.click(label);
  expect(screen.getByRole('radio', { name: '公众号阅读宽度' })).toBeChecked();
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
  container.querySelector('details.writer-document-options')!.setAttribute('open', '');
  const editor = screen.getByRole('textbox', { name: '结构化文档' });
  const markup = editor.innerHTML;
  onDocumentChange.mockClear();
  for (const [label, width] of [['公众号阅读宽度', 'reading'], ['加宽', 'wide'], ['默认', 'default']]) {
    fireEvent.click(screen.getByRole('radio', { name: label, exact: true }));
    expect(container.querySelector('.writer-ir')).toHaveClass(`writer-ir--width-${width}`);
    expect(editor.innerHTML).toBe(markup);
  }
  expect(onDocumentChange).not.toHaveBeenCalled();
  expect(onSave).not.toHaveBeenCalled();
});
