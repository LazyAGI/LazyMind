export type WriterStage = 'outline' | 'draft' | 'final' | string;

export interface WriterSpan {
  text: string;
  /** Current snapshot field. */
  style?: string[] | Record<string, unknown>;
  /** Compatibility with writer_ir_clean.py. */
  stype?: string[] | Record<string, unknown>;
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
  /** Syntax language for code blocks. Unknown values fall back to plain text. */
  language?: string;
  [key: string]: unknown;
}

export interface WriterDocument {
  document_id: string;
  stage: WriterStage;
  title: string;
  blocks: WriterBlock[];
  ui_editable?: boolean;
  revision?: string | null;
  metadata?: Record<string, unknown>;
  provider_binding?: Record<string, unknown>;
  [key: string]: unknown;
}

const WRITER_SYSTEM_ANCHOR_RE = /^<a\s+id=(["'])block-[^"']+\1\s*(?:\/>|>\s*<\/a>)$/i;

/** System anchors stay in the IR for numbering/reference round trips, but are not user content. */
export function isWriterSystemAnchorBlock(block: WriterBlock): boolean {
  return block.type === 'paragraph'
    && WRITER_SYSTEM_ANCHOR_RE.test(block.content?.trim() ?? '');
}

export type WriterHeadingLevel = 1 | 2 | 3 | 4 | 5 | 6;

export interface WriterOutlineItem {
  nodeId: string;
  title: string;
  level: WriterHeadingLevel;
}

export interface WriterReferenceTarget {
  nodeId: string;
  label: string;
  type: string;
}

export interface WriterInternalReference {
  targetNodeId: string;
  displayText?: string;
}

export function writerHeadingLevel(block: WriterBlock): WriterHeadingLevel {
  const raw = Number(block.numbering?.level ?? 2);
  if (!Number.isFinite(raw)) return 2;
  return Math.min(6, Math.max(1, Math.trunc(raw))) as WriterHeadingLevel;
}

/** Collects headings in document order for the client-side table of contents. */
export function collectWriterOutline(blocks: WriterBlock[]): WriterOutlineItem[] {
  const items: WriterOutlineItem[] = [];

  const visit = (current: WriterBlock[]) => {
    current.forEach((block) => {
      const title = block.content?.trim() ?? '';
      if (block.type === 'heading' && title) {
        items.push({
          nodeId: block.node_id,
          title,
          level: writerHeadingLevel(block),
        });
      }
      if (block.children?.length) visit(block.children);
    });
  };

  visit(blocks);
  return items;
}

/** Numbered IR blocks that can be addressed by an internal_ref span. */
export function collectWriterReferenceTargets(blocks: WriterBlock[]): WriterReferenceTarget[] {
  const targets: WriterReferenceTarget[] = [];
  const visit = (current: WriterBlock[]) => {
    current.forEach((block) => {
      if (['heading', 'image', 'table', 'code'].includes(block.type)) {
        targets.push({
          nodeId: block.node_id,
          label: block.content?.trim() || block.node_id,
          type: block.type,
        });
      }
      if (block.children?.length) visit(block.children);
    });
  };
  visit(blocks);
  return targets;
}

interface WriterMediaAsset {
  media_asset_id?: unknown;
  source_type?: unknown;
  uri?: unknown;
  local_path?: unknown;
  meta?: unknown;
}

interface WriterMediaAssetLibrary {
  assets?: Record<string, WriterMediaAsset>;
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === 'object'
    ? value as Record<string, unknown>
    : undefined;
}

/** Restores renderable paths for legacy Feishu image blocks without changing provider state. */
export function restoreLegacyWriterImageReference(
  document: WriterDocument,
  mediaLibrary: unknown,
): WriterDocument {
  const library = asRecord(mediaLibrary) as WriterMediaAssetLibrary | undefined;
  const assets = Object.values(library?.assets ?? {});
  const generatedAssets = assets.filter((asset) => {
    const uri = typeof asset.uri === 'string' ? asset.uri.trim() : '';
    return asset.source_type === 'image_generation' && uri !== '';
  });
  let changed = false;

  const restoreBlocks = (blocks: WriterBlock[]): WriterBlock[] => blocks.map((block) => {
    const children = block.children?.length ? restoreBlocks(block.children) : block.children;
    const mediaReference = block.references?.find((reference) => reference.type === 'media_asset');
    const rawBlock = asRecord(asRecord(block.provider_payload)?.raw_block);
    const image = asRecord(rawBlock?.image);
    const providerBinding = asRecord(block.provider_binding);
    const providerBlockID = typeof providerBinding?.block_id === 'string'
      ? providerBinding.block_id.trim()
      : '';
    const isLegacyFeishuImage = block.type === 'image'
      && typeof image?.token === 'string'
      && image.token.trim() !== '';
    const hasLocalPath = typeof mediaReference?.url === 'string'
      ? mediaReference.url.trim() !== ''
      : typeof mediaReference?.path === 'string' && mediaReference.path.trim() !== '';

    if (!isLegacyFeishuImage || hasLocalPath) {
      return children === block.children ? block : { ...block, children };
    }

    const sourceAssets = assets.filter((asset) => {
      const meta = asRecord(asset.meta);
      const assetBlockID = typeof meta?.provider_block_id === 'string'
        ? meta.provider_block_id.trim()
        : '';
      return asset.source_type === 'input_resource'
        && meta?.provider === 'feishu'
        && meta?.origin === 'source_document'
        && providerBlockID !== ''
        && assetBlockID === providerBlockID;
    });
    const asset = sourceAssets.length === 1
      ? sourceAssets[0]
      : generatedAssets.length === 1 ? generatedAssets[0] : undefined;
    if (!asset) {
      return children === block.children ? block : { ...block, children };
    }
    const localPath = typeof asset.local_path === 'string' ? asset.local_path.trim() : '';
    const uri = typeof asset.uri === 'string' ? asset.uri.trim() : '';
    const path = asset.source_type === 'input_resource' ? localPath || uri : uri || localPath;
    if (!path) {
      return children === block.children ? block : { ...block, children };
    }
    const assetID = typeof asset.media_asset_id === 'string' ? asset.media_asset_id : '';
    changed = true;
    return {
      ...block,
      children,
      references: [
        ...(block.references ?? []),
        { type: 'media_asset', id: assetID, path },
      ],
    };
  });

  const blocks = restoreBlocks(document.blocks);
  return changed ? { ...document, blocks } : document;
}

export interface WriterIRParseResult {
  ok: boolean;
  document?: WriterDocument;
  issues: string[];
}

export type WriterBlockMoveDirection = 'up' | 'down';
export type WriterInlineStyle = 'strong' | 'italic';
export type WriterBlockFormat = 'paragraph' | 'heading' | 'code' | 'list_item';
/** Feishu Docx FontColor / FontBackgroundColor value fields. */
export type WriterSpanColorField = 'text_color' | 'background_color';

const WRITER_CODE_LANGUAGE_ALIASES: Record<string, string> = {
  cs: 'csharp',
  dockerfile: 'docker',
  golang: 'go',
  html: 'markup',
  js: 'javascript',
  md: 'markdown',
  plaintext: 'text',
  py: 'python',
  sh: 'bash',
  shell: 'bash',
  ts: 'typescript',
  xml: 'markup',
  yml: 'yaml',
};

export function normalizeWriterCodeLanguage(language: unknown): string {
  if (typeof language !== 'string') return 'text';
  const normalized = language.trim().toLowerCase();
  return WRITER_CODE_LANGUAGE_ALIASES[normalized] || normalized || 'text';
}

export interface WriterPaletteColor {
  id: number;
  hex: string;
  /** i18n key suffix under chat.writerIR.colors.* */
  name: string;
}

/**
 * Feishu FontColor 1-7, ordered for the picker UI
 * (gray first, then chromatic colors — default/black is handled separately).
 */
export const WRITER_TEXT_COLOR_PALETTE: readonly WriterPaletteColor[] = [
  { id: 7, hex: '#8F959E', name: 'gray' },
  { id: 1, hex: '#D83931', name: 'red' },
  { id: 2, hex: '#DE7802', name: 'orange' },
  { id: 3, hex: '#DC9B04', name: 'yellow' },
  { id: 4, hex: '#2EA121', name: 'green' },
  { id: 5, hex: '#245BDB', name: 'blue' },
  { id: 6, hex: '#6425D0', name: 'purple' },
] as const;

/** Feishu FontBackgroundColor light series 1-7 (picker order). */
export const WRITER_BACKGROUND_LIGHT_PALETTE: readonly WriterPaletteColor[] = [
  { id: 7, hex: '#DEE0E3', name: 'gray' },
  { id: 1, hex: '#FBBFBC', name: 'red' },
  { id: 2, hex: '#FED4A4', name: 'orange' },
  { id: 3, hex: '#FFF67A', name: 'yellow' },
  { id: 4, hex: '#B7EDB1', name: 'green' },
  { id: 5, hex: '#BACEFD', name: 'blue' },
  { id: 6, hex: '#CDB2FA', name: 'purple' },
] as const;

/** Feishu FontBackgroundColor dark/saturated series 8-14. */
export const WRITER_BACKGROUND_DARK_PALETTE: readonly WriterPaletteColor[] = [
  { id: 14, hex: '#BBBFC4', name: 'gray' },
  { id: 8, hex: '#F76964', name: 'red' },
  { id: 9, hex: '#FFA53D', name: 'orange' },
  { id: 10, hex: '#FFE928', name: 'yellow' },
  { id: 11, hex: '#62D256', name: 'green' },
  { id: 12, hex: '#4E83FD', name: 'blue' },
  { id: 13, hex: '#935AF6', name: 'purple' },
] as const;

/** @deprecated Prefer light/dark palettes; kept for callers that need all ids. */
export const WRITER_BACKGROUND_COLOR_PALETTE: readonly WriterPaletteColor[] = [
  ...WRITER_BACKGROUND_LIGHT_PALETTE,
  ...WRITER_BACKGROUND_DARK_PALETTE,
] as const;

const WRITER_TEXT_COLOR_HEX = Object.fromEntries(
  WRITER_TEXT_COLOR_PALETTE.map((item) => [item.id, item.hex]),
) as Record<number, string>;

const WRITER_BACKGROUND_COLOR_HEX = Object.fromEntries(
  WRITER_BACKGROUND_COLOR_PALETTE.map((item) => [item.id, item.hex]),
) as Record<number, string>;

/** Default body text color shown on the toolbar "A" control. */
export const WRITER_DEFAULT_TEXT_COLOR_HEX = '#1F2329';

export function writerTextColorHex(colorId: number | null | undefined): string | undefined {
  if (typeof colorId !== 'number' || !Number.isFinite(colorId)) return undefined;
  return WRITER_TEXT_COLOR_HEX[Math.trunc(colorId)];
}

export function writerBackgroundColorHex(colorId: number | null | undefined): string | undefined {
  if (typeof colorId !== 'number' || !Number.isFinite(colorId)) return undefined;
  return WRITER_BACKGROUND_COLOR_HEX[Math.trunc(colorId)];
}

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
        if (!isRecord(span)) {
          issues.push(`${path}.spans[${index}].text must be a string`);
          valid = false;
          return;
        }
        if (span.text === undefined) {
          span.text = '';
        } else if (typeof span.text !== 'string') {
          issues.push(`${path}.spans[${index}].text must be a string`);
          valid = false;
          return;
        }
        for (const key of ['style', 'stype'] as const) {
          const styles = span[key];
          const validStyleArray = Array.isArray(styles)
            && styles.every((item) => typeof item === 'string');
          const validStyleMap = isRecord(styles);
          if (styles !== undefined && !validStyleArray && !validStyleMap) {
            issues.push(
              `${path}.spans[${index}].${key} must be a string array or object`,
            );
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
  if (value.ui_editable !== undefined && typeof value.ui_editable !== 'boolean') {
    issues.push('ui_editable must be a boolean');
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
  const styleMap = isRecord(span.style)
    ? span.style
    : isRecord(span.stype)
      ? span.stype
      : null;
  if (styleMap) {
    return Object.entries(styleMap)
      .filter(([, enabled]) => enabled === true)
      .map(([style]) => style === 'inline_code' ? 'code' : style);
  }
  return [];
}

export function getWriterInternalReference(
  span: WriterSpan,
): WriterInternalReference | undefined {
  const style = isRecord(span.style)
    ? span.style
    : isRecord(span.stype)
      ? span.stype
      : undefined;
  const link = style && isRecord(style.link) ? style.link : undefined;
  if (link?.type !== 'internal_ref' || typeof link.target_node_id !== 'string') {
    return undefined;
  }
  const targetNodeId = link.target_node_id.trim();
  if (!targetNodeId) return undefined;
  return {
    targetNodeId,
    ...(typeof link.display_text === 'string' ? { displayText: link.display_text } : {}),
  };
}

function writerStyleMap(span: WriterSpan): Record<string, unknown> {
  const source = span.style ?? span.stype;
  if (isRecord(source)) return { ...source };
  if (!Array.isArray(source)) return {};
  return Object.fromEntries(source.map((style) => [
    style === 'strong' ? 'bold' : style === 'code' ? 'inline_code' : style,
    true,
  ]));
}

function normalizeWriterBlockForSync(block: WriterBlock): WriterBlock {
  const spans = block.spans?.map((span) => {
    const normalized = { ...span, style: writerStyleMap(span) };
    delete normalized.stype;
    return normalized;
  });
  const children = block.children?.map(normalizeWriterBlockForSync);
  return {
    ...block,
    ...(spans ? { spans } : {}),
    ...(children ? { children } : {}),
  };
}

/**
 * Convert legacy array/stype rich-text values to the Writer IR wire contract
 * accepted by the backend.
 */
export function normalizeWriterDocumentForSync(
  document: WriterDocument,
): WriterDocument {
  return {
    ...document,
    blocks: document.blocks.map(normalizeWriterBlockForSync),
  };
}

/** Restore the user-authored label after the backend materializes an internal reference number. */
export function restoreWriterInternalReferenceDisplayText(
  document: WriterDocument,
): WriterDocument {
  let changed = false;
  const restoreBlocks = (blocks: WriterBlock[]): WriterBlock[] => blocks.map((block) => {
    const children = block.children?.length ? restoreBlocks(block.children) : block.children;
    let spans = block.spans;
    let spansChanged = false;
    if (spans?.length) {
      spans = spans.map((span) => {
        const reference = getWriterInternalReference(span);
        if (reference?.displayText === undefined || reference.displayText === span.text) return span;
        spansChanged = true;
        return { ...span, text: reference.displayText };
      });
    }
    if (!spansChanged && children === block.children) return block;
    changed = true;
    return {
      ...block,
      ...(spansChanged && spans ? {
        spans,
        content: spans.map((span) => span.text).join(''),
      } : {}),
      children,
    };
  });

  const blocks = restoreBlocks(document.blocks);
  return changed ? { ...document, blocks } : document;
}

/** Semantic equality for WriterDocument, ignoring object identity. */
export function sameWriterDocument(
  left: WriterDocument | undefined | null,
  right: WriterDocument | undefined | null,
): boolean {
  if (left === right) return true;
  if (!left || !right) return false;
  return JSON.stringify(left) === JSON.stringify(right);
}

/**
 * Compare documents after applying the same sync normalization used on the wire.
 * Useful when the server echoes a normalized copy of what the client just saved.
 */
export function sameWriterDocumentForSync(
  left: WriterDocument | undefined | null,
  right: WriterDocument | undefined | null,
): boolean {
  if (left === right) return true;
  if (!left || !right) return false;
  return sameWriterDocument(
    normalizeWriterDocumentForSync(left),
    normalizeWriterDocumentForSync(right),
  );
}

function hasWriterInlineStyle(span: WriterSpan, style: WriterInlineStyle): boolean {
  const styles = getWriterSpanStyles(span);
  return style === 'strong'
    ? styles.includes('strong') || styles.includes('bold')
    : styles.includes(style);
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

export function findWriterBlockParent(
  blocks: WriterBlock[],
  nodeId: string,
): WriterBlock | undefined {
  for (const block of blocks) {
    if ((block.children ?? []).some((child) => child.node_id === nodeId)) {
      return block;
    }
    const nested = findWriterBlockParent(block.children ?? [], nodeId);
    if (nested) return nested;
  }
  return undefined;
}

export function updateWriterCodeLanguage(
  document: WriterDocument,
  nodeId: string,
  language: string,
): WriterDocument {
  const normalized = normalizeWriterCodeLanguage(language);
  const result = replaceBlockInTree(document.blocks, nodeId, (block) => {
    if (
      block.type !== 'code'
      || block.editable === false
      || block.language === normalized
    ) return block;
    return { ...block, language: normalized };
  });
  return result.changed ? { ...document, blocks: result.blocks } : document;
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
  const styleTemplate = (block.spans ?? []).find(
    (span) => span.style !== undefined || span.stype !== undefined,
  );
  const usesSType = styleTemplate?.style === undefined
    && styleTemplate?.stype !== undefined;
  const emptyStyles = isRecord(
    usesSType ? styleTemplate?.stype : styleTemplate?.style,
  ) ? {} : [];
  return usesSType
    ? [{ text: content, stype: emptyStyles }]
    : [{ text: content, style: emptyStyles }];
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
  if (isRecord(template?.style)) return { text, style: { ...template.style } };
  if (Array.isArray(template?.stype)) return { text, stype: [...template.stype] };
  if (isRecord(template?.stype)) return { text, stype: { ...template.stype } };
  return spanForEditedContent(block, text)[0];
}

function haveSameWriterStyleValue(
  left: WriterSpan['style'],
  right: WriterSpan['style'],
): boolean {
  if (Array.isArray(left) || Array.isArray(right)) {
    return Array.isArray(left)
      && Array.isArray(right)
      && left.length === right.length
      && left.every((style, index) => style === right[index]);
  }
  if (isRecord(left) || isRecord(right)) {
    if (!isRecord(left) || !isRecord(right)) return false;
    const leftKeys = Object.keys(left);
    return leftKeys.length === Object.keys(right).length
      && leftKeys.every(
        (key) => Object.prototype.hasOwnProperty.call(right, key)
          && Object.is(left[key], right[key]),
      );
  }
  return left === right;
}

function haveSameWriterStyles(left: WriterSpan, right: WriterSpan): boolean {
  return haveSameWriterStyleValue(left.style, right.style)
    && haveSameWriterStyleValue(left.stype, right.stype);
}

function canMergeWriterSpans(left: WriterSpan, right: WriterSpan): boolean {
  const allowedKeys = new Set(['text', 'style', 'stype']);
  if (
    Object.keys(left).some((key) => !allowedKeys.has(key))
    || Object.keys(right).some((key) => !allowedKeys.has(key))
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

const WRITER_CODE_LANGUAGE_MENU_TEXT = [
  'Plain text',
  'JavaScript',
  'TypeScript',
  'JSX',
  'TSX',
  'Python',
  'Java',
  'Go',
  'Rust',
  'C',
  'C++',
  'C#',
  'SQL',
  'Shell',
  'JSON',
  'YAML',
  'HTML / XML',
  'CSS',
  'Markdown',
  'Dockerfile',
].join('');

function isWriterCodeToolbarPollution(content: string): boolean {
  const prefixes = ['代码块', 'Code block'];
  const suffixes = [
    '取消自动换行复制',
    '自动换行复制',
    '取消自动换行已复制',
    '自动换行已复制',
    'Disable wrappingCopy',
    'Wrap linesCopy',
    'Disable wrappingCopied',
    'Wrap linesCopied',
  ];
  return prefixes.some((prefix) => suffixes.some(
    (suffix) => content === `${prefix}${WRITER_CODE_LANGUAGE_MENU_TEXT}${suffix}`,
  ));
}

/**
 * Repairs documents affected by the short-lived code-toolbar parsing bug.
 * The match is intentionally exact so legitimate code is never modified.
 */
export function repairWriterCodeToolbarPollution(
  document: WriterDocument,
): WriterDocument {
  const pollutedNodeIds: string[] = [];
  const visit = (blocks: WriterBlock[]) => {
    blocks.forEach((block) => {
      if (
        block.type === 'code'
        && isWriterCodeToolbarPollution(block.content ?? '')
      ) pollutedNodeIds.push(block.node_id);
      visit(block.children ?? []);
    });
  };
  visit(document.blocks);
  return pollutedNodeIds.reduce(
    (current, nodeId) => updateWriterBlockContent(current, nodeId, ''),
    document,
  );
}

function withWriterInlineStyle(
  span: WriterSpan,
  style: WriterInlineStyle,
  enabled: boolean,
): WriterSpan {
  const key = span.style === undefined && span.stype !== undefined ? 'stype' : 'style';
  const aliases = style === 'strong' ? new Set(['strong', 'bold']) : new Set([style]);
  const currentStyles = span[key];
  if (isRecord(currentStyles)) {
    const nextStyles = { ...currentStyles };
    aliases.forEach((alias) => delete nextStyles[alias]);
    if (enabled) nextStyles[style === 'strong' ? 'bold' : style] = true;
    return { ...span, [key]: nextStyles };
  }
  const styles = getWriterSpanStyles(span).filter((item) => !aliases.has(item));
  if (enabled) styles.push(style);
  return { ...span, [key]: styles };
}

export function toggleWriterBlockInlineStyle(
  document: WriterDocument,
  nodeId: string,
  start: number,
  end: number,
  style: WriterInlineStyle,
): WriterDocument {
  if (start < 0 || end <= start) return document;
  const result = replaceBlockInTree(document.blocks, nodeId, (block) => {
    if (block.type === 'document' || block.editable === false) return block;
    const contentLength = Array.from(block.content ?? '').length;
    const safeStart = Math.min(start, contentLength);
    const safeEnd = Math.min(end, contentLength);
    if (safeEnd <= safeStart) return block;

    const sourceSpans = block.spans?.length
      && block.spans.map((span) => span.text).join('') === (block.content ?? '')
      ? block.spans
      : spanForEditedContent(block, block.content ?? '');
    const selected = sliceWriterSpans(sourceSpans, safeStart, safeEnd);
    const enable = !selected.every((span) => hasWriterInlineStyle(span, style));
    const spans = mergeAdjacentWriterSpans([
      ...sliceWriterSpans(sourceSpans, 0, safeStart),
      ...selected.map((span) => withWriterInlineStyle(span, style, enable)),
      ...sliceWriterSpans(sourceSpans, safeEnd, contentLength),
    ]);
    return { ...block, spans };
  });
  return result.changed ? { ...document, blocks: result.blocks } : document;
}

export function writerBlockRangeHasInlineStyle(
  block: WriterBlock,
  start: number,
  end: number,
  style: WriterInlineStyle,
): boolean {
  if (start < 0 || end <= start) return false;
  const contentLength = Array.from(block.content ?? '').length;
  const safeStart = Math.min(start, contentLength);
  const safeEnd = Math.min(end, contentLength);
  if (safeEnd <= safeStart) return false;
  const sourceSpans = block.spans?.length
    && block.spans.map((span) => span.text).join('') === (block.content ?? '')
    ? block.spans
    : spanForEditedContent(block, block.content ?? '');
  const selected = sliceWriterSpans(sourceSpans, safeStart, safeEnd);
  return selected.length > 0
    && selected.every((span) => hasWriterInlineStyle(span, style));
}

export function getWriterSpanColor(
  span: WriterSpan,
  field: WriterSpanColorField,
): number | undefined {
  const styleMap = writerStyleMap(span);
  const value = styleMap[field];
  if (typeof value !== 'number' || !Number.isFinite(value)) return undefined;
  const colorId = Math.trunc(value);
  return colorId > 0 ? colorId : undefined;
}

function withWriterSpanColor(
  span: WriterSpan,
  field: WriterSpanColorField,
  colorId: number | null,
): WriterSpan {
  const key = span.style === undefined && span.stype !== undefined ? 'stype' : 'style';
  const nextStyles = writerStyleMap(span);
  if (colorId == null) delete nextStyles[field];
  else nextStyles[field] = Math.trunc(colorId);
  return { ...span, [key]: nextStyles };
}

export function applyWriterBlockSpanColor(
  document: WriterDocument,
  nodeId: string,
  start: number,
  end: number,
  field: WriterSpanColorField,
  colorId: number | null,
): WriterDocument {
  if (start < 0 || end <= start) return document;
  const result = replaceBlockInTree(document.blocks, nodeId, (block) => {
    if (block.type === 'document' || block.editable === false) return block;
    const contentLength = Array.from(block.content ?? '').length;
    const safeStart = Math.min(start, contentLength);
    const safeEnd = Math.min(end, contentLength);
    if (safeEnd <= safeStart) return block;

    const sourceSpans = block.spans?.length
      && block.spans.map((span) => span.text).join('') === (block.content ?? '')
      ? block.spans
      : spanForEditedContent(block, block.content ?? '');
    const spans = mergeAdjacentWriterSpans([
      ...sliceWriterSpans(sourceSpans, 0, safeStart),
      ...sliceWriterSpans(sourceSpans, safeStart, safeEnd).map(
        (span) => withWriterSpanColor(span, field, colorId),
      ),
      ...sliceWriterSpans(sourceSpans, safeEnd, contentLength),
    ]);
    return { ...block, spans };
  });
  return result.changed ? { ...document, blocks: result.blocks } : document;
}

function withWriterInternalReference(
  span: WriterSpan,
  targetNodeId: string,
): WriterSpan {
  const key = span.style === undefined && span.stype !== undefined ? 'stype' : 'style';
  return {
    ...span,
    [key]: {
      ...writerStyleMap(span),
      link: {
        type: 'internal_ref',
        target_node_id: targetNodeId,
        display_text: span.text,
      },
    },
  };
}

function writerSpanWithDisplayText(span: WriterSpan): WriterSpan {
  const displayText = getWriterInternalReference(span)?.displayText;
  return displayText !== undefined && displayText !== span.text
    ? { ...span, text: displayText }
    : span;
}

function writerBlockDisplaySpans(block: WriterBlock): WriterSpan[] {
  const sourceSpans = block.spans?.length
    && block.spans.map((span) => span.text).join('') === (block.content ?? '')
    ? block.spans
    : spanForEditedContent(block, block.content ?? '');
  return sourceSpans.map(writerSpanWithDisplayText);
}

function writerSpanWithCurrentReferenceDisplayText(span: WriterSpan): WriterSpan {
  const reference = getWriterInternalReference(span);
  if (!reference) return span;
  const key = isRecord(span.style)
    ? 'style'
    : isRecord(span.stype)
      ? 'stype'
      : undefined;
  if (!key) return span;
  const styles = span[key] as Record<string, unknown>;
  const link = isRecord(styles.link) ? styles.link : undefined;
  if (!link || link.display_text === span.text) return span;
  return {
    ...span,
    [key]: {
      ...styles,
      link: { ...link, display_text: span.text },
    },
  };
}

function withoutWriterInternalReference(span: WriterSpan): WriterSpan {
  if (!getWriterInternalReference(span)) return span;
  const key = span.style === undefined && span.stype !== undefined ? 'stype' : 'style';
  const nextStyles = writerStyleMap(span);
  delete nextStyles.link;
  return { ...span, [key]: nextStyles };
}

/** Apply an IR internal reference to the selected source-text range. */
export function applyWriterBlockInternalReference(
  document: WriterDocument,
  nodeId: string,
  start: number,
  end: number,
  targetNodeId: string,
): WriterDocument {
  if (!targetNodeId || start < 0 || end <= start) return document;
  const result = replaceBlockInTree(document.blocks, nodeId, (block) => {
    if (block.type === 'document' || block.editable === false) return block;
    const contentLength = Array.from(block.content ?? '').length;
    const safeStart = Math.min(start, contentLength);
    const safeEnd = Math.min(end, contentLength);
    if (safeEnd <= safeStart) return block;

    const sourceSpans = block.spans?.length
      && block.spans.map((span) => span.text).join('') === (block.content ?? '')
      ? block.spans
      : spanForEditedContent(block, block.content ?? '');
    const spans = mergeAdjacentWriterSpans([
      ...sliceWriterSpans(sourceSpans, 0, safeStart),
      ...sliceWriterSpans(sourceSpans, safeStart, safeEnd).map(
        (span) => withWriterInternalReference(span, targetNodeId),
      ),
      ...sliceWriterSpans(sourceSpans, safeEnd, contentLength),
    ]);
    return { ...block, spans };
  });
  return result.changed ? { ...document, blocks: result.blocks } : document;
}

/** Whether any text in the selected range carries an IR internal reference. */
export function writerBlockRangeHasInternalReference(
  block: WriterBlock,
  start: number,
  end: number,
): boolean {
  if (start < 0 || end <= start) return false;
  const sourceSpans = writerBlockDisplaySpans(block);
  const contentLength = sourceSpans.reduce(
    (total, span) => total + Array.from(span.text).length,
    0,
  );
  const safeStart = Math.min(start, contentLength);
  const safeEnd = Math.min(end, contentLength);
  if (safeEnd <= safeStart) return false;
  return sliceWriterSpans(sourceSpans, safeStart, safeEnd)
    .some((span) => getWriterInternalReference(span) !== undefined);
}

/** Remove internal-reference styling from the selected text without changing its wording. */
export function removeWriterBlockInternalReference(
  document: WriterDocument,
  nodeId: string,
  start: number,
  end: number,
): WriterDocument {
  if (start < 0 || end <= start) return document;
  const result = replaceBlockInTree(document.blocks, nodeId, (block) => {
    if (block.type === 'document' || block.editable === false) return block;
    const sourceSpans = writerBlockDisplaySpans(block);
    const displayContent = sourceSpans.map((span) => span.text).join('');
    const contentLength = Array.from(displayContent).length;
    const safeStart = Math.min(start, contentLength);
    const safeEnd = Math.min(end, contentLength);
    if (safeEnd <= safeStart) return block;

    const displaySpansInRange = (rangeStart: number, rangeEnd: number) => (
      sliceWriterSpans(sourceSpans, rangeStart, rangeEnd)
        .map(writerSpanWithCurrentReferenceDisplayText)
    );
    let removed = false;
    const selectedSpans = displaySpansInRange(safeStart, safeEnd).map((span) => {
      const nextSpan = withoutWriterInternalReference(span);
      if (nextSpan !== span) removed = true;
      return nextSpan;
    });
    if (!removed) return block;

    const spans = mergeAdjacentWriterSpans([
      ...displaySpansInRange(0, safeStart),
      ...selectedSpans,
      ...displaySpansInRange(safeEnd, contentLength),
    ]);
    return { ...block, content: displayContent, spans };
  });
  return result.changed ? { ...document, blocks: result.blocks } : document;
}

/**
 * Shared color for a selection: number when uniform, null when unset,
 * undefined when the selection mixes multiple colors.
 */
export function writerBlockRangeSpanColor(
  block: WriterBlock,
  start: number,
  end: number,
  field: WriterSpanColorField,
): number | null | undefined {
  if (start < 0 || end <= start) return null;
  const contentLength = Array.from(block.content ?? '').length;
  const safeStart = Math.min(start, contentLength);
  const safeEnd = Math.min(end, contentLength);
  if (safeEnd <= safeStart) return null;
  const sourceSpans = block.spans?.length
    && block.spans.map((span) => span.text).join('') === (block.content ?? '')
    ? block.spans
    : spanForEditedContent(block, block.content ?? '');
  const selected = sliceWriterSpans(sourceSpans, safeStart, safeEnd);
  if (selected.length === 0) return null;
  const first = getWriterSpanColor(selected[0], field) ?? null;
  for (let index = 1; index < selected.length; index += 1) {
    const current = getWriterSpanColor(selected[index], field) ?? null;
    if (current !== first) return undefined;
  }
  return first;
}

export function updateWriterBlockFormat(
  document: WriterDocument,
  nodeId: string,
  format: WriterBlockFormat,
  options?: number | { headingLevel?: number; ordered?: boolean },
): WriterDocument {
  const resolved = typeof options === 'number'
    ? { headingLevel: options }
    : (options ?? {});
  const result = replaceBlockInTree(document.blocks, nodeId, (block) => {
    if (
      block.type === 'document'
      || block.editable === false
      || (block.children?.length ?? 0) > 0
    ) return block;
    const nextLevel = Math.min(6, Math.max(1, Math.trunc(resolved.headingLevel ?? 1)));
    const nextOrdered = Boolean(resolved.ordered);
    if (
      block.type === format
      && (format !== 'heading' || Number(block.numbering?.level) === nextLevel)
      && (format !== 'list_item' || Boolean(block.numbering?.ordered) === nextOrdered)
    ) {
      return block;
    }

    const numbering = { ...(block.numbering ?? {}) };
    delete numbering.level;
    delete numbering.ordered;
    if (format === 'heading') numbering.level = nextLevel;
    if (format === 'list_item') numbering.ordered = nextOrdered;
    return { ...block, type: format, numbering };
  });
  return result.changed ? { ...document, blocks: result.blocks } : document;
}

export function updateWriterDocumentTitle(
  document: WriterDocument,
  title: string,
): WriterDocument {
  if (document.title === title) return document;
  const metadata = document.metadata
    ? { ...document.metadata, title }
    : document.metadata;
  const documentRoot = document.blocks.find(
    (block) => block.type === 'document' && block.editable !== false,
  );
  const rootResult = documentRoot
    ? replaceBlockInTree(document.blocks, documentRoot.node_id, (block) => ({
      ...block,
      content: title,
      spans: spanForEditedContent(block, title),
    }))
    : { blocks: document.blocks };
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
    spans: [{ text: '', style: {} }],
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

export function splitWriterBlock(
  document: WriterDocument,
  nodeId: string,
  start: number,
  end = start,
): { document: WriterDocument; insertedNodeId?: string } {
  const target = findWriterBlock(document.blocks, nodeId);
  if (
    !target
    || !['paragraph', 'heading', 'list_item', 'quote'].includes(target.type)
    || target.editable === false
    || !Number.isFinite(start)
    || !Number.isFinite(end)
  ) {
    return { document };
  }

  const content = target.content ?? '';
  const characters = Array.from(content);
  const safeStart = Math.min(characters.length, Math.max(0, Math.trunc(start)));
  const safeEnd = Math.min(
    characters.length,
    Math.max(safeStart, Math.trunc(end)),
  );
  const sourceSpans = target.spans?.length
    && target.spans.map((span) => span.text).join('') === content
    ? target.spans
    : spanForEditedContent(target, content);
  const leadingContent = characters.slice(0, safeStart).join('');
  const trailingContent = characters.slice(safeEnd).join('');
  const leadingSpans = sliceWriterSpans(sourceSpans, 0, safeStart);
  const trailingSpans = sliceWriterSpans(sourceSpans, safeEnd, characters.length);
  const insertedType = target.type === 'heading' ? 'paragraph' : target.type;
  const insertedBlock: WriterBlock = {
    ...target,
    node_id: createNodeId(),
    type: insertedType,
    content: trailingContent,
    spans: trailingSpans.length > 0
      ? trailingSpans
      : spanForEditedContent(target, trailingContent),
    children: [],
    numbering: insertedType === target.type
      ? { ...(target.numbering ?? {}) }
      : {},
    provider_binding: {},
    provider_payload: {},
  };

  const result = updateChildrenArray(document.blocks, (siblings) => {
    const index = siblings.findIndex((block) => block.node_id === nodeId);
    if (index < 0) return { siblings, changed: false };

    const next = siblings.slice();
    next.splice(
      index,
      1,
      {
        ...siblings[index],
        content: leadingContent,
        spans: leadingSpans.length > 0
          ? leadingSpans
          : spanForEditedContent(target, leadingContent),
      },
      insertedBlock,
    );
    return { siblings: next, changed: true };
  });

  return result.changed
    ? {
      document: withUpdatedBlocks(document, result.blocks),
      insertedNodeId: insertedBlock.node_id,
    }
    : { document };
}

/**
 * Nest the current block as the last child of its previous sibling.
 * Used for Tab-based block hierarchy.
 */
export function indentWriterBlock(
  document: WriterDocument,
  nodeId: string,
): { document: WriterDocument; insertedNodeId?: string } {
  const target = findWriterBlock(document.blocks, nodeId);
  if (!target || target.type === 'document' || target.editable === false) {
    return { document };
  }

  const result = updateChildrenArray(document.blocks, (siblings) => {
    const index = siblings.findIndex((block) => block.node_id === nodeId);
    if (index <= 0) return { siblings, changed: false };

    const previous = siblings[index - 1];
    if (previous.type === 'document' || previous.editable === false) {
      return { siblings, changed: false };
    }

    const next = siblings.slice();
    next.splice(index, 1);
    next[index - 1] = {
      ...previous,
      children: [...(previous.children ?? []), target],
    };
    return { siblings: next, changed: true };
  });

  return result.changed
    ? { document: withUpdatedBlocks(document, result.blocks), insertedNodeId: nodeId }
    : { document };
}

/**
 * Move an empty nested block out of its parent children list and place it as
 * the next sibling of that parent. Used when Enter is pressed on an empty
 * indented block so editing does not keep deepening the tree.
 */
export function liftWriterBlockAfterParent(
  document: WriterDocument,
  nodeId: string,
): { document: WriterDocument; insertedNodeId?: string } {
  const target = findWriterBlock(document.blocks, nodeId);
  const parent = findWriterBlockParent(document.blocks, nodeId);
  if (
    !target
    || !parent
    || target.type === 'document'
    || parent.type === 'document'
    || target.editable === false
  ) {
    return { document };
  }

  const result = updateChildrenArray(document.blocks, (siblings) => {
    const parentIndex = siblings.findIndex((block) => block.node_id === parent.node_id);
    if (parentIndex < 0) return { siblings, changed: false };

    const parentBlock = siblings[parentIndex];
    const childIndex = (parentBlock.children ?? []).findIndex(
      (child) => child.node_id === nodeId,
    );
    if (childIndex < 0) return { siblings, changed: false };

    const nextChildren = (parentBlock.children ?? []).filter(
      (_, index) => index !== childIndex,
    );
    const next = siblings.slice();
    next[parentIndex] = { ...parentBlock, children: nextChildren };
    next.splice(parentIndex + 1, 0, target);
    return { siblings: next, changed: true };
  });

  return result.changed
    ? { document: withUpdatedBlocks(document, result.blocks), insertedNodeId: nodeId }
    : { document };
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

/** Insert an empty paragraph as the first child of a block (e.g. Enter at end of heading). */
export function insertWriterChildParagraph(
  document: WriterDocument,
  parentNodeId: string,
): { document: WriterDocument; insertedNodeId?: string } {
  const parent = findWriterBlock(document.blocks, parentNodeId);
  if (!parent || parent.type === 'document' || parent.editable === false) {
    return { document };
  }
  const paragraph = createWriterParagraph(document.stage);
  const result = replaceBlockInTree(document.blocks, parentNodeId, (block) => ({
    ...block,
    children: [paragraph, ...(block.children ?? [])],
  }));
  return result.changed
    ? { document: withUpdatedBlocks(document, result.blocks), insertedNodeId: paragraph.node_id }
    : { document };
}

/**
 * Split a heading at the caret: keep the leading text as the heading, and move
 * the trailing text into a new first-child paragraph.
 */
export function splitWriterHeadingIntoChild(
  document: WriterDocument,
  nodeId: string,
  start: number,
  end = start,
): { document: WriterDocument; insertedNodeId?: string } {
  const target = findWriterBlock(document.blocks, nodeId);
  if (
    !target
    || target.type !== 'heading'
    || target.editable === false
    || !Number.isFinite(start)
    || !Number.isFinite(end)
  ) {
    return { document };
  }

  const content = target.content ?? '';
  const characters = Array.from(content);
  const safeStart = Math.min(characters.length, Math.max(0, Math.trunc(start)));
  const safeEnd = Math.min(
    characters.length,
    Math.max(safeStart, Math.trunc(end)),
  );
  const sourceSpans = target.spans?.length
    && target.spans.map((span) => span.text).join('') === content
    ? target.spans
    : spanForEditedContent(target, content);
  const leadingContent = characters.slice(0, safeStart).join('');
  const trailingContent = characters.slice(safeEnd).join('');
  const leadingSpans = sliceWriterSpans(sourceSpans, 0, safeStart);
  const trailingSpans = sliceWriterSpans(sourceSpans, safeEnd, characters.length);

  const childParagraph: WriterBlock = {
    ...createWriterParagraph(document.stage),
    content: trailingContent,
    spans: trailingSpans.length > 0
      ? trailingSpans
      : spanForEditedContent(target, trailingContent),
  };

  const result = replaceBlockInTree(document.blocks, nodeId, (block) => ({
    ...block,
    content: leadingContent,
    spans: leadingSpans.length > 0
      ? leadingSpans
      : spanForEditedContent(target, leadingContent),
    children: [childParagraph, ...(block.children ?? [])],
  }));

  return result.changed
    ? {
      document: withUpdatedBlocks(document, result.blocks),
      insertedNodeId: childParagraph.node_id,
    }
    : { document };
}

/** Convert a block to a paragraph in place (empty heading / list exit). */
export function convertWriterBlockToParagraph(
  document: WriterDocument,
  nodeId: string,
): { document: WriterDocument; insertedNodeId?: string } {
  const target = findWriterBlock(document.blocks, nodeId);
  if (
    !target
    || target.type === 'document'
    || target.type === 'paragraph'
    || target.editable === false
  ) {
    return { document };
  }

  const result = replaceBlockInTree(document.blocks, nodeId, (block) => {
    if (block.type === 'paragraph') return block;
    const numbering = { ...(block.numbering ?? {}) };
    delete numbering.level;
    delete numbering.ordered;
    return { ...block, type: 'paragraph', numbering };
  });

  return result.changed
    ? { document: withUpdatedBlocks(document, result.blocks), insertedNodeId: nodeId }
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

function isWriterBlockDescendant(
  root: WriterBlock,
  nodeId: string,
): boolean {
  return Boolean(findWriterBlock(root.children ?? [], nodeId));
}

function extractWriterBlock(
  blocks: WriterBlock[],
  nodeId: string,
): { blocks: WriterBlock[]; extracted: WriterBlock } | null {
  let extracted: WriterBlock | undefined;
  const result = updateChildrenArray(blocks, (siblings) => {
    const index = siblings.findIndex((block) => block.node_id === nodeId);
    if (index < 0) return { siblings, changed: false };
    extracted = siblings[index];
    return {
      siblings: siblings.filter((_, siblingIndex) => siblingIndex !== index),
      changed: true,
    };
  });
  if (!result.changed || !extracted) return null;
  return { blocks: result.blocks, extracted };
}

export type WriterBlockRelocateTarget =
  | { type: 'child'; parentId: string; index?: number }
  | { type: 'after'; afterId: string }
  | { type: 'before'; beforeId: string };

/**
 * Move a block under a heading (as child) or beside another block (as sibling).
 * Used by drag-and-drop restructuring in the editor.
 */
export function relocateWriterBlock(
  document: WriterDocument,
  nodeId: string,
  target: WriterBlockRelocateTarget,
): WriterDocument {
  const source = findWriterBlock(document.blocks, nodeId);
  if (!source || source.type === 'document' || source.editable === false) {
    return document;
  }

  if (target.type === 'child') {
    if (target.parentId === nodeId) return document;
    const parent = findWriterBlock(document.blocks, target.parentId);
    if (
      !parent
      || parent.type === 'document'
      || parent.editable === false
      || isWriterBlockDescendant(source, target.parentId)
    ) {
      return document;
    }
  } else {
    const anchorId = target.type === 'after' ? target.afterId : target.beforeId;
    if (anchorId === nodeId) return document;
    const anchor = findWriterBlock(document.blocks, anchorId);
    if (!anchor || anchor.type === 'document') return document;
    if (isWriterBlockDescendant(source, anchorId)) return document;
  }

  const extracted = extractWriterBlock(document.blocks, nodeId);
  if (!extracted) return document;
  const moving = extracted.extracted;

  if (target.type === 'child') {
    const inserted = updateChildrenArray(extracted.blocks, (siblings) => {
      const parentIndex = siblings.findIndex(
        (block) => block.node_id === target.parentId,
      );
      if (parentIndex < 0) return { siblings, changed: false };
      const parentBlock = siblings[parentIndex];
      const children = [...(parentBlock.children ?? [])];
      const index = target.index == null
        ? children.length
        : Math.min(children.length, Math.max(0, Math.trunc(target.index)));
      children.splice(index, 0, moving);
      const next = siblings.slice();
      next[parentIndex] = { ...parentBlock, children };
      return { siblings: next, changed: true };
    });
    return inserted.changed
      ? withUpdatedBlocks(document, inserted.blocks)
      : document;
  }

  const anchorId = target.type === 'after' ? target.afterId : target.beforeId;
  const inserted = updateChildrenArray(extracted.blocks, (siblings) => {
    const anchorIndex = siblings.findIndex((block) => block.node_id === anchorId);
    if (anchorIndex < 0) return { siblings, changed: false };
    const next = siblings.slice();
    const insertAt = target.type === 'after' ? anchorIndex + 1 : anchorIndex;
    next.splice(insertAt, 0, moving);
    return { siblings: next, changed: true };
  });
  return inserted.changed
    ? withUpdatedBlocks(document, inserted.blocks)
    : document;
}
