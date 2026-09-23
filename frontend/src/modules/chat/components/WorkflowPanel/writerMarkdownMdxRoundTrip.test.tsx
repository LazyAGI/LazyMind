import { createRef } from 'react';
import { act, render, waitFor } from '@testing-library/react';
import {
  GenericJsxEditor,
  MDXEditor,
  createRootEditorSubscription$,
  headingsPlugin,
  imagePlugin,
  jsxPlugin,
  realmPlugin,
  type MDXEditorMethods,
} from '@mdxeditor/editor';
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest';
import {
  $getRoot,
  $isElementNode,
  $isTextNode,
  COMMAND_PRIORITY_LOW,
  CONTROLLED_TEXT_INSERTION_COMMAND,
  DELETE_CHARACTER_COMMAND,
  DELETE_LINE_COMMAND,
  DELETE_WORD_COMMAND,
  INSERT_PARAGRAPH_COMMAND,
  type LexicalEditor,
} from 'lexical';
import { writerEmptyHeadingPlugin } from './writerEmptyHeadingPlugin';
import {
  protectWriterMarkdownHeadingAnchors,
  writerMarkdownForEditing,
  writerMarkdownForSave,
} from './writerMarkdownAnchors';

const captureEditorPlugin = realmPlugin<{ onEditor: (editor: LexicalEditor) => void }>({
  init(realm, params) {
    realm.pub(createRootEditorSubscription$, (editor) => {
      params?.onEditor(editor);
      return () => undefined;
    });
  },
});

const rangeRectDescriptor = Object.getOwnPropertyDescriptor(Range.prototype, 'getBoundingClientRect');
beforeAll(() => {
  Object.defineProperty(Range.prototype, 'getBoundingClientRect', {
    configurable: true,
    value: () => new DOMRect(),
  });
});
afterAll(() => {
  if (rangeRectDescriptor) Object.defineProperty(Range.prototype, 'getBoundingClientRect', rangeRectDescriptor);
  else Reflect.deleteProperty(Range.prototype, 'getBoundingClientRect');
});

