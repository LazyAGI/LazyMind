import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { useState } from 'react';
import i18n from '@/i18n';
import { WriterIRControl } from './WriterIRControl';
import { WriterIRDocumentEditor } from './WriterIRDocumentEditor';
import type { WriterDocument } from './writerIR';
vi.mock('../MarkdownViewer/MermaidBlock', () => ({ default: ({ code }: { code: string }) => <pre>{code}</pre> }));
const original: WriterDocument = { document_id: 'fixture', title: '混合内容', stage: 'draft', ui_editable: true, blocks: [
  { node_id: 'first', type: 'paragraph', content: 'Before x^2 after.', spans: [{ text: 'Before ' }, { text: 'x^2', style: { math_source: true, 'fixture:metadata': 'keep' } }, { text: ' after.' }] },
  { node_id: 'math', type: 'math', content: 'a^2', references: [{ type: 'fixture', value: 'keep' }] },
  { node_id: 'chart', type: 'code', language: 'mermaid', content: 'graph TD; A-->B' },
  { node_id: 'last', type: 'paragraph', content: 'Last paragraph' },
] };
beforeEach(async () => { await i18n.changeLanguage('zh-CN'); });
it('edits inline and block formulas without serializing their controls, preserving metadata and the live editor', async () => {
  let latest = original;
  function Fixture() {
    const [value, setValue] = useState(original);
    return <WriterIRDocumentEditor document={value} ariaLabel='Document' onFocus={() => {}} onBlur={() => {}}
      onChange={next => { latest = next; setValue(next); }} />;
  }
  const { container } = render(<Fixture />);
  const editor = screen.getByRole('textbox', { name: 'Document' });
  fireEvent.click(screen.getAllByRole('button', { name: '编辑公式' })[0]);
  fireEvent.change(screen.getByRole('textbox', { name: '公式表达式' }), { target: { value: 'z^3' } });
  expect(latest.blocks[0].content).toBe('Before z^3 after.');
  expect(latest.blocks[0].spans?.[1].style).toMatchObject({ math_source: true, 'fixture:metadata': 'keep' });
  fireEvent.blur(screen.getByRole('textbox', { name: '公式表达式' }), { relatedTarget: editor });
  fireEvent.click(screen.getAllByRole('button', { name: '编辑公式' })[1]);
  fireEvent.change(screen.getByRole('textbox', { name: '公式表达式' }), { target: { value: 'b^2' } });
  const last = container.querySelector('[data-node-id="last"] [data-writer-block-content]')!;
  last.textContent = 'Edited elsewhere'; fireEvent.input(last);
  expect(latest.blocks.map(block => block.content)).toEqual(['Before z^3 after.', 'b^2', 'graph TD; A-->B', 'Edited elsewhere']);
  expect(latest.blocks[1].references).toEqual(original.blocks[1].references);
  expect(screen.getByRole('textbox', { name: 'Document' })).toBe(editor);
});
it('includes local formula changes in document undo, redo and autosave', async () => {
  const save = vi.fn(async (_text, document, sourceRevision) => ({ document, sourceRevision: Number(sourceRevision) + 1 }));
  render(<WriterIRControl document={original} sourceRevision={1} onSave={save} />);
  fireEvent.click(screen.getAllByRole('button', { name: '编辑公式' })[0]);
  const input = screen.getByRole('textbox', { name: '公式表达式' });
  fireEvent.change(input, { target: { value: 'y^9' } });
  fireEvent.keyDown(input, { key: 'z', ctrlKey: true });
  fireEvent.click(screen.getAllByRole('button', { name: '编辑公式' })[0]);
  expect(screen.getByRole('textbox', { name: '公式表达式' })).toHaveValue('x^2');
  fireEvent.keyDown(screen.getByRole('textbox', { name: '公式表达式' }), { key: 'z', ctrlKey: true, shiftKey: true });
  await waitFor(() => expect(save).toHaveBeenCalled(), { timeout: 2500 });
  expect(save.mock.calls[0][1].blocks[0].content).toBe('Before y^9 after.');
  await act(async () => {});
});
