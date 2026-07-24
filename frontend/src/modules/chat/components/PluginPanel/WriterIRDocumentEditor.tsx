import {
  BoldOutlined,
  ItalicOutlined,
  OrderedListOutlined,
  UnorderedListOutlined,
} from '@ant-design/icons';
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type ChangeEvent,
  type ClipboardEvent as ReactClipboardEvent,
  type FocusEvent as ReactFocusEvent,
  type FormEvent,
  type KeyboardEvent as ReactKeyboardEvent,
} from 'react';
import { useTranslation } from 'react-i18next';
import {
  countWriterBlocks,
  createWriterParagraph,
  findWriterBlock,
  findWriterBlockParent,
  getWriterOutlineInstruction,
  getWriterSpanStyles,
  indentWriterBlock,
  insertWriterParagraphAfter,
  liftWriterBlockAfterParent,
  splitWriterBlock,
  toggleWriterBlockInlineStyle,
  updateWriterBlockContent,
  updateWriterBlockFormat,
  updateWriterDocumentTitle,
  writerBlockRangeHasInlineStyle,
  type WriterBlockFormat,
  type WriterBlock,
  type WriterDocument,
  type WriterInlineStyle,
  type WriterSpan,
} from './writerIR';

interface WriterIRDocumentEditorProps {
  document: WriterDocument;
  ariaLabel: string;
  onChange: (document: WriterDocument) => void;
  onFocus: () => void;
  onBlur: () => void;
  disabled?: boolean;
}

function escapeHtml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

