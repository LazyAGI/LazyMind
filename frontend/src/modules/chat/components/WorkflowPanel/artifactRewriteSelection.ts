export interface SelectionActionAnchor {
  top: number;
  left: number;
  placement: 'above' | 'below';
}

interface FloatingToolbarAnchorInput {
  selectionRect: Pick<DOMRect, 'top' | 'right' | 'bottom' | 'left' | 'width'>;
  containerRect: Pick<DOMRect, 'top' | 'right' | 'bottom' | 'left' | 'width'>;
  toolbarWidth: number;
  toolbarHeight: number;
  gap?: number;
  inset?: number;
}

export interface FloatingToolbarAnchor {
  top: number;
  left: number;
  maxWidth: number;
  placement: 'above' | 'below';
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}

/**
 * Returns a viewport-relative anchor for a rich-text selection toolbar while
 * keeping the toolbar within the visible editor surface.
 */
export function floatingToolbarAnchor({
  selectionRect,
  containerRect,
  toolbarWidth,
  toolbarHeight,
  gap = 8,
  inset = 8,
}: FloatingToolbarAnchorInput): FloatingToolbarAnchor {
  const maxWidth = Math.max(0, containerRect.width - inset * 2);
  const visibleWidth = Math.min(toolbarWidth, maxWidth);
  const minLeft = containerRect.left + inset;
  const maxLeft = Math.max(minLeft, containerRect.right - inset - visibleWidth);
  const left = clamp(
    selectionRect.left + selectionRect.width / 2 - visibleWidth / 2,
    minLeft,
    maxLeft,
  );

  const minTop = containerRect.top + inset;
  const maxTop = Math.max(minTop, containerRect.bottom - inset - toolbarHeight);
  const preferredAbove = selectionRect.top - toolbarHeight - gap;
  const preferredBelow = selectionRect.bottom + gap;
  const placement = preferredAbove >= minTop || preferredBelow > maxTop
    ? 'above'
    : 'below';

  return {
    top: clamp(placement === 'above' ? preferredAbove : preferredBelow, minTop, maxTop),
    left,
    maxWidth,
    placement,
  };
}

export interface MarkdownSelection {
  sourceRange?: { selected_text: string; start: number; end: number };
  sourceRanges?: Array<{ selected_text: string; start: number; end: number }>;
  paragraphSelections?: Array<{ paragraph: HTMLElement; selectedText: string; startOffset: number }>;
  text: string;
  anchor: SelectionActionAnchor;
  supported: boolean;
  paragraph?: HTMLElement;
  startOffset?: number;
  internalReference?: {
    anchorId: string;
    occurrence: number;
  };
}

function closestElement(node: Node | null): HTMLElement | null {
  return node instanceof HTMLElement ? node : node?.parentElement ?? null;
}

export function markdownTextBlocks(container: HTMLElement): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>('p,h1,h2,h3,h4,h5,h6,li'))
    .filter(element => !element.closest('blockquote,pre,td,th,[data-writer-local-source]')
      && !(element.tagName === 'LI' && element.querySelector(':scope > p')));
}

function textBlockRange(element: Element): Range {
  const range = element.ownerDocument.createRange();
  range.selectNodeContents(element);
  // A tight list item owns its leading text, not its nested list's text.
  const nestedList = element.tagName === 'LI' ? element.querySelector(':scope > ul, :scope > ol') : null;
  if (nestedList) range.setEndBefore(nestedList);
  return range;
}

export function markdownBlockText(element: HTMLElement): string {
  return textBlockRange(element).toString();
}

function closestInternalReference(container: HTMLElement, node: Node): HTMLAnchorElement | null {
  const link = closestElement(node)?.closest<HTMLAnchorElement>('a[href^="#block-"]') ?? null;
  return link && container.contains(link) ? link : null;
}

function adjacentBoundaryInternalReference(
  container: HTMLElement,
  node: Node,
  offset: number,
  edge: 'start' | 'end',
): HTMLAnchorElement | null {
  if (!(node instanceof Element) || !container.contains(node)) return null;
  const child = node.childNodes.item(edge === 'start' ? offset : offset - 1);
  if (!(child instanceof Element)) return null;
  const link = child.matches('a[href^="#block-"]')
    ? child as HTMLAnchorElement
    : edge === 'start'
      ? child.querySelector<HTMLAnchorElement>('a[href^="#block-"]')
      : Array.from(child.querySelectorAll<HTMLAnchorElement>('a[href^="#block-"]')).slice(-1)[0] ?? null;
  return link && container.contains(link) ? link : null;
}

