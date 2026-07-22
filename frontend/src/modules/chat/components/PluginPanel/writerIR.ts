export type WriterStage = 'outline' | 'draft' | 'final' | string;

export interface WriterSpan {
  text: string;
  /** Current snapshot field. */
  style?: string[];
  /** Compatibility with writer_ir_clean.py. */
  stype?: string[];
  [key: string]: unknown;
}

export interface WriterBlock {
  node_id: string;
  type: string;
  content?: string;
  spans?: WriterSpan[];
  children?: WriterBlock[];
  stage?: WriterStage;
  status?: string;
  authoring?: Record<string, unknown>;
  numbering?: Record<string, unknown>;
  references?: Array<Record<string, unknown>>;
  source_refs?: Array<Record<string, unknown>>;
  provider_binding?: Record<string, unknown>;
  provider_payload?: Record<string, unknown>;
  editable?: boolean;
  [key: string]: unknown;
}

export interface WriterDocument {
  document_id: string;
  stage: WriterStage;
  title: string;
  blocks: WriterBlock[];
  revision?: string | null;
  metadata?: Record<string, unknown>;
  provider_binding?: Record<string, unknown>;
  [key: string]: unknown;
}

export interface WriterIRParseResult {
  ok: boolean;
  document?: WriterDocument;
  issues: string[];
}

export type WriterBlockMoveDirection = 'up' | 'down';

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

function validateBlock(
  value: unknown,
  path: string,
  ids: Set<string>,
  issues: string[],
): value is WriterBlock {
  if (!isRecord(value)) {
    issues.push(`${path} must be an object`);
    return false;
  }

  let valid = true;
  if (typeof value.node_id !== 'string' || !value.node_id.trim()) {
    issues.push(`${path}.node_id must be a non-empty string`);
    valid = false;
  } else if (ids.has(value.node_id)) {
    issues.push(`${path}.node_id is duplicated: ${value.node_id}`);
    valid = false;
  } else {
    ids.add(value.node_id);
  }

  if (typeof value.type !== 'string' || !value.type.trim()) {
    issues.push(`${path}.type must be a non-empty string`);
    valid = false;
  }
  if (value.content !== undefined && typeof value.content !== 'string') {
    issues.push(`${path}.content must be a string`);
    valid = false;
  }
  if (value.editable !== undefined && typeof value.editable !== 'boolean') {
    issues.push(`${path}.editable must be a boolean`);
    valid = false;
  }

  if (value.spans !== undefined) {
    if (!Array.isArray(value.spans)) {
      issues.push(`${path}.spans must be an array`);
      valid = false;
    } else {
      value.spans.forEach((span, index) => {
        if (!isRecord(span) || typeof span.text !== 'string') {
          issues.push(`${path}.spans[${index}].text must be a string`);
          valid = false;
          return;
        }
        for (const key of ['style', 'stype'] as const) {
          const styles = span[key];
          if (styles !== undefined && (!Array.isArray(styles) || styles.some((item) => typeof item !== 'string'))) {
            issues.push(`${path}.spans[${index}].${key} must be a string array`);
            valid = false;
          }
        }
      });
    }
  }

  if (value.children !== undefined) {
    if (!Array.isArray(value.children)) {
      issues.push(`${path}.children must be an array`);
      valid = false;
    } else {
      value.children.forEach((child, index) => {
        if (!validateBlock(child, `${path}.children[${index}]`, ids, issues)) {
          valid = false;
        }
      });
    }
  }

  return valid;
}

/**
 * Parses the concrete Writer IR snapshot contract. Unknown fields are accepted and
 * retained so editing a known node never strips provider-specific extensions.
 */
