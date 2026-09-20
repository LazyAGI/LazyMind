import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@ant-design/icons', () => ({
  BoldOutlined: () => null,
  CodeOutlined: () => null,
  DisconnectOutlined: () => null,
  DownOutlined: () => null,
  FontSizeOutlined: () => null,
  HighlightOutlined: () => null,
  ItalicOutlined: () => null,
  LinkOutlined: () => null,
  OrderedListOutlined: () => null,
  PictureOutlined: () => null,
  TableOutlined: () => null,
  UnorderedListOutlined: () => null,
}));

vi.mock('antd', async () => {
  const React = await import('react');

  function Dropdown({
    children,
    disabled,
    menu,
    onOpenChange,
  }: {
    children: React.ReactElement;
    disabled?: boolean;
    menu: {
      items: Array<{ key: string; label: React.ReactNode }>;
      onClick: (info: { key: string }) => void;
    };
    onOpenChange?: (open: boolean) => void;
  }) {
    const [open, setOpen] = React.useState(false);
    const setMenuOpen = (nextOpen: boolean) => {
      setOpen(nextOpen);
      onOpenChange?.(nextOpen);
    };

    return (
      <>
        {React.cloneElement(children, {
          onClick: () => {
            if (!disabled) setMenuOpen(!open);
          },
        })}
        {open && (
          <div className='writer-ir__reference-dropdown'>
            {menu.items.map((item) => (
              <button
                type='button'
                key={item.key}
                onClick={() => {
                  menu.onClick({ key: item.key });
                  setMenuOpen(false);
                }}
              >
                {item.label}
              </button>
            ))}
          </div>
        )}
      </>
    );
  }

  return { Dropdown };
});

vi.mock('react-i18next', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-i18next')>();
  return {
    ...actual,
    useTranslation: () => ({ t: (key: string) => key }),
  };
});

vi.mock('./ArtifactRewriteDialog', () => ({
  ArtifactRewriteInlineDiff: () => null,
}));

vi.mock('./ArtifactRewriteSelectionHighlight', () => ({
  ArtifactRewriteSelectionHighlight: ({ active }: { active: boolean }) => (
    <div data-testid='selection-highlight' data-active={String(active)} />
  ),
}));

import { WriterIRDocumentEditor } from './WriterIRDocumentEditor';
import * as imageUrl from '@/modules/knowledge/utils/imageUrl';
import {
  getWriterInternalReference,
  writerBlockRangeHasInlineStyle,
  writerBlockRangeSpanColor,
  type WriterDocument,
} from './writerIR';

const document: WriterDocument = {
  document_id: 'writer-doc-1',
  stage: 'draft',
  title: 'Test document',
  blocks: [
    {
      node_id: 'sec-1',
      type: 'heading',
      content: '1. Target section',
      numbering: { level: 1 },
    },
    {
      node_id: 'p-1',
      type: 'paragraph',
      content: 'Alpha beta gamma',
    },
  ],
};

const imageTargetDocument: WriterDocument = {
  ...document,
  blocks: [
    document.blocks[0],
    {
      node_id: 'image-1',
      type: 'image',
      content: '图1 雨后山间溪流图',
    },
    document.blocks[1],
  ],
};

const headingDocument: WriterDocument = {
  document_id: 'writer-doc-heading',
  stage: 'draft',
  title: 'Rain after the storm',
  blocks: [
    {
      node_id: 'heading-1',
      type: 'heading',
      content: '第一章 雨后山林',
      numbering: { level: 1 },
      children: [
        {
          node_id: 'paragraph-existing',
          type: 'paragraph',
          content: '既有正文',
        },
      ],
    },
  ],
};

const outlineInstructionDocument: WriterDocument = {
  document_id: 'writer-outline-instructions',
  stage: 'outline',
  title: '长篇小说大纲',
  blocks: [
    {
      node_id: 'outline-section-1',
      type: 'heading',
      content: '旧日的仪式',
      numbering: { level: 1 },
      target_chars: 1800,
      context_relations: [
        {
          target_node_id: 'outline-section-0',
          relation: 'continuity',
          guidance: '承接前文已暴露的禁忌线索',
        },
      ],
      subtasks: [
        {
          subtask_id: 'subtask-1',
          node_id: 'outline-section-1',
          question: '仪式为什么会在此时失控？',
          subtask_type: 'reason',
          status: 'pending',
        },
      ],
    },
  ],
};

const referencedDocument: WriterDocument = {
  ...document,
  blocks: [
    document.blocks[0],
    {
      node_id: 'p-1',
      type: 'paragraph',
      content: 'Alpha beta gamma',
      spans: [
        {
          text: 'Alpha',
          style: {
            link: {
              type: 'internal_ref',
              target_node_id: 'sec-1',
              display_text: 'Alpha',
            },
          },
        },
        { text: ' beta gamma', style: {} },
      ],
    },
  ],
};

const twiceReferencedDocument: WriterDocument = {
  ...document,
  blocks: [
    document.blocks[0],
    {
      node_id: 'p-1',
      type: 'paragraph',
      content: 'Alpha beta gamma',
      spans: [
        {
          text: 'Alpha',
          style: {
            link: {
              type: 'internal_ref',
              target_node_id: 'sec-1',
              display_text: 'Alpha',
            },
          },
        },
        { text: ' ', style: {} },
        {
          text: 'beta',
          style: {
            link: {
              type: 'internal_ref',
              target_node_id: 'sec-1',
              display_text: 'beta',
            },
          },
        },
        { text: ' gamma', style: {} },
      ],
    },
  ],
};

const originalRangeBoundingRect = Object.getOwnPropertyDescriptor(
  Range.prototype,
  'getBoundingClientRect',
);
const originalRangeClientRects = Object.getOwnPropertyDescriptor(
  Range.prototype,
  'getClientRects',
);
const originalScrollIntoView = Object.getOwnPropertyDescriptor(
  HTMLElement.prototype,
  'scrollIntoView',
);