function selectedInternalReference(
  container: HTMLElement,
  range: Range,
): MarkdownSelection['internalReference'] {
  const startLink = closestInternalReference(container, range.startContainer)
    ?? adjacentBoundaryInternalReference(
      container,
      range.startContainer,
      range.startOffset,
      'start',
    );
  const endLink = closestInternalReference(container, range.endContainer)
    ?? adjacentBoundaryInternalReference(
      container,
      range.endContainer,
      range.endOffset,
      'end',
    );
  if (!startLink || startLink !== endLink) return undefined;

  const href = startLink.getAttribute('href') ?? '';
  if (!href.startsWith('#block-')) return undefined;
  const matchingLinks = Array.from(
    container.querySelectorAll<HTMLAnchorElement>('a[href^="#block-"]'),
  ).filter((candidate) => candidate.getAttribute('href') === href);
  const occurrence = matchingLinks.indexOf(startLink);
  if (occurrence < 0) return undefined;
  let anchorId = href.slice(1);
  try {
    anchorId = decodeURIComponent(anchorId);
  } catch {
    return undefined;
  }
  return { anchorId, occurrence };
}

export function selectionActionAnchor(range: Range): SelectionActionAnchor | null {
  const rect = range.getBoundingClientRect();
  if (rect.width === 0 && rect.height === 0) return null;

  const placement = rect.top >= 48 ? 'above' : 'below';
  const edge = Math.min(56, window.innerWidth / 2);
  return {
    top: placement === 'above'
      ? Math.max(8, rect.top - 8)
      : Math.min(window.innerHeight - 40, rect.bottom + 8),
    left: Math.min(Math.max(edge, rect.left + rect.width / 2), window.innerWidth - edge),
    placement,
  };
}

/**
 * Captures paragraph, heading and list-item text separately, preserving their
 * block-local offsets. The server remains the source of truth for matching it.
 */
export function selectedMarkdownParagraph(container: HTMLElement, allowMultiple = false): MarkdownSelection | null {
  const selection = globalThis.getSelection();
  if (!selection || selection.rangeCount === 0 || selection.isCollapsed) return null;

  const range = selection.getRangeAt(0);
  if (!container.contains(range.startContainer) || !container.contains(range.endContainer)) {
    return null;
  }

  const selectedText = selection.toString();
  const text = selectedText.trim();
  const anchor = selectionActionAnchor(range);
  if (!text || !anchor) return null;

  const forbidden = Array.from(container.querySelectorAll('blockquote,pre,table,hr,img,video,audio,[data-writer-local-source],[data-writer-inline-math]'));
  const invalid = forbidden.some(element => range.intersectsNode(element)
    && (['IMG','HR','VIDEO','AUDIO'].includes(element.tagName) || rangeTextWithin(range, element)?.selectedText));
  const paragraphs = markdownTextBlocks(container)
    .map(paragraph => ({ paragraph, ...rangeTextWithin(range, paragraph) }))
    .filter((item): item is { paragraph: HTMLElement; selectedText: string; startOffset: number } => Boolean(item.selectedText));
  const supported = !invalid && paragraphs.length > 0 && (allowMultiple || paragraphs.length === 1);
  return {
    text: paragraphs.length ? paragraphs.map(item => item.selectedText).join('\n\n') : text,
    anchor,
    supported,
    paragraph: paragraphs[0]?.paragraph,
    startOffset: paragraphs.length === 1 ? paragraphs[0].startOffset : undefined,
    paragraphSelections: paragraphs.length > 1 ? paragraphs : undefined,
    internalReference: selectedInternalReference(container, range),
  };
}

/** Intersect a DOM range with one text block, preserving the block-local offset. */
export function rangeTextWithin(range: Range, element: Element): {selectedText: string; startOffset: number} | undefined {
  if (!range.intersectsNode(element)) return;
  const part = textBlockRange(element);
  if (range.compareBoundaryPoints(Range.END_TO_START, part) >= 0
    || range.compareBoundaryPoints(Range.START_TO_END, part) <= 0) return;
  if (range.compareBoundaryPoints(Range.START_TO_START,part)>0) part.setStart(range.startContainer,range.startOffset);
  if (range.compareBoundaryPoints(Range.END_TO_END,part)<0) part.setEnd(range.endContainer,range.endOffset);
  const raw=part.toString(),selectedText=raw.trim();if(!selectedText)return;
  const prefix=element.ownerDocument.createRange();prefix.selectNodeContents(element);prefix.setEnd(part.startContainer,part.startOffset);
  return {selectedText,startOffset:prefix.toString().length+raw.length-raw.trimStart().length};
}