function escapeHtmlAttribute(value: string): string {
  return escapeHtml(value)
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function renderSpan(span: WriterSpan): string {
  let content = escapeHtml(span.text);
  const styles = getWriterSpanStyles(span);
  if (styles.includes('code')) content = `<code>${content}</code>`;
  if (styles.includes('strong') || styles.includes('bold')) content = `<strong>${content}</strong>`;
  if (styles.includes('italic')) content = `<em>${content}</em>`;
  if (styles.includes('underline')) content = `<u>${content}</u>`;
  if (styles.includes('strike') || styles.includes('strikethrough')) {
    content = `<s>${content}</s>`;
  }
  return content;
}

function renderBlockText(block: WriterBlock): string {
  const spans = block.spans ?? [];
  if (spans.length > 0 && spans.map((span) => span.text).join('') === (block.content ?? '')) {
    return spans.map(renderSpan).join('');
  }
  return escapeHtml(block.content ?? '');
}

function renderEditableBlockText(block: WriterBlock): string {
  const text = renderBlockText(block);
  if (text || block.editable === false) return text;
  return '<br data-writer-empty-placeholder="true">';
}

function headingLevel(block: WriterBlock): number {
  const level = Number(block.numbering?.level ?? 2);
  return Number.isFinite(level) ? Math.min(6, Math.max(1, Math.trunc(level))) : 2;
}

function renderOutlineInstruction(block: WriterBlock, show: boolean): string {
  if (!show) return '';
  const instruction = getWriterOutlineInstruction(block);
  if (!instruction) return '';
  return `<p class="writer-ir__outline-instruction" data-writer-outline-instruction="true" contenteditable="false">${escapeHtml(instruction)}</p>`;
}

function renderBlockSequence(
  blocks: WriterBlock[],
  showOutlineInstruction: boolean,
): string {
  const rendered: string[] = [];

  for (let index = 0; index < blocks.length;) {
    const block = blocks[index];
    if (block.type === 'list_item') {
      const ordered = Boolean(block.numbering?.ordered);
      const tag = ordered ? 'ol' : 'ul';
      const items: string[] = [];
      while (
        index < blocks.length
        && blocks[index].type === 'list_item'
        && Boolean(blocks[index].numbering?.ordered) === ordered
      ) {
        items.push(renderBlock(blocks[index], showOutlineInstruction));
        index += 1;
      }
      rendered.push(`<${tag} class="writer-ir__list">${items.join('')}</${tag}>`);
      continue;
    }

    rendered.push(renderBlock(block, showOutlineInstruction));
    index += 1;
  }

  return rendered.join('');
}

function renderBlock(block: WriterBlock, showOutlineInstruction: boolean): string {
  if (block.type === 'document') {
    return [
      `<section data-writer-document-root="${escapeHtmlAttribute(block.node_id)}"`,
      ` class="writer-ir__document-root">`,
      renderBlockSequence(block.children ?? [], showOutlineInstruction),
      '</section>',
    ].join('');
  }

  const attributes = [
    `data-writer-block="true"`,
    `data-node-id="${escapeHtmlAttribute(block.node_id)}"`,
    `data-node-type="${escapeHtmlAttribute(block.type)}"`,
    block.type === 'heading' ? `data-heading-level="${headingLevel(block)}"` : '',
    `class="writer-ir__block writer-ir__block--${escapeHtmlAttribute(block.type)}"`,
    block.editable === false ? 'contenteditable="false"' : '',
  ].filter(Boolean).join(' ');
  const text = renderEditableBlockText(block);
  const outlineInstruction = renderOutlineInstruction(block, showOutlineInstruction);
  const children = block.children?.length
    ? block.type === 'list_item'
      ? renderBlockSequence(block.children, showOutlineInstruction)
      : `<div data-writer-children="true" class="writer-ir__children">${renderBlockSequence(block.children, showOutlineInstruction)}</div>`
    : '';

  if (block.type === 'heading') {
    const level = headingLevel(block);
    return [
      `<div ${attributes}>`,
      `<h${level} data-writer-block-content="true" class="writer-ir__heading writer-ir__heading--${level}">${text}</h${level}>`,
      outlineInstruction,
      children,
      '</div>',
    ].join('');
  }
  if (block.type === 'code') {
    return `<div ${attributes}><pre data-writer-block-content="true" class="writer-ir__code"><code>${text}</code></pre>${outlineInstruction}${children}</div>`;
  }
  if (block.type === 'paragraph') {
    return `<div ${attributes}><p data-writer-block-content="true" class="writer-ir__paragraph">${text}</p>${outlineInstruction}${children}</div>`;
  }
  if (block.type === 'quote') {
    return `<div ${attributes}><blockquote data-writer-block-content="true" class="writer-ir__quote">${text}</blockquote>${outlineInstruction}${children}</div>`;
  }
  if (block.type === 'divider') {
    return `<div ${attributes}><hr data-writer-block-content="true" class="writer-ir__divider"></div>`;
  }
  if (block.type === 'list_item') {
    return `<li ${attributes}><span data-writer-block-content="true">${text}</span>${outlineInstruction}${children}</li>`;
  }
  return `<div ${attributes}><div data-writer-block-content="true" class="writer-ir__fallback">${text}</div>${outlineInstruction}${children}</div>`;
}

function renderDocument(document: WriterDocument): string {
  const documentRoot = document.blocks.find((block) => block.type === 'document');
  return [
    `<h1 class="writer-ir__title" data-writer-document-title="true"`,
    documentRoot?.editable === false ? ' contenteditable="false">' : '>',
    escapeHtml(document.title),
    '</h1>',
    renderBlockSequence(document.blocks, document.stage === 'outline'),
  ].join('');
}

function inferredBlockType(element: HTMLElement): string {
  const tagName = element.tagName.toLowerCase();
  if (/^h[1-6]$/.test(tagName)) return 'heading';
  if (tagName === 'pre') return 'code';
  if (tagName === 'blockquote') return 'quote';
  if (tagName === 'hr') return 'divider';
  if (tagName === 'li') return 'list_item';
  return 'paragraph';
}

function textFromElement(element: HTMLElement): string {
  const clone = element.cloneNode(true) as HTMLElement;
  clone.querySelectorAll('[data-writer-empty-placeholder]').forEach(
    (placeholder) => placeholder.remove(),
  );
  for (const child of Array.from(clone.children)) {
    if (
      child.matches('[data-writer-children]')
      || child.matches('[data-writer-outline-instruction]')
      || child.matches('ul, ol')
    ) child.remove();
  }

  const collect = (node: Node): string => {
    if (node.nodeType === Node.TEXT_NODE) return node.textContent ?? '';
    if (!(node instanceof HTMLElement)) return '';
    if (node.tagName === 'BR') return '\n';
    const value = Array.from(node.childNodes).map(collect).join('');
    return ['DIV', 'P'].includes(node.tagName) ? `${value}\n` : value;
  };

  const value = Array.from(clone.childNodes).map(collect).join('')
    .replace(/\u00a0/g, ' ');
  return element.tagName === 'PRE' ? value : value.replace(/\n+$/, '');
}

function textFromBlockElement(
  blockElement: HTMLElement,
  contentElement: HTMLElement,
): string {
  let content = textFromElement(contentElement);
  let foundContent = false;

  for (const child of Array.from(blockElement.childNodes)) {
    if (child === contentElement) {
      foundContent = true;
      continue;
    }
    if (!foundContent) continue;
    if (
      child instanceof HTMLElement
      && child.matches(
        '[data-writer-block], [data-writer-children], '
        + '[data-writer-outline-instruction], ul, ol',
      )
    ) {
      break;
    }

    const trailingContent = child instanceof HTMLElement
      ? child.tagName === 'BR' ? '' : textFromElement(child)
      : child.textContent?.replace(/\u00a0/g, ' ') ?? '';
    if (!trailingContent) continue;
    content = content ? `${content}\n${trailingContent}` : trailingContent;
  }

  return content;
}

function childElements(element: HTMLElement, selector: string): HTMLElement[] {
  return Array.from(element.children).filter(
    (child): child is HTMLElement => child instanceof HTMLElement && child.matches(selector),
  );
}

function blockContentElement(element: HTMLElement): HTMLElement {
  if (element.matches('[data-writer-block-content]')) return element;
  return childElements(element, '[data-writer-block-content]')[0] ?? element;
}

function parseEditorDocument(editor: HTMLElement, source: WriterDocument): WriterDocument {
  const documentRoot = source.blocks.find((block) => block.type === 'document');
  const titledDocument = documentRoot?.editable === false
    ? source
    : updateWriterDocumentTitle(
      source,
      editor.querySelector<HTMLElement>('[data-writer-document-title]')?.textContent ?? source.title,
    );

  const parseBlockElement = (
    element: HTMLElement,
    forcedType?: string,
    ordered?: boolean,
  ): WriterBlock => {
    const nodeId = element.dataset.nodeId || createWriterParagraph(source.stage).node_id;
    const type = forcedType || element.dataset.nodeType || inferredBlockType(element);
    element.dataset.writerBlock = 'true';
    element.dataset.nodeId = nodeId;
    element.dataset.nodeType = type;

    const existing = findWriterBlock(titledDocument.blocks, nodeId);
    const contentElement = blockContentElement(element);
    const content = type === 'divider'
      ? ''
      : textFromBlockElement(element, contentElement);
    const contentDocument = existing
      ? updateWriterBlockContent(titledDocument, nodeId, content)
      : undefined;
    const contentBlock = contentDocument
      ? findWriterBlock(contentDocument.blocks, nodeId)
      : undefined;
    const template = contentBlock ?? {
      ...createWriterParagraph(source.stage),
      node_id: nodeId,
      type,
      content,
      spans: [{ text: content, style: {} }],
    };

    if (existing?.editable === false) return existing;

    const nestedContainers = childElements(element, '[data-writer-children], ul, ol');
    let children = nestedContainers.flatMap((container) => parseSequence(container));
    // List items render nested blocks as direct children without a
    // data-writer-children wrapper.
    if (type === 'list_item') {
      children = [
        ...children,
        ...childElements(element, ':scope > [data-writer-block]').map(
          (nested) => parseBlockElement(nested),
        ),
      ];
    }
    const numbering = type === 'heading'
      ? {
        ...(template.numbering ?? {}),
        level: Number(contentElement.tagName.slice(1))
          || Number(element.dataset.headingLevel)
          || Number(template.numbering?.level)
          || 2,
      }
      : type === 'list_item'
        ? { ...(template.numbering ?? {}), ordered: Boolean(ordered) }
        : template.numbering;

    return {
      ...template,
      type,
      content,
      numbering,
      children,
    };
  };

  const parseSequence = (container: HTMLElement): WriterBlock[] => {
    const blocks: WriterBlock[] = [];
    for (const child of Array.from(container.children)) {
      if (!(child instanceof HTMLElement)) continue;
      if (child.matches('[data-writer-document-title]')) continue;
      if (child.matches('[data-writer-document-root]')) {
        blocks.push(...parseSequence(child));
        continue;
      }
      if (child.matches('ul, ol')) {
        const ordered = child.tagName === 'OL';
        blocks.push(...childElements(child, 'li').map(
          (item) => parseBlockElement(item, 'list_item', ordered),
        ));
        continue;
      }
      if (child.matches('[data-writer-children]')) {
        blocks.push(...parseSequence(child));
        continue;
      }
      const parsed = parseBlockElement(child);
      blocks.push(parsed);
      // Browser Enter can nest a new block under the previous wrapper. Promote
      // those stray direct writer-blocks to siblings instead of deepening the
      // tree. List-item children are intentional direct nests and stay put.
      if (parsed.type === 'list_item') continue;
      for (const stray of childElements(child, ':scope > [data-writer-block]')) {
        blocks.push(parseBlockElement(stray));
      }
    }
    return blocks;
  };

  const nextTopLevel: WriterBlock[] = [];
  for (const child of Array.from(editor.children)) {
    if (!(child instanceof HTMLElement) || child.matches('[data-writer-document-title]')) continue;
    const documentRootId = child.dataset.writerDocumentRoot;
    if (documentRootId) {
      const existingRoot = findWriterBlock(titledDocument.blocks, documentRootId);
      if (existingRoot?.type === 'document') {
        nextTopLevel.push({ ...existingRoot, children: parseSequence(child) });
      }
      continue;
    }
    if (child.matches('ul, ol')) {
      const ordered = child.tagName === 'OL';
      nextTopLevel.push(...childElements(child, 'li').map(
        (item) => parseBlockElement(item, 'list_item', ordered),
      ));
      continue;
    }
    nextTopLevel.push(parseBlockElement(child));
  }

  const metadata = titledDocument.metadata
    && Object.prototype.hasOwnProperty.call(titledDocument.metadata, 'block_count')
    ? { ...titledDocument.metadata, block_count: countWriterBlocks(nextTopLevel) }
    : titledDocument.metadata;
  return { ...titledDocument, blocks: nextTopLevel, metadata };
}

interface WriterEditorSelection {
  nodeId: string;
  start: number;
  end: number;
}

function closestWriterBlock(node: Node | null, editor: HTMLElement): HTMLElement | null {
  const element = node instanceof HTMLElement ? node : node?.parentElement;
  const block = element?.closest<HTMLElement>('[data-writer-block][data-node-id]') ?? null;
  return block && editor.contains(block) ? block : null;
}

function readEditorSelection(editor: HTMLElement): WriterEditorSelection | null {
  const selection = globalThis.getSelection();
  if (!selection || selection.rangeCount === 0) return null;
  const range = selection.getRangeAt(0);
  const startBlock = closestWriterBlock(range.startContainer, editor);
  const endBlock = closestWriterBlock(range.endContainer, editor);
  if (!startBlock || !endBlock || startBlock.dataset.nodeId !== endBlock.dataset.nodeId) {
    return null;
  }

  const contentElement = blockContentElement(startBlock);
  if (
    !contentElement.contains(range.startContainer)
    || !contentElement.contains(range.endContainer)
  ) {
    return null;
  }

  const beforeStart = globalThis.document.createRange();
  beforeStart.selectNodeContents(contentElement);
  beforeStart.setEnd(range.startContainer, range.startOffset);
  const beforeEnd = globalThis.document.createRange();
  beforeEnd.selectNodeContents(contentElement);
  beforeEnd.setEnd(range.endContainer, range.endOffset);
  return {
    nodeId: startBlock.dataset.nodeId!,
    start: Array.from(beforeStart.toString()).length,
    end: Array.from(beforeEnd.toString()).length,
  };
}

function findRenderedBlock(editor: HTMLElement, nodeId: string): HTMLElement | undefined {
  return Array.from(
    editor.querySelectorAll<HTMLElement>('[data-writer-block][data-node-id]'),
  ).find((element) => element.dataset.nodeId === nodeId);
}

function textBoundaryAt(
  contentElement: HTMLElement,
  offset: number,
): { node: Node; offset: number } {
  const walker = globalThis.document.createTreeWalker(
    contentElement,
    NodeFilter.SHOW_TEXT,
  );
  let remaining = Math.max(0, offset);
  let textNode = walker.nextNode();
  while (textNode) {
    const characters = Array.from(textNode.textContent ?? '');
    if (remaining <= characters.length) {
      return {
        node: textNode,
        offset: characters.slice(0, remaining).join('').length,
      };
    }
    remaining -= characters.length;
    textNode = walker.nextNode();
  }
  const placeholder = contentElement.querySelector<HTMLElement>(
    '[data-writer-empty-placeholder]',
  );
  if (placeholder && !(contentElement.textContent ?? '')) {
    const parent = placeholder.parentNode ?? contentElement;
    return {
      node: parent,
      offset: Array.from(parent.childNodes).indexOf(placeholder),
    };
  }
  return { node: contentElement, offset: contentElement.childNodes.length };
}

function restoreEditorSelection(
  editor: HTMLElement,
  savedSelection: WriterEditorSelection,
): void {
  const block = findRenderedBlock(editor, savedSelection.nodeId);
  if (!block) return;
  const contentElement = blockContentElement(block);
  const start = textBoundaryAt(contentElement, savedSelection.start);
  const end = textBoundaryAt(contentElement, savedSelection.end);
  const range = globalThis.document.createRange();
  range.setStart(start.node, start.offset);
  range.setEnd(end.node, end.offset);
  const selection = globalThis.getSelection();
  selection?.removeAllRanges();
  selection?.addRange(range);
  editor.focus({ preventScroll: true });
}

export function WriterIRDocumentEditor({
  document,
  ariaLabel,
  onChange,
  onFocus,
  onBlur,
  disabled = false,
}: WriterIRDocumentEditorProps) {
  const { t } = useTranslation();
  const shellRef = useRef<HTMLDivElement | null>(null);
  const editorRef = useRef<HTMLElement | null>(null);
  const formatToolbarRef = useRef<HTMLDivElement | null>(null);
  const lastEmittedDocumentRef = useRef<WriterDocument>();
  const savedSelectionRef = useRef<WriterEditorSelection | null>(null);
  const pendingSelectionRef = useRef<WriterEditorSelection | null>(null);
  const isComposingRef = useRef(false);
  const handledEnterKeyDownRef = useRef(false);
  const [activeSelection, setActiveSelection] = useState<WriterEditorSelection | null>(null);
  const [formatToolbarStyle, setFormatToolbarStyle] = useState<CSSProperties | undefined>();

  useLayoutEffect(() => {
    const editor = editorRef.current;
    if (!editor || lastEmittedDocumentRef.current === document) return;
    const html = renderDocument(document);
    if (editor.innerHTML !== html) editor.innerHTML = html;
    lastEmittedDocumentRef.current = undefined;
    const pendingSelection = pendingSelectionRef.current;
    pendingSelectionRef.current = null;
    if (pendingSelection) restoreEditorSelection(editor, pendingSelection);
  }, [document]);

  const recordSelection = useCallback(() => {
    const editor = editorRef.current;
    if (!editor) return;
    const selection = readEditorSelection(editor);
    if (!selection) {
      savedSelectionRef.current = null;
      setActiveSelection(null);
      return;
    }
    savedSelectionRef.current = selection;
    setActiveSelection(selection);
  }, []);

  useEffect(() => {
    const handleSelectionChange = () => {
      const editor = editorRef.current;
      const selection = globalThis.getSelection();
      if (!editor || !selection?.anchorNode || !editor.contains(selection.anchorNode)) return;
      recordSelection();
    };
    globalThis.document.addEventListener('selectionchange', handleSelectionChange);
    return () => globalThis.document.removeEventListener('selectionchange', handleSelectionChange);
  }, [recordSelection]);

  const handleInput = (event: FormEvent<HTMLElement>) => {
    if (disabled) return;
    const nextDocument = parseEditorDocument(event.currentTarget, document);
    for (const contentElement of Array.from(
      event.currentTarget.querySelectorAll<HTMLElement>('[data-writer-block-content]'),
    )) {
      if (!textFromElement(contentElement)) continue;
      contentElement.querySelectorAll('[data-writer-empty-placeholder]').forEach(
        (placeholder) => placeholder.remove(),
      );
    }
    lastEmittedDocumentRef.current = nextDocument;
    onChange(nextDocument);
    recordSelection();
  };

  const handleBlur = (event: ReactFocusEvent<HTMLElement>) => {
    if (
      event.relatedTarget instanceof Node
      && event.currentTarget.contains(event.relatedTarget)
    ) return;
    savedSelectionRef.current = null;
    setActiveSelection(null);
    onBlur();
  };

  const handlePaste = (event: ReactClipboardEvent<HTMLElement>) => {
    event.preventDefault();
    globalThis.document.execCommand('insertText', false, event.clipboardData.getData('text/plain'));
  };

  const activeBlock = activeSelection
    ? findWriterBlock(document.blocks, activeSelection.nodeId)
    : undefined;
  const hasTextSelection = Boolean(
    activeSelection && activeSelection.end > activeSelection.start,
  );
  const canFormatBlock = Boolean(activeBlock && activeBlock.editable !== false);
  const showFormatToolbar = Boolean(
    !disabled
    && canFormatBlock
    && activeSelection
    && activeSelection.end > activeSelection.start,
  );
  const canChangeBlockFormat = canFormatBlock && (activeBlock?.children?.length ?? 0) === 0;

  const updateFormatToolbarPosition = useCallback(() => {
    if (!showFormatToolbar || !activeSelection) {
      setFormatToolbarStyle(undefined);
      return;
    }
    const shell = shellRef.current;
    const editor = editorRef.current;
    if (!shell || !editor) {
      setFormatToolbarStyle(undefined);
      return;
    }

    const selection = window.getSelection();
    const range = selection && selection.rangeCount > 0 ? selection.getRangeAt(0) : null;
    const anchorRect = range && !range.collapsed && editor.contains(range.commonAncestorContainer)
      ? range.getBoundingClientRect()
      : findRenderedBlock(editor, activeSelection.nodeId)?.getBoundingClientRect();
    if (!anchorRect || (anchorRect.width === 0 && anchorRect.height === 0)) {
      setFormatToolbarStyle(undefined);
      return;
    }

    const shellRect = shell.getBoundingClientRect();
    const toolbarHeight = formatToolbarRef.current?.offsetHeight ?? 40;
    const toolbarWidth = formatToolbarRef.current?.offsetWidth ?? 280;
    const gap = 8;
    const placeAbove = anchorRect.top - shellRect.top >= toolbarHeight + gap;
    const top = placeAbove
      ? anchorRect.top - shellRect.top - gap
      : anchorRect.bottom - shellRect.top + gap;
    const preferredLeft = anchorRect.left - shellRect.left
      + Math.max(0, (anchorRect.width - toolbarWidth) / 2);
    const maxLeft = Math.max(0, shell.clientWidth - toolbarWidth - 4);
    const left = Math.min(Math.max(0, preferredLeft), maxLeft);

    setFormatToolbarStyle({
      top,
      left,
      transform: placeAbove ? 'translateY(-100%)' : undefined,
    });
  }, [activeSelection, showFormatToolbar]);

  useLayoutEffect(() => {
    updateFormatToolbarPosition();
  }, [updateFormatToolbarPosition, document, activeSelection]);

  useEffect(() => {
    if (!showFormatToolbar) return undefined;
    const handleReposition = () => updateFormatToolbarPosition();
    const root = window.document;
    window.addEventListener('resize', handleReposition);
    root.addEventListener('scroll', handleReposition, true);
    return () => {
      window.removeEventListener('resize', handleReposition);
      root.removeEventListener('scroll', handleReposition, true);
    };
  }, [showFormatToolbar, updateFormatToolbarPosition]);
  const selectionIsBold = Boolean(
    activeBlock
    && activeSelection
    && writerBlockRangeHasInlineStyle(
      activeBlock,
      activeSelection.start,
      activeSelection.end,
      'strong',
    ),
  );
  const selectionIsItalic = Boolean(
    activeBlock
    && activeSelection
    && writerBlockRangeHasInlineStyle(
      activeBlock,
      activeSelection.start,
      activeSelection.end,
      'italic',
    ),
  );
  const blockFormatValue = activeBlock?.type === 'heading'
    ? `heading-${headingLevel(activeBlock)}`
    : activeBlock?.type === 'list_item'
      ? (activeBlock.numbering?.ordered ? 'ordered-list' : 'unordered-list')
      : activeBlock?.type === 'paragraph' || activeBlock?.type === 'code'
        ? activeBlock.type
        : '';
  const isUnorderedList = activeBlock?.type === 'list_item'
    && !activeBlock.numbering?.ordered;
  const isOrderedList = activeBlock?.type === 'list_item'
    && Boolean(activeBlock.numbering?.ordered);

  const handleBlockFormatChange = (event: ChangeEvent<HTMLSelectElement>) => {
    if (
      !activeBlock
      || disabled
      || activeBlock.editable === false
      || (activeBlock.children?.length ?? 0) > 0
    ) return;
    const value = event.target.value;
    const format: WriterBlockFormat = value.startsWith('heading-')
      ? 'heading'
      : value === 'code'
        ? 'code'
        : value === 'ordered-list' || value === 'unordered-list'
          ? 'list_item'
          : 'paragraph';
    const level = format === 'heading' ? Number(value.slice('heading-'.length)) : undefined;
    const nextDocument = updateWriterBlockFormat(
      document,
      activeBlock.node_id,
      format,
      {
        headingLevel: level,
        ordered: value === 'ordered-list',
      },
    );
    if (nextDocument === document) return;
    pendingSelectionRef.current = savedSelectionRef.current;
    onChange(nextDocument);
  };

  const applyListFormat = useCallback((ordered: boolean) => {
    if (disabled) return;
    const selection = savedSelectionRef.current;
    if (!selection) return;
    const block = findWriterBlock(document.blocks, selection.nodeId);
    if (
      !block
      || block.editable === false
      || (block.children?.length ?? 0) > 0
    ) return;

    const alreadySame = block.type === 'list_item'
      && Boolean(block.numbering?.ordered) === ordered;
    const nextDocument = updateWriterBlockFormat(
      document,
      block.node_id,
      alreadySame ? 'paragraph' : 'list_item',
      { ordered },
    );
    if (nextDocument === document) return;
    pendingSelectionRef.current = selection;
    lastEmittedDocumentRef.current = undefined;
    onChange(nextDocument);
  }, [disabled, document, onChange]);

  const applyBlockIndent = useCallback((direction: 'in' | 'out') => {
    if (disabled) return;
    const selection = savedSelectionRef.current;
    if (!selection) return;
    const block = findWriterBlock(document.blocks, selection.nodeId);
    if (!block || block.editable === false || block.type === 'document') return;

    const result = direction === 'in'
      ? indentWriterBlock(document, block.node_id)
      : liftWriterBlockAfterParent(document, block.node_id);
    if (!result.insertedNodeId) return;

    const nextSelection = {
      nodeId: result.insertedNodeId,
      start: selection.start,
      end: selection.end,
    };
    savedSelectionRef.current = nextSelection;
    pendingSelectionRef.current = nextSelection;
    setActiveSelection(nextSelection);
    lastEmittedDocumentRef.current = undefined;
    onChange(result.document);
  }, [disabled, document, onChange]);

  const applyInlineStyle = useCallback((style: WriterInlineStyle) => {
    if (disabled) return;
    const selection = savedSelectionRef.current;
    if (!selection || selection.end <= selection.start) return;
    const block = findWriterBlock(document.blocks, selection.nodeId);
    if (!block || block.editable === false) return;
    const nextDocument = toggleWriterBlockInlineStyle(
      document,
      selection.nodeId,
      selection.start,
      selection.end,
      style,
    );
    if (nextDocument === document) return;
    pendingSelectionRef.current = selection;
    onChange(nextDocument);
  }, [disabled, document, onChange]);

  const insertLineBreak = useCallback((editor: HTMLElement) => {
    if (disabled) return;
    const selection = readEditorSelection(editor);
    const block = selection
      ? findWriterBlock(document.blocks, selection.nodeId)
      : undefined;
    if (!selection || !block || block.editable === false) return;

    if (block.type === 'code') {
      const characters = Array.from(block.content ?? '');
      const nextContent = [
        ...characters.slice(0, selection.start),
        '\n',
        ...characters.slice(selection.end),
      ].join('');
      const nextDocument = updateWriterBlockContent(
        document,
        block.node_id,
        nextContent,
      );
      if (nextDocument === document) return;

      const nextSelection = {
        nodeId: block.node_id,
        start: selection.start + 1,
        end: selection.start + 1,
      };
      savedSelectionRef.current = nextSelection;
      pendingSelectionRef.current = nextSelection;
      setActiveSelection(nextSelection);
      onChange(nextDocument);
      return;
    }

    const contentLength = Array.from(block.content ?? '').length;
    const isEmptyBlock = contentLength === 0;
    const isAtEnd = selection.start >= contentLength && selection.end >= contentLength;
    const parent = findWriterBlockParent(document.blocks, block.node_id);

    // Empty indented block: outdent as a sibling of the parent instead of
    // creating another nested child under the same section.
    if (isEmptyBlock && parent && parent.type !== 'document') {
      const lifted = liftWriterBlockAfterParent(document, block.node_id);
      if (lifted.insertedNodeId) {
        const nextSelection = {
          nodeId: lifted.insertedNodeId,
          start: 0,
          end: 0,
        };
        savedSelectionRef.current = nextSelection;
        pendingSelectionRef.current = nextSelection;
        setActiveSelection(nextSelection);
        lastEmittedDocumentRef.current = undefined;
        onChange(lifted.document);
        return;
      }
    }

    // Enter at the end of the last child under a section should create a new
    // sibling of that section, not keep appending nested children.
    if (
      !isEmptyBlock
      && isAtEnd
      && parent
      && parent.type !== 'document'
    ) {
      const siblings = parent.children ?? [];
      const isLastChild = siblings[siblings.length - 1]?.node_id === block.node_id;
      if (isLastChild) {
        const inserted = insertWriterParagraphAfter(document, parent.node_id);
        if (inserted.insertedNodeId) {
          const nextSelection = {
            nodeId: inserted.insertedNodeId,
            start: 0,
            end: 0,
          };
          savedSelectionRef.current = nextSelection;
          pendingSelectionRef.current = nextSelection;
          setActiveSelection(nextSelection);
          lastEmittedDocumentRef.current = undefined;
          onChange(inserted.document);
          return;
        }
      }
    }

    const result = splitWriterBlock(
      document,
      block.node_id,
      selection.start,
      selection.end,
    );
    if (!result.insertedNodeId) return;
    const nextSelection = {
      nodeId: result.insertedNodeId,
      start: 0,
      end: 0,
    };
    savedSelectionRef.current = nextSelection;
    pendingSelectionRef.current = nextSelection;
    setActiveSelection(nextSelection);
    lastEmittedDocumentRef.current = undefined;
    onChange(result.document);
  }, [disabled, document, onChange]);

  const handleBeforeInput = (event: FormEvent<HTMLElement>) => {
    const inputType = (event.nativeEvent as InputEvent).inputType;
    if (inputType !== 'insertParagraph' && inputType !== 'insertLineBreak') return;
    event.preventDefault();
    if (handledEnterKeyDownRef.current) {
      handledEnterKeyDownRef.current = false;
      return;
    }
    insertLineBreak(event.currentTarget);
  };

  const handleKeyDown = (event: ReactKeyboardEvent<HTMLElement>) => {
    if (
      event.key === 'Enter'
      && !isComposingRef.current
      && !event.nativeEvent.isComposing
      && event.keyCode !== 229
    ) {
      event.preventDefault();
      handledEnterKeyDownRef.current = true;
      insertLineBreak(event.currentTarget);
      return;
    }

    if (event.key === 'Tab') {
      event.preventDefault();
      const selection = readEditorSelection(event.currentTarget);
      if (selection) {
        savedSelectionRef.current = selection;
        setActiveSelection(selection);
      }
      const block = selection
        ? findWriterBlock(document.blocks, selection.nodeId)
        : undefined;
      if (block?.type === 'code') {
        const characters = Array.from(block.content ?? '');
        const insertion = '  ';
        const nextContent = [
          ...characters.slice(0, selection?.start ?? 0),
          ...Array.from(insertion),
          ...characters.slice(selection?.end ?? 0),
        ].join('');
        const nextDocument = updateWriterBlockContent(
          document,
          block.node_id,
          nextContent,
        );
        if (nextDocument === document || !selection) return;
        const nextOffset = selection.start + insertion.length;
        const nextSelection = {
          nodeId: block.node_id,
          start: nextOffset,
          end: nextOffset,
        };
        savedSelectionRef.current = nextSelection;
        pendingSelectionRef.current = nextSelection;
        setActiveSelection(nextSelection);
        lastEmittedDocumentRef.current = undefined;
        onChange(nextDocument);
        return;
      }
      applyBlockIndent(event.shiftKey ? 'out' : 'in');
      return;
    }

    if (!(event.metaKey || event.ctrlKey)) return;
    const key = event.key.toLowerCase();
    if (key !== 'b' && key !== 'i') return;
    event.preventDefault();
    recordSelection();
    applyInlineStyle(key === 'b' ? 'strong' : 'italic');
  };

  const handleEditorFocus = () => {
    onFocus();
    window.requestAnimationFrame(recordSelection);
  };

  const handleKeyUp = (event: ReactKeyboardEvent<HTMLElement>) => {
    if (event.key === 'Enter') handledEnterKeyDownRef.current = false;
    recordSelection();
  };

  return (
    <div
      className='writer-ir__editor-shell'
      ref={shellRef}
      onBlur={handleBlur}
    >
      {showFormatToolbar && (
        <div
          ref={formatToolbarRef}
          className='writer-ir__format-toolbar writer-ir__format-toolbar--floating'
          role='toolbar'
          aria-label={t('chat.writerIR.formatToolbar')}
          style={formatToolbarStyle}
        >
          <div className='writer-ir__format-group'>
            <select
              className='writer-ir__format-select'
              value={blockFormatValue}
              onChange={handleBlockFormatChange}
              disabled={disabled || !canChangeBlockFormat}
              aria-label={t('chat.writerIR.blockStyle')}
              title={t('chat.writerIR.blockStyle')}
            >
              {!blockFormatValue && (
                <option value='' disabled>
                  {t('chat.writerIR.chooseBlockStyle')}
                </option>
              )}
              <option value='paragraph'>{t('chat.writerIR.paragraph')}</option>
              {Array.from({ length: 6 }, (_, index) => index + 1).map((level) => (
                <option value={`heading-${level}`} key={level}>
                  {t('chat.writerIR.headingLevelShort', { level })}
                </option>
              ))}
              <option value='unordered-list'>{t('chat.writerIR.unorderedList')}</option>
              <option value='ordered-list'>{t('chat.writerIR.orderedList')}</option>
              <option value='code'>{t('chat.writerIR.codeBlock')}</option>
            </select>
          </div>

          <span className='writer-ir__format-divider' aria-hidden='true' />

          <div className='writer-ir__format-group'>
            <button
              type='button'
              className='writer-ir__format-button'
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => applyListFormat(false)}
              disabled={disabled || !canChangeBlockFormat}
              aria-label={t('chat.writerIR.unorderedList')}
              aria-pressed={isUnorderedList}
              title={t('chat.writerIR.unorderedList')}
            >
              <UnorderedListOutlined aria-hidden />
            </button>
            <button
              type='button'
              className='writer-ir__format-button'
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => applyListFormat(true)}
              disabled={disabled || !canChangeBlockFormat}
              aria-label={t('chat.writerIR.orderedList')}
              aria-pressed={isOrderedList}
              title={t('chat.writerIR.orderedList')}
            >
              <OrderedListOutlined aria-hidden />
            </button>
          </div>

          <span className='writer-ir__format-divider' aria-hidden='true' />

          <div className='writer-ir__format-group'>
            <button
              type='button'
              className='writer-ir__format-button'
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => applyInlineStyle('strong')}
              disabled={disabled || !hasTextSelection || !canFormatBlock}
              aria-label={t('chat.writerIR.bold')}
              aria-pressed={selectionIsBold}
              title={`${t('chat.writerIR.bold')} ⌘B`}
            >
              <BoldOutlined aria-hidden />
            </button>
            <button
              type='button'
              className='writer-ir__format-button'
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => applyInlineStyle('italic')}
              disabled={disabled || !hasTextSelection || !canFormatBlock}
              aria-label={t('chat.writerIR.italic')}
              aria-pressed={selectionIsItalic}
              title={`${t('chat.writerIR.italic')} ⌘I`}
            >
              <ItalicOutlined aria-hidden />
            </button>
          </div>
        </div>
      )}
      <article
        ref={editorRef}
        className='writer-ir__document writer-ir__document--editable'
        contentEditable={!disabled}
        suppressContentEditableWarning
        role='textbox'
        aria-label={ariaLabel}
        aria-multiline='true'
        spellCheck
        onBeforeInput={handleBeforeInput}
        onInput={handleInput}
        onFocus={handleEditorFocus}
        onPaste={handlePaste}
        onKeyDown={handleKeyDown}
        onCompositionStart={() => {
          isComposingRef.current = true;
        }}
        onCompositionEnd={() => {
          isComposingRef.current = false;
        }}
        onMouseUp={recordSelection}
        onKeyUp={handleKeyUp}
      />
    </div>
  );
}
