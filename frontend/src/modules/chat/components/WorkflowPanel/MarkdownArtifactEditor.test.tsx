import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@mdxeditor/editor', async () => {
  const React = await import('react');
  const { flushSync } = await import('react-dom');
  const emptyPlugin = () => ({});
  const EmptyControl = () => null;
  const MDXEditor = React.forwardRef((props: Record<string, unknown>, ref) => {
    const markdownRef = React.useRef(String(props.markdown ?? ''));
    const surfaceRef = React.useRef<HTMLDivElement>(null);
    const [renderedMarkdown, setRenderedMarkdown] = React.useState(markdownRef.current);
    React.useImperativeHandle(ref, () => ({
      getMarkdown: () => markdownRef.current,
      setMarkdown: (markdown: string) => {
        markdownRef.current = markdown;
        if (surfaceRef.current) surfaceRef.current.scrollTop = 0;
        flushSync(() => setRenderedMarkdown(markdown));
      },
    }));
    const plugins = props.plugins as Array<{ toolbarContents?: () => React.ReactNode }>;
    const toolbar = plugins.find((plugin) => plugin.toolbarContents)?.toolbarContents?.();
    const hasInternalReference = renderedMarkdown.includes('[beta](#block-sec-1)');
    return (
      <div className={String(props.className ?? '')} ref={surfaceRef}>
        <div className='mdxeditor-toolbar'>{toolbar}</div>
        <div className='mdxeditor-root-contenteditable'>
          <div contentEditable suppressContentEditableWarning>
            <p>
              {'Alpha '}
              {hasInternalReference ? <a href='#block-sec-1'>beta</a> : 'beta'}
              {' gamma'}
            </p>
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
  DisconnectOutlined: () => null,
  DownOutlined: () => null,
  FontSizeOutlined: () => null,
  HighlightOutlined: () => null,
  LinkOutlined: () => null,
  MenuFoldOutlined: () => null,
  MenuUnfoldOutlined: () => null,
  PictureOutlined: () => null,
}));