export function parseWriterDocument(value: unknown): WriterIRParseResult {
  if (!isRecord(value)) {
    return { ok: false, issues: ['document must be an object'] };
  }

  const issues: string[] = [];
  if (typeof value.document_id !== 'string' || !value.document_id.trim()) {
    issues.push('document_id must be a non-empty string');
  }
  if (typeof value.stage !== 'string' || !value.stage.trim()) {
    issues.push('stage must be a non-empty string');
  }
  if (typeof value.title !== 'string') {
    issues.push('title must be a string');
  }
  if (!Array.isArray(value.blocks)) {
    issues.push('blocks must be an array');
  } else {
    const ids = new Set<string>();
    value.blocks.forEach((block, index) => {
      validateBlock(block, `blocks[${index}]`, ids, issues);
    });
  }
  if (value.revision !== undefined && value.revision !== null && typeof value.revision !== 'string') {
    issues.push('revision must be a string or null');
  }

  if (issues.length > 0) return { ok: false, issues };
  return { ok: true, document: value as unknown as WriterDocument, issues: [] };
}

export function isWriterDocument(value: unknown): value is WriterDocument {
  return parseWriterDocument(value).ok;
}

export function getWriterSpanStyles(span: WriterSpan): string[] {
  if (Array.isArray(span.style)) return span.style;
  if (Array.isArray(span.stype)) return span.stype;
  return [];
}

export function countWriterBlocks(blocks: WriterBlock[]): number {
  return blocks.reduce(
    (total, block) => total + 1 + countWriterBlocks(block.children ?? []),
    0,
  );
}

export function findWriterBlock(
  blocks: WriterBlock[],
  nodeId: string,
): WriterBlock | undefined {
  for (const block of blocks) {
    if (block.node_id === nodeId) return block;
    const nested = findWriterBlock(block.children ?? [], nodeId);
    if (nested) return nested;
  }
  return undefined;
}

function replaceBlockInTree(
  blocks: WriterBlock[],
  nodeId: string,
  update: (block: WriterBlock) => WriterBlock,
): { blocks: WriterBlock[]; changed: boolean } {
  let changed = false;
  const next = blocks.map((block) => {
    if (block.node_id === nodeId) {
      const updated = update(block);
      if (updated !== block) changed = true;
      return updated;
    }
    const nested = replaceBlockInTree(block.children ?? [], nodeId, update);
    if (!nested.changed) return block;
    changed = true;
    return { ...block, children: nested.blocks };
  });
  return { blocks: changed ? next : blocks, changed };
}

function spanForEditedContent(block: WriterBlock, content: string): WriterSpan[] {
  const usesSType = (block.spans ?? []).some(
    (span) => Array.isArray(span.stype) && !Array.isArray(span.style),
  );
  return usesSType
    ? [{ text: content, stype: [] }]
    : [{ text: content, style: [] }];
}

function sliceWriterSpans(
  spans: WriterSpan[],
  start: number,
  end: number,
): WriterSpan[] {
  let offset = 0;
  const result: WriterSpan[] = [];

  for (const span of spans) {
    const characters = Array.from(span.text);
    const spanStart = offset;
    const spanEnd = offset + characters.length;
    offset = spanEnd;

    const overlapStart = Math.max(start, spanStart);
    const overlapEnd = Math.min(end, spanEnd);
    if (overlapStart >= overlapEnd) continue;
    result.push({
      ...span,
      text: characters
        .slice(overlapStart - spanStart, overlapEnd - spanStart)
        .join(''),
    });
  }
  return result;
}

function writerSpanAt(spans: WriterSpan[], index: number): WriterSpan | undefined {
  let offset = 0;
  for (const span of spans) {
    const length = Array.from(span.text).length;
    if (index >= offset && index < offset + length) return span;
    offset += length;
  }
  return undefined;
}

function styledSpanForInsertedText(
  block: WriterBlock,
  template: WriterSpan | undefined,
  text: string,
): WriterSpan {
  if (Array.isArray(template?.style)) return { text, style: [...template.style] };
  if (Array.isArray(template?.stype)) return { text, stype: [...template.stype] };
  return spanForEditedContent(block, text)[0];
}

function haveSameWriterStyles(left: WriterSpan, right: WriterSpan): boolean {
  const leftStyles = getWriterSpanStyles(left);
  const rightStyles = getWriterSpanStyles(right);
  return leftStyles.length === rightStyles.length
    && leftStyles.every((style, index) => style === rightStyles[index]);
}

