import {
  BoldOutlined,
  CodeOutlined,
  DisconnectOutlined,
  DownOutlined,
  FontSizeOutlined,
  ItalicOutlined,
  LinkOutlined,
  OrderedListOutlined,
  PictureOutlined,
  TableOutlined,
  UnorderedListOutlined,
} from '@ant-design/icons';
import { Dropdown } from 'antd';
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type ChangeEvent,
  type ClipboardEvent as ReactClipboardEvent,
  type FocusEvent as ReactFocusEvent,
  type FormEvent,
  type KeyboardEvent as ReactKeyboardEvent,
  type MouseEvent as ReactMouseEvent,
} from 'react';
import { useTranslation } from 'react-i18next';
import {
  applyWriterBlockInternalReference,
  applyWriterBlockSpanColor,
  collectWriterReferenceTargets,
  countWriterBlocks,
  createWriterParagraph,
  findWriterBlock,
  findWriterBlockParent,
  getWriterInternalReference,
  getWriterSpanColor,
  getWriterSpanStyles,
  convertWriterBlockToParagraph,
  indentWriterBlock,
  isWriterSystemAnchorBlock,
  insertWriterChildParagraph,
  liftWriterBlockAfterParent,
  normalizeWriterCodeLanguage,
  relocateWriterBlock,
  removeWriterBlockInternalReference,
  sameWriterDocument,
  sameWriterDocumentForSync,
  splitWriterBlock,
  splitWriterHeadingIntoChild,
  toggleWriterBlockInlineStyle,
  updateWriterBlockContent,
  updateWriterBlockFormat,
  updateWriterCodeLanguage,
  updateWriterDocumentTitle,
  WRITER_BACKGROUND_DARK_PALETTE,
  WRITER_BACKGROUND_LIGHT_PALETTE,
  WRITER_DEFAULT_TEXT_COLOR_HEX,
  WRITER_TEXT_COLOR_PALETTE,
  writerBackgroundColorHex,
  writerBlockRangeHasInlineStyle,
  writerBlockRangeHasInternalReference,
  writerBlockRangeSpanColor,
  writerTextColorHex,
  type WriterBlockFormat,
  type WriterBlock,
  type WriterBlockRelocateTarget,
  type WriterDocument,
  type WriterInlineStyle,
  type WriterSpan,
  type WriterSpanColorField,
} from './writerIR';
import { ArtifactRewriteInlineDiff } from './ArtifactRewriteDialog';
import { ArtifactRewriteSelectionHighlight } from './ArtifactRewriteSelectionHighlight';
import { selectionActionAnchor, type SelectionActionAnchor } from './artifactRewriteSelection';
import { highlightCode } from '../MarkdownViewer/syntaxHighlight';
import type {
  RewriteSelectionPreview,
  WriterNumberingState,
  WriterNumberingUpdate,
} from '@/modules/chat/utils/request';
import { resolveMarkdownImageUrlAsync } from '@/modules/knowledge/utils/imageUrl';
import { WriterHeadingNumberingMenu } from './WriterHeadingNumberingMenu';

const WRITER_CODE_LANGUAGES = [
  ['text', 'Plain text'],
  ['javascript', 'JavaScript'],
  ['typescript', 'TypeScript'],
  ['jsx', 'JSX'],
  ['tsx', 'TSX'],
  ['python', 'Python'],
  ['java', 'Java'],
  ['go', 'Go'],
  ['rust', 'Rust'],
  ['c', 'C'],
  ['cpp', 'C++'],
  ['csharp', 'C#'],
  ['sql', 'SQL'],
  ['bash', 'Shell'],
  ['json', 'JSON'],
  ['yaml', 'YAML'],
  ['markup', 'HTML / XML'],
  ['css', 'CSS'],
  ['markdown', 'Markdown'],
  ['docker', 'Dockerfile'],
] as const;

function imageReferencePath(block: WriterBlock): string {
  const reference = block.references?.find((item) => item.type === 'preview_asset')
    ?? block.references?.find((item) => item.type === 'media_asset');
  const value = reference?.url ?? reference?.path;
  return typeof value === 'string' ? value.trim() : '';
}

export interface WriterIRRewriteSelection {
  nodeId: string;
  selectedText: string;
  anchor?: SelectionActionAnchor;
}

interface WriterIRDocumentEditorProps {
  document: WriterDocument;
  numbering?: WriterNumberingState;
  ariaLabel: string;
  onChange: (document: WriterDocument) => void;
  onCrossReferenceApplied?: (document: WriterDocument) => void;
  onNumberingUpdate?: (update: WriterNumberingUpdate) => void;
  onFocus: () => void;
  onBlur: () => void;
  disabled?: boolean;
  rewriteDialogOpen?: boolean;
  onRewriteSelection?: (selection: WriterIRRewriteSelection) => void;
  rewritePreview?: WriterIRRewritePreview | null;
  onRewritePreviewApplied?: (revision?: number) => void;
  onRewritePreviewRejected?: () => void;
}

interface WriterNumberingMenuState {
  nodeId: string;
  x: number;
  y: number;
}

