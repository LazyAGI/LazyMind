import {
  useLayoutEffect,
  useRef,
  type ClipboardEvent as ReactClipboardEvent,
  type FocusEvent as ReactFocusEvent,
  type FormEvent,
} from 'react';
import {
  countWriterBlocks,
  createWriterParagraph,
  findWriterBlock,
  getWriterSpanStyles,
  updateWriterBlockContent,
  updateWriterDocumentTitle,
  type WriterBlock,
  type WriterDocument,
  type WriterSpan,
} from './writerIR';

interface WriterIRDocumentEditorProps {
  document: WriterDocument;
  ariaLabel: string;
  onChange: (document: WriterDocument) => void;
  onFocus: () => void;
  onBlur: () => void;
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
  if (styles.includes('bold')) content = `<strong>${content}</strong>`;
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
  return Number.isFinite(level) ? Math.min(6, Math.max(2, Math.trunc(level))) : 2;
}

function renderBlockSequence(blocks: WriterBlock[]): string {
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
        items.push(renderBlock(blocks[index]));
        index += 1;
      }
      rendered.push(`<${tag} class="writer-ir__list">${items.join('')}</${tag}>`);
      continue;
    }

    rendered.push(renderBlock(block));
    index += 1;
  }

  return rendered.join('');
}

function renderBlock(block: WriterBlock): string {
  if (block.type === 'document') {
    return [
      `<section data-writer-document-root="${escapeHtmlAttribute(block.node_id)}"`,
      ` class="writer-ir__document-root">`,
      renderBlockSequence(block.children ?? []),
      '</section>',
    ].join('');
  }

  const attributes = [
    `data-writer-block="true"`,
    `data-node-id="${escapeHtmlAttribute(block.node_id)}"`,
    `data-node-type="${escapeHtmlAttribute(block.type)}"`,
    `class="writer-ir__block writer-ir__block--${escapeHtmlAttribute(block.type)}"`,
    block.editable === false ? 'contenteditable="false"' : '',
  ].filter(Boolean).join(' ');
  const text = renderBlockText(block);
  const children = block.children?.length
    ? block.type === 'list_item'
      ? renderBlockSequence(block.children)
      : `<div data-writer-children="true" class="writer-ir__children">${renderBlockSequence(block.children)}</div>`
    : '';

  if (block.type === 'heading') {
    const level = headingLevel(block);
    return [
      `<div ${attributes}>`,
      `<h${level} data-writer-block-content="true" class="writer-ir__heading writer-ir__heading--${level}">${text}</h${level}>`,
      children,
      '</div>',
    ].join('');
  }
  if (block.type === 'code') {
    return `<div ${attributes}><pre data-writer-block-content="true" class="writer-ir__code"><code>${text}</code></pre>${children}</div>`;
  }
  if (block.type === 'paragraph') {
    return `<div ${attributes}><p data-writer-block-content="true" class="writer-ir__paragraph">${text}</p>${children}</div>`;
  }
  if (block.type === 'quote') {
    return `<div ${attributes}><blockquote data-writer-block-content="true" class="writer-ir__quote">${text}</blockquote>${children}</div>`;
  }
  if (block.type === 'divider') {
    return `<div ${attributes}><hr data-writer-block-content="true" class="writer-ir__divider"></div>`;
  }
  if (block.type === 'list_item') {
    return `<li ${attributes}><span data-writer-block-content="true">${text}</span>${children}</li>`;
  }
  return `<div ${attributes}><div data-writer-block-content="true" class="writer-ir__fallback">${text}</div>${children}</div>`;
}

function renderDocument(document: WriterDocument): string {
  return [
    `<h1 class="writer-ir__title" data-writer-document-title="true">`,
    escapeHtml(document.title),
    '</h1>',
    renderBlockSequence(document.blocks),
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

function parseEditorDocument(editor: HTMLElement, source: WriterDocument): WriterDocument {
  const titledDocument = updateWriterDocumentTitle(
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
    const content = type === 'divider' ? '' : textFromElement(element);
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
      ? { ...(template.numbering ?? {}), level: Number(element.tagName.slice(1)) || 2 }
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

export function WriterIRDocumentEditor({
  document,
  ariaLabel,
  onChange,
  onFocus,
  onBlur,
}: WriterIRDocumentEditorProps) {
  const editorRef = useRef<HTMLElement | null>(null);
  const lastEmittedDocumentRef = useRef<WriterDocument>();

  useLayoutEffect(() => {
    const editor = editorRef.current;
    if (!editor || lastEmittedDocumentRef.current === document) return;
    const html = renderDocument(document);
    if (editor.innerHTML !== html) editor.innerHTML = html;
    lastEmittedDocumentRef.current = undefined;
  }, [document]);

  const handleInput = (event: FormEvent<HTMLElement>) => {
    const nextDocument = parseEditorDocument(event.currentTarget, document);
    lastEmittedDocumentRef.current = nextDocument;
    onChange(nextDocument);
  };

  const handleBlur = (event: ReactFocusEvent<HTMLElement>) => {
    if (event.relatedTarget instanceof Node && event.currentTarget.contains(event.relatedTarget)) return;
    onBlur();
  };

  const handlePaste = (event: ReactClipboardEvent<HTMLElement>) => {
    event.preventDefault();
    globalThis.document.execCommand('insertText', false, event.clipboardData.getData('text/plain'));
  };

  return (
    <article
      ref={editorRef}
      className='writer-ir__document writer-ir__document--editable'
      contentEditable
      suppressContentEditableWarning
      role='textbox'
      aria-label={ariaLabel}
      aria-multiline='true'
      spellCheck
      onInput={handleInput}
      onFocus={onFocus}
      onBlur={handleBlur}
      onPaste={handlePaste}
    />
  );
}