function canMergeWriterSpans(left: WriterSpan, right: WriterSpan): boolean {
  const allowedKeys = new Set(['text', 'style', 'stype']);
  if (
    Object.keys(left).some((key) => !allowedKeys.has(key))
    || Object.keys(right).some((key) => !allowedKeys.has(key))
  ) return false;
  if (Array.isArray(left.style) !== Array.isArray(right.style)) return false;
  if (Array.isArray(left.stype) !== Array.isArray(right.stype)) return false;
  if (
    Array.isArray(left.style)
    && Array.isArray(right.style)
    && (
      left.style.length !== right.style.length
      || left.style.some((style, index) => style !== right.style?.[index])
    )
  ) return false;
  if (
    Array.isArray(left.stype)
    && Array.isArray(right.stype)
    && (
      left.stype.length !== right.stype.length
      || left.stype.some((style, index) => style !== right.stype?.[index])
    )
  ) return false;
  return haveSameWriterStyles(left, right);
}

function mergeAdjacentWriterSpans(spans: WriterSpan[]): WriterSpan[] {
  const merged: WriterSpan[] = [];
  for (const span of spans) {
    const previous = merged[merged.length - 1];
    if (previous && canMergeWriterSpans(previous, span)) {
      merged[merged.length - 1] = { ...previous, text: previous.text + span.text };
    } else {
      merged.push(span);
    }
  }
  return merged;
}

/**
 * Preserve unaffected rich-text spans while applying a plain-text edit. Text typed
 * inside or immediately after a styled range inherits that range's style.
 */
function spansForUpdatedContent(block: WriterBlock, content: string): WriterSpan[] {
  const previousContent = block.content ?? '';
  const spans = block.spans ?? [];
  if (content === previousContent) return spans;
  if (spans.length === 0 || spans.map((span) => span.text).join('') !== previousContent) {
    return spanForEditedContent(block, content);
  }

  const previousCharacters = Array.from(previousContent);
  const nextCharacters = Array.from(content);
  let prefixLength = 0;
  while (
    prefixLength < previousCharacters.length
    && prefixLength < nextCharacters.length
    && previousCharacters[prefixLength] === nextCharacters[prefixLength]
  ) {
    prefixLength += 1;
  }

  let suffixLength = 0;
  while (
    suffixLength < previousCharacters.length - prefixLength
    && suffixLength < nextCharacters.length - prefixLength
    && previousCharacters[previousCharacters.length - 1 - suffixLength]
      === nextCharacters[nextCharacters.length - 1 - suffixLength]
  ) {
    suffixLength += 1;
  }

  const previousChangeEnd = previousCharacters.length - suffixLength;
  const nextChangeEnd = nextCharacters.length - suffixLength;
  const insertedText = nextCharacters.slice(prefixLength, nextChangeEnd).join('');
  const replacedSpans = sliceWriterSpans(spans, prefixLength, previousChangeEnd);
  let template: WriterSpan | undefined;
  if (prefixLength === previousChangeEnd) {
    const templateIndex = prefixLength > 0 ? prefixLength - 1 : previousChangeEnd;
    template = writerSpanAt(spans, templateIndex);
  } else if (
    replacedSpans.length > 0
    && replacedSpans.every((span) => haveSameWriterStyles(replacedSpans[0], span))
  ) {
    template = replacedSpans[0];
  }
  const nextSpans = [
    ...sliceWriterSpans(spans, 0, prefixLength),
    ...(insertedText ? [styledSpanForInsertedText(block, template, insertedText)] : []),
    ...sliceWriterSpans(spans, previousChangeEnd, previousCharacters.length),
  ];
  return nextSpans.length > 0
    ? mergeAdjacentWriterSpans(nextSpans)
    : spanForEditedContent(block, content);
}

export function updateWriterBlockContent(
  document: WriterDocument,
  nodeId: string,
  content: string,
): WriterDocument {
  const result = replaceBlockInTree(document.blocks, nodeId, (block) => {
    if ((block.content ?? '') === content) return block;
    return {
      ...block,
      content,
      spans: spansForUpdatedContent(block, content),
    };
  });
  return result.changed ? { ...document, blocks: result.blocks } : document;
}

