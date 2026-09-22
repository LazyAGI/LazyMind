import { createHash, webcrypto } from 'node:crypto';
import { createRef } from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { MDXEditor, codeBlockPlugin, codeMirrorPlugin, type MDXEditorMethods } from '@mdxeditor/editor';
import { useWriterLocalDisplayHints, WriterCodeDisplayContext } from './writerLocalDisplayHints';
import { writerLocalCodeEditor } from './writerLocalSourcePlugin';
vi.mock('../MarkdownViewer/MermaidBlock', () => ({ default: ({ code }: { code: string }) => <pre>{code}</pre> }));
vi.mock('react-i18next', async original => ({ ...await original<typeof import('react-i18next')>(), useTranslation: () => ({ t: (key: string) => key }) }));
afterEach(() => vi.unstubAllGlobals());
it('keeps duplicate display hints and image sizes separate without rewriting source, including while a hinted fence is edited', async () => {
  vi.stubGlobal('crypto', webcrypto);
  const fence = '```text\ngraph LR; A-->B\n```';
  const picture = '![image](https://example.test/image.png)';
  const source = `${fence}\n\n${fence}\n\n${picture}\n\n${picture}`;
  const imagesStart = 2 * fence.length + 4;
  const context = { source_hash: createHash('sha256').update(source).digest('hex'),
    code_fences: [{ start: 0, end: fence.length, language: 'mermaid' }, { start: fence.length + 2, end: 2 * fence.length + 2, language: 'mermaid' }],
    images: [{ start: imagesStart, end: imagesStart + picture.length, width: 100 }, { start: imagesStart + picture.length + 2, end: source.length, width: 300 }] };
  const ref = createRef<MDXEditorMethods>();
  const root = createRef<HTMLDivElement>();
  const resolve = async (src: string) => src;
  function Fixture() {
    const hints = useWriterLocalDisplayHints(root, source, context, resolve);
    return <div ref={root}><WriterCodeDisplayContext.Provider value={hints}>
      <MDXEditor ref={ref} markdown={`${fence}\n\n${fence}`} plugins={[codeBlockPlugin({ codeBlockEditorDescriptors: [writerLocalCodeEditor] }), codeMirrorPlugin({ codeBlockLanguages: { text: 'Text' } })]} />
      <img src='https://example.test/image.png' alt='First' /><img src='https://example.test/image.png' alt='Second' />
    </WriterCodeDisplayContext.Provider></div>;
  }
  render(<Fixture />);
  await waitFor(() => expect(screen.getAllByRole('button', { name: 'chat.writerLocal.edit' })).toHaveLength(2));
  await waitFor(() => expect(screen.getByAltText('First')).toHaveStyle({ width: '100px' }));
  expect(screen.getByAltText('Second')).toHaveStyle({ width: '300px' });
  fireEvent.click(screen.getAllByRole('button', { name: 'chat.writerLocal.edit' })[1]);
  const input = screen.getByRole('textbox', { name: 'chat.writerLocal.input' });
  fireEvent.change(input, { target: { value: 'graph LR; B-->C' } });
  await act(async () => {});
  expect(screen.getByRole('textbox', { name: 'chat.writerLocal.input' })).toBe(input);
  expect(ref.current?.getMarkdown()).toContain('```text\ngraph LR; B-->C');
  expect(ref.current?.getMarkdown()).not.toContain('```mermaid');
});