function restoreProperty(
  target: object,
  property: PropertyKey,
  descriptor: PropertyDescriptor | undefined,
) {
  if (descriptor) {
    Object.defineProperty(target, property, descriptor);
    return;
  }
  Reflect.deleteProperty(target, property);
}

function selectionRect(): DOMRect {
  return {
    top: 100,
    right: 160,
    bottom: 120,
    left: 100,
    width: 60,
    height: 20,
    x: 100,
    y: 100,
    toJSON: () => ({}),
  } as DOMRect;
}

function placeCaret(contentElement: HTMLElement, offset: number) {
  const textNode = contentElement.firstChild;
  expect(textNode).not.toBeNull();
  const range = window.document.createRange();
  range.setStart(textNode!, offset);
  range.setEnd(textNode!, offset);
  Object.defineProperty(range, 'getBoundingClientRect', { value: selectionRect });
  Object.defineProperty(range, 'getClientRects', { value: () => [selectionRect()] });
  const selection = window.getSelection();
  selection?.removeAllRanges();
  selection?.addRange(range);
}

function selectionBlockId(): string | undefined {
  const anchorNode = window.getSelection()?.anchorNode;
  const anchorElement = anchorNode instanceof HTMLElement
    ? anchorNode
    : anchorNode?.parentElement;
  return anchorElement?.closest<HTMLElement>('[data-writer-block]')?.dataset.nodeId;
}

function ControlledWriter({
  initialDocument,
  onDocumentChange,
}: {
  initialDocument: WriterDocument;
  onDocumentChange: (document: WriterDocument) => void;
}) {
  const [currentDocument, setCurrentDocument] = useState(initialDocument);
  return (
    <div data-testid='outer-scroll' style={{ height: 400, overflowY: 'auto' }}>
      <div data-testid='writer-scroll' style={{ height: 240, overflowY: 'auto' }}>
        <WriterIRDocumentEditor
          document={currentDocument}
          ariaLabel='Writer document'
          onChange={(nextDocument) => {
            onDocumentChange(nextDocument);
            setCurrentDocument(nextDocument);
          }}
          onFocus={vi.fn()}
          onBlur={vi.fn()}
        />
      </div>
    </div>
  );
}

beforeEach(() => {
  Object.defineProperty(Range.prototype, 'getBoundingClientRect', {
    configurable: true,
    value: selectionRect,
  });
  Object.defineProperty(Range.prototype, 'getClientRects', {
    configurable: true,
    value: () => [selectionRect()],
  });
});

afterEach(() => {
  restoreProperty(Range.prototype, 'getBoundingClientRect', originalRangeBoundingRect);
  restoreProperty(Range.prototype, 'getClientRects', originalRangeClientRects);
  restoreProperty(HTMLElement.prototype, 'scrollIntoView', originalScrollIntoView);
  window.getSelection()?.removeAllRanges();
  vi.restoreAllMocks();
});

describe('WriterIRDocumentEditor image previews', () => {
  it.each(['before', 'after'] as const)('keeps images visible when the first URL resolves %s numbering arrives', async (timing) => {
    let resolveFirst!: (url: string) => void;
    vi.spyOn(imageUrl, 'resolveMarkdownImageUrlAsync')
      .mockImplementationOnce(() => new Promise<string>((resolve) => { resolveFirst = resolve; }))
      .mockResolvedValue('/static-files/test-image.png?version=2');
    const imageDocument: WriterDocument = {
      ...document,
      blocks: [...document.blocks, {
        node_id: 'image-preview', type: 'image', content: 'Test image',
        references: [{ type: 'media_asset', id: 'test-image', path: '/var/lib/lazymind/uploads/test-image.png' }],
      }],
    };
    const onChange = vi.fn();
    const props = { document: imageDocument, ariaLabel: 'Writer document', onChange, onFocus: vi.fn(), onBlur: vi.fn() };
    const { container, rerender, unmount } = render(<WriterIRDocumentEditor {...props} />);
    const firstImage = container.querySelector('img')!;
    if (timing === 'before') {
      await act(async () => { resolveFirst('/static-files/test-image.png?version=1'); });
      expect(firstImage).toHaveAttribute('src', '/static-files/test-image.png?version=1');
    }

    rerender(<WriterIRDocumentEditor {...props} numbering={{ entries: { 'sec-1': { label: '1.' } } }} />);
    await waitFor(() => {
      expect(container.querySelector('img')).toHaveAttribute('src', '/static-files/test-image.png?version=2');
    });
    if (timing === 'after') {
      await act(async () => { resolveFirst('/static-files/test-image.png?version=1'); });
      expect(firstImage).not.toHaveAttribute('src');
      expect(container.querySelector('img')).toHaveAttribute('src', '/static-files/test-image.png?version=2');
    }
    expect(onChange).not.toHaveBeenCalled();
    unmount();
  });
});