vi.mock('antd', () => ({
  Dropdown: ({
    children,
    menu,
    open,
    onOpenChange,
  }: {
    children: React.ReactNode;
    menu?: {
      items?: Array<{ key: string; label: React.ReactNode }>;
      onClick?: (info: { key: string }) => void;
    };
    open?: boolean;
    onOpenChange?: (open: boolean) => void;
  }) => (
    <div data-testid='reference-dropdown' onClick={() => onOpenChange?.(!open)}>
      {children}
      {open && (
        <div className='writer-markdown-editor__reference-dropdown' role='menu'>
          {menu?.items?.map((item) => (
            <button
              type='button'
              role='menuitem'
              key={item.key}
              onClick={(event) => {
                event.stopPropagation();
                menu.onClick?.({ key: item.key });
                onOpenChange?.(false);
              }}
            >
              {item.label}
            </button>
          ))}
        </div>
      )}
    </div>
  ),
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

const rangeBoundingRectDescriptor = Object.getOwnPropertyDescriptor(
  window.Range.prototype,
  'getBoundingClientRect',
);
const rangeClientRectsDescriptor = Object.getOwnPropertyDescriptor(
  window.Range.prototype,
  'getClientRects',
);

beforeEach(() => {
  Object.defineProperty(window.Range.prototype, 'getBoundingClientRect', {
    configurable: true,
    value: () => rect(),
  });
  Object.defineProperty(window.Range.prototype, 'getClientRects', {
    configurable: true,
    value: () => [rect()],
  });
});

function Harness() {
  const [rewriteOpen, setRewriteOpen] = useState(false);
  return (
    <>
      <button type='button' onClick={() => setRewriteOpen(false)}>close rewrite</button>
      <MarkdownArtifactEditor
        markdown={'Alpha [beta](#block-sec-1) gamma\n\n<a id="block-sec-1"></a>\n## 1 Target'}
        sourceRevision={1}
        onSave={async () => 1}
        onRewriteSelection={() => setRewriteOpen(true)}
        rewriteDialogOpen={rewriteOpen}
      />
    </>
  );
}

function ReferenceHarness({ onSave }: { onSave: (markdown: string, revision: number) => Promise<number> }) {
  const [source, setSource] = useState({
    markdown: 'Alpha [beta](#block-sec-1) gamma\n\n<a id="block-sec-1"></a>\n## 1 Target',
    revision: 7,
  });
  return (
    <MarkdownArtifactEditor
      markdown={source.markdown}
      sourceRevision={source.revision}
      onSave={async (markdown, revision) => {
        const savedRevision = await onSave(markdown, revision);
        setSource({ markdown, revision: savedRevision });
        return savedRevision;
      }}
    />
  );
}

function ImageReferenceHarness({
  onSave,
}: {
  onSave: (markdown: string, revision: number) => Promise<number>;
}) {
  return (
    <MarkdownArtifactEditor
      markdown={[
        'Alpha beta gamma',
        '',
        '<a id="block-image-1"></a>',
        '![图1 雨后山间溪流图](https://example.com/rain.png)',
      ].join('\n')}
      sourceRevision={11}
      onSave={onSave}
    />
  );
}

afterEach(() => {
  window.getSelection()?.removeAllRanges();
  vi.restoreAllMocks();
  if (rangeBoundingRectDescriptor) {
    Object.defineProperty(
      window.Range.prototype,
      'getBoundingClientRect',
      rangeBoundingRectDescriptor,
    );
  } else {
    Reflect.deleteProperty(window.Range.prototype, 'getBoundingClientRect');
  }
  if (rangeClientRectsDescriptor) {
    Object.defineProperty(window.Range.prototype, 'getClientRects', rangeClientRectsDescriptor);
  } else {
    Reflect.deleteProperty(window.Range.prototype, 'getClientRects');
  }
});

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
    expect((polish as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(polish);

    await waitFor(() => {
      expect(screen.getByTestId('rewrite-selection-highlight').getAttribute('data-active')).toBe('true');
    });

    fireEvent.click(screen.getByRole('button', { name: 'close rewrite' }));
    await waitFor(() => {
      expect(screen.getByTestId('rewrite-selection-highlight').getAttribute('data-active')).toBe('false');
    });
  });

  it('reports the controlled reference dropdown expanded state', async () => {
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

    const referenceTrigger = await screen.findByTitle('chat.writerIR.crossReference');
    expect((referenceTrigger as HTMLButtonElement).disabled).toBe(false);
    expect(referenceTrigger.getAttribute('aria-expanded')).toBe('false');

    fireEvent.mouseDown(referenceTrigger);
    fireEvent.click(referenceTrigger);
    expect(referenceTrigger.getAttribute('aria-expanded')).toBe('true');

    fireEvent.click(referenceTrigger);
    expect(referenceTrigger.getAttribute('aria-expanded')).toBe('false');
  });

  it('applies and saves a cross-reference to an anchored image', async () => {
    const onSave = vi.fn(async () => 12);
    const { container } = render(<ImageReferenceHarness onSave={onSave} />);
    const paragraph = container.querySelector('p');
    const textNode = paragraph?.firstChild;
    expect(paragraph).not.toBeNull();
    expect(textNode).not.toBeNull();

    const range = document.createRange();
    range.setStart(textNode!, 0);
    range.setEnd(textNode!, 5);
    const browserSelection = window.getSelection();
    browserSelection?.removeAllRanges();
    browserSelection?.addRange(range);
    fireEvent.mouseUp(paragraph!);

    const referenceTrigger = await screen.findByTitle('chat.writerIR.crossReference');
    fireEvent.mouseDown(referenceTrigger);
    fireEvent.click(referenceTrigger);
    fireEvent.click(screen.getByTitle('图1 雨后山间溪流图'));

    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave).toHaveBeenCalledWith(
      [
        '[Alpha](#block-image-1) beta gamma',
        '',
        '<a id="block-image-1"></a>',
        '![图1 雨后山间溪流图](https://example.com/rain.png)',
      ].join('\n'),
      11,
    );
  });

  it('removes an internal reference and saves the unchanged visible wording', async () => {
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      callback(0);
      return 0;
    });
    const onSave = vi.fn(async () => 8);
    const { container } = render(<ReferenceHarness onSave={onSave} />);
    const surface = container.querySelector<HTMLElement>('.writer-markdown-editor__surface');
    const reference = container.querySelector<HTMLAnchorElement>('p a[href="#block-sec-1"]');
    const textNode = reference?.firstChild;
    expect(surface).not.toBeNull();
    expect(reference).not.toBeNull();
    expect(textNode).not.toBeNull();
    surface!.scrollTop = 64;

    const range = document.createRange();
    range.setStart(textNode!, 0);
    range.setEnd(textNode!, 4);
    Object.defineProperty(range, 'getBoundingClientRect', { value: () => rect() });
    Object.defineProperty(range, 'getClientRects', { value: () => [rect()] });
    const browserSelection = window.getSelection();
    browserSelection?.removeAllRanges();
    browserSelection?.addRange(range);
    fireEvent.mouseUp(reference!);

    const scrollTo = vi.fn();
    Object.defineProperty(surface!, 'scrollTo', { value: scrollTo });
    fireEvent.click(reference!);
    expect(scrollTo).not.toHaveBeenCalled();

    const remove = await screen.findByTitle('chat.writerIR.removeCrossReference');
    expect((remove as HTMLButtonElement).disabled).toBe(false);
    expect((screen.getByTitle('chat.writerIR.crossReference') as HTMLButtonElement).disabled)
      .toBe(true);
    fireEvent.mouseDown(remove);
    fireEvent.click(remove);

    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave).toHaveBeenCalledWith(
      'Alpha beta gamma\n\n<a id="block-sec-1"></a>\n## 1 Target',
      7,
    );
    await waitFor(() => {
      const restoredSelection = window.getSelection();
      expect(restoredSelection?.toString()).toBe('beta');
      expect(container.contains(restoredSelection?.anchorNode ?? null)).toBe(true);
      expect(container.contains(restoredSelection?.focusNode ?? null)).toBe(true);
    });
    expect(container.querySelector('p a[href="#block-sec-1"]')).toBeNull();
    expect(surface!.scrollTop).toBe(64);
  });
});
