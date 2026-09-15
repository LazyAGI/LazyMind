import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@mdxeditor/editor', async () => {
  const React = await import('react');
  const { flushSync } = await import('react-dom');
  const emptyPlugin = () => ({});
  const EmptyControl = () => null;
  const FormattingControl = () => <button type='button' title='format-bold'>B</button>;
  const MDXEditor = React.forwardRef((props: Record<string, unknown>, ref) => {
    const markdownRef = React.useRef(String(props.markdown ?? ''));
    const surfaceRef = React.useRef<HTMLDivElement>(null);
    const [renderedMarkdown, setRenderedMarkdown] = React.useState(markdownRef.current);
    React.useImperativeHandle(ref, () => ({
      getMarkdown: () => markdownRef.current,
      setMarkdown: (markdown: string) => {
        markdownRef.current = markdown;
        if (surfaceRef.current) {
          surfaceRef.current.dataset.markdown = markdown;
          surfaceRef.current.scrollTop = 0;
        }
        const update = () => setRenderedMarkdown(markdown);
        if (globalThis.getSelection()?.rangeCount) flushSync(update);
        else update();
      },
    }));
    const plugins = props.plugins as Array<{ toolbarContents?: () => React.ReactNode }>;
    const toolbar = plugins.find((plugin) => plugin.toolbarContents)?.toolbarContents?.();
    const hasInternalReference = renderedMarkdown.includes('[beta](#block-sec-1)');
    const hasSourceReference = renderedMarkdown.includes('[1](#source-4.1)');
    React.useEffect(() => {
      if (renderedMarkdown.includes('<!-- unsupported -->')) {
        (props.onError as ((payload: { error: string; source: string }) => void) | undefined)?.({
          error: 'MDX parse error',
          source: renderedMarkdown,
        });
      }
    }, [props.onError, renderedMarkdown]);
    return (
      <div
        className={String(props.className ?? '')}
        data-markdown={renderedMarkdown}
        ref={surfaceRef}
      >
        <div className='mdxeditor-toolbar'>{toolbar}</div>
        <div className='mdxeditor-root-contenteditable'>
          <div
            contentEditable
            data-testid='markdown-editable'
            suppressContentEditableWarning
            onInput={(event) => {
              const nextMarkdown = event.currentTarget.textContent ?? '';
              markdownRef.current = nextMarkdown;
              (props.onChange as ((markdown: string) => void) | undefined)?.(nextMarkdown);
            }}
          >
            <p>
              {'Alpha '}
              {hasInternalReference ? <a href='#block-sec-1'>beta</a> : 'beta'}
              {' gamma'}
            </p>
            <a href='https://example.com'>External link</a>
            {hasSourceReference && <a href='#source-4.1'>1</a>}
            <span id='block-sec-1'>Target</span>
          </div>
        </div>
      </div>
    );
  });
  return {
    BlockTypeSelect: EmptyControl,
    BoldItalicUnderlineToggles: FormattingControl,
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
  CommentOutlined: () => null,
  DisconnectOutlined: () => null,
  DownOutlined: () => null,
  FontSizeOutlined: () => null,
  HighlightOutlined: () => null,
  LinkOutlined: () => null,
  MenuFoldOutlined: () => null,
  MenuUnfoldOutlined: () => null,
  MoreOutlined: () => null,
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
  initReactI18next: { type: '3rdParty', init: () => undefined },
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
import { SlotEditingContext } from './slotEditingContext';

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
    markdown: '**Alpha** [beta](#block-sec-1) gamma\n\n<a id="block-sec-1"></a>\n## 1 Target',
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

function BackendUpdateHarness() {
  const [source, setSource] = useState({ markdown: 'Initial draft', revision: 7 });
  return (
    <>
      <button
        type='button'
        onClick={() => setSource({ markdown: 'Backend replacement', revision: 8 })}
      >
        update backend
      </button>
      <MarkdownArtifactEditor
        markdown={source.markdown}
        sourceRevision={source.revision}
        onSave={async () => source.revision}
      />
    </>
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

describe('MarkdownArtifactEditor MDX compatibility', () => {
  it('renders PDF text without passing HTML page comments to the MDX parser', () => {
    const { container } = render(
      <MarkdownArtifactEditor
        markdown={'<!-- 第 1 页 -->\n\n招标文件正文'}
        sourceRevision={1}
        onSave={async () => 1}
      />,
    );

    expect(
      container.querySelector<HTMLElement>('.writer-markdown-editor__surface')?.dataset.markdown,
    ).toBe('<a id="writer-page-marker-1" />\n\n招标文件正文');
  });

  it('shows the original text when the MDX parser rejects a document', async () => {
    render(
      <MarkdownArtifactEditor
        markdown={'<!-- unsupported -->\n\n正文仍需可见'}
        sourceRevision={1}
        onSave={async () => 1}
      />,
    );

    const fallback = await screen.findByRole('alert');
    expect(fallback).toHaveTextContent('chat.writerMarkdown.renderFallback');
    expect(fallback).toHaveTextContent('正文仍需可见');
  });

  it('renders outline controls only when the document has a heading', () => {
    const withoutHeading = render(
      <MarkdownArtifactEditor
        markdown='正文内容'
        sourceRevision={1}
        onSave={async () => 1}
      />,
    );

    expect(withoutHeading.container.querySelector('.writer-markdown-editor'))
      .toHaveClass('writer-markdown-editor--no-outline');
    expect(
      screen.queryByRole('button', { name: 'chat.writerIR.expandOutline' }),
    ).not.toBeInTheDocument();
    expect(screen.queryByText('chat.writerIR.noHeadings')).not.toBeInTheDocument();

    withoutHeading.unmount();
    render(
      <MarkdownArtifactEditor
        markdown='# 文档标题'
        sourceRevision={1}
        onSave={async () => 1}
      />,
    );

    expect(
      screen.getByRole('button', { name: 'chat.writerIR.expandOutline' }),
    ).toBeInTheDocument();
  });

  it('renders plain-text JSON braces without changing inline or fenced code', () => {
    const markdown = [
      '配图：{"reference_image_index": 0}',
      '',
      '行内代码：`{"keep": true}`',
      '',
      '```json',
      '{"keep": true}',
      '```',
    ].join('\n');

    const { container } = render(
      <MarkdownArtifactEditor
        markdown={markdown}
        sourceRevision={1}
        onSave={async () => 1}
      />,
    );

    expect(
      container.querySelector<HTMLElement>('.writer-markdown-editor__surface')?.dataset.markdown,
    ).toBe([
      '配图：\\{"reference_image_index": 0\\}',
      '',
      '行内代码：`{"keep": true}`',
      '',
      '```json',
      '{"keep": true}',
      '```',
    ].join('\n'));
  });
});

describe('MarkdownArtifactEditor rewrite selection highlight', () => {
  it('navigates internal references without opening the link editor', () => {
    const { container } = render(<Harness />);
    const surface = container.querySelector<HTMLElement>('.writer-markdown-editor__surface');
    const editableRoot = container.querySelector<HTMLElement>('.mdxeditor-root-contenteditable');
    const internalLink = container.querySelector<HTMLAnchorElement>('a[href^="#block-"]');
    const externalLink = container.querySelector<HTMLAnchorElement>('a[href^="https://"]');
    const linkEditorClick = vi.fn();
    const linkEditorMouseDown = vi.fn();
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
    editableRoot!.addEventListener('mousedown', linkEditorMouseDown);

    const internalMouseDown = new MouseEvent('mousedown', { bubbles: true, cancelable: true });
    internalLink!.dispatchEvent(internalMouseDown);

    expect(internalMouseDown.defaultPrevented).toBe(true);
    expect(linkEditorMouseDown).not.toHaveBeenCalled();

    const internalClick = new MouseEvent('click', { bubbles: true, cancelable: true });
    internalLink!.dispatchEvent(internalClick);

    expect(internalClick.defaultPrevented).toBe(true);
    expect(linkEditorClick).not.toHaveBeenCalled();
    expect(scrollTo).toHaveBeenCalledTimes(1);

    externalLink!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true }));
    externalLink!.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
    expect(linkEditorMouseDown).toHaveBeenCalledTimes(1);
    expect(linkEditorClick).toHaveBeenCalledTimes(1);
  });

  it('renders chat source references as non-editable badges and opens their source', () => {
    const onOpenSourceReference = vi.fn();
    const { container } = render(
      <MarkdownArtifactEditor
        markdown='Evidence [1](#source-4.1)'
        sourceRevision={1}
        presentation='chat'
        onSave={async () => 1}
        onOpenSourceReference={onOpenSourceReference}
        sourceReferences={[{
          citationId: '4.1',
          faviconUrl: 'https://www.google.com/s2/favicons?domain=docs.python.org&sz=64',
          href: 'https://docs.python.org/3/',
          label: 'docs.python.org',
          title: 'Python documentation',
        }]}
      />,
    );
    const editableRoot = container.querySelector<HTMLElement>('.mdxeditor-root-contenteditable');
    const sourceLink = container.querySelector<HTMLAnchorElement>('a[href="#source-4.1"]');
    const linkEditorClick = vi.fn();

    expect(editableRoot).not.toBeNull();
    expect(sourceLink).not.toBeNull();
    expect(sourceLink).toHaveAttribute('data-writer-source-citation', 'true');
    expect(sourceLink).toHaveAttribute('contenteditable', 'false');
    expect(sourceLink).toHaveAttribute('role', 'button');
    expect(sourceLink).toHaveAttribute('tabindex', '0');
    expect(sourceLink).toHaveAttribute('data-writer-source-label', 'docs.python.org');
    expect(sourceLink).toHaveAttribute('data-writer-source-initial', 'D');
    expect(sourceLink).toHaveAttribute('data-writer-source-has-icon', 'true');
    expect(sourceLink).toHaveAttribute('aria-label', 'chat.references docs.python.org');
    expect(sourceLink).not.toHaveAttribute('title');
    expect(sourceLink?.style.getPropertyValue('--writer-source-icon'))
      .toContain('docs.python.org');
    editableRoot!.addEventListener('click', linkEditorClick);

    fireEvent.mouseOver(sourceLink!);
    expect(screen.getByRole('tooltip')).toHaveTextContent('Python documentation');
    expect(screen.getByRole('tooltip')).toHaveTextContent('https://docs.python.org/3/');

    fireEvent.mouseOut(sourceLink!, { relatedTarget: editableRoot });
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument();

    const click = new MouseEvent('click', { bubbles: true, cancelable: true });
    sourceLink!.dispatchEvent(click);

    expect(click.defaultPrevented).toBe(true);
    expect(linkEditorClick).not.toHaveBeenCalled();
    expect(onOpenSourceReference).toHaveBeenCalledWith('4.1');

    fireEvent.keyDown(sourceLink!, { key: 'Enter' });
    expect(onOpenSourceReference).toHaveBeenCalledTimes(2);
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
    polish.focus();
    browserSelection?.removeAllRanges();
    document.dispatchEvent(new Event('selectionchange'));
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

  it('keeps the editor selection state during toolbar mousedown', async () => {
    const { container } = render(<Harness />);
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

    const polish = await screen.findByTitle('chat.artifactRewrite.action');
    const bold = await screen.findByTitle('format-bold');
    expect((polish as HTMLButtonElement).disabled).toBe(false);

    fireEvent.mouseDown(bold);
    browserSelection?.removeAllRanges();
    document.dispatchEvent(new Event('selectionchange'));

    expect((polish as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(bold);
  });

  it('positions the selection toolbar in the scrollable editor surface', async () => {
    const { container } = render(<Harness />);
    const editor = container.querySelector<HTMLElement>('.writer-markdown-editor');
    const surface = container.querySelector<HTMLElement>('.writer-markdown-editor__surface');
    const toolbar = container.querySelector<HTMLElement>('.mdxeditor-toolbar');
    const paragraph = container.querySelector('p');
    const textNode = paragraph?.firstChild;
    expect(editor).not.toBeNull();
    expect(surface).not.toBeNull();
    expect(toolbar).not.toBeNull();
    expect(textNode).not.toBeNull();

    vi.spyOn(surface!, 'getBoundingClientRect').mockReturnValue({
      ...rect(),
      top: 80,
      right: 800,
      bottom: 500,
      left: 300,
      width: 500,
      height: 420,
    });
    Object.defineProperty(surface!, 'clientTop', { configurable: true, value: 3 });
    Object.defineProperty(surface!, 'clientLeft', { configurable: true, value: 2 });
    Object.defineProperty(surface!, 'scrollTop', { configurable: true, value: 20 });
    Object.defineProperty(surface!, 'scrollLeft', { configurable: true, value: 5 });
    vi.spyOn(toolbar!, 'getBoundingClientRect').mockReturnValue({
      ...rect(),
      top: 0,
      right: 320,
      bottom: 42,
      left: 0,
      width: 320,
      height: 42,
    });

    const range = document.createRange();
    range.setStart(textNode!, 0);
    range.setEnd(textNode!, 5);
    const selectedRect = {
      ...rect(),
      top: 200,
      right: 740,
      bottom: 220,
      left: 620,
      width: 120,
      height: 20,
    };
    Object.defineProperty(range, 'getBoundingClientRect', { value: () => selectedRect });
    Object.defineProperty(range, 'getClientRects', { value: () => [selectedRect] });
    const browserSelection = window.getSelection();
    browserSelection?.removeAllRanges();
    browserSelection?.addRange(range);
    fireEvent.mouseUp(paragraph!);

    await waitFor(() => {
      expect(editor!.style.getPropertyValue('--writer-markdown-selection-toolbar-top'))
        .toBe('87px');
      expect(editor!.style.getPropertyValue('--writer-markdown-selection-toolbar-left'))
        .toBe('175px');
      expect(editor!.style.getPropertyValue('--writer-markdown-selection-toolbar-max-width'))
        .toBe('484px');
    });
  });

  it('offers the chat citation action inside the selection toolbar', async () => {
    const onCiteSelection = vi.fn();
    const { container } = render(
      <MarkdownArtifactEditor
        markdown='Alpha beta gamma'
        sourceRevision={1}
        presentation='chat'
        onSave={async () => 1}
        onCiteSelection={onCiteSelection}
      />,
    );
    const paragraph = container.querySelector('p');
    const textNode = paragraph?.firstChild;
    expect(textNode).not.toBeNull();

    const range = document.createRange();
    range.setStart(textNode!, 0);
    range.setEnd(textNode!, 5);
    const browserSelection = window.getSelection();
    browserSelection?.removeAllRanges();
    browserSelection?.addRange(range);
    fireEvent.mouseUp(paragraph!);

    fireEvent.click(await screen.findByTitle('chat.cite'));
    expect(onCiteSelection).toHaveBeenCalledWith('Alpha');
  });

  it('selects a whole chat paragraph after hovering its leading gutter for one second', () => {
    vi.useFakeTimers();
    try {
      const { container } = render(
        <MarkdownArtifactEditor
          markdown='Alpha beta gamma'
          sourceRevision={1}
          presentation='chat'
          onSave={async () => 1}
        />,
      );
      const editor = container.querySelector<HTMLElement>('.writer-markdown-editor');
      const editable = screen.getByTestId('markdown-editable');
      const paragraph = container.querySelector<HTMLElement>('p');
      expect(editor).not.toBeNull();
      expect(paragraph).not.toBeNull();
      vi.spyOn(editable, 'getBoundingClientRect').mockReturnValue({
        ...rect(),
        left: 80,
        right: 500,
        width: 420,
      });
      vi.spyOn(paragraph!, 'getBoundingClientRect').mockReturnValue({
        ...rect(),
        left: 100,
        right: 500,
        width: 400,
      });

      fireEvent.mouseMove(editor!, { clientX: 90, clientY: 110 });
      act(() => vi.advanceTimersByTime(999));
      expect(window.getSelection()?.toString()).toBe('');
      act(() => vi.advanceTimersByTime(1));
      expect(window.getSelection()?.toString()).toContain('Alpha beta gamma');
    } finally {
      vi.useRealTimers();
    }
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

  it('keeps overflow actions available from the compact toolbar menu', async () => {
    const { container } = render(<Harness />);
    const paragraph = container.querySelector('p');
    const textNode = paragraph?.firstChild;
    expect(textNode).not.toBeNull();

    const range = document.createRange();
    range.setStart(textNode!, 0);
    range.setEnd(textNode!, 5);
    const browserSelection = window.getSelection();
    browserSelection?.removeAllRanges();
    browserSelection?.addRange(range);
    fireEvent.mouseUp(paragraph!);

    const moreActions = await screen.findByTitle('chat.writerMarkdown.moreActions');
    expect(moreActions).toHaveAttribute('aria-expanded', 'false');
    fireEvent.mouseDown(moreActions);
    fireEvent.click(moreActions);
    expect(moreActions).toHaveAttribute('aria-expanded', 'true');
    const overflowMenu = screen.getByRole('menu', { hidden: true });
    expect(overflowMenu).toHaveTextContent('chat.artifactRewrite.action');
    expect(overflowMenu).toHaveTextContent('chat.writerIR.crossReference');
    expect(overflowMenu).toHaveTextContent('chat.writerIR.removeCrossReference');
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
      'draft',
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
      '**Alpha** beta gamma\n\n<a id="block-sec-1"></a>\n## 1 Target',
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

describe('MarkdownArtifactEditor autosave', () => {
  it('uses a checkpoint when pending edits are flushed at a version boundary', async () => {
    const onSave = vi.fn(async () => 8);
    let flush: (() => Promise<boolean>) | undefined;
    render(
      <SlotEditingContext.Provider value={{
        setEditing: vi.fn(),
        registerFlush: (_key, callback) => {
          flush = callback;
          return () => undefined;
        },
        registerFooterAction: () => () => undefined,
      }}>
        <MarkdownArtifactEditor
          markdown='Initial draft'
          sourceRevision={7}
          editingKey='writer:document'
          onSave={onSave}
        />
      </SlotEditingContext.Provider>,
    );
    const editable = screen.getByTestId('markdown-editable');
    editable.textContent = 'Checkpoint edit';
    fireEvent.input(editable);

    await waitFor(() => expect(flush).toBeDefined());
    await act(async () => {
      expect(await flush?.()).toBe(true);
    });

    expect(onSave).toHaveBeenCalledWith('Checkpoint edit', 7, 'checkpoint');
  });

  it('replaces clean backend updates without remounting or moving the viewport', async () => {
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      callback(0);
      return 0;
    });
    const { container } = render(<BackendUpdateHarness />);
    const surface = container.querySelector<HTMLElement>('.writer-markdown-editor__surface');
    const editable = screen.getByTestId('markdown-editable');
    expect(surface).not.toBeNull();
    surface!.scrollTop = 180;
    editable.focus();
    window.getSelection()?.removeAllRanges();

    fireEvent.click(screen.getByRole('button', { name: 'update backend' }));

    await waitFor(() => {
      expect(surface?.dataset.markdown).toBe('Backend replacement');
    });
    expect(container.querySelector('.writer-markdown-editor__surface')).toBe(surface);
    expect(surface!.scrollTop).toBe(180);
    expect(document.activeElement).toBe(editable);
    expect(screen.queryByText('chat.writerMarkdown.externalUpdate')).toBeNull();
  });

  it('adopts changed content returned by the save without moving the viewport', async () => {
    vi.useFakeTimers();
    try {
      vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
        callback(0);
        return 0;
      });
      let resolveSave: ((result: { markdown: string; revision: number }) => void) | undefined;
      const onSave = vi.fn(() => new Promise<{ markdown: string; revision: number }>((resolve) => {
        resolveSave = resolve;
      }));
      const { container } = render(
        <MarkdownArtifactEditor
          markdown='Initial draft'
          sourceRevision={7}
          onSave={onSave}
        />,
      );
      const surface = container.querySelector<HTMLElement>('.writer-markdown-editor__surface');
      const editable = screen.getByTestId('markdown-editable');
      surface!.scrollTop = 140;
      editable.focus();
      window.getSelection()?.removeAllRanges();
      editable.textContent = 'Local draft';
      fireEvent.input(editable);

      await act(async () => {
        vi.advanceTimersByTime(1_000);
      });
      expect(onSave).toHaveBeenCalledWith('Local draft', 7, 'draft');
      await act(async () => {
        resolveSave?.({ markdown: 'Backend normalized draft', revision: 8 });
        await Promise.resolve();
      });
      const currentSurface = container.querySelector<HTMLElement>('.writer-markdown-editor__surface');
      expect(currentSurface?.dataset.markdown).toBe('Backend normalized draft');
      expect(currentSurface).toBe(surface);
      expect(currentSurface!.scrollTop).toBe(140);
      expect(document.activeElement).toBe(editable);
      expect(screen.queryByText('chat.writerMarkdown.externalUpdate')).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it('silently saves only after input has been idle for one second', async () => {
    vi.useFakeTimers();
    try {
      const onSave = vi.fn(async () => 8);
      render(
        <MarkdownArtifactEditor
          markdown='Initial draft'
          sourceRevision={7}
          onSave={onSave}
        />,
      );
      const editable = screen.getByTestId('markdown-editable');

      await act(async () => {
        vi.advanceTimersByTime(2_000);
      });
      expect(onSave).not.toHaveBeenCalled();

      editable.textContent = 'First edit';
      fireEvent.input(editable);
      await act(async () => {
        vi.advanceTimersByTime(700);
      });
      editable.textContent = 'Final edit';
      fireEvent.input(editable);
      await act(async () => {
        vi.advanceTimersByTime(999);
      });
      expect(onSave).not.toHaveBeenCalled();

      await act(async () => {
        vi.advanceTimersByTime(1);
        await Promise.resolve();
      });
      expect(onSave).toHaveBeenCalledTimes(1);
      expect(onSave).toHaveBeenCalledWith('Final edit', 7, 'draft');
      expect(screen.queryByText('chat.writerMarkdown.saved')).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it('keeps edits made during a save and persists them in a follow-up request', async () => {
    vi.useFakeTimers();
    try {
      let resolveFirstSave: ((revision: number) => void) | undefined;
      const onSave = vi.fn((markdown: string) => (
        markdown === 'First edit'
          ? new Promise<number>((resolve) => { resolveFirstSave = resolve; })
          : Promise.resolve(9)
      ));
      render(
        <MarkdownArtifactEditor
          markdown='Initial draft'
          sourceRevision={7}
          onSave={onSave}
        />,
      );
      const editable = screen.getByTestId('markdown-editable');

      editable.textContent = 'First edit';
      fireEvent.input(editable);
      await act(async () => {
        vi.advanceTimersByTime(1_000);
      });
      expect(onSave).toHaveBeenCalledWith('First edit', 7, 'draft');

      editable.textContent = 'Second edit';
      fireEvent.input(editable);
      await act(async () => {
        resolveFirstSave?.(8);
        await Promise.resolve();
      });
      await act(async () => {
        vi.advanceTimersByTime(999);
      });
      expect(onSave).toHaveBeenCalledTimes(1);

      await act(async () => {
        vi.advanceTimersByTime(1);
        await Promise.resolve();
      });
      expect(onSave).toHaveBeenCalledTimes(2);
      expect(onSave).toHaveBeenLastCalledWith('Second edit', 8, 'draft');
    } finally {
      vi.useRealTimers();
    }
  });
});