describe('WriterIRDocumentEditor numbering sidecar', () => {
  it('preserves divider structure when editing another block', () => {
    const divider = {
      node_id: 'divider-1',
      type: 'divider',
      content: '---',
      spans: [],
      metadata: { provider_owned: true },
    } satisfies WriterDocument['blocks'][number];
    const dividerDocument: WriterDocument = {
      ...document,
      blocks: [divider, document.blocks[1]],
    };
    const onChange = vi.fn();
    const { container } = render(
      <WriterIRDocumentEditor
        document={dividerDocument}
        ariaLabel='Writer document'
        onChange={onChange}
      />,
    );
    const paragraph = container.querySelector<HTMLElement>(
      '[data-node-id="p-1"] > [data-writer-block-content]',
    )!;
    paragraph.textContent = 'Edited paragraph';
    fireEvent.input(paragraph);

    const updated = onChange.mock.calls.at(-1)?.[0] as WriterDocument;
    expect(updated.blocks[0]).toEqual(divider);
    expect(updated.blocks[1].content).toBe('Edited paragraph');
  });

  it.each([1, 2, 3, 4, 5, 6])('uses the Markdown level %i placeholder without persisting it or changing numbering', (level) => {
    const onChange = vi.fn();
    const emptyDocument: WriterDocument = {
      ...document,
      blocks: [{ node_id: 'empty-heading', type: 'heading', content: '', numbering: { level } }],
    };
    const { container } = render(<WriterIRDocumentEditor document={emptyDocument}
      numbering={{ ordered_style: 'hierarchical', entries: { 'empty-heading': { label: '1.', mode: 'ordered' } } }}
      ariaLabel='Writer document' onChange={onChange} />);
    const heading = container.querySelector<HTMLElement>(`h${level}[data-writer-block-content]`)!;
    expect(heading).toHaveAttribute('data-writer-heading-placeholder', `chat.writerMarkdown.headingPlaceholders.h${level}`);
    expect(heading).toHaveTextContent('1.');
    expect(onChange).not.toHaveBeenCalled();

    const text = window.document.createTextNode('标题');
    heading.append(text);
    fireEvent.input(heading);
    expect(heading).not.toHaveAttribute('data-writer-heading-placeholder');
    expect(onChange.mock.calls.at(-1)?.[0].blocks[0]).toMatchObject({ content: '标题', numbering: { level }, type: 'heading' });

    text.remove();
    fireEvent.input(heading);
    expect(heading).toHaveAttribute('data-writer-heading-placeholder', `chat.writerMarkdown.headingPlaceholders.h${level}`);
    expect(heading.querySelector('[data-writer-numbering-marker]')).toHaveTextContent('1.');
    expect(onChange.mock.calls.at(-1)?.[0].blocks[0]).toMatchObject({ content: '', numbering: { level }, type: 'heading' });
  });

  it('renders an immutable highlighted marker without persisting it in heading content', async () => {
    const onChange = vi.fn();
    const cleanDocument: WriterDocument = {
      ...document,
      blocks: [{ ...document.blocks[0], content: 'Target section' }],
    };
    const { container } = render(
      <WriterIRDocumentEditor
        document={cleanDocument}
        numbering={{
          ordered_style: 'hierarchical',
          entries: { 'sec-1': { label: '1.', mode: 'ordered' } },
        }}
        ariaLabel='Writer document'
        onFocus={vi.fn()}
        onBlur={vi.fn()}
        onChange={onChange}
      />,
    );

    const heading = container.querySelector<HTMLElement>(
      '[data-node-id="sec-1"] > [data-writer-block-content]',
    );
    const marker = heading?.querySelector<HTMLElement>('[data-writer-numbering-marker="sec-1"]');
    expect(marker).toHaveTextContent('1.');
    expect(marker).toHaveAttribute('contenteditable', 'false');
    expect(marker).toHaveClass('writer-ir__numbering-marker');

    const editableText = heading?.cloneNode(true) as HTMLElement;
    editableText.querySelector('[data-writer-numbering-marker]')?.remove();
    expect(editableText).toHaveTextContent('Target section');

    const textNode = Array.from(heading?.childNodes ?? []).find((node) => node.nodeType === Node.TEXT_NODE);
    expect(textNode).toBeDefined();
    textNode!.textContent = 'Renamed section';
    fireEvent.input(heading!);

    await waitFor(() => expect(onChange).toHaveBeenCalled());
    const updated = onChange.mock.calls[onChange.mock.calls.length - 1]?.[0] as WriterDocument;
    expect(updated.blocks[0].content).toBe('Renamed section');
  });
});

describe('WriterIRDocumentEditor image preview', () => {
  it('renders a Notion preview asset URL without requiring a media asset', async () => {
    const previewDocument: WriterDocument = {
      ...document,
      blocks: [{
        node_id: 'notion-image-1',
        type: 'image',
        content: '台风安全示意图',
        references: [{
          type: 'preview_asset',
          provider: 'notion',
          url: 'https://example.com/notion-preview.png',
          expires_at: '2026-09-02T02:00:00Z',
        }],
      }],
    };
    const { container } = render(
      <WriterIRDocumentEditor
        document={previewDocument}
        ariaLabel='Writer document'
        onChange={vi.fn()}
        onFocus={vi.fn()}
        onBlur={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(container.querySelector('img')?.getAttribute('src')).toBe(
        'https://example.com/notion-preview.png',
      );
    });
  });

  it('routes a signed Core preview through the API proxy', async () => {
    const previewDocument: WriterDocument = {
      ...document,
      blocks: [{
        node_id: 'generated-image-1',
        type: 'image',
        content: '深海城市',
        references: [{
          type: 'preview_asset',
          id: 'asset-1',
          url: '/static-files/subagent/user/task/image.jpg?expires=4102444800&sig=test',
        }],
      }],
    };
    const { container } = render(
      <WriterIRDocumentEditor
        document={previewDocument}
        ariaLabel='Writer document'
        onChange={vi.fn()}
        onFocus={vi.fn()}
        onBlur={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(container.querySelector('img')?.getAttribute('src')).toContain(
        '/api/core/static-files/subagent/user/task/image.jpg?expires=4102444800&sig=test',
      );
    });
  });
});

