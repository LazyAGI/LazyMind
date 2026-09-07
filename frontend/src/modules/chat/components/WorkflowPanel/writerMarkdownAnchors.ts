const MATERIALIZED_SYSTEM_ANCHOR_RE = /<a\s+id=(["'])(block-[^"']+)\1((?:\s+[^>]*?)?)\s*>\s*<\/a>/gi;
const EDITOR_SYSTEM_ANCHOR_RE = /<a\s+id=(["'])(block-[^"']+)\1((?:\s+[^>]*?)?)\s*\/>/gi;
const SYSTEM_ANCHOR_LINE_RE = /^<a\s+id=(["'])(block-[^"']+)\1((?:\s+[^>]*?)?)\s*(?:\/>|>\s*<\/a>)$/i;
const HEADING_NUMBERING_CONFIG_LINE_RE = /^\s*<!--\s*heading-numbering:[^\r\n]*-->\s*$/i;
const OUTLINE_INSTRUCTION_LINE_RE = /^\s*<!--\s*writer:outline\s+(\{.*\})\s*-->\s*$/;
const SOURCE_PAGE_MARKER_RE = /^([ \t]*)<!--[ \t]*第[ \t]*(\d+)[ \t]*页[ \t]*-->[ \t]*$/i;
const EDITOR_PAGE_MARKER_RE = /^([ \t]*)<a\s+id=(["'])writer-page-marker-(\d+)\2\s*(?:\/>|>\s*<\/a>)[ \t]*$/i;

export interface WriterMarkdownOutlineInstruction {
  node_id: string;
  target_chars?: number;
  context_relations: Array<{
    relation: string;
    guidance?: string;
    target_node_id: string;
  }>;
  subtasks: Array<{
    subtask_id: string;
    subtask_type: 'retrieve' | 'extract' | 'reason';
    question: string;
    status: 'pending' | 'running' | 'completed' | 'retrying' | 'failed';
  }>;
}

function mapMarkdownLinesOutsideFences(
  markdown: string,
  mapLine: (line: string) => string,
): string {
  let fenceCharacter = '';
  let fenceLength = 0;

  return markdown.split(/\r?\n/).map((line) => {
    const fence = line.match(/^\s*(`{3,}|~{3,})/);
    if (fence) {
      const marker = fence[1];
      if (!fenceCharacter) {
        fenceCharacter = marker[0];
        fenceLength = marker.length;
      } else if (marker[0] === fenceCharacter && marker.length >= fenceLength) {
        fenceCharacter = '';
        fenceLength = 0;
      }
      return line;
    }
    return fenceCharacter ? line : mapLine(line);
  }).join('\n');
}

function pageMarkersForEditor(markdown: string): string {
  return mapMarkdownLinesOutsideFences(markdown, (line) => line.replace(
    SOURCE_PAGE_MARKER_RE,
    (_match, indent: string, page: string) => `${indent}<a id="writer-page-marker-${page}" />`,
  ));
}

function pageMarkersForSave(markdown: string): string {
  return mapMarkdownLinesOutsideFences(markdown, (line) => line.replace(
    EDITOR_PAGE_MARKER_RE,
    (_match, indent: string, _quote: string, page: string) => `${indent}<!-- 第 ${page} 页 -->`,
  ));
}

export interface WriterMarkdownReferenceTarget {
  anchorId: string;
  label: string;
  type: 'heading' | 'image';
}

export interface WriterMarkdownOutlineItem {
  anchorId: string;
  label: string;
  level: number;
  instructions?: WriterMarkdownOutlineInstruction;
}

export interface WriterMarkdownOutline {
  title?: string;
  items: WriterMarkdownOutlineItem[];
}

interface WriterMarkdownTargetBinding {
  lineIndex: number;
  anchorLineIndex?: number;
  anchorId?: string;
  anchorAttributes?: string;
  type: 'heading' | 'image';
  level?: number;
  signature: string;
  outlineInstructionLine?: string;
}

interface WriterMarkdownImageTarget {
  source: string;
  label: string;
}

interface WriterAnchorAssignment {
  anchorId?: string;
  anchorAttributes?: string;
}

function headingNumberingConfigLine(markdown: string): string | undefined {
  return markdown.split(/\r?\n/).find(
    (line) => HEADING_NUMBERING_CONFIG_LINE_RE.test(line),
  )?.trim();
}

function withHeadingNumberingConfigLine(
  markdown: string,
  configLine?: string,
): string {
  const lines = markdown.split(/\r?\n/).filter(
    (line) => !HEADING_NUMBERING_CONFIG_LINE_RE.test(line),
  );
  if (!configLine) return lines.join('\n');
  let insertAt = 0;
  if (lines[0]?.trim() === '---') {
    const end = lines.slice(1).findIndex((line) => /^(?:---|\.\.\.)\s*$/.test(line));
    if (end >= 0) insertAt = end + 2;
  }
  lines.splice(
    insertAt,
    0,
    configLine,
  );
  return lines.join('\n');
}

function writerMarkdownImageTarget(line: string): WriterMarkdownImageTarget | undefined {
  const markdownImage = line.match(/!\[((?:\\.|[^\\\]])*)\]\(((?:\\.|[^)])*)\)/);
  if (markdownImage) {
    return {
      source: markdownImage[2].trim(),
      label: markdownImage[1].replace(/\\([\\\]])/g, '$1').trim(),
    };
  }

  // MDXEditor serializes images with dimensions as HTML <img> elements.
  // Treat that as the same semantic image so its sidecar anchor survives the
  // Markdown -> editor -> Markdown round trip.
  const htmlImage = line.match(/<img\b([^>]*)\/?\s*>/i);
  if (!htmlImage) return undefined;
  const attributes = new Map<string, string>();
  const attributePattern = /\b(src|alt)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>`]+))/gi;
  let attribute: RegExpExecArray | null;
  while ((attribute = attributePattern.exec(htmlImage[1])) !== null) {
    attributes.set(attribute[1].toLowerCase(), attribute[2] ?? attribute[3] ?? attribute[4] ?? '');
  }
  const source = attributes.get('src')?.trim();
  if (!source) return undefined;
  return { source, label: attributes.get('alt')?.trim() ?? '' };
}

function writerMarkdownTargetBindings(markdown: string): WriterMarkdownTargetBinding[] {
  const bindings: WriterMarkdownTargetBinding[] = [];
  let pendingAnchor: {
    id: string;
    lineIndex: number;
    attributes?: string;
  } | undefined;
  let fenceCharacter = '';
  let fenceLength = 0;
  let currentHeadingIndex: number | undefined;

  markdown.split(/\r?\n/).forEach((line, lineIndex) => {
    const fence = line.match(/^\s*(`{3,}|~{3,})/);
    if (fence) {
      const marker = fence[1];
      if (!fenceCharacter) {
        fenceCharacter = marker[0];
        fenceLength = marker.length;
      } else if (marker[0] === fenceCharacter && marker.length >= fenceLength) {
        fenceCharacter = '';
        fenceLength = 0;
      }
      pendingAnchor = undefined;
      return;
    }
    if (fenceCharacter) return;

    const trimmed = line.trim();
    if (OUTLINE_INSTRUCTION_LINE_RE.test(trimmed)) {
      if (currentHeadingIndex !== undefined) {
        bindings[currentHeadingIndex].outlineInstructionLine = trimmed;
      }
      return;
    }
    const anchor = trimmed.match(SYSTEM_ANCHOR_LINE_RE);
    if (anchor) {
      pendingAnchor = {
        id: anchor[2],
        lineIndex,
        attributes: anchor[3]?.trim() || undefined,
      };
      return;
    }
    if (!trimmed) return;

    const heading = trimmed.match(/^(#{1,6})[ \t]+(.+?)(?:[ \t]+#+[ \t]*)?$/);
    if (heading) {
      bindings.push({
        lineIndex,
        anchorLineIndex: pendingAnchor?.lineIndex,
        anchorId: pendingAnchor?.id,
        anchorAttributes: pendingAnchor?.attributes,
        type: 'heading',
        level: heading[1].length,
        signature: `${heading[1].length}:${heading[2].trim()}`,
      });
      currentHeadingIndex = bindings.length - 1;
    } else {
      currentHeadingIndex = undefined;
      const image = writerMarkdownImageTarget(trimmed);
      if (image) {
        bindings.push({
          lineIndex,
          anchorLineIndex: pendingAnchor?.lineIndex,
          anchorId: pendingAnchor?.id,
          anchorAttributes: pendingAnchor?.attributes,
          type: 'image',
          // The URL is the stable identity when numbering changes image alt text.
          signature: `image:${image.source}`,
        });
      }
    }
    pendingAnchor = undefined;
  });

  return bindings;
}

function nextWriterUserAnchorId(usedAnchorIds: Set<string>): string {
  let anchorId = '';
  do {
    anchorId = `block-user-${globalThis.crypto.randomUUID()}`;
  } while (usedAnchorIds.has(anchorId));
  usedAnchorIds.add(anchorId);
  return anchorId;
}

/**
 * Keep system anchors attached to their headings and images when MDXEditor
 * serializes edits. Existing ids are authoritative; genuinely new reference
 * targets receive a new id.
 */
export function protectWriterMarkdownAnchors(
  previousMarkdown: string,
  nextMarkdown: string,
  generateMissingAnchors = true,
): string {
  const configLine = headingNumberingConfigLine(nextMarkdown)
    ?? headingNumberingConfigLine(previousMarkdown);
  const previous = writerMarkdownTargetBindings(previousMarkdown);
  const next = writerMarkdownTargetBindings(nextMarkdown);
  if (next.length === 0) {
    return withHeadingNumberingConfigLine(nextMarkdown, configLine);
  }

  const previousBySignature = new Map<string, number[]>();
  const previousAnchorOwner = new Map<string, number>();
  const usedAnchorIds = new Set<string>();
  previous.forEach((target, index) => {
    const indexes = previousBySignature.get(target.signature) ?? [];
    indexes.push(index);
    previousBySignature.set(target.signature, indexes);
    if (target.anchorId) {
      previousAnchorOwner.set(target.anchorId, index);
      usedAnchorIds.add(target.anchorId);
    }
  });
  next.forEach((target) => {
    if (target.anchorId) usedAnchorIds.add(target.anchorId);
  });

  const matchedPrevious = next.map((target) => previousBySignature.get(target.signature)?.shift());
  const consumedPrevious = new Set(
    matchedPrevious.filter((index): index is number => index !== undefined),
  );
  const assignedAnchorIds = new Set<string>();
  const assignments: WriterAnchorAssignment[] = next.map((target, index) => {
    const previousIndex = matchedPrevious[index];
    const previousTarget = previousIndex === undefined ? undefined : previous[previousIndex];
    const previousAnchorId = previousIndex === undefined
      ? undefined
      : previous[previousIndex]?.anchorId;
    if (previousAnchorId && !assignedAnchorIds.has(previousAnchorId)) {
      assignedAnchorIds.add(previousAnchorId);
      return {
        anchorId: previousAnchorId,
        anchorAttributes: previousTarget?.anchorAttributes ?? target.anchorAttributes,
      };
    }

    if (target.anchorId && !assignedAnchorIds.has(target.anchorId)) {
      const previousOwner = previousAnchorOwner.get(target.anchorId);
      if (previousOwner === undefined || !consumedPrevious.has(previousOwner)) {
        assignedAnchorIds.add(target.anchorId);
        return { anchorId: target.anchorId, anchorAttributes: target.anchorAttributes };
      }
    }

    // Preserve an anchor across a simple heading rename without treating an
    // insertion before an existing heading as a rename.
    if (previous.length === next.length && previous[index]?.anchorId) {
      const anchorId = previous[index].anchorId;
      if (!assignedAnchorIds.has(anchorId)) {
        assignedAnchorIds.add(anchorId);
        return {
          anchorId,
          anchorAttributes: previous[index].anchorAttributes ?? target.anchorAttributes,
        };
      }
    }

    // A leading H1 is the document title. Other unmatched headings and images
    // are reference targets and need a stable id.
    const anchorId = !generateMissingAnchors
      || (index === 0 && target.type === 'heading' && target.level === 1)
      ? undefined
      : nextWriterUserAnchorId(usedAnchorIds);
    return { anchorId, anchorAttributes: target.anchorAttributes };
  });

  const lines = nextMarkdown.split(/\r?\n/);
  const targetAnchorLines = new Set(
    next
      .map((target) => target.anchorLineIndex)
      .filter((lineIndex): lineIndex is number => lineIndex !== undefined),
  );
  const insertBefore = new Map<number, string>();
  const insertAfter = new Map<number, string>();
  next.forEach((target, index) => {
    const assignment = assignments[index];
    if (assignment.anchorId) {
      insertBefore.set(
        target.lineIndex,
        `<a id="${assignment.anchorId}"${
          assignment.anchorAttributes ? ` ${assignment.anchorAttributes}` : ''
        } />`,
      );
    }
    if (target.type === 'heading' && assignment.anchorId) {
      const previousTarget = previous.find(
        (item) => item.anchorId === assignment.anchorId && item.type === 'heading',
      );
      if (previousTarget?.outlineInstructionLine) {
        insertAfter.set(target.lineIndex, previousTarget.outlineInstructionLine);
      }
    }
  });

  const result: string[] = [];
  lines.forEach((line, lineIndex) => {
    if (targetAnchorLines.has(lineIndex) || OUTLINE_INSTRUCTION_LINE_RE.test(line)) return;
    const anchor = insertBefore.get(lineIndex);
    if (anchor) {
      // MDX serializers may surround a standalone JSX anchor with extra empty
      // paragraphs. Keep only the normal Markdown separator before a section.
      while (
        result.length >= 2
        && !result[result.length - 1].trim()
        && !result[result.length - 2].trim()
      ) result.pop();
      result.push(anchor);
    }
    result.push(line);
    const instruction = insertAfter.get(lineIndex);
    if (instruction) result.push(instruction);
  });
  return withHeadingNumberingConfigLine(result.join('\n'), configLine);
}

/** Backward-compatible name for callers outside the editor module. */
export const protectWriterMarkdownHeadingAnchors = protectWriterMarkdownAnchors;

/** MDXEditor preserves empty anchors as JSX, whose canonical form is self-closing. */
export function writerMarkdownForEditor(markdown: string): string {
  return pageMarkersForEditor(markdown).replace(
    MATERIALIZED_SYSTEM_ANCHOR_RE,
    (_match, _quote: string, anchorId: string, attributes = '') => {
      const suffix = attributes.trim();
      return `<a id="${anchorId}"${suffix ? ` ${suffix}` : ''} />`;
    },
  );
}

/**
 * Remove target anchors from the editable representation. They remain in the
 * persisted Markdown and are restored at the save boundary, but never become
 * standalone MDXEditor blocks that occupy space or interfere with deletion.
 */
export function writerMarkdownForEditing(markdown: string): string {
  const editorMarkdown = writerMarkdownForEditor(markdown);
  const bindings = writerMarkdownTargetBindings(editorMarkdown);
  const anchorLines = new Set(
    bindings
      .map((target) => target.anchorLineIndex)
      .filter((lineIndex): lineIndex is number => lineIndex !== undefined),
  );
  const anchoredTargetLines = new Set(
    bindings
      .filter((target) => target.anchorLineIndex !== undefined)
      .map((target) => target.lineIndex),
  );
  const hasConfig = editorMarkdown.split(/\r?\n/).some(
    (line) => HEADING_NUMBERING_CONFIG_LINE_RE.test(line),
  );
  const hasOutlineInstructions = editorMarkdown.split(/\r?\n/).some(
    (line) => OUTLINE_INSTRUCTION_LINE_RE.test(line),
  );
  if (anchorLines.size === 0 && !hasConfig && !hasOutlineInstructions) {
    return editorMarkdown;
  }

  const result: string[] = [];
  editorMarkdown.split(/\r?\n/).forEach((line, lineIndex) => {
    if (
      anchorLines.has(lineIndex)
      || HEADING_NUMBERING_CONFIG_LINE_RE.test(line)
      || OUTLINE_INSTRUCTION_LINE_RE.test(line)
    ) return;
    if (anchoredTargetLines.has(lineIndex)) {
      while (
        result.length >= 2
        && !result[result.length - 1].trim()
        && !result[result.length - 2].trim()
      ) result.pop();
    }
    result.push(line);
  });
  return result.join('\n');
}

export interface WriterMarkdownDomAnchor {
  anchorId: string;
  type: 'heading' | 'image';
  targetIndex: number;
}

/** Map persisted sidecar ids to their target positions in the editable DOM. */
export function collectWriterMarkdownDomAnchors(markdown: string): WriterMarkdownDomAnchor[] {
  let headingIndex = 0;
  let imageIndex = 0;
  const anchors: WriterMarkdownDomAnchor[] = [];
  writerMarkdownTargetBindings(markdown).forEach((target) => {
    const targetIndex = target.type === 'heading' ? headingIndex++ : imageIndex++;
    if (target.anchorId) {
      anchors.push({
        anchorId: target.anchorId,
        type: target.type,
        targetIndex,
      });
    }
  });
  return anchors;
}

/**
 * Return the persisted Markdown identity of an editor draft. CommonMark does
 * not encode empty paragraphs: consecutive blank lines outside fenced code
 * and frontmatter are layout, not document content. Keeping that distinction
 * prevents autosave from round-tripping a transient empty editor paragraph
 * through the server and deleting it before the user can type into it.
 */
export function writerMarkdownPersistenceIdentity(markdown: string): string {
  const lines = markdown.replace(/\r\n?/g, '\n').split('\n');
  const result: string[] = [];
  let fenceCharacter = '';
  let fenceLength = 0;
  let inFrontmatter = lines[0]?.trim() === '---';
  let pendingBlank = false;

  lines.forEach((line, lineIndex) => {
    if (inFrontmatter) {
      result.push(line);
      if (lineIndex > 0 && /^(?:---|\.\.\.)\s*$/.test(line)) inFrontmatter = false;
      return;
    }

    const fence = line.match(/^\s*(`{3,}|~{3,})/);
    if (fence) {
      if (pendingBlank && result.length > 0) result.push('');
      pendingBlank = false;
      result.push(line);
      const marker = fence[1];
      if (!fenceCharacter) {
        fenceCharacter = marker[0];
        fenceLength = marker.length;
      } else if (marker[0] === fenceCharacter && marker.length >= fenceLength) {
        fenceCharacter = '';
        fenceLength = 0;
      }
      return;
    }

    if (fenceCharacter) {
      result.push(line);
      return;
    }
    if (!line.trim()) {
      pendingBlank = true;
      return;
    }
    if (pendingBlank && result.length > 0) result.push('');
    pendingBlank = false;
    result.push(line);
  });

  return result.join('\n');
}

/** The Writer numbering service consumes paired system anchors. */
export function writerMarkdownForSave(markdown: string): string {
  return pageMarkersForSave(markdown.replace(
    EDITOR_SYSTEM_ANCHOR_RE,
    (_match, _quote: string, anchorId: string, attributes = '') => {
      const suffix = attributes.trim();
      return `<a id="${anchorId}"${suffix ? ` ${suffix}` : ''}></a>`;
    },
  ));
}

/** Collect the document title plus anchored headings used by the Writer table of contents. */
export function collectWriterMarkdownOutline(markdown: string): WriterMarkdownOutline {
  const items: WriterMarkdownOutlineItem[] = [];
  let title: string | undefined;
  let pendingAnchorId: string | undefined;
  let fenceCharacter = '';
  let fenceLength = 0;
  let currentItemIndex: number | undefined;

  for (const line of markdown.split(/\r?\n/)) {
    const fence = line.match(/^\s*(`{3,}|~{3,})/);
    if (fence) {
      const marker = fence[1];
      if (!fenceCharacter) {
        fenceCharacter = marker[0];
        fenceLength = marker.length;
      } else if (marker[0] === fenceCharacter && marker.length >= fenceLength) {
        fenceCharacter = '';
        fenceLength = 0;
      }
      pendingAnchorId = undefined;
      currentItemIndex = undefined;
      continue;
    }
    if (fenceCharacter) continue;

    const trimmed = line.trim();
    const instruction = trimmed.match(OUTLINE_INSTRUCTION_LINE_RE);
    if (instruction) {
      if (currentItemIndex !== undefined) {
        try {
          const payload = JSON.parse(instruction[1]);
          if (payload && typeof payload === 'object') {
            items[currentItemIndex].instructions = {
              node_id: items[currentItemIndex].anchorId.replace(/^block-/, ''),
              target_chars: typeof payload.target_chars === 'number'
                ? payload.target_chars
                : undefined,
              context_relations: Array.isArray(payload.context_relations)
                ? payload.context_relations
                : [],
              subtasks: Array.isArray(payload.subtasks) ? payload.subtasks : [],
            };
          }
        } catch {
          // Invalid sidecars stay hidden and are ignored by the outline UI.
        }
      }
      continue;
    }
    const anchor = trimmed.match(SYSTEM_ANCHOR_LINE_RE);
    if (anchor) {
      pendingAnchorId = anchor[2];
      currentItemIndex = undefined;
      continue;
    }
    if (!trimmed) continue;

    const heading = trimmed.match(/^(#{1,6})[ \t]+(.+?)(?:[ \t]+#+[ \t]*)?$/);
    if (heading) {
      const label = heading[2].trim();
      title ??= label;
      if (pendingAnchorId) {
        items.push({
          anchorId: pendingAnchorId,
          label,
          level: heading[1].length,
        });
        currentItemIndex = items.length - 1;
      } else {
        currentItemIndex = undefined;
      }
    } else {
      currentItemIndex = undefined;
    }
    pendingAnchorId = undefined;
  }

  return { title, items };
}

/** Collect anchored headings and images that can be used as cross-reference targets. */
export function collectWriterMarkdownReferenceTargets(
  markdown: string,
): WriterMarkdownReferenceTarget[] {
  const targets: WriterMarkdownReferenceTarget[] = [];
  let pendingAnchorId: string | undefined;
  let fenceCharacter = '';
  let fenceLength = 0;

  for (const line of markdown.split(/\r?\n/)) {
    const fence = line.match(/^\s*(`{3,}|~{3,})/);
    if (fence) {
      const marker = fence[1];
      if (!fenceCharacter) {
        fenceCharacter = marker[0];
        fenceLength = marker.length;
      } else if (marker[0] === fenceCharacter && marker.length >= fenceLength) {
        fenceCharacter = '';
        fenceLength = 0;
      }
      pendingAnchorId = undefined;
      continue;
    }
    if (fenceCharacter) continue;

    const trimmed = line.trim();
    const anchor = trimmed.match(SYSTEM_ANCHOR_LINE_RE);
    if (anchor) {
      pendingAnchorId = anchor[2];
      continue;
    }
    if (!trimmed) continue;

    const heading = trimmed.match(/^(#{1,6})[ \t]+(.+?)(?:[ \t]+#+[ \t]*)?$/);
    if (heading && pendingAnchorId) {
      targets.push({
        anchorId: pendingAnchorId,
        label: heading[2].trim(),
        type: 'heading',
      });
    } else if (pendingAnchorId) {
      const image = writerMarkdownImageTarget(trimmed);
      if (image) {
        targets.push({
          anchorId: pendingAnchorId,
          label: image.label || pendingAnchorId,
          type: 'image',
        });
      }
    }
    pendingAnchorId = undefined;
  }

  return targets;
}

export function writerMarkdownInternalReference(text: string, anchorId: string): string {
  const label = text
    .replace(/\\/g, '\\\\')
    .replace(/\]/g, '\\]')
    .replace(/\s+/g, ' ')
    .trim();
  return label && anchorId.startsWith('block-')
    ? `[${label}](#${anchorId})`
    : '';
}

/** Replace the selected source text in-place so its visible wording stays unchanged. */
export function applyWriterMarkdownInternalReference(
  markdown: string,
  paragraphText: string,
  startOffset: number,
  selectedText: string,
  anchorId: string,
): string {
  const reference = writerMarkdownInternalReference(selectedText, anchorId);
  if (!reference || !paragraphText || startOffset < 0) return markdown;

  const matches: number[] = [];
  const blockPattern = /(?:^|\n{2,})([^\n][\s\S]*?)(?=\n{2,}|$)/g;
  for (const blockMatch of markdown.matchAll(blockPattern)) {
    const block = blockMatch[1];
    const blockStart = (blockMatch.index ?? 0) + blockMatch[0].length - block.length;
    let visibleText = '';
    const sourceOffsets: number[] = [];
    for (let index = 0; index < block.length;) {
      const link = block.slice(index).match(/^\[([^\]]*)\]\([^)]+\)/);
      if (link) {
        visibleText += link[1];
        for (let labelIndex = 0; labelIndex < link[1].length; labelIndex += 1) {
          sourceOffsets.push(index + 1 + labelIndex);
        }
        index += link[0].length;
        continue;
      }
      visibleText += block[index];
      sourceOffsets.push(index);
      index += 1;
    }
    if (visibleText !== paragraphText) continue;
    const sourceOffset = sourceOffsets[startOffset];
    if (sourceOffset === undefined) continue;
    const selectionStart = blockStart + sourceOffset;
    if (markdown.slice(selectionStart, selectionStart + selectedText.length) === selectedText) {
      matches.push(selectionStart);
    }
  }
  if (matches.length !== 1) return markdown;

  const selectionStart = matches[0];
  return `${markdown.slice(0, selectionStart)}${reference}${markdown.slice(selectionStart + selectedText.length)}`;
}

interface WriterMarkdownReferenceRange {
  sourceStart: number;
  sourceEnd: number;
  labelSource: string;
  visibleStart: number;
  visibleEnd: number;
}

function writerMarkdownLinkLabelText(labelSource: string): string {
  return labelSource.replace(/\\([\\\]])/g, '$1');
}

function writerMarkdownVisibleBlock(block: string): {
  text: string;
  references: WriterMarkdownReferenceRange[];
} {
  let text = '';
  const references: WriterMarkdownReferenceRange[] = [];
  for (let index = 0; index < block.length;) {
    const link = block.slice(index).match(/^\[((?:\\.|[^\\\]])*)\]\(([^)]*)\)/);
    if (!link) {
      text += block[index];
      index += 1;
      continue;
    }

    const labelSource = link[1];
    const label = writerMarkdownLinkLabelText(labelSource);
    const visibleStart = text.length;
    text += label;
    if (link[2].startsWith('#block-')) {
      references.push({
        sourceStart: index,
        sourceEnd: index + link[0].length,
        labelSource,
        visibleStart,
        visibleEnd: text.length,
      });
    }
    index += link[0].length;
  }
  return { text, references };
}

/** Unwrap the internal Markdown link containing the selection while preserving its label. */
export function removeWriterMarkdownInternalReference(
  markdown: string,
  paragraphText: string,
  startOffset: number,
  selectedText: string,
  selectedReference?: { anchorId: string; occurrence: number },
): string {
  if (
    selectedReference?.anchorId.startsWith('block-')
    && selectedReference.occurrence >= 0
  ) {
    let occurrence = 0;
    const referencePattern = /\[((?:\\.|[^\\\]])*)\]\(#(block-[^)]+)\)/g;
    for (const match of markdown.matchAll(referencePattern)) {
      if (match[2] !== selectedReference.anchorId) continue;
      if (occurrence !== selectedReference.occurrence) {
        occurrence += 1;
        continue;
      }
      const sourceStart = match.index ?? -1;
      if (sourceStart < 0) return markdown;
      return `${markdown.slice(0, sourceStart)}${match[1]}${markdown.slice(sourceStart + match[0].length)}`;
    }
    return markdown;
  }

  if (!paragraphText || !selectedText || startOffset < 0) return markdown;
  const selectionEnd = startOffset + selectedText.length;
  if (paragraphText.slice(startOffset, selectionEnd) !== selectedText) return markdown;

  const matches: Array<{
    blockStart: number;
    references: WriterMarkdownReferenceRange[];
  }> = [];
  const blockPattern = /(?:^|\n{2,})([^\n][\s\S]*?)(?=\n{2,}|$)/g;
  for (const blockMatch of markdown.matchAll(blockPattern)) {
    const block = blockMatch[1];
    const blockStart = (blockMatch.index ?? 0) + blockMatch[0].length - block.length;
    const parsed = writerMarkdownVisibleBlock(block);
    if (parsed.text !== paragraphText) continue;
    matches.push({ blockStart, references: parsed.references });
  }
  if (matches.length !== 1) return markdown;

  const { blockStart, references } = matches[0];
  const reference = references.find(
    (candidate) => startOffset >= candidate.visibleStart
      && selectionEnd <= candidate.visibleEnd,
  );
  if (!reference) return markdown;
  const sourceStart = blockStart + reference.sourceStart;
  const sourceEnd = blockStart + reference.sourceEnd;
  return `${markdown.slice(0, sourceStart)}${reference.labelSource}${markdown.slice(sourceEnd)}`;
}

/** Keep user-authored Markdown link labels after the server materializes numbering. */
export function restoreWriterMarkdownInternalReferenceLabels(
  materializedMarkdown: string,
  sourceMarkdown: string,
): string {
  const referencePattern = /\[([^\]]*)\]\(#(block-[^)]+)\)/g;
  const sourceLabels = new Map<string, string[]>();
  for (const match of sourceMarkdown.matchAll(referencePattern)) {
    const labels = sourceLabels.get(match[2]) ?? [];
    labels.push(match[1]);
    sourceLabels.set(match[2], labels);
  }

  return materializedMarkdown.replace(referencePattern, (reference, _label: string, anchorId: string) => {
    const labels = sourceLabels.get(anchorId);
    const sourceLabel = labels?.shift();
    return sourceLabel ? `[${sourceLabel}](#${anchorId})` : reference;
  });
}
