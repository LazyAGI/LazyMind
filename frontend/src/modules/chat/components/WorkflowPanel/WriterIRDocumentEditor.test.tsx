import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@ant-design/icons', () => ({
  BoldOutlined: () => null,
  CodeOutlined: () => null,
  DisconnectOutlined: () => null,
  DownOutlined: () => null,
  FontSizeOutlined: () => null,
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
import {
  getWriterInternalReference,
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

describe('WriterIRDocumentEditor numbering sidecar', () => {
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
    const updated = onChange.mock.calls.at(-1)?.[0] as WriterDocument;
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