describe('WriterIRDocumentEditor tables', () => {
  it('renders and saves structured table cells', async () => {
    const onDocumentChange = vi.fn();
    const tableDocument: WriterDocument = {
      ...document,
      blocks: [{
        node_id: 'table-1',
        type: 'table',
        content: '指标表',
        children: [{
          node_id: 'row-1',
          type: 'table_row',
          children: [
            {
              node_id: 'cell-1',
              type: 'table_cell',
              content: '指标',
              numbering: { header: true, align: 'center', column_span: 2 },
            },
            { node_id: 'cell-2', type: 'table_cell', content: '100' },
          ],
        }],
      }],
    };
    const { container } = render(
      <ControlledWriter
        initialDocument={tableDocument}
        onDocumentChange={onDocumentChange}
      />,
    );

    const header = container.querySelector<HTMLElement>('[data-node-id="cell-1"]');
    const value = container.querySelector<HTMLElement>('[data-node-id="cell-2"]');
    expect(header?.tagName).toBe('TH');
    expect(header).toHaveAttribute('data-table-align', 'center');
    expect(header).toHaveAttribute('colspan', '2');
    expect(value?.tagName).toBe('TD');
    expect(container.querySelector('.writer-ir__table-caption')).toHaveTextContent('指标表');

    value!.textContent = '200';
    fireEvent.input(value!);

    await waitFor(() => expect(onDocumentChange).toHaveBeenCalled());
    const updated = onDocumentChange.mock.calls.at(-1)?.[0] as WriterDocument;
    expect(updated.blocks[0].children?.[0].children?.[1].content).toBe('200');
    expect(updated.blocks[0].content).toBe('指标表');
    expect(updated.blocks[0].children?.[0].children?.[0].numbering).toEqual({
      header: true, align: 'center', column_span: 2,
    });
    expect(container.querySelector('[data-node-id="cell-2"]')).toHaveTextContent('200');
  });
});

