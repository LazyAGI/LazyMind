import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';

vi.mock('@mdxeditor/editor', async () => {
  const React = await import('react');
  const emptyPlugin = () => ({});
  const EmptyControl = () => null;
  const MDXEditor = React.forwardRef((props: Record<string, unknown>, ref) => {
    React.useImperativeHandle(ref, () => ({
      getMarkdown: () => 'Alpha beta gamma',
      setMarkdown: () => undefined,
    }));
    const plugins = props.plugins as Array<{ toolbarContents?: () => React.ReactNode }>;
    const toolbar = plugins.find((plugin) => plugin.toolbarContents)?.toolbarContents?.();
    return (
      <div className={String(props.className ?? '')}>
        <div className='mdxeditor-toolbar'>{toolbar}</div>
        <div className='mdxeditor-root-contenteditable'>
          <div contentEditable suppressContentEditableWarning>
            <p>Alpha beta gamma</p>
            <a href='#block-sec-1'>Chapter 1</a>
            <a href='https://example.com'>External link</a>
            <span id='block-sec-1'>Target</span>
          </div>
        </div>
      </div>
    );
  });
  return {
    BlockTypeSelect: EmptyControl,
    BoldItalicUnderlineToggles: EmptyControl,
    ListsToggle: EmptyControl,
    MDXEditor,
    GenericJsxEditor: EmptyControl,
    codeBlockPlugin: emptyPlugin,
    codeMirrorPlugin: emptyPlugin,
    frontmatterPlugin: emptyPlugin,
    headingsPlugin: emptyPlugin,
    imagePlugin: emptyPlugin,
    jsxPlugin: emptyPlugin,
    linkDialogPlugin: emptyPlugin,
    linkPlugin: emptyPlugin,
    listsPlugin: emptyPlugin,
    markdownShortcutPlugin: emptyPlugin,
    quotePlugin: emptyPlugin,
    tablePlugin: emptyPlugin,
    thematicBreakPlugin: emptyPlugin,
    toolbarPlugin: ({ toolbarContents }: { toolbarContents: () => React.ReactNode }) => ({
      toolbarContents,
    }),
  };
});

vi.mock('@ant-design/icons', () => ({
  DownOutlined: () => null,
  HighlightOutlined: () => null,
  LinkOutlined: () => null,
  MenuFoldOutlined: () => null,
  MenuUnfoldOutlined: () => null,
}));

vi.mock('antd', () => ({
  Dropdown: ({ children }: { children: React.ReactNode }) => children,
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock('./ArtifactRewriteDialog', () => ({
  ArtifactRewriteInlineDiff: () => null,
}));

vi.mock('./ArtifactRewriteSelectionHighlight', () => ({
  ArtifactRewriteSelectionHighlight: ({ active }: { active: boolean }) => (
    <div data-testid='rewrite-selection-highlight' data-active={String(active)} />
  ),
}));

import { MarkdownArtifactEditor } from './MarkdownArtifactEditor';

function rect(): DOMRect {
  return {
    top: 100,
    right: 220,
    bottom: 120,
    left: 100,
    width: 120,
    height: 20,
    x: 100,
    y: 100,
    toJSON: () => ({}),
  } as DOMRect;
}

function Harness() {
  const [rewriteOpen, setRewriteOpen] = useState(false);
  return (
    <>
      <button type='button' onClick={() => setRewriteOpen(false)}>close rewrite</button>
      <MarkdownArtifactEditor
        markdown='Alpha beta gamma'
        sourceRevision={1}
        onSave={async () => 1}
        onRewriteSelection={() => setRewriteOpen(true)}
        rewriteDialogOpen={rewriteOpen}
      />
    </>
  );
}

describe('MarkdownArtifactEditor rewrite selection highlight', () => {
  it('navigates internal references without opening the link editor', () => {
    const { container } = render(<Harness />);
    const surface = container.querySelector<HTMLElement>('.writer-markdown-editor__surface');
    const editableRoot = container.querySelector<HTMLElement>('.mdxeditor-root-contenteditable');
    const internalLink = container.querySelector<HTMLAnchorElement>('a[href^="#block-"]');
    const externalLink = container.querySelector<HTMLAnchorElement>('a[href^="https://"]');
    const linkEditorClick = vi.fn();
    const scrollTo = vi.fn();

    expect(surface).not.toBeNull();
    expect(editableRoot).not.toBeNull();
    expect(internalLink).not.toBeNull();
    expect(externalLink).not.toBeNull();
    Object.defineProperty(surface!, 'scrollTo', { value: scrollTo });
    editableRoot!.addEventListener('click', (event) => {
      event.preventDefault();
      linkEditorClick();
    });

    const internalClick = new MouseEvent('click', { bubbles: true, cancelable: true });
    internalLink!.dispatchEvent(internalClick);

    expect(internalClick.defaultPrevented).toBe(true);
    expect(linkEditorClick).not.toHaveBeenCalled();
    expect(scrollTo).toHaveBeenCalledTimes(1);

    externalLink!.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
    expect(linkEditorClick).toHaveBeenCalledTimes(1);
  });

  it('keeps the selection highlighted while AI polish is open and clears it on close', async () => {
    const { container } = render(<Harness />);
    const paragraph = container.querySelector('p');
    const textNode = paragraph?.firstChild;
    expect(paragraph).not.toBeNull();
    expect(textNode).not.toBeNull();

    const range = document.createRange();
    range.setStart(textNode!, 0);
    range.setEnd(textNode!, 5);
    Object.defineProperty(range, 'getBoundingClientRect', { value: () => rect() });
    Object.defineProperty(range, 'getClientRects', { value: () => [rect()] });
    const browserSelection = window.getSelection();
    browserSelection?.removeAllRanges();
    browserSelection?.addRange(range);
    fireEvent.mouseUp(paragraph!);

    const polish = await screen.findByTitle('chat.artifactRewrite.action');
    expect(polish).toBeEnabled();
    fireEvent.click(polish);

    await waitFor(() => {
      expect(screen.getByTestId('rewrite-selection-highlight')).toHaveAttribute('data-active', 'true');
    });

    fireEvent.click(screen.getByRole('button', { name: 'close rewrite' }));
    await waitFor(() => {
      expect(screen.getByTestId('rewrite-selection-highlight')).toHaveAttribute('data-active', 'false');
    });
  });
});
