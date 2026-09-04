import { createRef } from 'react';
import { render, waitFor } from '@testing-library/react';
import {
  GenericJsxEditor,
  MDXEditor,
  headingsPlugin,
  imagePlugin,
  jsxPlugin,
  type MDXEditorMethods,
} from '@mdxeditor/editor';
import { describe, expect, it } from 'vitest';
import {
  protectWriterMarkdownHeadingAnchors,
  writerMarkdownForEditing,
  writerMarkdownForSave,
} from './writerMarkdownAnchors';

describe('Writer Markdown real MDXEditor round trip', () => {
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