describe('WriterIRDocumentEditor multi-block toolbar', () => {
  const multiDocument: WriterDocument = {
    ...document,
    blocks: [
      document.blocks[0],
      { node_id: 'first', type: 'paragraph', content: '😀 Alpha Alpha' },
      { node_id: 'second', type: 'paragraph', content: 'Beta rest' },
    ],
  };

  function selectAcross(container: HTMLElement) {
    const first = container.querySelector('[data-node-id="first"] [data-writer-block-content]')!;
    const second = container.querySelector('[data-node-id="second"] [data-writer-block-content]')!;
    const range = window.document.createRange();
    const firstText = window.document.createTreeWalker(first, NodeFilter.SHOW_TEXT).nextNode()!;
    const secondText = window.document.createTreeWalker(second, NodeFilter.SHOW_TEXT).nextNode()!;
    range.setStart(firstText, 9); // The second Alpha, after an emoji.
    range.setEnd(secondText, 4);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
    fireEvent.mouseUp(second);
  }

  it('shows the complete toolbar without requiring AI rewrite support and formats the precise range', () => {
    const changed = vi.fn();
    const { container } = render(<ControlledWriter initialDocument={multiDocument} onDocumentChange={changed} />);
    selectAcross(container);
    expect(screen.getByRole('toolbar', { name: 'chat.writerIR.formatToolbar' })).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'chat.writerIR.blockStyle' })).toBeEnabled();
    fireEvent.click(screen.getByRole('button', { name: 'chat.writerIR.bold' }));
    const next = changed.mock.calls[0][0] as WriterDocument;
    expect(writerBlockRangeHasInlineStyle(next.blocks[1], 8, 13, 'strong')).toBe(true);
    expect(writerBlockRangeHasInlineStyle(next.blocks[1], 0, 8, 'strong')).toBe(false);
    expect(writerBlockRangeHasInlineStyle(next.blocks[2], 0, 4, 'strong')).toBe(true);
    expect(writerBlockRangeHasInlineStyle(next.blocks[2], 4, 9, 'strong')).toBe(false);
    expect(window.getSelection()?.getRangeAt(0).startContainer.parentElement?.closest('[data-node-id]')?.getAttribute('data-node-id')).toBe('first');
    expect(window.getSelection()?.getRangeAt(0).endContainer.parentElement?.closest('[data-node-id]')?.getAttribute('data-node-id')).toBe('second');
    expect(screen.getByRole('button', { name: 'chat.writerIR.bold' })).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(screen.getByRole('button', { name: 'chat.writerIR.bold' }));
    const cleared = changed.mock.calls[1][0] as WriterDocument;
    expect(writerBlockRangeHasInlineStyle(cleared.blocks[1], 8, 13, 'strong')).toBe(false);
    expect(writerBlockRangeHasInlineStyle(cleared.blocks[2], 0, 4, 'strong')).toBe(false);
  });

  it('applies a mixed bold selection uniformly instead of inverting each paragraph', () => {
    const changed = vi.fn();
    const initial = { ...multiDocument, blocks: multiDocument.blocks.map(block => block.node_id === 'second'
      ? { ...block, spans: [{ text: block.content!, style: ['strong'] }] } : block) };
    const { container } = render(<ControlledWriter initialDocument={initial} onDocumentChange={changed} />);
    selectAcross(container);
    fireEvent.click(screen.getByRole('button', { name: 'chat.writerIR.bold' }));
    const next = changed.mock.calls[0][0] as WriterDocument;
    expect(writerBlockRangeHasInlineStyle(next.blocks[1], 8, 13, 'strong')).toBe(true);
    expect(writerBlockRangeHasInlineStyle(next.blocks[2], 0, 4, 'strong')).toBe(true);
  });

  it.each(['heading-2', 'ordered-list'])('applies %s to every selected block', (format) => {
    const changed = vi.fn();
    const { container } = render(<ControlledWriter initialDocument={multiDocument} onDocumentChange={changed} />);
    selectAcross(container);
    fireEvent.change(screen.getByRole('combobox', { name: 'chat.writerIR.blockStyle' }), { target: { value: format } });
    const next = changed.mock.calls[0][0] as WriterDocument;
    expect(next.blocks[0]).toEqual(multiDocument.blocks[0]);
    expect(next.blocks.slice(1).map(block => block.type)).toEqual(format === 'heading-2' ? ['heading', 'heading'] : ['list_item', 'list_item']);
    expect(next.blocks.slice(1).map(block => block.content)).toEqual(['😀 Alpha Alpha', 'Beta rest']);
    expect(window.getSelection()?.isCollapsed).toBe(false);
  });

  it('toggles a selected range into a list and back without losing the selection', () => {
    const changed = vi.fn();
    const { container } = render(<ControlledWriter initialDocument={multiDocument} onDocumentChange={changed} />);
    selectAcross(container);
    fireEvent.click(screen.getByRole('button', { name: 'chat.writerIR.orderedList' }));
    expect(changed.mock.calls[0][0].blocks.slice(1).every((block: { type: string }) => block.type === 'list_item')).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'chat.writerIR.orderedList' }));
    expect(changed.mock.calls[1][0].blocks.slice(1).every((block: { type: string }) => block.type === 'paragraph')).toBe(true);
    expect(window.getSelection()?.getRangeAt(0).endContainer.parentElement?.closest('[data-node-id]')?.getAttribute('data-node-id')).toBe('second');
  });

  it('applies and removes a cross-reference across paragraphs without changing their text', () => {
    const changed = vi.fn();
    const { container } = render(<ControlledWriter initialDocument={multiDocument} onDocumentChange={changed} />);
    selectAcross(container);
    fireEvent.click(screen.getByRole('button', { name: 'chat.writerIR.crossReference' }));
    fireEvent.click(screen.getByTitle('1. Target section'));
    const next = changed.mock.calls[0][0] as WriterDocument;
    expect(next.blocks.slice(1).map(block => block.content)).toEqual(['😀 Alpha Alpha', 'Beta rest']);
    for (const block of next.blocks.slice(1)) {
      expect(block.spans?.some(span => getWriterInternalReference(span)?.targetNodeId === 'sec-1')).toBe(true);
    }
    fireEvent.click(screen.getByRole('button', { name: 'chat.writerIR.removeCrossReference' }));
    const cleared = changed.mock.calls[1][0] as WriterDocument;
    expect(cleared.blocks.slice(1).every(block => block.spans?.every(span => !getWriterInternalReference(span)))).toBe(true);
  });

  it('colors only selected text across blocks and restores the default colors', () => {
    const changed = vi.fn();
    const { container } = render(<ControlledWriter initialDocument={multiDocument} onDocumentChange={changed} />);
    selectAcross(container);
    fireEvent.click(screen.getByRole('button', { name: 'chat.writerIR.colorPanel' }));
    fireEvent.click(screen.getAllByRole('button', { name: 'chat.writerIR.colors.red' })[0]);
    const next = changed.mock.calls[0][0] as WriterDocument;
    expect(writerBlockRangeSpanColor(next.blocks[1], 8, 13, 'text_color')).toBeTypeOf('number');
    expect(writerBlockRangeSpanColor(next.blocks[1], 0, 8, 'text_color')).toBeNull();
    expect(writerBlockRangeSpanColor(next.blocks[2], 0, 4, 'text_color')).toBe(writerBlockRangeSpanColor(next.blocks[1], 8, 13, 'text_color'));
    fireEvent.click(screen.getByRole('button', { name: 'chat.writerIR.restoreDefaultColors' }));
    const cleared = changed.mock.calls[1][0] as WriterDocument;
    expect(writerBlockRangeSpanColor(cleared.blocks[1], 8, 13, 'text_color')).toBeNull();
    expect(writerBlockRangeSpanColor(cleared.blocks[2], 0, 4, 'text_color')).toBeNull();
  });

  it('supports keyboard formatting and dismisses the toolbar on outside focus', () => {
    const changed = vi.fn();
    const { container } = render(<ControlledWriter initialDocument={multiDocument} onDocumentChange={changed} />);
    selectAcross(container);
    const editor = screen.getByRole('textbox', { name: 'Writer document' });
    fireEvent.keyDown(editor, { key: 'i', ctrlKey: true });
    const next = changed.mock.calls[0][0] as WriterDocument;
    expect(writerBlockRangeHasInlineStyle(next.blocks[1], 8, 13, 'italic')).toBe(true);
    expect(writerBlockRangeHasInlineStyle(next.blocks[2], 0, 4, 'italic')).toBe(true);
    fireEvent.blur(editor, { relatedTarget: window.document.body });
    expect(screen.queryByRole('toolbar')).not.toBeInTheDocument();
  });

  it('keeps multi-paragraph rewrite in the toolbar and clears it when the selection collapses', () => {
    const rewrite = vi.fn();
    const { container } = render(<WriterIRDocumentEditor document={multiDocument} ariaLabel='Writer document'
      onChange={vi.fn()} onFocus={vi.fn()} onBlur={vi.fn()} onRewriteSelection={rewrite} allowMultipleParagraphs />);
    selectAcross(container);
    const action = screen.getByRole('button', { name: 'chat.artifactRewrite.action' });
    expect(action.closest('[role="toolbar"]')).not.toBeNull();
    fireEvent.click(action);
    expect(rewrite).toHaveBeenCalledWith(expect.objectContaining({ nodeSelections: [
      { node_id: 'first', selected_text: 'Alpha' }, { node_id: 'second', selected_text: 'Beta' },
    ] }));
    const paragraph = container.querySelector<HTMLElement>('[data-node-id="second"] [data-writer-block-content]')!;
    placeCaret(paragraph, 1);
    fireEvent.mouseUp(paragraph);
    expect(screen.queryByRole('toolbar')).not.toBeInTheDocument();
  });

  it.each([false, true])('does not expose formatting for a protected selection (disabled=%s)', (disabled) => {
    const changed = vi.fn();
    const protectedDocument = disabled ? multiDocument : { ...multiDocument, blocks: multiDocument.blocks.map(block =>
      block.node_id === 'second' ? { ...block, editable: false } : block) };
    const { container } = render(<WriterIRDocumentEditor document={protectedDocument} disabled={disabled}
      ariaLabel='Writer document' onChange={changed} onFocus={vi.fn()} onBlur={vi.fn()} />);
    selectAcross(container);
    expect(screen.queryByRole('toolbar')).not.toBeInTheDocument();
    expect(changed).not.toHaveBeenCalled();
  });
});