export interface WriterIRRewritePreview {
  nodeId: string;
  sessionId: string;
  slotId: string;
  listIndex: number;
  preview: RewriteSelectionPreview;
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

function writerBlockDomId(nodeId: string): string {
  return `writer-block-${nodeId}`;
}

function writerReferenceTargetIcon(type: string) {
  if (type === 'image') return <PictureOutlined aria-hidden />;
  if (type === 'table') return <TableOutlined aria-hidden />;
  if (type === 'code') return <CodeOutlined aria-hidden />;
  return <FontSizeOutlined aria-hidden />;
}

function renderSpan(span: WriterSpan): string {
  const reference = getWriterInternalReference(span);
  let content = escapeHtml(reference?.displayText ?? span.text);
  const styles = getWriterSpanStyles(span);
  if (styles.includes('code')) content = `<code>${content}</code>`;
  if (styles.includes('strong') || styles.includes('bold')) content = `<strong>${content}</strong>`;
  if (styles.includes('italic')) content = `<em>${content}</em>`;
  if (styles.includes('underline')) content = `<u>${content}</u>`;
  if (styles.includes('strike') || styles.includes('strikethrough')) {
    content = `<s>${content}</s>`;
  }

  const textColorId = getWriterSpanColor(span, 'text_color');
  const backgroundColorId = getWriterSpanColor(span, 'background_color');
  const textColor = writerTextColorHex(textColorId);
  const backgroundColor = writerBackgroundColorHex(backgroundColorId);
  if (textColor || backgroundColor) {
    const cssParts: string[] = [];
    if (textColor) cssParts.push(`color:${textColor}`);
    if (backgroundColor) cssParts.push(`background-color:${backgroundColor}`);
    const attrs = [
      'data-writer-colored="true"',
      textColorId ? `data-writer-text-color="${textColorId}"` : '',
      backgroundColorId ? `data-writer-background-color="${backgroundColorId}"` : '',
      `style="${escapeHtmlAttribute(cssParts.join(';'))}"`,
    ].filter(Boolean).join(' ');
    content = `<span ${attrs}>${content}</span>`;
  }
  if (reference) {
    const targetNodeId = escapeHtmlAttribute(reference.targetNodeId);
    const targetId = escapeHtmlAttribute(writerBlockDomId(reference.targetNodeId));
    content = `<a class="writer-ir__internal-ref" href="#${targetId}" data-writer-internal-ref="${targetNodeId}" contenteditable="false">${content}</a>`;
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

function headingSectionEndIndex(blocks: WriterBlock[], headingIndex: number): number {
  const heading = blocks[headingIndex];
  if (!heading || heading.type !== 'heading') return headingIndex + 1;
  const level = headingLevel(heading);
  let end = headingIndex + 1;
  while (end < blocks.length) {
    const next = blocks[end];
    if (next.type === 'heading' && headingLevel(next) <= level) break;
    end += 1;
  }
  return end;
}

function headingIsFoldable(blocks: WriterBlock[], headingIndex: number): boolean {
  const heading = blocks[headingIndex];
  if (!heading || heading.type !== 'heading') return false;
  if ((heading.children?.length ?? 0) > 0) return true;
  return headingSectionEndIndex(blocks, headingIndex) > headingIndex + 1;
}

interface WriterFoldLabels {
  collapse: string;
  expand: string;
}

interface WriterEditorLabels extends WriterFoldLabels {
  codeBlock: string;
  codeLanguage: string;
  collapseCode: string;
  expandCode: string;
  enableCodeWrap: string;
  disableCodeWrap: string;
  copyCode: string;
  codeCopied: string;
  outlineInstructions: string;
  targetChars: string;
  contextRelations: string;
  writingSubtasks: string;
  subtaskTypes: Record<'retrieve' | 'extract' | 'reason', string>;
}

const DEFAULT_WRITER_EDITOR_LABELS: WriterEditorLabels = {
  collapse: 'Collapse',
  expand: 'Expand',
  codeBlock: 'Code block',
  codeLanguage: 'Code language',
  collapseCode: 'Collapse code',
  expandCode: 'Expand code',
  enableCodeWrap: 'Wrap lines',
  disableCodeWrap: 'Disable wrapping',
  copyCode: 'Copy',
  codeCopied: 'Copied',
  outlineInstructions: 'Writing instructions',
  targetChars: 'Length',
  contextRelations: 'Context',
  writingSubtasks: 'Subtasks',
  subtaskTypes: {
    retrieve: 'Retrieve',
    extract: 'Extract',
    reason: 'Reason',
  },
};

function renderOutlineInstructions(block: WriterBlock, labels: WriterEditorLabels): string {
  const targetChars = Number(block.target_chars);
  const contextRelations = block.context_relations ?? [];
  const subtasks = block.subtasks ?? [];
  if (!(targetChars > 0) && contextRelations.length === 0 && subtasks.length === 0) return '';

  const targetMarkup = targetChars > 0
    ? `<div class="writer-ir__outline-instruction-row"><strong>${escapeHtml(labels.targetChars)}</strong><span>${Math.trunc(targetChars)}</span></div>`
    : '';
  const contextMarkup = contextRelations.length > 0
    ? `<div class="writer-ir__outline-instruction-group"><strong>${escapeHtml(labels.contextRelations)}</strong><ul>${contextRelations.map((item) => {
      const guidance = item.guidance?.trim();
      const relation = item.relation?.trim();
      const target = item.target_node_id?.trim();
      const detail = guidance || [relation, target].filter(Boolean).join(' / ');
      return `<li>${escapeHtml(detail || '-')}</li>`;
    }).join('')}</ul></div>`
    : '';
  const subtaskMarkup = subtasks.length > 0
    ? `<div class="writer-ir__outline-instruction-group"><strong>${escapeHtml(labels.writingSubtasks)}</strong><ul>${subtasks.map((item) => {
      const type = labels.subtaskTypes[item.subtask_type] ?? item.subtask_type;
      return `<li><span class="writer-ir__outline-subtask-type">${escapeHtml(type)}</span>${escapeHtml(item.question)}</li>`;
    }).join('')}</ul></div>`
    : '';

  return [
    '<details class="writer-ir__outline-instructions" data-writer-outline-instructions="true" contenteditable="false" open>',
    `<summary>${escapeHtml(labels.outlineInstructions)}</summary>`,
    '<div class="writer-ir__outline-instruction-body">',
    targetMarkup,
    contextMarkup,
    subtaskMarkup,
    '</div>',
    '</details>',
  ].join('');
}

function renderFoldToggle(
  nodeId: string,
  collapsed: boolean,
  labels: WriterFoldLabels,
): string {
  const label = collapsed ? labels.expand : labels.collapse;
  return [
    `<button type="button"`,
    ` class="writer-ir__fold-toggle${collapsed ? ' writer-ir__fold-toggle--collapsed' : ''}"`,
    ` data-writer-fold-toggle="true"`,
    ` data-fold-node-id="${escapeHtmlAttribute(nodeId)}"`,
    ` data-fold-collapsed="${collapsed ? 'true' : 'false'}"`,
    ` contenteditable="false"`,
    ` tabindex="-1"`,
    ` aria-label="${escapeHtmlAttribute(label)}"`,
    ` title="${escapeHtmlAttribute(label)}"`,
    `>`,
    `<span class="writer-ir__fold-caret" aria-hidden="true"></span>`,
    `</button>`,
  ].join('');
}

function renderDragHandle(nodeId: string, label: string): string {
  return [
    `<button type="button"`,
    ` class="writer-ir__drag-handle"`,
    ` data-writer-drag-handle="true"`,
    ` data-drag-node-id="${escapeHtmlAttribute(nodeId)}"`,
    ` contenteditable="false"`,
    ` tabindex="-1"`,
    ` aria-label="${escapeHtmlAttribute(label)}"`,
    ` title="${escapeHtmlAttribute(label)}"`,
    `>`,
    `<span class="writer-ir__drag-grip" aria-hidden="true"></span>`,
    `</button>`,
  ].join('');
}

function renderCodeLanguageSelect(block: WriterBlock, label: string): string {
  const language = normalizeWriterCodeLanguage(block.language);
  const knownLanguage = WRITER_CODE_LANGUAGES.some(([value]) => value === language);
  const options = [
    ...(!knownLanguage
      ? [`<option value="${escapeHtmlAttribute(language)}">${escapeHtml(language)}</option>`]
      : []),
    ...WRITER_CODE_LANGUAGES.map(([value, name]) => (
      `<option value="${value}"${value === language ? ' selected' : ''}>${name}</option>`
    )),
  ].join('');
  return [
    `<select class="writer-ir__code-language" data-writer-code-language="${escapeHtmlAttribute(block.node_id)}" data-writer-code-language-editable="${block.editable !== false ? 'true' : 'false'}"`,
    ` aria-label="${escapeHtmlAttribute(label)}" title="${escapeHtmlAttribute(label)}"`,
    block.editable === false ? ' disabled' : '',
    `>${options}</select>`,
  ].join('');
}

function codeLineCount(content: string): number {
  return Math.max(1, content.split('\n').length);
}

function codeLineNumbers(content: string): string {
  const lineCount = codeLineCount(content);
  return Array.from({ length: lineCount }, (_, index) => index + 1).join('\n');
}

function codeContentHeight(content: string): string {
  return `${codeLineCount(content) * 1.7}em`;
}

function renderCodeTrailingCaret(content: string): string {
  if (content && !content.endsWith('\n')) return '';
  return [
    '<span class="writer-ir__code-trailing-line" data-writer-code-trailing-line="true">',
    '<span data-writer-code-trailing-caret="true" contenteditable="false">\u200b</span>',
    '</span>',
  ].join('');
}

function renderCodeToolbar(block: WriterBlock, labels: WriterEditorLabels): string {
  return [
    '<div class="writer-ir__code-header" contenteditable="false">',
    `<button type="button" class="writer-ir__code-tool writer-ir__code-title" data-writer-code-action="collapse" data-code-node-id="${escapeHtmlAttribute(block.node_id)}" aria-expanded="true" title="${escapeHtmlAttribute(labels.collapseCode)}">`,
    '<span class="writer-ir__code-collapse-caret" aria-hidden="true"></span>',
    `<span>${escapeHtml(labels.codeBlock)}</span>`,
    '</button>',
    '<div class="writer-ir__code-actions">',
    renderCodeLanguageSelect(block, labels.codeLanguage),
    `<button type="button" class="writer-ir__code-tool writer-ir__code-wrap" data-writer-code-action="wrap" data-code-node-id="${escapeHtmlAttribute(block.node_id)}" aria-pressed="true" title="${escapeHtmlAttribute(labels.disableCodeWrap)}">`,
    '<span class="writer-ir__code-wrap-icon" aria-hidden="true"></span>',
    `<span data-writer-code-action-label="true">${escapeHtml(labels.disableCodeWrap)}</span>`,
    '</button>',
    `<button type="button" class="writer-ir__code-tool writer-ir__code-copy" data-writer-code-action="copy" data-code-node-id="${escapeHtmlAttribute(block.node_id)}" title="${escapeHtmlAttribute(labels.copyCode)}">`,
    '<span class="writer-ir__code-copy-icon" aria-hidden="true"></span>',
    `<span data-writer-code-action-label="true">${escapeHtml(labels.copyCode)}</span>`,
    '</button>',
    '</div>',
    '</div>',
  ].join('');
}

function renderBlockSequence(
  blocks: WriterBlock[],
  collapsedNodeIds: ReadonlySet<string> = new Set(),
  foldLabels: WriterEditorLabels,
  dragLabel: string,
  numbering?: WriterNumberingState,
): string {
  const rendered: string[] = [];
  let suppressBelowLevel: number | null = null;

  for (let index = 0; index < blocks.length;) {
    const block = blocks[index];
    if (block.type === 'list_item') {
      const ordered = Boolean(block.numbering?.ordered);
      const tag = ordered ? 'ol' : 'ul';
      const items: string[] = [];
      const listHidden = suppressBelowLevel !== null;
      while (
        index < blocks.length
        && blocks[index].type === 'list_item'
        && Boolean(blocks[index].numbering?.ordered) === ordered
      ) {
        items.push(renderBlock(
          blocks[index],
          collapsedNodeIds,
          foldLabels,
          dragLabel,
          listHidden,
          undefined,
          numbering,
        ));
        index += 1;
      }
      rendered.push(
        `<${tag} class="writer-ir__list${listHidden ? ' writer-ir__section-hidden' : ''}"${
          listHidden ? ' hidden' : ''
        }>${items.join('')}</${tag}>`,
      );
      continue;
    }

    if (block.type === 'heading') {
      const level = headingLevel(block);
      if (suppressBelowLevel !== null && level <= suppressBelowLevel) {
        suppressBelowLevel = null;
      }
      const hiddenByAncestor = suppressBelowLevel !== null;
      const foldable = !hiddenByAncestor && headingIsFoldable(blocks, index);
      const collapsed = foldable && collapsedNodeIds.has(block.node_id);
      rendered.push(renderBlock(
        block,
        collapsedNodeIds,
        foldLabels,
        dragLabel,
        hiddenByAncestor,
        { foldable, collapsed },
        numbering,
      ));
      // Nested sections hide via block.children. Only flat heading sequences (no children)
      // suppress following siblings until the next same/higher heading.
      if (collapsed && (block.children?.length ?? 0) === 0) {
        suppressBelowLevel = level;
      }
      index += 1;
      continue;
    }

    rendered.push(renderBlock(
      block,
      collapsedNodeIds,
      foldLabels,
      dragLabel,
      suppressBelowLevel !== null,
      undefined,
      numbering,
    ));
    index += 1;
  }

  return rendered.join('');
}

function renderBlock(
  block: WriterBlock,
  collapsedNodeIds: ReadonlySet<string> = new Set(),
  foldLabels: WriterEditorLabels = DEFAULT_WRITER_EDITOR_LABELS,
  dragLabel = 'Drag',
  hiddenByAncestor = false,
  foldState?: { foldable: boolean; collapsed: boolean },
  numbering?: WriterNumberingState,
): string {
  if (block.type === 'document') {
    return [
      `<section data-writer-document-root="${escapeHtmlAttribute(block.node_id)}"`,
      ` class="writer-ir__document-root">`,
      renderBlockSequence(
        block.children ?? [],
        collapsedNodeIds,
        foldLabels,
        dragLabel,
        numbering,
      ),
      '</section>',
    ].join('');
  }

  if (isWriterSystemAnchorBlock(block)) {
    return [
      `<div data-writer-block="true"`,
      ` data-node-id="${escapeHtmlAttribute(block.node_id)}"`,
      ` data-node-type="paragraph"`,
      ` class="writer-ir__block writer-ir__block--system-anchor"`,
      ` contenteditable="false" hidden aria-hidden="true">`,
      `<p data-writer-block-content="true">${renderBlockText(block)}</p>`,
      '</div>',
    ].join('');
  }

  const collapsed = Boolean(foldState?.collapsed);
  const foldable = Boolean(foldState?.foldable);
  const draggable = block.editable !== false;
  const attributes = [
    `data-writer-block="true"`,
    `id="${escapeHtmlAttribute(writerBlockDomId(block.node_id))}"`,
    `data-node-id="${escapeHtmlAttribute(block.node_id)}"`,
    `data-node-type="${escapeHtmlAttribute(block.type)}"`,
    block.type === 'heading' ? `data-heading-level="${headingLevel(block)}"` : '',
    foldable ? 'data-writer-foldable="true"' : '',
    collapsed ? 'data-writer-folded="true"' : '',
    draggable ? 'data-writer-draggable="true"' : '',
    `class="writer-ir__block writer-ir__block--${escapeHtmlAttribute(block.type)}${
      foldable ? ' writer-ir__block--foldable' : ''
    }${draggable ? ' writer-ir__block--draggable' : ''}${
      collapsed ? ' writer-ir__block--folded' : ''
    }${hiddenByAncestor ? ' writer-ir__section-hidden' : ''}"`,
    block.editable === false ? 'contenteditable="false"' : '',
    hiddenByAncestor ? 'hidden' : '',
  ].filter(Boolean).join(' ');
  const text = renderEditableBlockText(block);
  const nestedCollapsed = collapsed;
  const children = block.children?.length
    ? block.type === 'list_item'
      ? renderBlockSequence(
        block.children,
        collapsedNodeIds,
        foldLabels,
        dragLabel,
        numbering,
      )
      : `<div data-writer-children="true" class="writer-ir__children${
        nestedCollapsed ? ' writer-ir__section-hidden' : ''
      }"${nestedCollapsed ? ' hidden' : ''}>${
        renderBlockSequence(
          block.children,
          collapsedNodeIds,
          foldLabels,
          dragLabel,
          numbering,
        )
      }</div>`
    : '';
  const foldToggle = foldable
    ? renderFoldToggle(block.node_id, collapsed, foldLabels)
    : '';
  const dragHandle = draggable
    ? renderDragHandle(block.node_id, dragLabel)
    : '';

  if (block.type === 'heading') {
    const level = headingLevel(block);
    const entry = numbering?.entries[block.node_id];
    const label = entry?.label;
    const numberingMode = entry?.mode ?? 'ordered';
    const headingText = renderEditableBlockText(block);
    const marker = label
      ? `<span class="writer-ir__numbering-marker" data-writer-numbering-marker="${escapeHtmlAttribute(block.node_id)}" contenteditable="false" role="button" tabindex="-1">${escapeHtml(label)}</span>`
      : '';
    return [
      `<div ${attributes}>`,
      foldToggle,
      dragHandle,
      `<h${level} data-writer-block-content="true" data-writer-heading-mode="${numberingMode}" class="writer-ir__heading writer-ir__heading--${level}">${marker}${headingText}</h${level}>`,
      renderOutlineInstructions(block, foldLabels),
      children,
      '</div>',
    ].join('');
  }
  if (block.type === 'code') {
    const language = normalizeWriterCodeLanguage(block.language);
    const content = block.content ?? '';
    const highlighted = highlightCode(content, language);
    const code = highlighted || (content ? text : '');
    return `<div ${attributes}>${dragHandle}<div class="writer-ir__code-shell" data-writer-code-shell="${escapeHtmlAttribute(block.node_id)}">${renderCodeToolbar(block, foldLabels)}<pre data-writer-block-content="true" class="writer-ir__code writer-ir__code--wrapped" data-line-numbers="${escapeHtmlAttribute(codeLineNumbers(content))}" style="--writer-code-content-height: ${codeContentHeight(content)}"><code class="language-${escapeHtmlAttribute(language)}">${code}${renderCodeTrailingCaret(content)}</code></pre></div>${children}</div>`;
  }
  if (block.type === 'paragraph') {
    return `<div ${attributes}>${dragHandle}<p data-writer-block-content="true" class="writer-ir__paragraph">${text}</p>${children}</div>`;
  }
  if (block.type === 'quote') {
    return `<div ${attributes}>${dragHandle}<blockquote data-writer-block-content="true" class="writer-ir__quote">${text}</blockquote>${children}</div>`;
  }
  if (block.type === 'divider') {
    return `<div ${attributes}>${dragHandle}<hr data-writer-block-content="true" class="writer-ir__divider"></div>`;
  }
  if (block.type === 'list_item') {
    return `<li ${attributes}>${dragHandle}<span data-writer-block-content="true">${text}</span>${children}</li>`;
  }
  if (block.type === 'image') {
    const source = imageReferencePath(block);
    const image = source
      ? `<img data-writer-image="true" data-writer-image-source="${escapeHtmlAttribute(source)}" alt="${escapeHtmlAttribute(block.content ?? '')}">`
      : '<div class="writer-ir__image-placeholder" aria-hidden="true"></div>';
    const caption = block.content?.trim()
      ? `<figcaption data-writer-block-content="true">${text}</figcaption>`
      : '<figcaption data-writer-block-content="true"></figcaption>';
    return `<div ${attributes}>${dragHandle}<figure class="writer-ir__image" contenteditable="false">${image}${caption}</figure>${children}</div>`;
  }
  return `<div ${attributes}>${dragHandle}<div data-writer-block-content="true" class="writer-ir__fallback">${text}</div>${children}</div>`;
}

function renderDocument(
  document: WriterDocument,
  collapsedNodeIds: ReadonlySet<string> = new Set(),
  foldLabels: WriterEditorLabels = DEFAULT_WRITER_EDITOR_LABELS,
  dragLabel = 'Drag',
  numbering?: WriterNumberingState,
): string {
  const documentRoot = document.blocks.find((block) => block.type === 'document');
  return [
    `<h1 class="writer-ir__title" data-writer-document-title="true"`,
    documentRoot?.editable === false ? ' contenteditable="false">' : '>',
    escapeHtml(document.title),
    '</h1>',
    renderBlockSequence(
      document.blocks,
      collapsedNodeIds,
      foldLabels,
      dragLabel,
      numbering,
    ),
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
  clone.querySelectorAll(
    '[data-writer-empty-placeholder], [data-writer-code-trailing-caret], [data-writer-numbering-marker]',
  ).forEach(
    (placeholder) => placeholder.remove(),
  );
  for (const child of Array.from(clone.children)) {
    if (
      child.matches('[data-writer-children]')
      || child.matches('[data-writer-fold-toggle]')
      || child.matches('[data-writer-drag-handle]')
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
      && child.matches('[data-writer-outline-instructions]')
    ) {
      continue;
    }
    if (
      child instanceof HTMLElement
      && child.matches(
        '[data-writer-block], [data-writer-children], ul, ol',
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
  if (element.dataset.nodeType === 'image') {
    return element.querySelector<HTMLElement>(
      ':scope > figure > figcaption[data-writer-block-content]',
    ) ?? element;
  }
  return childElements(element, '[data-writer-block-content]')[0]
    ?? element.querySelector<HTMLElement>(
      ':scope > .writer-ir__code-shell > [data-writer-block-content]',
    )
    ?? element;
}

/** Align fold/drag controls to the first text line of each block (avoids CSS em/margin drift). */
function syncBlockControlPositions(editor: HTMLElement) {
  editor.querySelectorAll<HTMLElement>('[data-writer-block]').forEach((block) => {
    const content = childElements(block, '[data-writer-block-content]')[0];
    if (!content) return;
    const fold = childElements(block, '[data-writer-fold-toggle]')[0];
    const drag = childElements(block, '[data-writer-drag-handle]')[0];
    if (!fold && !drag) return;

    const styles = window.getComputedStyle(content);
    const fontSize = Number.parseFloat(styles.fontSize) || 14;
    let lineHeight = Number.parseFloat(styles.lineHeight);
    if (!Number.isFinite(lineHeight)) lineHeight = fontSize * 1.45;
    const paddingTop = Number.parseFloat(styles.paddingTop) || 0;
    const top = content.offsetTop + paddingTop;
    const height = Math.max(18, Math.min(lineHeight, content.offsetHeight || lineHeight));

    for (const control of [fold, drag]) {
      if (!control) continue;
      control.style.top = `${top}px`;
      control.style.height = `${height}px`;
      control.style.minHeight = '0';
    }
  });
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
      if (child.matches('[data-writer-fold-toggle]')) continue;
      if (child.matches('[data-writer-drag-handle]')) continue;
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

type WriterSelectAllScope =
  | { type: 'code'; nodeId: string }
  | { type: 'editor' }
  | null;

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

  const textOffset = (textRange: Range): number => {
    const contents = textRange.cloneContents();
    const ignoredLength = Array.from(
      contents.querySelectorAll<HTMLElement>('[data-writer-code-trailing-caret]'),
    ).reduce(
      (length, placeholder) => length + Array.from(placeholder.textContent ?? '').length,
      0,
    );
    return Math.max(0, Array.from(textRange.toString()).length - ignoredLength);
  };

  return {
    nodeId: startBlock.dataset.nodeId!,
    start: textOffset(beforeStart),
    end: textOffset(beforeEnd),
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
  const trailingLine = contentElement.querySelector<HTMLElement>(
    '[data-writer-code-trailing-line]',
  );
  if (
    trailingLine
    && !textFromElement(trailingLine)
    && offset >= Array.from(textFromElement(contentElement)).length
  ) {
    return { node: trailingLine, offset: 0 };
  }

  const walker = globalThis.document.createTreeWalker(
    contentElement,
    NodeFilter.SHOW_TEXT,
    {
      acceptNode: (node) => (
        node.parentElement?.closest('[data-writer-code-trailing-caret]')
          ? NodeFilter.FILTER_REJECT
          : NodeFilter.FILTER_ACCEPT
      ),
    },
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

function closestVerticalScrollContainer(editor: HTMLElement): HTMLElement | null {
  let scroller: HTMLElement | null = editor.parentElement;
  while (scroller) {
    const style = globalThis.getComputedStyle(scroller);
    const canScrollY = /(auto|scroll|overlay)/.test(style.overflowY);
    if (canScrollY && scroller.scrollHeight > scroller.clientHeight) return scroller;
    scroller = scroller.parentElement;
  }
  return null;
}

function scrollSelectionIntoView(editor: HTMLElement): void {
  const selection = globalThis.getSelection();
  if (!selection || selection.rangeCount === 0 || !editor.contains(selection.anchorNode)) {
    return;
  }

  const range = selection.getRangeAt(0);
  const anchorElement = (
    range.startContainer instanceof Element
      ? range.startContainer
      : range.startContainer.parentElement
  );
  const target = anchorElement?.closest<HTMLElement>('[data-writer-block][data-node-id]')
    ?? (anchorElement instanceof HTMLElement ? anchorElement : null);
  const rangeRect = typeof range.getBoundingClientRect === 'function'
    ? range.getBoundingClientRect()
    : undefined;
  const hasRangeRect = Boolean(
    rangeRect
    && (rangeRect.width !== 0 || rangeRect.height !== 0 || rangeRect.top !== 0),
  );
  const rect = hasRangeRect
    ? rangeRect!
    : target
      ? blockContentElement(target).getBoundingClientRect()
      : undefined;
  if (!rect || (rect.width === 0 && rect.height === 0 && rect.top === 0)) return;
  const scroller = closestVerticalScrollContainer(editor);
  if (!scroller) return;

  const scrollerRect = scroller.getBoundingClientRect();
  const padding = 24;
  let nextScrollTop = scroller.scrollTop;
  if (rect.bottom > scrollerRect.bottom - padding) {
    nextScrollTop += rect.bottom - (scrollerRect.bottom - padding);
  } else if (rect.top < scrollerRect.top + padding) {
    nextScrollTop -= (scrollerRect.top + padding) - rect.top;
  }
  const maxScrollTop = Math.max(0, scroller.scrollHeight - scroller.clientHeight);
  scroller.scrollTop = Math.min(maxScrollTop, Math.max(0, nextScrollTop));
}

function writerEditorSelectionRange(
  editor: HTMLElement,
  savedSelection: WriterEditorSelection,
): Range | null {
  const block = findRenderedBlock(editor, savedSelection.nodeId);
  if (!block) return null;
  const contentElement = blockContentElement(block);
  const start = textBoundaryAt(contentElement, savedSelection.start);
  const end = textBoundaryAt(contentElement, savedSelection.end);
  const range = globalThis.document.createRange();
  range.setStart(start.node, start.offset);
  range.setEnd(end.node, end.offset);
  return range;
}

function restoreEditorSelection(
  editor: HTMLElement,
  savedSelection: WriterEditorSelection,
  options?: { scrollIntoView?: boolean },
): void {
  const range = writerEditorSelectionRange(editor, savedSelection);
  if (!range) return;
  // Focus first so the browser cannot replace the restored Range with its
  // default contenteditable caret while processing focus.
  editor.focus({ preventScroll: true });
  const selection = globalThis.getSelection();
  selection?.removeAllRanges();
  selection?.addRange(range);
  if (options?.scrollIntoView) scrollSelectionIntoView(editor);
}

function selectWriterEditorContents(editor: HTMLElement): void {
  const range = globalThis.document.createRange();
  range.selectNodeContents(editor);
  editor.focus({ preventScroll: true });
  const selection = globalThis.getSelection();
  selection?.removeAllRanges();
  selection?.addRange(range);
}

function isWriterEditorFullySelected(editor: HTMLElement): boolean {
  const selection = globalThis.getSelection();
  if (!selection || selection.isCollapsed || selection.rangeCount !== 1) return false;
  const range = selection.getRangeAt(0);
  return range.startContainer === editor
    && range.startOffset === 0
    && range.endContainer === editor
    && range.endOffset === editor.childNodes.length;
}

function writerEditorPlainText(editor: HTMLElement): string {
  const title = editor.querySelector<HTMLElement>('[data-writer-document-title]')
    ?.textContent
    ?.replace(/\u00a0/g, ' ') ?? '';
  const blockText = Array.from(
    editor.querySelectorAll<HTMLElement>('[data-writer-block-content]'),
  ).flatMap((contentElement) => {
    if (contentElement.closest('[hidden]')) return [];
    const block = contentElement.closest<HTMLElement>('[data-writer-block]');
    if (block?.dataset.nodeType === 'divider') return ['---'];
    return [textFromElement(contentElement)];
  });
  return [title, ...blockText].join('\n');
}

function replaceWriterEditorContents(
  source: WriterDocument,
  content: string,
): { document: WriterDocument; nodeId: string } {
  const paragraph: WriterBlock = {
    ...createWriterParagraph(source.stage),
    content,
    spans: [{ text: content, style: {} }],
  };
  const documentRoot = source.blocks.find((block) => block.type === 'document');
  const blocks = documentRoot
    ? [{ ...documentRoot, children: [paragraph] }]
    : [paragraph];
  const metadata = source.metadata
    && Object.prototype.hasOwnProperty.call(source.metadata, 'block_count')
    ? { ...source.metadata, block_count: countWriterBlocks(blocks) }
    : source.metadata;
  return {
    document: {
      ...source,
      title: '',
      blocks,
      metadata,
    },
    nodeId: paragraph.node_id,
  };
}

export function WriterIRDocumentEditor({
  document,
  numbering,
  ariaLabel,
  onChange,
  onCrossReferenceApplied,
  onNumberingUpdate,
  onFocus,
  onBlur,
  disabled = false,
  rewriteDialogOpen = false,
  onRewriteSelection,
  rewritePreview,
  onRewritePreviewApplied,
  onRewritePreviewRejected,
}: WriterIRDocumentEditorProps) {
  const { t } = useTranslation();
  const shellRef = useRef<HTMLDivElement | null>(null);
  const editorRef = useRef<HTMLElement | null>(null);
  const formatToolbarRef = useRef<HTMLDivElement | null>(null);
  const [rewriteLayer, setRewriteLayer] = useState<HTMLDivElement | null>(null);
  const [rewriteTarget, setRewriteTarget] = useState<HTMLElement | null>(null);
  const lastEmittedDocumentRef = useRef<WriterDocument>();
  const lastRenderedDocumentRef = useRef<WriterDocument | undefined>(undefined);
  const lastRenderedNumberingRef = useRef<WriterNumberingState | undefined>();
  const savedSelectionRef = useRef<WriterEditorSelection | null>(null);
  const referenceSelectionRef = useRef<WriterEditorSelection | null>(null);
  const referenceMenuOpenRef = useRef(false);
  const pendingSelectionRef = useRef<WriterEditorSelection | null>(null);
  const isComposingRef = useRef(false);
  const handledEnterKeyDownRef = useRef(false);
  const selectAllScopeRef = useRef<WriterSelectAllScope>(null);
  const [activeSelection, setActiveSelection] = useState<WriterEditorSelection | null>(null);
  const [pinnedRewriteSelection, setPinnedRewriteSelection] = useState<WriterEditorSelection | null>(null);
  const rewriteDialogOpenRef = useRef(rewriteDialogOpen);
  const pinnedRewriteSelectionRef = useRef<WriterEditorSelection | null>(null);
  rewriteDialogOpenRef.current = rewriteDialogOpen;
  const [formatToolbarStyle, setFormatToolbarStyle] = useState<CSSProperties | undefined>();
  const [colorPanelOpen, setColorPanelOpen] = useState(false);
  const [referenceMenuOpen, setReferenceMenuOpen] = useState(false);
  const [numberingMenu, setNumberingMenu] = useState<WriterNumberingMenuState | null>(null);
  const [collapseVersion, setCollapseVersion] = useState(0);
  const [draggingNodeId, setDraggingNodeId] = useState<string | null>(null);
  const collapsedNodeIdsRef = useRef<Set<string>>(new Set());
  const lastCollapseVersionRef = useRef(0);
  const draggingNodeIdRef = useRef<string | null>(null);
  const dropHintRef = useRef<WriterBlockRelocateTarget | null>(null);
  const pendingReferenceTargetRef = useRef<string | null>(null);
  const foldLabels = useMemo<WriterEditorLabels>(() => ({
    collapse: t('chat.writerIR.collapseSection'),
    expand: t('chat.writerIR.expandSection'),
    codeBlock: t('chat.writerIR.codeBlock'),
    codeLanguage: t('chat.writerIR.codeLanguage'),
    collapseCode: t('chat.writerIR.collapseCode'),
    expandCode: t('chat.writerIR.expandCode'),
    enableCodeWrap: t('chat.writerIR.enableCodeWrap'),
    disableCodeWrap: t('chat.writerIR.disableCodeWrap'),
    copyCode: t('chat.writerIR.copyCode'),
    codeCopied: t('chat.writerIR.codeCopied'),
    outlineInstructions: t('chat.writerIR.outlineInstructions'),
    targetChars: t('chat.writerIR.targetChars'),
    contextRelations: t('chat.writerIR.contextRelations'),
    writingSubtasks: t('chat.writerIR.writingSubtasks'),
    subtaskTypes: {
      retrieve: t('chat.writerIR.subtaskTypes.retrieve'),
      extract: t('chat.writerIR.subtaskTypes.extract'),
      reason: t('chat.writerIR.subtaskTypes.reason'),
    },
  }), [t]);
  const dragLabel = t('chat.writerIR.dragBlock');

  useLayoutEffect(() => {
    const editor = editorRef.current;
    if (!editor) return;
    const hadPendingSelection = pendingSelectionRef.current != null;
    const collapseChanged = collapseVersion !== lastCollapseVersionRef.current;
    const numberingChanged = lastRenderedNumberingRef.current !== numbering;
    const documentUnchanged = lastEmittedDocumentRef.current === document
      || sameWriterDocument(lastRenderedDocumentRef.current, document)
      || sameWriterDocumentForSync(lastRenderedDocumentRef.current, document);

    if (documentUnchanged && !collapseChanged && !numberingChanged) {
      lastEmittedDocumentRef.current = document;
      lastRenderedDocumentRef.current = document;
      const pendingSelection = pendingSelectionRef.current;
      pendingSelectionRef.current = null;
      if (pendingSelection) {
        restoreEditorSelection(editor, pendingSelection, { scrollIntoView: true });
      }
      syncBlockControlPositions(editor);
      return;
    }

    const selectionToRestore = pendingSelectionRef.current ?? readEditorSelection(editor);
    const scrollContainer = closestVerticalScrollContainer(editor);
    const previousScrollTop = scrollContainer?.scrollTop;
    pendingSelectionRef.current = null;
    editor.innerHTML = renderDocument(
      document,
      collapsedNodeIdsRef.current,
      foldLabels,
      dragLabel,
      numbering,
    );
    lastEmittedDocumentRef.current = document;
    lastRenderedDocumentRef.current = document;
    lastRenderedNumberingRef.current = numbering;
    lastCollapseVersionRef.current = collapseVersion;
    syncBlockControlPositions(editor);
    if (scrollContainer && previousScrollTop != null) {
      scrollContainer.scrollTop = previousScrollTop;
    }
    if (selectionToRestore) {
      restoreEditorSelection(editor, selectionToRestore, {
        // Only follow the caret for user-driven structural edits (Enter/Tab/…).
        scrollIntoView: hadPendingSelection,
      });
    }
  }, [collapseVersion, document, dragLabel, foldLabels, numbering]);

  useEffect(() => {
    const editor = editorRef.current;
    if (!editor) return undefined;
    let cancelled = false;
    editor.querySelectorAll<HTMLImageElement>('img[data-writer-image-source]').forEach((image) => {
      const source = image.dataset.writerImageSource ?? '';
      if (!source) return;
      resolveMarkdownImageUrlAsync(source)
        .then((resolved) => {
          if (!cancelled && resolved) image.src = resolved;
        })
        .catch(() => {
          // Keep the caption visible when an individual image cannot be resolved.
        });
    });
    return () => { cancelled = true; };
  }, [collapseVersion, document]);

  useLayoutEffect(() => {
    const editor = editorRef.current;
    if (!editor || !rewritePreview) {
      setRewriteTarget(null);
      return;
    }
    const block = Array.from(editor.querySelectorAll<HTMLElement>('[data-writer-block][data-node-id]'))
      .find((element) => element.dataset.nodeId === rewritePreview.nodeId);
    setRewriteTarget(block ? blockContentElement(block) : null);
  }, [document, rewritePreview]);

  useEffect(() => {
    if (!rewriteDialogOpen) {
      pinnedRewriteSelectionRef.current = null;
      setPinnedRewriteSelection(null);
    }
  }, [rewriteDialogOpen]);

  const getPinnedRewriteRange = useCallback((): Range | null => {
    const editor = editorRef.current;
    const selection = pinnedRewriteSelection
      ?? (referenceMenuOpenRef.current ? referenceSelectionRef.current : null)
      ?? savedSelectionRef.current;
    if (!editor || !selection || selection.end <= selection.start) return null;
    return writerEditorSelectionRange(editor, selection);
  }, [pinnedRewriteSelection]);

  useEffect(() => {
    const editor = editorRef.current;
    if (!editor) return undefined;
    const handleResize = () => syncBlockControlPositions(editor);
    window.addEventListener('resize', handleResize);
    return () => window.removeEventListener('resize', handleResize);
  }, []);

  useEffect(() => {
    const editor = editorRef.current;
    if (!editor) return undefined;
    const handleFoldClick = (event: MouseEvent) => {
      if (draggingNodeIdRef.current) return;
      const target = event.target;
      if (!(target instanceof Element)) return;
      const toggle = target.closest<HTMLElement>('[data-writer-fold-toggle]');
      if (!toggle || !editor.contains(toggle)) return;
      event.preventDefault();
      event.stopPropagation();
      const nodeId = toggle.dataset.foldNodeId;
      if (!nodeId) return;
      const next = new Set(collapsedNodeIdsRef.current);
      if (next.has(nodeId)) next.delete(nodeId);
      else next.add(nodeId);
      collapsedNodeIdsRef.current = next;
      setCollapseVersion((value) => value + 1);
    };
    editor.addEventListener('mousedown', handleFoldClick);
    return () => editor.removeEventListener('mousedown', handleFoldClick);
  }, []);

  const scrollToReferenceTarget = useCallback((nodeId: string) => {
    const editor = editorRef.current;
    const target = editor ? findRenderedBlock(editor, nodeId) : undefined;
    if (!target) return;
    const reduceMotion = globalThis.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
    target.scrollIntoView({
      behavior: reduceMotion ? 'auto' : 'smooth',
      block: 'center',
      inline: 'nearest',
    });
    target.classList.add('writer-ir__block--reference-target');
    window.setTimeout(() => {
      if (target.isConnected) target.classList.remove('writer-ir__block--reference-target');
    }, 1_600);
  }, []);

  const navigateToReferenceTarget = useCallback((nodeId: string) => {
    const editor = editorRef.current;
    const target = editor ? findRenderedBlock(editor, nodeId) : undefined;
    if (!target) return;
    if (target.closest('[hidden]') && collapsedNodeIdsRef.current.size > 0) {
      pendingReferenceTargetRef.current = nodeId;
      collapsedNodeIdsRef.current = new Set();
      setCollapseVersion((value) => value + 1);
      return;
    }
    scrollToReferenceTarget(nodeId);
  }, [scrollToReferenceTarget]);

  useLayoutEffect(() => {
    const nodeId = pendingReferenceTargetRef.current;
    if (!nodeId) return;
    pendingReferenceTargetRef.current = null;
    window.requestAnimationFrame(() => scrollToReferenceTarget(nodeId));
  }, [collapseVersion, scrollToReferenceTarget]);

  useEffect(() => {
    const editor = editorRef.current;
    if (!editor) return undefined;
    const handleReferenceClick = (event: MouseEvent) => {
      const target = event.target;
      if (!(target instanceof Element)) return;
      const reference = target.closest<HTMLElement>('[data-writer-internal-ref]');
      if (!reference || !editor.contains(reference)) return;
      const targetNodeId = reference.dataset.writerInternalRef;
      if (!targetNodeId) return;
      event.preventDefault();
      event.stopPropagation();
      navigateToReferenceTarget(targetNodeId);
    };
    editor.addEventListener('click', handleReferenceClick);
    return () => editor.removeEventListener('click', handleReferenceClick);
  }, [navigateToReferenceTarget]);

  useEffect(() => {
    const editor = editorRef.current;
    if (!editor) return;
    editor.querySelectorAll('.writer-ir__block--dragging').forEach((node) => {
      node.classList.remove('writer-ir__block--dragging');
    });
    if (!draggingNodeId) return;
    editor
      .querySelector(`[data-writer-block][data-node-id="${CSS.escape(draggingNodeId)}"]`)
      ?.classList.add('writer-ir__block--dragging');
  }, [collapseVersion, document, draggingNodeId]);

  useEffect(() => {
    const editor = editorRef.current;
    if (!editor || disabled) return undefined;

    const clearDropMarks = () => {
      editor.querySelectorAll('.writer-ir__block--drop-child, .writer-ir__block--drop-after, .writer-ir__block--drop-before')
        .forEach((node) => {
          node.classList.remove(
            'writer-ir__block--drop-child',
            'writer-ir__block--drop-after',
            'writer-ir__block--drop-before',
          );
        });
    };

    const paintDropHint = (hint: WriterBlockRelocateTarget | null) => {
      clearDropMarks();
      if (!hint) return;
      if (hint.type === 'child') {
        editor.querySelector(`[data-writer-block][data-node-id="${CSS.escape(hint.parentId)}"]`)
          ?.classList.add('writer-ir__block--drop-child');
        return;
      }
      if (hint.type === 'after') {
        editor.querySelector(`[data-writer-block][data-node-id="${CSS.escape(hint.afterId)}"]`)
          ?.classList.add('writer-ir__block--drop-after');
        return;
      }
      editor.querySelector(`[data-writer-block][data-node-id="${CSS.escape(hint.beforeId)}"]`)
        ?.classList.add('writer-ir__block--drop-before');
    };

    const resolveDropHint = (
      clientX: number,
      clientY: number,
      sourceId: string,
    ): WriterBlockRelocateTarget | null => {
      const hovered = globalThis.document.elementFromPoint(clientX, clientY);
      if (!(hovered instanceof Element) || !editor.contains(hovered)) return null;
      const block = hovered.closest<HTMLElement>('[data-writer-block][data-node-id]');
      if (!block || !editor.contains(block)) return null;
      const targetId = block.dataset.nodeId;
      const targetType = block.dataset.nodeType;
      if (!targetId || targetId === sourceId) return null;

      const rect = block.getBoundingClientRect();
      const ratio = (clientY - rect.top) / Math.max(rect.height, 1);

      // Drop onto a heading: upper third = before, middle/lower = nest as child.
      if (targetType === 'heading') {
        if (ratio < 0.28) return { type: 'before', beforeId: targetId };
        return { type: 'child', parentId: targetId };
      }

      if (ratio < 0.5) return { type: 'before', beforeId: targetId };
      return { type: 'after', afterId: targetId };
    };

    const stopDragging = () => {
      draggingNodeIdRef.current = null;
      dropHintRef.current = null;
      setDraggingNodeId(null);
      clearDropMarks();
      editor.classList.remove('writer-ir__document--dragging');
    };

    const handleMouseDown = (event: MouseEvent) => {
      if (event.button !== 0) return;
      const target = event.target;
      if (!(target instanceof Element)) return;
      const handle = target.closest<HTMLElement>('[data-writer-drag-handle]');
      if (!handle || !editor.contains(handle)) return;
      const nodeId = handle.dataset.dragNodeId;
      if (!nodeId) return;
      event.preventDefault();
      event.stopPropagation();
      draggingNodeIdRef.current = nodeId;
      dropHintRef.current = null;
      setDraggingNodeId(nodeId);
      editor.classList.add('writer-ir__document--dragging');
    };

    const handleMouseMove = (event: MouseEvent) => {
      const sourceId = draggingNodeIdRef.current;
      if (!sourceId) return;
      const hint = resolveDropHint(event.clientX, event.clientY, sourceId);
      dropHintRef.current = hint;
      paintDropHint(hint);
    };

    const handleMouseUp = () => {
      const sourceId = draggingNodeIdRef.current;
      const hint = dropHintRef.current;
      if (!sourceId) return;
      if (hint) {
        const nextDocument = relocateWriterBlock(document, sourceId, hint);
        if (nextDocument !== document) {
          pendingSelectionRef.current = {
            nodeId: sourceId,
            start: 0,
            end: 0,
          };
          lastEmittedDocumentRef.current = undefined;
          onChange(nextDocument);
        }
      }
      stopDragging();
    };

    editor.addEventListener('mousedown', handleMouseDown);
    window.addEventListener('mousemove', handleMouseMove);
    window.addEventListener('mouseup', handleMouseUp);
    return () => {
      editor.removeEventListener('mousedown', handleMouseDown);
      window.removeEventListener('mousemove', handleMouseMove);
      window.removeEventListener('mouseup', handleMouseUp);
      stopDragging();
    };
  }, [disabled, document, onChange]);

  useEffect(() => {
    const editor = editorRef.current;
    if (!editor) return undefined;
    editor.querySelectorAll<HTMLSelectElement>('[data-writer-code-language]').forEach((select) => {
      const nodeId = select.dataset.writerCodeLanguage;
      const block = nodeId ? findWriterBlock(document.blocks, nodeId) : undefined;
      select.disabled = disabled || !block || block.editable === false;
    });
    const handleCodeLanguageChange = (event: Event) => {
      if (disabled) return;
      const select = event.target;
      if (!(select instanceof HTMLSelectElement)) return;
      const nodeId = select.dataset.writerCodeLanguage;
      if (!nodeId) return;
      const nextDocument = updateWriterCodeLanguage(document, nodeId, select.value);
      if (nextDocument === document) return;
      lastEmittedDocumentRef.current = undefined;
      onChange(nextDocument);
    };
    editor.addEventListener('change', handleCodeLanguageChange);
    const handleCodeToolClick = (event: MouseEvent) => {
      const target = event.target;
      if (!(target instanceof Element)) return;
      const button = target.closest<HTMLButtonElement>('[data-writer-code-action]');
      if (!button || !editor.contains(button)) return;
      const nodeId = button.dataset.codeNodeId;
      const shell = nodeId
        ? editor.querySelector<HTMLElement>(`[data-writer-code-shell="${CSS.escape(nodeId)}"]`)
        : null;
      if (!nodeId || !shell) return;
      event.preventDefault();
      event.stopPropagation();

      const action = button.dataset.writerCodeAction;
      const code = shell.querySelector<HTMLElement>('.writer-ir__code');
      const label = button.querySelector<HTMLElement>('[data-writer-code-action-label]');
      if (action === 'collapse') {
        const collapsed = shell.classList.toggle('writer-ir__code-shell--collapsed');
        button.setAttribute('aria-expanded', String(!collapsed));
        button.title = collapsed ? foldLabels.expandCode : foldLabels.collapseCode;
        return;
      }
      if (action === 'wrap' && code) {
        const wrapped = code.classList.toggle('writer-ir__code--wrapped');
        button.setAttribute('aria-pressed', String(wrapped));
        const nextLabel = wrapped ? foldLabels.disableCodeWrap : foldLabels.enableCodeWrap;
        button.title = nextLabel;
        if (label) label.textContent = nextLabel;
        return;
      }
      if (action !== 'copy') return;
      const block = findWriterBlock(document.blocks, nodeId);
      if (!block) return;
      void navigator.clipboard.writeText(block.content ?? '').then(() => {
        button.classList.add('writer-ir__code-copy--copied');
        button.title = foldLabels.codeCopied;
        if (label) label.textContent = foldLabels.codeCopied;
        window.setTimeout(() => {
          if (!button.isConnected) return;
          button.classList.remove('writer-ir__code-copy--copied');
          button.title = foldLabels.copyCode;
          if (label) label.textContent = foldLabels.copyCode;
        }, 1_600);
      }).catch(() => undefined);
    };
    editor.addEventListener('click', handleCodeToolClick);
    return () => {
      editor.removeEventListener('change', handleCodeLanguageChange);
      editor.removeEventListener('click', handleCodeToolClick);
    };
  }, [disabled, document, foldLabels, onChange]);

  const recordSelection = useCallback(() => {
    const editor = editorRef.current;
    if (!editor) return;
    const selection = readEditorSelection(editor);
    if (!selection) {
      if ((rewriteDialogOpenRef.current || pinnedRewriteSelectionRef.current) && savedSelectionRef.current) {
        setActiveSelection(savedSelectionRef.current);
        return;
      }
      savedSelectionRef.current = null;
      setActiveSelection(null);
      return;
    }
    if (selectAllScopeRef.current?.type === 'editor') {
      selectAllScopeRef.current = null;
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
    selectAllScopeRef.current = null;
    const target = event.target;
    if (
      target instanceof Element
      && target.closest('.writer-ir__code-header')
    ) return;
    const nextDocument = parseEditorDocument(event.currentTarget, document);
    for (const contentElement of Array.from(
      event.currentTarget.querySelectorAll<HTMLElement>('[data-writer-block-content]'),
    )) {
      const content = textFromElement(contentElement);
      if (content) {
        contentElement.querySelectorAll('[data-writer-empty-placeholder]').forEach(
          (placeholder) => placeholder.remove(),
        );
      }
      if (contentElement.matches('.writer-ir__code')) {
        contentElement.dataset.lineNumbers = codeLineNumbers(content);
        contentElement.style.setProperty('--writer-code-content-height', codeContentHeight(content));
      }
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
    if (
      event.relatedTarget instanceof Element
      && (
        event.relatedTarget.closest('.artifact-rewrite-form')
        || event.relatedTarget.closest('.writer-ir__format-toolbar')
        || event.relatedTarget.closest('.writer-ir__reference-dropdown')
      )
    ) return;
    if (
      rewriteDialogOpenRef.current
      || pinnedRewriteSelectionRef.current
      || referenceMenuOpenRef.current
    ) return;
    selectAllScopeRef.current = null;
    savedSelectionRef.current = null;
    setActiveSelection(null);
    onBlur();
  };

  const replaceEditorSelection = useCallback((content: string) => {
    const result = replaceWriterEditorContents(document, content);
    const nextSelection = {
      nodeId: result.nodeId,
      start: Array.from(content).length,
      end: Array.from(content).length,
    };
    selectAllScopeRef.current = null;
    savedSelectionRef.current = nextSelection;
    pendingSelectionRef.current = nextSelection;
    setActiveSelection(nextSelection);
    lastEmittedDocumentRef.current = undefined;
    onChange(result.document);
  }, [document, onChange]);

  const handleCopy = (event: ReactClipboardEvent<HTMLElement>) => {
    if (selectAllScopeRef.current?.type !== 'editor') return;
    event.preventDefault();
    event.clipboardData.setData('text/plain', writerEditorPlainText(event.currentTarget));
  };

  const handleCut = (event: ReactClipboardEvent<HTMLElement>) => {
    if (disabled || selectAllScopeRef.current?.type !== 'editor') return;
    event.preventDefault();
    event.clipboardData.setData('text/plain', writerEditorPlainText(event.currentTarget));
    replaceEditorSelection('');
  };

  const handlePaste = (event: ReactClipboardEvent<HTMLElement>) => {
    event.preventDefault();
    if (selectAllScopeRef.current?.type === 'editor') {
      replaceEditorSelection(event.clipboardData.getData('text/plain'));
      return;
    }
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
    && activeBlock?.type !== 'code'
    && activeSelection
    && activeSelection.end > activeSelection.start,
  );
  const canChangeBlockFormat = canFormatBlock && (activeBlock?.children?.length ?? 0) === 0;
  const selectionHasInternalReference = Boolean(
    activeBlock
    && activeSelection
    && writerBlockRangeHasInternalReference(
      activeBlock,
      activeSelection.start,
      activeSelection.end,
    ),
  );
  const referenceTargets = useMemo(
    () => collectWriterReferenceTargets(document.blocks)
      .filter((target) => target.nodeId !== activeBlock?.node_id),
    [activeBlock?.node_id, document.blocks],
  );

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
    const toolbarWidth = formatToolbarRef.current?.offsetWidth ?? 360;
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
  const selectionTextColor = activeBlock && activeSelection
    ? writerBlockRangeSpanColor(
      activeBlock,
      activeSelection.start,
      activeSelection.end,
      'text_color',
    )
    : null;
  const selectionBackgroundColor = activeBlock && activeSelection
    ? writerBlockRangeSpanColor(
      activeBlock,
      activeSelection.start,
      activeSelection.end,
      'background_color',
    )
    : null;
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
  const numberingBlock = numberingMenu
    ? findWriterBlock(document.blocks, numberingMenu.nodeId)
    : undefined;
  const numberingEntry = numberingMenu
    ? numbering?.entries[numberingMenu.nodeId]
    : undefined;
  const numberingMode = numberingEntry?.mode ?? 'ordered';
  const orderedNumberingStyle = numbering?.ordered_style ?? 'hierarchical';

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

  const applyHeadingNumbering = (update: WriterNumberingUpdate) => {
    if (!numberingMenu || !onNumberingUpdate || disabled || numberingBlock?.editable === false) return;
    onNumberingUpdate(update);
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

  const applyCrossReference = useCallback((targetNodeId: string) => {
    if (disabled || !targetNodeId) return;
    const selection = referenceSelectionRef.current ?? savedSelectionRef.current;
    if (!selection || selection.end <= selection.start) return;
    const block = findWriterBlock(document.blocks, selection.nodeId);
    if (!block || block.editable === false || block.node_id === targetNodeId) return;
    const nextDocument = applyWriterBlockInternalReference(
      document,
      selection.nodeId,
      selection.start,
      selection.end,
      targetNodeId,
    );
    if (nextDocument === document) return;
    pendingSelectionRef.current = selection;
    referenceSelectionRef.current = null;
    lastEmittedDocumentRef.current = undefined;
    (onCrossReferenceApplied ?? onChange)(nextDocument);
  }, [disabled, document, onChange, onCrossReferenceApplied]);

  const removeCrossReference = useCallback(() => {
    if (disabled) return;
    const selection = savedSelectionRef.current;
    if (!selection || selection.end <= selection.start) return;
    const block = findWriterBlock(document.blocks, selection.nodeId);
    if (!block || block.editable === false) return;
    const nextDocument = removeWriterBlockInternalReference(
      document,
      selection.nodeId,
      selection.start,
      selection.end,
    );
    if (nextDocument === document) return;
    pendingSelectionRef.current = selection;
    referenceSelectionRef.current = null;
    lastEmittedDocumentRef.current = undefined;
    (onCrossReferenceApplied ?? onChange)(nextDocument);
  }, [disabled, document, onChange, onCrossReferenceApplied]);

  const handleReferenceMenuOpenChange = useCallback((open: boolean) => {
    referenceMenuOpenRef.current = open;
    if (open) {
      referenceSelectionRef.current = savedSelectionRef.current;
    }
    setReferenceMenuOpen(open);
  }, []);

  const applySpanColor = useCallback((
    field: WriterSpanColorField,
    colorId: number | null,
    sourceDocument: WriterDocument = document,
  ) => {
    if (disabled) return sourceDocument;
    const selection = savedSelectionRef.current;
    if (!selection || selection.end <= selection.start) return sourceDocument;
    const block = findWriterBlock(sourceDocument.blocks, selection.nodeId);
    if (!block || block.editable === false) return sourceDocument;
    const nextDocument = applyWriterBlockSpanColor(
      sourceDocument,
      selection.nodeId,
      selection.start,
      selection.end,
      field,
      colorId,
    );
    if (nextDocument === sourceDocument) return sourceDocument;
    pendingSelectionRef.current = selection;
    onChange(nextDocument);
    return nextDocument;
  }, [disabled, document, onChange]);

  const restoreDefaultColors = useCallback(() => {
    if (disabled) return;
    const selection = savedSelectionRef.current;
    if (!selection || selection.end <= selection.start) return;
    let nextDocument = document;
    nextDocument = applyWriterBlockSpanColor(
      nextDocument,
      selection.nodeId,
      selection.start,
      selection.end,
      'text_color',
      null,
    );
    nextDocument = applyWriterBlockSpanColor(
      nextDocument,
      selection.nodeId,
      selection.start,
      selection.end,
      'background_color',
      null,
    );
    if (nextDocument === document) {
      setColorPanelOpen(false);
      return;
    }
    pendingSelectionRef.current = selection;
    setColorPanelOpen(false);
    onChange(nextDocument);
  }, [disabled, document, onChange]);

  useEffect(() => {
    if (!showFormatToolbar) setColorPanelOpen(false);
  }, [showFormatToolbar]);

  useEffect(() => {
    if (!colorPanelOpen) return undefined;
    const handlePointerDown = (event: MouseEvent) => {
      const target = event.target;
      if (!(target instanceof Node)) return;
      if (formatToolbarRef.current?.contains(target)) return;
      setColorPanelOpen(false);
    };
    window.document.addEventListener('mousedown', handlePointerDown);
    return () => window.document.removeEventListener('mousedown', handlePointerDown);
  }, [colorPanelOpen]);

  const activeTextColorHex = writerTextColorHex(
    typeof selectionTextColor === 'number' ? selectionTextColor : undefined,
  ) ?? WRITER_DEFAULT_TEXT_COLOR_HEX;
  const activeBackgroundColorHex = writerBackgroundColorHex(
    typeof selectionBackgroundColor === 'number' ? selectionBackgroundColor : undefined,
  );

  const insertSoftLineBreak = useCallback((editor: HTMLElement) => {
    if (disabled) return;
    const selection = readEditorSelection(editor);
    const block = selection
      ? findWriterBlock(document.blocks, selection.nodeId)
      : undefined;
    if (!selection || !block || block.editable === false) return;
    if (block.type === 'divider') return;

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
    lastEmittedDocumentRef.current = undefined;
    onChange(nextDocument);
  }, [disabled, document, onChange]);

  const insertLineBreak = useCallback((editor: HTMLElement) => {
    if (disabled) return;
    const selection = readEditorSelection(editor);
    const block = selection
      ? findWriterBlock(document.blocks, selection.nodeId)
      : undefined;
    if (!selection || !block || block.editable === false) return;

    // Code blocks always insert a literal newline.
    if (block.type === 'code') {
      insertSoftLineBreak(editor);
      return;
    }

    const contentLength = Array.from(block.content ?? '').length;
    const isEmptyBlock = contentLength === 0;
    const isAtEnd = selection.start >= contentLength && selection.end >= contentLength;
    const parent = findWriterBlockParent(document.blocks, block.node_id);

    const commitStructuralEdit = (
      nextDocument: WriterDocument,
      nodeId: string,
      start = 0,
      end = start,
    ) => {
      const nextSelection = { nodeId, start, end };
      savedSelectionRef.current = nextSelection;
      pendingSelectionRef.current = nextSelection;
      setActiveSelection(nextSelection);
      lastEmittedDocumentRef.current = undefined;
      onChange(nextDocument);
    };

    // Empty heading / list item: demote to paragraph (exit special type).
    if (isEmptyBlock && (block.type === 'heading' || block.type === 'list_item')) {
      const converted = convertWriterBlockToParagraph(document, block.node_id);
      if (converted.insertedNodeId) {
        commitStructuralEdit(converted.document, converted.insertedNodeId);
      }
      return;
    }

    // Empty nested block → outdent one level (sibling of the parent heading).
    // Flow: Enter at end of text creates a blank; Enter again on that blank exits.
    if (isEmptyBlock && parent && parent.type !== 'document') {
      const lifted = liftWriterBlockAfterParent(document, block.node_id);
      if (lifted.insertedNodeId) {
        commitStructuralEdit(lifted.document, lifted.insertedNodeId);
        return;
      }
    }

    // Heading at end: always create a new first body paragraph. Reusing an
    // existing child makes Enter jump into old content instead of adding a line.
    if (block.type === 'heading' && isAtEnd) {
      const inserted = insertWriterChildParagraph(document, block.node_id);
      if (inserted.insertedNodeId) {
        commitStructuralEdit(inserted.document, inserted.insertedNodeId);
      }
      return;
    }

    // Heading mid-split → trailing text becomes the first child paragraph.
    if (block.type === 'heading') {
      const result = splitWriterHeadingIntoChild(
        document,
        block.node_id,
        selection.start,
        selection.end,
      );
      if (result.insertedNodeId) {
        commitStructuralEdit(result.document, result.insertedNodeId);
      }
      return;
    }

    // Default (including empty paragraphs): same-level sibling.
    const result = splitWriterBlock(
      document,
      block.node_id,
      selection.start,
      selection.end,
    );
    if (!result.insertedNodeId) return;
    commitStructuralEdit(result.document, result.insertedNodeId);
  }, [disabled, document, insertSoftLineBreak, onChange]);

  const handleBeforeInput = (event: FormEvent<HTMLElement>) => {
    const nativeEvent = event.nativeEvent as InputEvent;
    const inputType = nativeEvent.inputType;
    const entireEditorSelected = selectAllScopeRef.current?.type === 'editor'
      && isWriterEditorFullySelected(event.currentTarget);
    if (selectAllScopeRef.current?.type === 'editor' && !entireEditorSelected) {
      selectAllScopeRef.current = null;
    }
    if (entireEditorSelected) {
      if (inputType.startsWith('delete')) {
        event.preventDefault();
        replaceEditorSelection('');
        return;
      }
      if (inputType === 'insertText' || inputType === 'insertCompositionText') {
        event.preventDefault();
        replaceEditorSelection(nativeEvent.data ?? '');
        return;
      }
    }
    if (inputType !== 'insertParagraph' && inputType !== 'insertLineBreak') return;
    event.preventDefault();
    if (entireEditorSelected) {
      replaceEditorSelection('');
      return;
    }
    if (handledEnterKeyDownRef.current) {
      handledEnterKeyDownRef.current = false;
      return;
    }
    // insertLineBreak from beforeinput is always a hard break; soft breaks are
    // handled via Shift+Enter in keydown.
    insertLineBreak(event.currentTarget);
  };

  const handleKeyDown = (event: ReactKeyboardEvent<HTMLElement>) => {
    const commandKey = event.metaKey || event.ctrlKey;
    const key = event.key.toLowerCase();
    const target = event.target;
    if (
      commandKey
      && !event.altKey
      && key === 'a'
      && !(target instanceof Element && target.closest('.writer-ir__code-header'))
    ) {
      event.preventDefault();
      const editor = event.currentTarget;
      const selection = readEditorSelection(editor);
      const block = selection
        ? findWriterBlock(document.blocks, selection.nodeId)
        : undefined;
      const previousScope = selectAllScopeRef.current;
      if (
        block?.type === 'code'
        && !(
          previousScope?.type === 'code'
          && previousScope.nodeId === block.node_id
          && selection?.start === 0
          && selection.end === Array.from(block.content ?? '').length
        )
      ) {
        const nextSelection = {
          nodeId: block.node_id,
          start: 0,
          end: Array.from(block.content ?? '').length,
        };
        selectAllScopeRef.current = { type: 'code', nodeId: block.node_id };
        savedSelectionRef.current = nextSelection;
        setActiveSelection(nextSelection);
        restoreEditorSelection(editor, nextSelection);
        return;
      }

      selectAllScopeRef.current = { type: 'editor' };
      savedSelectionRef.current = null;
      setActiveSelection(null);
      selectWriterEditorContents(editor);
      return;
    }

    const entireEditorSelected = selectAllScopeRef.current?.type === 'editor'
      && isWriterEditorFullySelected(event.currentTarget);
    if (selectAllScopeRef.current?.type === 'editor' && !entireEditorSelected) {
      selectAllScopeRef.current = null;
    }

    if (
      event.key === 'Enter'
      && entireEditorSelected
      && !isComposingRef.current
      && !event.nativeEvent.isComposing
      && event.keyCode !== 229
    ) {
      event.preventDefault();
      replaceEditorSelection('');
      return;
    }

    if (
      event.key === 'Enter'
      && !isComposingRef.current
      && !event.nativeEvent.isComposing
      && event.keyCode !== 229
    ) {
      event.preventDefault();
      handledEnterKeyDownRef.current = true;
      if (event.shiftKey) insertSoftLineBreak(event.currentTarget);
      else insertLineBreak(event.currentTarget);
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

    if (!commandKey) return;
    if (key !== 'b' && key !== 'i') return;
    event.preventDefault();
    recordSelection();
    applyInlineStyle(key === 'b' ? 'strong' : 'italic');
  };

  const handleEditorFocus = () => {
    onFocus();
    window.requestAnimationFrame(recordSelection);
  };

  const handleEditorMouseDown = (event: ReactMouseEvent<HTMLElement>) => {
    if (disabled) return;
    selectAllScopeRef.current = null;
    const target = event.target;
    if (!(target instanceof Element)) return;
    const trailingLine = target.closest<HTMLElement>('[data-writer-code-trailing-line]');
    if (!trailingLine || !event.currentTarget.contains(trailingLine)) return;

    event.preventDefault();
    const range = globalThis.document.createRange();
    range.setStart(trailingLine, 0);
    range.setEnd(trailingLine, 0);
    const selection = globalThis.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);
    event.currentTarget.focus({ preventScroll: true });
    recordSelection();
  };

  const handleKeyUp = (event: ReactKeyboardEvent<HTMLElement>) => {
    if (event.key === 'Enter') handledEnterKeyDownRef.current = false;
    recordSelection();
  };

  const requestRewriteSelection = useCallback(() => {
    const selection = savedSelectionRef.current;
    if (!selection || selection.end <= selection.start || !onRewriteSelection) return;
    const block = findWriterBlock(document.blocks, selection.nodeId);
    if (!block) return;
    const selectedText = Array.from(block.content ?? '')
      .slice(selection.start, selection.end)
      .join('')
      .trim();
    if (!selectedText) return;

    let anchor: SelectionActionAnchor | undefined;
    const browserSelection = globalThis.getSelection();
    if (browserSelection?.rangeCount && !browserSelection.isCollapsed) {
      anchor = selectionActionAnchor(browserSelection.getRangeAt(0)) ?? undefined;
    }
    if (!anchor && formatToolbarRef.current) {
      const toolbarRect = formatToolbarRef.current.getBoundingClientRect();
      anchor = {
        top: toolbarRect.bottom + 8,
        left: toolbarRect.left + toolbarRect.width / 2,
        placement: 'below',
      };
    }

    setPinnedRewriteSelection(selection);
    pinnedRewriteSelectionRef.current = selection;
    onRewriteSelection({ nodeId: block.node_id, selectedText, anchor });
  }, [document.blocks, onRewriteSelection]);

  const openNumberingMenu = (event: ReactMouseEvent<HTMLElement>) => {
    if (disabled) return;
    const target = event.target instanceof Element ? event.target : null;
    const marker = target?.closest<HTMLElement>('[data-writer-numbering-marker]');
    const unorderedHeading = marker ? null : target?.closest<HTMLElement>(
      '[data-writer-block-content][data-writer-heading-mode="unordered"]',
    );
    if (!marker && (
      !unorderedHeading
      || event.clientX >= unorderedHeading.getBoundingClientRect().left
    )) return;
    event.preventDefault();
    event.stopPropagation();
    const nodeId = marker?.dataset.writerNumberingMarker
      ?? unorderedHeading?.closest<HTMLElement>('[data-writer-block]')?.dataset.nodeId;
    if (!nodeId) return;
    setNumberingMenu({ nodeId, x: event.clientX, y: event.clientY });
  };

  return (
    <div
      className='writer-ir__editor-shell'
      ref={shellRef}
      onBlur={handleBlur}
      onClickCapture={openNumberingMenu}
    >
      {numberingMenu && numberingBlock?.type === 'heading' && (
        <WriterHeadingNumberingMenu
          x={numberingMenu.x}
          y={numberingMenu.y}
          targetId={numberingMenu.nodeId}
          mode={numberingMode}
          orderedStyle={orderedNumberingStyle}
          restart={Boolean(numberingEntry?.restart)}
          disabled={disabled || !onNumberingUpdate || numberingBlock.editable === false}
          onApply={applyHeadingNumbering}
          onClose={() => setNumberingMenu(null)}
        />
      )}
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

          <span className='writer-ir__format-divider' aria-hidden='true' />

          <div className='writer-ir__format-group'>
            <Dropdown
              trigger={['click']}
              placement='bottomLeft'
              overlayClassName='writer-ir__reference-dropdown'
              disabled={disabled || !hasTextSelection || referenceTargets.length === 0}
              onOpenChange={handleReferenceMenuOpenChange}
              menu={{
                selectable: false,
                items: referenceTargets.map((target) => ({
                  key: target.nodeId,
                  label: (
                    <span
                      className='writer-ir__reference-option'
                      title={target.label}
                    >
                      <span className='writer-ir__reference-option-icon' aria-hidden='true'>
                        {writerReferenceTargetIcon(target.type)}
                      </span>
                      <span className='writer-ir__reference-option-label'>
                        {target.label}
                      </span>
                    </span>
                  ),
                })),
                onClick: ({ key }: { key: string }) => applyCrossReference(key),
              }}
            >
              <button
                type='button'
                className={`writer-ir__reference-trigger${
                  referenceMenuOpen ? ' writer-ir__reference-trigger--open' : ''
                }`}
                disabled={disabled || !hasTextSelection || referenceTargets.length === 0}
                aria-label={t('chat.writerIR.crossReference')}
                aria-haspopup='menu'
                aria-expanded={referenceMenuOpen}
                title={t('chat.writerIR.crossReference')}
                onMouseDown={(event) => {
                  event.preventDefault();
                  referenceSelectionRef.current = savedSelectionRef.current;
                }}
              >
                <LinkOutlined aria-hidden />
                <span>{t('chat.writerIR.crossReference')}</span>
                <DownOutlined className='writer-ir__reference-caret' aria-hidden />
              </button>
            </Dropdown>
            <button
              type='button'
              className='writer-ir__format-button'
              disabled={disabled || !selectionHasInternalReference}
              aria-label={t('chat.writerIR.removeCrossReference')}
              title={t('chat.writerIR.removeCrossReference')}
              onMouseDown={(event) => event.preventDefault()}
              onClick={removeCrossReference}
            >
              <DisconnectOutlined aria-hidden />
            </button>
          </div>

          <span className='writer-ir__format-divider' aria-hidden='true' />

          <div className='writer-ir__format-group writer-ir__format-group--color'>
            <button
              type='button'
              className='writer-ir__format-button writer-ir__format-button--color-trigger'
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => setColorPanelOpen((open) => !open)}
              disabled={disabled || !hasTextSelection || !canFormatBlock}
              aria-label={t('chat.writerIR.colorPanel')}
              aria-expanded={colorPanelOpen}
              aria-haspopup='dialog'
              title={t('chat.writerIR.colorPanel')}
            >
              <span
                className='writer-ir__color-trigger-letter'
                style={{
                  color: activeTextColorHex,
                  backgroundColor: activeBackgroundColorHex,
                }}
              >
                A
                <span
                  className='writer-ir__color-indicator'
                  style={{ backgroundColor: activeTextColorHex }}
                />
              </span>
              <DownOutlined className='writer-ir__color-trigger-caret' aria-hidden />
            </button>

            {colorPanelOpen && (
              <div
                className='writer-ir__color-panel'
                role='dialog'
                aria-label={t('chat.writerIR.colorPanel')}
              >
                <div className='writer-ir__color-section'>
                  <div className='writer-ir__color-section-title'>
                    {t('chat.writerIR.textColor')}
                  </div>
                  <div className='writer-ir__color-row' role='listbox'>
                    <button
                      type='button'
                      className={`writer-ir__color-tile writer-ir__color-tile--letter${
                        selectionTextColor == null ? ' writer-ir__color-tile--active' : ''
                      }`}
                      style={{ color: WRITER_DEFAULT_TEXT_COLOR_HEX }}
                      onMouseDown={(event) => event.preventDefault()}
                      onClick={() => applySpanColor('text_color', null)}
                      aria-label={t('chat.writerIR.colors.default')}
                      title={t('chat.writerIR.colors.default')}
                      aria-selected={selectionTextColor == null}
                    >
                      A
                    </button>
                    {WRITER_TEXT_COLOR_PALETTE.map((color) => {
                      const label = t(`chat.writerIR.colors.${color.name}`);
                      return (
                        <button
                          key={`text-${color.id}`}
                          type='button'
                          className={`writer-ir__color-tile writer-ir__color-tile--letter${
                            selectionTextColor === color.id ? ' writer-ir__color-tile--active' : ''
                          }`}
                          style={{ color: color.hex }}
                          onMouseDown={(event) => event.preventDefault()}
                          onClick={() => applySpanColor('text_color', color.id)}
                          aria-label={label}
                          title={label}
                          aria-selected={selectionTextColor === color.id}
                        >
                          A
                        </button>
                      );
                    })}
                  </div>
                </div>

                <div className='writer-ir__color-section'>
                  <div className='writer-ir__color-section-title'>
                    {t('chat.writerIR.backgroundColor')}
                  </div>
                  <div className='writer-ir__color-row' role='listbox'>
                    <button
                      type='button'
                      className={`writer-ir__color-tile writer-ir__color-tile--clear${
                        selectionBackgroundColor == null ? ' writer-ir__color-tile--active' : ''
                      }`}
                      onMouseDown={(event) => event.preventDefault()}
                      onClick={() => applySpanColor('background_color', null)}
                      aria-label={t('chat.writerIR.clearColor')}
                      title={t('chat.writerIR.clearColor')}
                      aria-selected={selectionBackgroundColor == null}
                    />
                    {WRITER_BACKGROUND_LIGHT_PALETTE.map((color) => {
                      const label = t(`chat.writerIR.colors.${color.name}`);
                      return (
                        <button
                          key={`bg-light-${color.id}`}
                          type='button'
                          className={`writer-ir__color-tile${
                            selectionBackgroundColor === color.id
                              ? ' writer-ir__color-tile--active'
                              : ''
                          }`}
                          style={{ backgroundColor: color.hex }}
                          onMouseDown={(event) => event.preventDefault()}
                          onClick={() => applySpanColor('background_color', color.id)}
                          aria-label={label}
                          title={label}
                          aria-selected={selectionBackgroundColor === color.id}
                        />
                      );
                    })}
                  </div>
                  <div className='writer-ir__color-row' role='listbox'>
                    {WRITER_BACKGROUND_DARK_PALETTE.map((color) => {
                      const label = t(`chat.writerIR.colors.${color.name}`);
                      return (
                        <button
                          key={`bg-dark-${color.id}`}
                          type='button'
                          className={`writer-ir__color-tile${
                            selectionBackgroundColor === color.id
                              ? ' writer-ir__color-tile--active'
                              : ''
                          }`}
                          style={{ backgroundColor: color.hex }}
                          onMouseDown={(event) => event.preventDefault()}
                          onClick={() => applySpanColor('background_color', color.id)}
                          aria-label={label}
                          title={label}
                          aria-selected={selectionBackgroundColor === color.id}
                        />
                      );
                    })}
                  </div>
                </div>

                <button
                  type='button'
                  className='writer-ir__color-reset'
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={restoreDefaultColors}
                >
                  {t('chat.writerIR.restoreDefaultColors')}
                </button>
              </div>
            )}
          </div>
          {onRewriteSelection && (
            <>
              <span className='writer-ir__format-divider' aria-hidden='true' />
              <button
                type='button'
                className='writer-ir__format-button writer-ir__format-button--rewrite'
                onMouseDown={(event) => event.preventDefault()}
                onClick={requestRewriteSelection}
                disabled={disabled || !hasTextSelection}
                aria-label={t('chat.artifactRewrite.action')}
                title={t('chat.artifactRewrite.action')}
              >
                {t('chat.artifactRewrite.action')}
              </button>
            </>
          )}
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
        onCopy={handleCopy}
        onCut={handleCut}
        onPaste={handlePaste}
        onKeyDown={handleKeyDown}
        onCompositionStart={() => {
          isComposingRef.current = true;
        }}
        onCompositionEnd={() => {
          isComposingRef.current = false;
        }}
        onMouseDown={handleEditorMouseDown}
        onMouseUp={recordSelection}
        onKeyUp={handleKeyUp}
      />
      <div className='writer-ir__rewrite-layer' ref={setRewriteLayer} />
      <ArtifactRewriteSelectionHighlight
        layer={rewriteLayer}
        getRange={getPinnedRewriteRange}
        active={Boolean(pinnedRewriteSelection) || referenceMenuOpen}
      />
      {rewritePreview && rewriteTarget && rewriteLayer && onRewritePreviewApplied && onRewritePreviewRejected && (
        <ArtifactRewriteInlineDiff
          target={rewriteTarget}
          layer={rewriteLayer}
          sessionId={rewritePreview.sessionId}
          slotId={rewritePreview.slotId}
          listIndex={rewritePreview.listIndex}
          preview={rewritePreview.preview}
          onApplied={onRewritePreviewApplied}
          onReject={onRewritePreviewRejected}
        />
      )}
    </div>
  );
}
