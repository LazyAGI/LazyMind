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

/** MDXEditor preserves empty anchors as JSX, whose canonical form is self-closing. */
export function writerMarkdownForEditor(markdown: string): string {
  return markdown.replace(
    MATERIALIZED_SYSTEM_ANCHOR_RE,
    (_match, _quote: string, anchorId: string) => `<a id="${anchorId}" />`,
  );
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