describe('WriterIRDocumentEditor cross-reference menu', () => {
  it('keeps the selected text highlighted and applies the reference without rewriting it', async () => {
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
      configurable: true,
      value: vi.fn(),
    });
    const onCrossReferenceApplied = vi.fn();
    const { container } = render(
      <WriterIRDocumentEditor
        document={document}
        ariaLabel='Writer document'
        onChange={vi.fn()}
        onCrossReferenceApplied={onCrossReferenceApplied}
        onFocus={vi.fn()}
        onBlur={vi.fn()}
      />,
    );
    const paragraph = container.querySelector<HTMLElement>(
      '[data-node-id="p-1"] [data-writer-block-content]',
    );
    const textNode = paragraph?.firstChild;
    expect(paragraph).not.toBeNull();
    expect(textNode).not.toBeNull();

    const range = window.document.createRange();
    range.setStart(textNode!, 0);
    range.setEnd(textNode!, 5);
    Object.defineProperty(range, 'getBoundingClientRect', { value: selectionRect });
    Object.defineProperty(range, 'getClientRects', { value: () => [selectionRect()] });
    const selection = window.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);
    fireEvent.mouseUp(paragraph!);

    const trigger = await screen.findByRole('button', {
      name: 'chat.writerIR.crossReference',
    });
    expect((trigger as HTMLButtonElement).disabled).toBe(false);
    fireEvent.mouseDown(trigger);
    fireEvent.click(trigger);

    expect(screen.getByTestId('selection-highlight').getAttribute('data-active')).toBe('true');
    fireEvent.click(screen.getByTitle('1. Target section'));

    expect(onCrossReferenceApplied).toHaveBeenCalledTimes(1);
    const updated = onCrossReferenceApplied.mock.calls[0][0] as WriterDocument;
    const paragraphBlock = updated.blocks.find((block) => block.node_id === 'p-1');
    expect(paragraphBlock?.spans?.map((span) => span.text).join('')).toBe('Alpha beta gamma');
    expect(getWriterInternalReference(paragraphBlock?.spans?.[0] ?? { text: '' })).toMatchObject({
      targetNodeId: 'sec-1',
      displayText: 'Alpha',
    });
    await waitFor(() => {
      expect(screen.getByTestId('selection-highlight').getAttribute('data-active')).toBe('false');
    });
  });

  it.each(['1.1', '3.2'])('displays independent heading number %s and preserves the reference target', async (label) => {
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
      configurable: true,
      value: vi.fn(),
    });
    const onCrossReferenceApplied = vi.fn();
    const { container } = render(
      <WriterIRDocumentEditor
        document={{
          ...document,
          blocks: document.blocks.map((block) => block.type === 'heading'
            ? { ...block, content: 'Target section' }
            : block),
        }}
        numbering={{ entries: { 'sec-1': { label } } }}
        ariaLabel='Writer document'
        onChange={vi.fn()}
        onCrossReferenceApplied={onCrossReferenceApplied}
        onFocus={vi.fn()}
        onBlur={vi.fn()}
      />,
    );
    const paragraph = container.querySelector<HTMLElement>(
      '[data-node-id="p-1"] [data-writer-block-content]',
    );
    const textNode = paragraph?.firstChild;
    expect(paragraph).not.toBeNull();
    expect(textNode).not.toBeNull();

    const range = window.document.createRange();
    range.setStart(textNode!, 0);
    range.setEnd(textNode!, 5);
    Object.defineProperty(range, 'getBoundingClientRect', { value: selectionRect });
    Object.defineProperty(range, 'getClientRects', { value: () => [selectionRect()] });
    const selection = window.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);
    fireEvent.mouseUp(paragraph!);

    const trigger = await screen.findByRole('button', {
      name: 'chat.writerIR.crossReference',
    });
    expect((trigger as HTMLButtonElement).disabled).toBe(false);
    fireEvent.mouseDown(trigger);
    fireEvent.click(trigger);

    expect(screen.getByTestId('selection-highlight').getAttribute('data-active')).toBe('true');
    fireEvent.click(screen.getByTitle(`${label} Target section`));

    expect(onCrossReferenceApplied).toHaveBeenCalledTimes(1);
    const updated = onCrossReferenceApplied.mock.calls[0][0] as WriterDocument;
    const paragraphBlock = updated.blocks.find((block) => block.node_id === 'p-1');
    expect(paragraphBlock?.spans?.map((span) => span.text).join('')).toBe('Alpha beta gamma');
    expect(getWriterInternalReference(paragraphBlock?.spans?.[0] ?? { text: '' })).toMatchObject({
      targetNodeId: 'sec-1',
      displayText: 'Alpha',
    });
    await waitFor(() => {
      expect(screen.getByTestId('selection-highlight').getAttribute('data-active')).toBe('false');
    });
  });

  it('applies a cross-reference to an image target without changing the selected wording', async () => {
    const onCrossReferenceApplied = vi.fn();
    const { container } = render(
      <WriterIRDocumentEditor
        document={imageTargetDocument}
        ariaLabel='Writer document'
        onChange={vi.fn()}
        onCrossReferenceApplied={onCrossReferenceApplied}
        onFocus={vi.fn()}
        onBlur={vi.fn()}
      />,
    );
    const paragraph = container.querySelector<HTMLElement>(
      '[data-node-id="p-1"] [data-writer-block-content]',
    );
    const textNode = paragraph?.firstChild;
    expect(paragraph).not.toBeNull();
    expect(textNode).not.toBeNull();

    const range = window.document.createRange();
    range.setStart(textNode!, 0);
    range.setEnd(textNode!, 5);
    const selection = window.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);
    fireEvent.mouseUp(paragraph!);

    const trigger = await screen.findByRole('button', {
      name: 'chat.writerIR.crossReference',
    });
    fireEvent.mouseDown(trigger);
    fireEvent.click(trigger);
    fireEvent.click(screen.getByTitle('图1 雨后山间溪流图'));

    expect(onCrossReferenceApplied).toHaveBeenCalledTimes(1);
    const updated = onCrossReferenceApplied.mock.calls[0][0] as WriterDocument;
    const paragraphBlock = updated.blocks.find((block) => block.node_id === 'p-1');
    expect(paragraphBlock?.content).toBe('Alpha beta gamma');
    expect(getWriterInternalReference(paragraphBlock?.spans?.[0] ?? { text: '' }))
      .toMatchObject({
        targetNodeId: 'image-1',
        displayText: 'Alpha',
      });
  });

  it('removes the selected cross-reference without changing its wording', async () => {
    const onCrossReferenceApplied = vi.fn();
    const { container } = render(
      <WriterIRDocumentEditor
        document={referencedDocument}
        ariaLabel='Writer document'
        onChange={vi.fn()}
        onCrossReferenceApplied={onCrossReferenceApplied}
        onFocus={vi.fn()}
        onBlur={vi.fn()}
      />,
    );
    const reference = container.querySelector<HTMLAnchorElement>(
      '[data-node-id="p-1"] a[data-writer-internal-ref]',
    );
    const textNode = reference?.firstChild;
    expect(reference).not.toBeNull();
    expect(textNode).not.toBeNull();

    const range = window.document.createRange();
    range.setStart(textNode!, 0);
    range.setEnd(textNode!, 5);
    const selection = window.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);
    fireEvent.mouseUp(reference!);

    const remove = await screen.findByRole('button', {
      name: 'chat.writerIR.removeCrossReference',
    });
    expect((remove as HTMLButtonElement).disabled).toBe(false);
    fireEvent.mouseDown(remove);
    fireEvent.click(remove);

    expect(onCrossReferenceApplied).toHaveBeenCalledTimes(1);
    const updated = onCrossReferenceApplied.mock.calls[0][0] as WriterDocument;
    const paragraph = updated.blocks.find((block) => block.node_id === 'p-1');
    expect(paragraph?.content).toBe('Alpha beta gamma');
    expect(paragraph?.spans).toEqual([{ text: 'Alpha beta gamma', style: {} }]);
    expect(getWriterInternalReference(paragraph?.spans?.[0] ?? { text: '' })).toBeUndefined();
  });

  it('uses the current saved selection when keyboard activation follows a closed menu', async () => {
    const onCrossReferenceApplied = vi.fn();
    const { container } = render(
      <WriterIRDocumentEditor
        document={twiceReferencedDocument}
        ariaLabel='Writer document'
        onChange={vi.fn()}
        onCrossReferenceApplied={onCrossReferenceApplied}
        onFocus={vi.fn()}
        onBlur={vi.fn()}
      />,
    );
    const references = container.querySelectorAll<HTMLAnchorElement>(
      '[data-node-id="p-1"] a[data-writer-internal-ref]',
    );
    expect(references).toHaveLength(2);

    const firstRange = window.document.createRange();
    firstRange.selectNodeContents(references[0]);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(firstRange);
    fireEvent.mouseUp(references[0]);

    const trigger = await screen.findByRole('button', {
      name: 'chat.writerIR.crossReference',
    });
    fireEvent.mouseDown(trigger);
    fireEvent.click(trigger);
    fireEvent.click(trigger);

    const secondRange = window.document.createRange();
    secondRange.selectNodeContents(references[1]);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(secondRange);
    fireEvent.mouseUp(references[1]);

    const remove = await screen.findByRole('button', {
      name: 'chat.writerIR.removeCrossReference',
    });
    remove.focus();
    fireEvent.click(remove, { detail: 0 });

    expect(onCrossReferenceApplied).toHaveBeenCalledTimes(1);
    const updated = onCrossReferenceApplied.mock.calls[0][0] as WriterDocument;
    const paragraph = updated.blocks.find((block) => block.node_id === 'p-1');
    expect(paragraph?.content).toBe('Alpha beta gamma');
    expect(getWriterInternalReference(paragraph?.spans?.[0] ?? { text: '' })).toMatchObject({
      targetNodeId: 'sec-1',
      displayText: 'Alpha',
    });
    expect(paragraph?.spans?.some(
      (span) => span.text.includes('beta') && getWriterInternalReference(span) !== undefined,
    )).toBe(false);
  });
});

