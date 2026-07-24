import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
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
  getWriterOutlineInstruction,
  getWriterSpanStyles,
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
  const text = renderBlockText(block);
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
    const content = type === 'divider' ? '' : textFromElement(contentElement);
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
      spans: [{ text: content, style: [] }],
    };

    if (existing?.editable === false) return existing;

    const nestedContainers = childElements(element, '[data-writer-children], ul, ol');
    const children = nestedContainers.flatMap((container) => parseSequence(container));
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
      blocks.push(parseBlockElement(child));
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
  const lastEmittedDocumentRef = useRef<WriterDocument>();
  const savedSelectionRef = useRef<WriterEditorSelection | null>(null);
  const pendingSelectionRef = useRef<WriterEditorSelection | null>(null);
  const [activeSelection, setActiveSelection] = useState<WriterEditorSelection | null>(null);

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
  const canChangeBlockFormat = canFormatBlock && (activeBlock?.children?.length ?? 0) === 0;
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
    : activeBlock?.type === 'paragraph' || activeBlock?.type === 'code'
      ? activeBlock.type
      : '';

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
        : 'paragraph';
    const level = format === 'heading' ? Number(value.slice('heading-'.length)) : undefined;
    const nextDocument = updateWriterBlockFormat(
      document,
      activeBlock.node_id,
      format,
      level,
    );
    if (nextDocument === document) return;
    pendingSelectionRef.current = savedSelectionRef.current;
    onChange(nextDocument);
  };

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

  const handleKeyDown = (event: ReactKeyboardEvent<HTMLElement>) => {
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

  return (
    <div
      className='writer-ir__editor-shell'
      ref={shellRef}
      onBlur={handleBlur}
    >
      <div
        className='writer-ir__format-toolbar'
        role='toolbar'
        aria-label={t('chat.writerIR.formatToolbar')}
      >
        <label className='writer-ir__format-select-label'>
          <span>{t('chat.writerIR.blockStyle')}</span>
          <select
            value={blockFormatValue}
            onChange={handleBlockFormatChange}
            disabled={disabled || !canChangeBlockFormat}
            aria-label={t('chat.writerIR.blockStyle')}
          >
            {!blockFormatValue && (
              <option value='' disabled>
                {t('chat.writerIR.chooseBlockStyle')}
              </option>
            )}
            <option value='paragraph'>{t('chat.writerIR.paragraph')}</option>
            {Array.from({ length: 6 }, (_, index) => index + 1).map((level) => (
              <option value={`heading-${level}`} key={level}>
                {t('chat.writerIR.headingLevel', { level })}
              </option>
            ))}
            <option value='code'>{t('chat.writerIR.codeBlock')}</option>
          </select>
        </label>
        <button
          type='button'
          className='writer-ir__format-button'
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => applyInlineStyle('strong')}
          disabled={disabled || !hasTextSelection || !canFormatBlock}
          aria-label={t('chat.writerIR.bold')}
          aria-pressed={selectionIsBold}
          title={t('chat.writerIR.bold')}
        >
          <strong>B</strong>
        </button>
        <button
          type='button'
          className='writer-ir__format-button'
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => applyInlineStyle('italic')}
          disabled={disabled || !hasTextSelection || !canFormatBlock}
          aria-label={t('chat.writerIR.italic')}
          aria-pressed={selectionIsItalic}
          title={t('chat.writerIR.italic')}
        >
          <em>I</em>
        </button>
      </div>
      <article
        ref={editorRef}
        className='writer-ir__document writer-ir__document--editable'
        contentEditable={!disabled}
        suppressContentEditableWarning
        role='textbox'
        aria-label={ariaLabel}
        aria-multiline='true'
        spellCheck
        onInput={handleInput}
        onFocus={handleEditorFocus}
        onPaste={handlePaste}
        onKeyDown={handleKeyDown}
        onMouseUp={recordSelection}
        onKeyUp={recordSelection}
      />
    </div>
  );
}