describe('Writer Markdown real MDXEditor round trip', () => {
  it.each([1, 2, 3, 4, 5, 6])('keeps an H%i when its entire text is deleted, then supports typing and Enter', async (level) => {
    let editor!: LexicalEditor;
    const { container } = render(
      <MDXEditor
        markdown={`${'#'.repeat(level)} Title\n\nBody`}
        plugins={[
          headingsPlugin(),
          writerEmptyHeadingPlugin(),
          captureEditorPlugin({ onEditor: (value) => { editor = value; } }),
        ]}
      />,
    );
    await waitFor(() => expect(editor).toBeDefined());
    act(() => {
      editor.update(() => {
        const heading = $getRoot().getFirstChild();
        const text = $isElementNode(heading) ? heading.getFirstChild() : null;
        if (!$isTextNode(text)) throw new Error('Expected heading text');
        text.select(0, text.getTextContentSize());
      }, { discrete: true });
      editor.dispatchCommand(DELETE_CHARACTER_COMMAND, true);
    });
    await waitFor(() => expect(container.querySelector(`h${level}`)?.textContent).toBe(''));
    act(() => { editor.dispatchCommand(CONTROLLED_TEXT_INSERTION_COMMAND, 'New title'); });
    await waitFor(() => expect(container.querySelector(`h${level}`)?.textContent).toBe('New title'));
    act(() => { editor.dispatchCommand(INSERT_PARAGRAPH_COMMAND, undefined); });
    await waitFor(() => expect(container.querySelectorAll('p')).toHaveLength(2));
  });

  it.each([1, 2, 3, 4, 5, 6])('passes further Backspace in an empty H%i to normal deletion', async (level) => {
    let editor!: LexicalEditor;
    const { container } = render(<MDXEditor
      markdown={`${'#'.repeat(level)} Title\n\nBody`}
      plugins={[headingsPlugin(), writerEmptyHeadingPlugin(),
        captureEditorPlugin({ onEditor: (value) => { editor = value; } })]} />);
    await waitFor(() => expect(editor).toBeDefined());
    act(() => {
      editor.update(() => {
        const heading = $getRoot().getFirstChild();
        const text = $isElementNode(heading) ? heading.getFirstChild() : null;
        if (!$isTextNode(text)) throw new Error('Expected heading text');
        text.select(0, text.getTextContentSize());
      }, { discrete: true });
      editor.dispatchCommand(DELETE_CHARACTER_COMMAND, true);
    });
    await waitFor(() => expect(container.querySelector(`h${level}`)?.textContent).toBe(''));
    // Observe command fallthrough: jsdom cannot perform native Selection.modify.
    // The real browser covers deleting the block and continuing in the preceding paragraph.
    const normalDeletion = vi.fn(() => true);
    const unregister = editor.registerCommand(DELETE_CHARACTER_COMMAND, normalDeletion, COMMAND_PRIORITY_LOW);
    act(() => { editor.dispatchCommand(DELETE_CHARACTER_COMMAND, true); });
    expect(normalDeletion).toHaveBeenCalledOnce();
    unregister();
  });

  it.each([
    ['Backspace', DELETE_CHARACTER_COMMAND, true],
    ['Delete', DELETE_CHARACTER_COMMAND, false],
    ['word deletion', DELETE_WORD_COMMAND, true],
    ['line deletion', DELETE_LINE_COMMAND, true],
  ] as const)('does not swallow %s in a saved empty trailing heading', async (_label, command, backward) => {
    let editor!: LexicalEditor;
    render(<MDXEditor markdown={'Previous paragraph\n\n##'}
      plugins={[headingsPlugin(), writerEmptyHeadingPlugin(),
        captureEditorPlugin({ onEditor: (value) => { editor = value; } })]} />);
    await waitFor(() => expect(editor).toBeDefined());
    const normalDeletion = vi.fn(() => true);
    const unregister = editor.registerCommand(command, normalDeletion, COMMAND_PRIORITY_LOW);
    act(() => {
      editor.update(() => {
        const heading = $getRoot().getLastChild();
        if (!$isElementNode(heading)) throw new Error('Expected heading');
        heading.selectEnd();
      }, { discrete: true });
      editor.dispatchCommand(command, backward);
    });
    expect(normalDeletion).toHaveBeenCalledOnce();
    unregister();
  });

  it('round-trips empty headings with their original level and numbering metadata', async () => {
    const source = '#\n\n<a id="block-sec-1" numbering="restart"></a>\n##\n\n<a id="block-sec-2"></a>\n### Next';
    const editorRef = createRef<MDXEditorMethods>();
    const { container } = render(
      <MDXEditor ref={editorRef} markdown={writerMarkdownForEditing(source)} plugins={[headingsPlugin()]} />,
    );

    await waitFor(() => expect(editorRef.current).not.toBeNull());
    expect(container.querySelector('h1')?.textContent).toBe('');
    expect(container.querySelector('h2')?.textContent).toBe('');
    expect(writerMarkdownForSave(protectWriterMarkdownHeadingAnchors(
      source, editorRef.current!.getMarkdown(),
    ))).toBe(source);
  });

  it('keeps image and following heading ids out of MDXEditor and stable on save', async () => {
    const source = [
      '# 标题',
      '',
      '[因果链](#block-IMAGE-1)',
      '',
      '<a id="block-IMAGE-1"></a>',
      '![恐惧递进因果链](/data/chain.jpg)',
      '',
      '<a id="block-sec-002-002"></a>',
      '### 不可名状的征兆',
    ].join('\n');
    const editable = writerMarkdownForEditing(source);
    const editorRef = createRef<MDXEditorMethods>();

    render(
      <MDXEditor
        ref={editorRef}
        markdown={editable}
        plugins={[
          headingsPlugin(),
          jsxPlugin({
            jsxComponentDescriptors: [{
              name: 'a',
              kind: 'flow',
              props: [{ name: 'id', type: 'string' }],
              hasChildren: true,
              Editor: GenericJsxEditor,
            }],
          }),
          imagePlugin(),
        ]}
      />,
    );

    await waitFor(() => expect(editorRef.current).not.toBeNull());
    const serialized = editorRef.current?.getMarkdown() ?? editable;
    expect(serialized).not.toContain('<a id=');

    const saved = writerMarkdownForSave(
      protectWriterMarkdownHeadingAnchors(source, serialized),
    );
    expect(saved).toContain('<a id="block-IMAGE-1"></a>');
    expect(saved).toMatch(
      /<a id="block-sec-002-002"><\/a>\n+### 不可名状的征兆/,
    );
    expect(saved).not.toContain('block-user-');
  });
});