describe('WriterIRDocumentEditor outline instructions', () => {
  it('renders structured instructions under the heading and preserves them after editing', async () => {
    const onDocumentChange = vi.fn();
    const { container } = render(
      <ControlledWriter
        initialDocument={outlineInstructionDocument}
        onDocumentChange={onDocumentChange}
      />,
    );

    const details = container.querySelector<HTMLDetailsElement>(
      '[data-writer-outline-instructions]',
    );
    expect(details).not.toBeNull();
    expect(details?.open).toBe(true);
    expect(details).toHaveTextContent('chat.writerIR.outlineInstructions');
    expect(details).toHaveTextContent('1800');
    expect(details).toHaveTextContent('承接前文已暴露的禁忌线索');
    expect(details).toHaveTextContent('仪式为什么会在此时失控？');

    const heading = container.querySelector<HTMLElement>(
      '[data-node-id="outline-section-1"] > [data-writer-block-content]',
    );
    expect(heading).not.toBeNull();
    heading!.textContent = '旧日的仪式（修订）';
    fireEvent.input(heading!);

    await waitFor(() => expect(onDocumentChange).toHaveBeenCalled());
    const lastCall = onDocumentChange.mock.calls[onDocumentChange.mock.calls.length - 1];
    const updated = lastCall?.[0] as WriterDocument;
    expect(updated.blocks[0]).toMatchObject({
      content: '旧日的仪式（修订）',
      target_chars: 1800,
      context_relations: outlineInstructionDocument.blocks[0].context_relations,
      subtasks: outlineInstructionDocument.blocks[0].subtasks,
    });
  });
});