export function updateWriterDocumentTitle(
  document: WriterDocument,
  title: string,
): WriterDocument {
  const metadata = document.metadata
    ? { ...document.metadata, title }
    : document.metadata;
  const rootResult = replaceBlockInTree(document.blocks, document.document_id, (block) => {
    if (block.type !== 'document') return block;
    return {
      ...block,
      content: title,
      spans: spanForEditedContent(block, title),
    };
  });
  return {
    ...document,
    title,
    metadata,
    blocks: rootResult.blocks,
  };
}

function updateChildrenArray(
  blocks: WriterBlock[],
  updater: (siblings: WriterBlock[]) => { siblings: WriterBlock[]; changed: boolean },
): { blocks: WriterBlock[]; changed: boolean } {
  const direct = updater(blocks);
  if (direct.changed) return { blocks: direct.siblings, changed: true };

  for (let index = 0; index < blocks.length; index += 1) {
    const block = blocks[index];
    const nested = updateChildrenArray(block.children ?? [], updater);
    if (!nested.changed) continue;
    const next = blocks.slice();
    next[index] = { ...block, children: nested.blocks };
    return { blocks: next, changed: true };
  }
  return { blocks, changed: false };
}

function withUpdatedBlocks(
  document: WriterDocument,
  blocks: WriterBlock[],
): WriterDocument {
  const metadata = document.metadata
    && Object.prototype.hasOwnProperty.call(document.metadata, 'block_count')
    ? { ...document.metadata, block_count: countWriterBlocks(blocks) }
    : document.metadata;
  return { ...document, blocks, metadata };
}

function createNodeId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  return `writer-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}

export function createWriterParagraph(stage: WriterStage): WriterBlock {
  const nodeId = createNodeId();
  return {
    node_id: nodeId,
    type: 'paragraph',
    content: '',
    spans: [{ text: '', style: [] }],
    children: [],
    stage,
    status: '',
    authoring: {},
    numbering: {},
    references: [],
    source_refs: [],
    provider_binding: {},
    provider_payload: {},
    editable: true,
  };
}

export function insertWriterParagraphAfter(
  document: WriterDocument,
  nodeId: string,
): { document: WriterDocument; insertedNodeId?: string } {
  const paragraph = createWriterParagraph(document.stage);
  const result = updateChildrenArray(document.blocks, (siblings) => {
    const index = siblings.findIndex((block) => block.node_id === nodeId);
    if (index < 0 || siblings[index].type === 'document') {
      return { siblings, changed: false };
    }
    const next = siblings.slice();
    next.splice(index + 1, 0, paragraph);
    return { siblings: next, changed: true };
  });
  return result.changed
    ? { document: withUpdatedBlocks(document, result.blocks), insertedNodeId: paragraph.node_id }
    : { document };
}

export function deleteWriterBlock(
  document: WriterDocument,
  nodeId: string,
): WriterDocument {
  const target = findWriterBlock(document.blocks, nodeId);
  if (!target || target.type === 'document' || target.editable === false) return document;
  const result = updateChildrenArray(document.blocks, (siblings) => {
    const index = siblings.findIndex((block) => block.node_id === nodeId);
    if (index < 0) return { siblings, changed: false };
    return {
      siblings: siblings.filter((_, siblingIndex) => siblingIndex !== index),
      changed: true,
    };
  });
  return result.changed ? withUpdatedBlocks(document, result.blocks) : document;
}

export function moveWriterBlock(
  document: WriterDocument,
  nodeId: string,
  direction: WriterBlockMoveDirection,
): WriterDocument {
  const target = findWriterBlock(document.blocks, nodeId);
  if (!target || target.type === 'document' || target.editable === false) return document;
  const result = updateChildrenArray(document.blocks, (siblings) => {
    const index = siblings.findIndex((block) => block.node_id === nodeId);
    const nextIndex = direction === 'up' ? index - 1 : index + 1;
    if (index < 0 || nextIndex < 0 || nextIndex >= siblings.length) {
      return { siblings, changed: false };
    }
    const next = siblings.slice();
    [next[index], next[nextIndex]] = [next[nextIndex], next[index]];
    return { siblings: next, changed: true };
  });
  return result.changed ? withUpdatedBlocks(document, result.blocks) : document;
}
