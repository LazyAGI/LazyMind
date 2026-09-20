import { createRef } from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { MDXEditor, codeBlockPlugin, headingsPlugin, quotePlugin, type MDXEditorMethods } from '@mdxeditor/editor';
import { writerLocalCodeEditor, writerLocalSourcePlugin } from './writerLocalSourcePlugin';
vi.mock('../MarkdownViewer/MermaidBlock', () => ({ default: ({ code }: { code: string }) => <pre>{code}</pre> }));
vi.mock('react-i18next', async (importOriginal) => ({ ...await importOriginal<typeof import('react-i18next')>(), useTranslation: () => ({ t: (key: string, options?: { kind?: string }) => options?.kind ? `${key} ${options.kind}` : key }) }));

function mount(markdown: string, readOnly = false) {
  const ref = createRef<MDXEditorMethods>();
  const onError = vi.fn();
  const view = render(<MDXEditor ref={ref} markdown={markdown} readOnly={readOnly} suppressHtmlProcessing onError={onError}
    plugins={[headingsPlugin(), quotePlugin(), writerLocalSourcePlugin(), codeBlockPlugin({ codeBlockEditorDescriptors: [writerLocalCodeEditor] })]} />);
  return { ...view, ref, onError };
}

describe('local special content editing', () => {
  it('edits formulas and Mermaid inside the same mounted document and exports their source', async () => {
    const { ref, container, onError } = mount('Before\n\n$$\nx^2\n$$\n\n```mermaid\ngraph TD; A-->B\n```\n\nAfter');
    expect(onError).not.toHaveBeenCalled();
    const root = container.querySelector('[contenteditable=true]');
    fireEvent.click(screen.getByRole('button', { name: 'chat.writerLocal.edit chat.writerLocal.math' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'chat.writerLocal.input chat.writerLocal.math' }), { target: { value: 'y^3' } });
    await waitFor(() => expect(ref.current?.getMarkdown()).toContain('y^3'));
    fireEvent.click(screen.getByRole('button', { name: 'chat.writerLocal.edit chat.writerLocal.mermaid' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'chat.writerLocal.input chat.writerLocal.mermaid' }), { target: { value: 'graph TD; B-->C' } });
    await waitFor(() => expect(ref.current?.getMarkdown()).toContain('B-->C'));
    expect(ref.current?.getMarkdown()).toContain('Before');
    expect(ref.current?.getMarkdown()).toContain('After');
    expect(container.querySelector('[contenteditable=true]')).toBe(root);
  });
  it('keeps inline formulas inline and retains invalid expressions', async () => {
    const { ref, onError } = mount('Before $x^2$ after.');
    expect(onError).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'chat.writerLocal.edit chat.writerLocal.math' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'chat.writerLocal.input chat.writerLocal.math' }), { target: { value: '\\badcommand{' } });
    await waitFor(() => expect(ref.current?.getMarkdown()).toContain('$\\badcommand{$'));
    await waitFor(() => expect(screen.getByText('chat.writerLocal.mathError')).toBeInTheDocument());
    fireEvent.keyDown(screen.getByRole('textbox', { name: 'chat.writerLocal.input chat.writerLocal.math' }), { key: 'Escape' });
    expect(screen.queryByRole('textbox', { name: 'chat.writerLocal.input chat.writerLocal.math' })).not.toBeInTheDocument();
  });
  it('preserves HTML without enabling executable content', async () => {
    const { ref, onError } = mount('Before\n\n<video src="https://example.test/v.mp4"></video>\n\nAfter');
    expect(onError).not.toHaveBeenCalled();
    expect(ref.current?.getMarkdown()).toContain('<video src="https://example.test/v.mp4"></video>');
    await act(async () => {});
  });
  it('renders protected source blocks without losing adjacent editable paragraphs', () => {
    const { ref, onError } = mount('Before\n\n> [!NOTE]\n> Details\n\nAfter');
    expect(onError).not.toHaveBeenCalled();
    expect(ref.current?.getMarkdown()).toContain('Details');
    expect(screen.getByRole('button', { name: 'chat.writerLocal.edit chat.writerLocal.source' })).toBeInTheDocument();
  });
  it('respects a read-only document', () => {
    const { onError } = mount('Before $x^2$ after.', true);
    expect(onError).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: /chat.writerLocal.edit/ })).not.toBeInTheDocument();
  });
});