describe('WriterIRDocumentEditor heading Enter behavior', () => {
  it('adds a blank line before existing body content without selecting or scrolling the document', async () => {
    const nativeScrollIntoView = vi.fn();
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
      configurable: true,
      value: nativeScrollIntoView,
    });
    const onDocumentChange = vi.fn();
    const { container } = render(
      <ControlledWriter
        initialDocument={headingDocument}
        onDocumentChange={onDocumentChange}
      />,
    );
    const scrollOwner = screen.getByTestId('writer-scroll');
    const outerScroll = screen.getByTestId('outer-scroll');
    Object.defineProperties(scrollOwner, {
      clientHeight: { configurable: true, value: 240 },
      scrollHeight: { configurable: true, value: 1_000 },
    });
    Object.defineProperties(outerScroll, {
      clientHeight: { configurable: true, value: 400 },
      scrollHeight: { configurable: true, value: 1_600 },
    });
    scrollOwner.scrollTop = 360;
    outerScroll.scrollTop = 520;
    const boundingRect = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect')
      .mockImplementation(function rectForElement(this: HTMLElement) {
        if (this === scrollOwner) {
          return { ...selectionRect(), top: 0, bottom: 240, height: 240 } as DOMRect;
        }
        if (this === outerScroll) {
          return { ...selectionRect(), top: 0, bottom: 400, height: 400 } as DOMRect;
        }
        return selectionRect();
      });
    const heading = container.querySelector<HTMLElement>(
      '[data-node-id="heading-1"] > [data-writer-block-content]',
    );
    expect(heading).not.toBeNull();
    placeCaret(heading!, (headingDocument.blocks[0]!.content ?? '').length);

    fireEvent.keyDown(heading!, { key: 'Enter', code: 'Enter', keyCode: 13 });

    await waitFor(() => expect(onDocumentChange).toHaveBeenCalledTimes(1));
    const updated = onDocumentChange.mock.calls[0][0] as WriterDocument;
    const updatedHeading = updated.blocks[0];
    const insertedParagraph = updatedHeading.children?.[0];
    expect(updatedHeading.content).toBe('第一章 雨后山林');
    expect(updatedHeading.content).not.toMatch(/^#{1,6}\s/);
    expect(insertedParagraph).toMatchObject({ type: 'paragraph', content: '' });
    expect(updatedHeading.children?.[1]).toMatchObject({
      node_id: 'paragraph-existing',
      content: '既有正文',
    });
    expect(window.getSelection()?.isCollapsed).toBe(true);
    await waitFor(() => expect(selectionBlockId()).toBe(insertedParagraph?.node_id));
    expect(nativeScrollIntoView).not.toHaveBeenCalled();
    expect(scrollOwner.scrollTop).toBe(360);
    expect(outerScroll.scrollTop).toBe(520);
    expect(boundingRect).toHaveBeenCalled();
  });

  it('splits a heading once for the full Enter event sequence', async () => {
    const onDocumentChange = vi.fn();
    const { container } = render(
      <ControlledWriter
        initialDocument={headingDocument}
        onDocumentChange={onDocumentChange}
      />,
    );
    const heading = container.querySelector<HTMLElement>(
      '[data-node-id="heading-1"] > [data-writer-block-content]',
    );
    expect(heading).not.toBeNull();
    placeCaret(heading!, '第一章 '.length);

    fireEvent.keyDown(heading!, { key: 'Enter', code: 'Enter', keyCode: 13 });
    fireEvent(
      heading!,
      new InputEvent('beforeinput', {
        bubbles: true,
        cancelable: true,
        inputType: 'insertParagraph',
      }),
    );
    fireEvent.keyUp(heading!, { key: 'Enter', code: 'Enter', keyCode: 13 });

    await waitFor(() => expect(onDocumentChange).toHaveBeenCalledTimes(1));
    const updated = onDocumentChange.mock.calls[0][0] as WriterDocument;
    const updatedHeading = updated.blocks[0]!;
    const insertedParagraph = updatedHeading.children?.[0];
    expect(updatedHeading.content).toBe('第一章 ');
    expect(updatedHeading.content).not.toMatch(/^#{1,6}\s/);
    expect(insertedParagraph).toMatchObject({ type: 'paragraph', content: '雨后山林' });
    expect(updatedHeading.children?.[1]).toMatchObject({
      node_id: 'paragraph-existing',
      content: '既有正文',
    });
    expect(updatedHeading.children).toHaveLength(2);
    expect(window.getSelection()?.isCollapsed).toBe(true);
    await waitFor(() => expect(selectionBlockId()).toBe(insertedParagraph?.node_id));
  });

  it('does not treat a collapsed caret as a stale whole-document selection', async () => {
    const onDocumentChange = vi.fn();
    const { container } = render(
      <ControlledWriter
        initialDocument={headingDocument}
        onDocumentChange={onDocumentChange}
      />,
    );
    const editor = screen.getByRole('textbox', { name: 'Writer document' });
    const heading = container.querySelector<HTMLElement>(
      '[data-node-id="heading-1"] > [data-writer-block-content]',
    );
    expect(heading).not.toBeNull();
    placeCaret(heading!, (headingDocument.blocks[0]!.content ?? '').length);
    fireEvent.keyDown(editor, { key: 'a', code: 'KeyA', metaKey: true });

    placeCaret(heading!, (headingDocument.blocks[0]!.content ?? '').length);
    fireEvent.keyUp(heading!, { key: 'ArrowLeft', code: 'ArrowLeft' });
    fireEvent.keyDown(heading!, { key: 'Enter', code: 'Enter', keyCode: 13 });

    await waitFor(() => expect(onDocumentChange).toHaveBeenCalledTimes(1));
    const updated = onDocumentChange.mock.calls[0][0] as WriterDocument;
    expect(updated.title).toBe(headingDocument.title);
    expect(updated.blocks[0]).toMatchObject({
      node_id: 'heading-1',
      type: 'heading',
      content: '第一章 雨后山林',
    });
    expect(updated.blocks[0].children).toHaveLength(2);
  });
});
