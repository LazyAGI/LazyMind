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
}

function closestElement(node: Node | null): HTMLElement | null {
  return node instanceof HTMLElement ? node : node?.parentElement ?? null;
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

  const startParagraph = closestElement(range.startContainer)?.closest('p');
  const endParagraph = closestElement(range.endContainer)?.closest('p');
  const selectedText = selection.toString();
  const text = selectedText.trim();
  const anchor = selectionActionAnchor(range);
  if (!text || !anchor) return null;

  const supported = Boolean(
    startParagraph
      && startParagraph === endParagraph
      && container.contains(startParagraph)
      && !startParagraph.closest('li, blockquote, pre, td, th'),
  );
  let startOffset: number | undefined;
  if (supported && startParagraph) {
    const prefixRange = range.cloneRange();
    prefixRange.selectNodeContents(startParagraph);
    prefixRange.setEnd(range.startContainer, range.startOffset);
    startOffset = prefixRange.toString().length + selectedText.length - selectedText.trimStart().length;
  }
  return { text, anchor, supported, paragraph: startParagraph ?? undefined, startOffset };
}
