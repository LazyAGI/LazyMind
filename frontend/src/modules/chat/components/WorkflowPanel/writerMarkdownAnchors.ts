const MATERIALIZED_SYSTEM_ANCHOR_RE = /<a\s+id=(["'])(block-[^"']+)\1\s*>\s*<\/a>/gi;
const EDITOR_SYSTEM_ANCHOR_RE = /<a\s+id=(["'])(block-[^"']+)\1\s*\/>/gi;

export interface WriterMarkdownReferenceTarget {
  anchorId: string;
  label: string;
  type: 'heading' | 'image';
}

export interface WriterMarkdownOutlineItem {
  anchorId: string;
  label: string;
  level: number;
}

export interface WriterMarkdownOutline {
  title?: string;
  items: WriterMarkdownOutlineItem[];
}

interface WriterMarkdownTargetBinding {
  lineIndex: number;
  anchorLineIndex?: number;
  anchorId?: string;
  type: 'heading' | 'image';
  level?: number;
  signature: string;
}

function writerMarkdownTargetBindings(markdown: string): WriterMarkdownTargetBinding[] {
  const bindings: WriterMarkdownTargetBinding[] = [];
  let pendingAnchor: { id: string; lineIndex: number } | undefined;
  let fenceCharacter = '';
  let fenceLength = 0;

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
    const anchor = trimmed.match(
      /^<a\s+id=(["'])(block-[^"']+)\1\s*(?:\/>|>\s*<\/a>)$/i,
    );
    if (anchor) {
      pendingAnchor = { id: anchor[2], lineIndex };
      return;
    }
    if (!trimmed) return;

    const heading = trimmed.match(/^(#{1,6})[ \t]+(.+?)(?:[ \t]+#+[ \t]*)?$/);
    if (heading) {
      bindings.push({
        lineIndex,
        anchorLineIndex: pendingAnchor?.lineIndex,
        anchorId: pendingAnchor?.id,
        type: 'heading',
        level: heading[1].length,
        signature: `${heading[1].length}:${heading[2].trim()}`,
      });
    } else {
      const image = trimmed.match(/!\[((?:\\.|[^\\\]])*)\]\(((?:\\.|[^)])*)\)/);
      if (image) {
        bindings.push({
          lineIndex,
          anchorLineIndex: pendingAnchor?.lineIndex,
          anchorId: pendingAnchor?.id,
          type: 'image',
          // The URL is the stable identity when numbering changes image alt text.
          signature: `image:${image[2].trim()}`,
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
  const previous = writerMarkdownTargetBindings(previousMarkdown);
  const next = writerMarkdownTargetBindings(nextMarkdown);
  if (next.length === 0) return nextMarkdown;

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
  const assignments = next.map((target, index) => {
    const previousIndex = matchedPrevious[index];
    const previousAnchorId = previousIndex === undefined
      ? undefined
      : previous[previousIndex]?.anchorId;
    if (previousAnchorId && !assignedAnchorIds.has(previousAnchorId)) {
      assignedAnchorIds.add(previousAnchorId);
      return previousAnchorId;
    }

    if (target.anchorId && !assignedAnchorIds.has(target.anchorId)) {
      const previousOwner = previousAnchorOwner.get(target.anchorId);
      if (previousOwner === undefined || !consumedPrevious.has(previousOwner)) {
        assignedAnchorIds.add(target.anchorId);
        return target.anchorId;
      }
    }

    // Preserve an anchor across a simple heading rename without treating an
    // insertion before an existing heading as a rename.
    if (previous.length === next.length && previous[index]?.anchorId) {
      const anchorId = previous[index].anchorId;
      if (!assignedAnchorIds.has(anchorId)) {
        assignedAnchorIds.add(anchorId);
        return anchorId;
      }
    }

    // A leading H1 is the document title. Other unmatched headings and images
    // are reference targets and need a stable id.
    return !generateMissingAnchors
      || (index === 0 && target.type === 'heading' && target.level === 1)
      ? undefined
      : nextWriterUserAnchorId(usedAnchorIds);
  });

  const lines = nextMarkdown.split(/\r?\n/);
  const targetAnchorLines = new Set(
    next
      .map((target) => target.anchorLineIndex)
      .filter((lineIndex): lineIndex is number => lineIndex !== undefined),
  );
  const insertBefore = new Map<number, string>();
  next.forEach((target, index) => {
    const anchorId = assignments[index];
    if (anchorId) insertBefore.set(target.lineIndex, `<a id="${anchorId}" />`);
  });

  const result: string[] = [];
  lines.forEach((line, lineIndex) => {
    if (targetAnchorLines.has(lineIndex)) return;
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
  });
  return result.join('\n');
}

/** Backward-compatible name for callers outside the editor module. */
export const protectWriterMarkdownHeadingAnchors = protectWriterMarkdownAnchors;

/** MDXEditor preserves empty anchors as JSX, whose canonical form is self-closing. */
export function writerMarkdownForEditor(markdown: string): string {
  return markdown.replace(
    MATERIALIZED_SYSTEM_ANCHOR_RE,
    (_match, _quote: string, anchorId: string) => `<a id="${anchorId}" />`,
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
      .map((heading) => heading.anchorLineIndex)
      .filter((lineIndex): lineIndex is number => lineIndex !== undefined),
  );
  const anchoredTargetLines = new Set(
    bindings
      .filter((heading) => heading.anchorLineIndex !== undefined)
      .map((heading) => heading.lineIndex),
  );
  if (anchorLines.size === 0) return editorMarkdown;

  const result: string[] = [];
  editorMarkdown.split(/\r?\n/).forEach((line, lineIndex) => {
    if (anchorLines.has(lineIndex)) return;
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
      anchors.push({ anchorId: target.anchorId, type: target.type, targetIndex });
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
  return markdown.replace(
    EDITOR_SYSTEM_ANCHOR_RE,
    (_match, _quote: string, anchorId: string) => `<a id="${anchorId}"></a>`,
  );
}

/** Collect the document title plus anchored headings used by the Writer table of contents. */
export function collectWriterMarkdownOutline(markdown: string): WriterMarkdownOutline {
  const items: WriterMarkdownOutlineItem[] = [];
  let title: string | undefined;
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
    const anchor = trimmed.match(
      /^<a\s+id=(["'])(block-[^"']+)\1\s*(?:\/>|>\s*<\/a>)$/i,
    );
    if (anchor) {
      pendingAnchorId = anchor[2];
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
      }
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
    const anchor = trimmed.match(
      /^<a\s+id=(["'])(block-[^"']+)\1\s*(?:\/>|>\s*<\/a>)$/i,
    );
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
      const image = trimmed.match(/!\[((?:\\.|[^\\\]])*)\]\((?:\\.|[^)])*\)/);
      if (image) {
        targets.push({
          anchorId: pendingAnchorId,
          label: image[1].replace(/\\([\\\]])/g, '$1').trim() || pendingAnchorId,
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
): string {
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
