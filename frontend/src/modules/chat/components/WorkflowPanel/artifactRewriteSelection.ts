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

function closestParagraph(container: HTMLElement, node: Node): HTMLElement | null {
  const paragraph = closestElement(node)?.closest<HTMLElement>('p') ?? null;
  return paragraph && container.contains(paragraph) ? paragraph : null;
}

function adjacentBoundaryParagraph(
  container: HTMLElement,
  node: Node,
  offset: number,
  edge: 'start' | 'end',
): HTMLElement | null {
  if (!(node instanceof Element) || !container.contains(node)) return null;
  const childIndex = edge === 'start' ? offset : offset - 1;
  const child = node.childNodes.item(childIndex);
  if (!(child instanceof Element)) return null;
  const paragraphs = child.matches('p')
    ? [child as HTMLElement]
    : Array.from(child.querySelectorAll<HTMLElement>('p'));
  return (edge === 'start' ? paragraphs[0] : paragraphs.at(-1)) ?? null;
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
      : Array.from(child.querySelectorAll<HTMLAnchorElement>('a[href^="#block-"]')).at(-1) ?? null;
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
 * Captures the visible selection and whether it stays inside one ordinary
 * Markdown paragraph. The server remains the source of truth for matching it.
 */
export function selectedMarkdownParagraph(container: HTMLElement): MarkdownSelection | null {
  const selection = globalThis.getSelection();
  if (!selection || selection.rangeCount === 0 || selection.isCollapsed) return null;

  const range = selection.getRangeAt(0);
  if (!container.contains(range.startContainer) || !container.contains(range.endContainer)) {
    return null;
  }

  const directStartParagraph = closestParagraph(container, range.startContainer);
  const directEndParagraph = closestParagraph(container, range.endContainer);
  const startParagraph = directStartParagraph ?? adjacentBoundaryParagraph(
    container,
    range.startContainer,
    range.startOffset,
    'start',
  );
  const endParagraph = directEndParagraph ?? adjacentBoundaryParagraph(
    container,
    range.endContainer,
    range.endOffset,
    'end',
  );
  const selectedText = selection.toString();
  const text = selectedText.trim();
  const anchor = selectionActionAnchor(range);
  if (!text || !anchor) return null;

  let supported = Boolean(
    startParagraph
      && startParagraph === endParagraph
      && container.contains(startParagraph)
      && !startParagraph.closest('li, blockquote, pre, td, th'),
  );
  let startOffset: number | undefined;
  if (supported && startParagraph) {
    if (directStartParagraph) {
      const prefixRange = range.cloneRange();
      prefixRange.selectNodeContents(startParagraph);
      prefixRange.setEnd(range.startContainer, range.startOffset);
      startOffset = prefixRange.toString().length
        + selectedText.length
        - selectedText.trimStart().length;
    } else {
      startOffset = selectedText.length - selectedText.trimStart().length;
    }
    if (
      (!directStartParagraph || !directEndParagraph)
      && (startParagraph.textContent ?? '').slice(startOffset, startOffset + text.length) !== text
    ) {
      supported = false;
      startOffset = undefined;
    }
  }
  return {
    text,
    anchor,
    supported,
    paragraph: startParagraph ?? undefined,
    startOffset,
    internalReference: selectedInternalReference(container, range),
  };
}
