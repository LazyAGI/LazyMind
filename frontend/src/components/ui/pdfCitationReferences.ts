import type { PdfCitationTarget, PdfReferenceAction } from "./pdfReferenceGeometry";

export type { PdfCitationTarget } from "./pdfReferenceGeometry";

function numericKey(value: string | undefined): string | undefined {
  const key = value?.trim();
  return key && /^\d{1,4}$/.test(key) ? String(Number(key)) : undefined;
}

export function groupPdfCitationTargets(actions: PdfReferenceAction[]): PdfCitationTarget[] {
  const targets = new Map<string, PdfCitationTarget>();
  for (const action of actions) {
    // Author-year bibliographies have no numeric key. Keep each entry by its
    // server ID so native PDF destinations can still resolve to its regions.
    const referenceKey = numericKey(action.referenceKey) ?? (action.referenceId ? `ref:${action.referenceId}` : undefined);
    if (referenceKey === undefined) continue;
    let target = targets.get(referenceKey);
    if (!target) {
      const { page: _page, bbox: _bbox, ...metadata } = action;
      target = { ...metadata, referenceKey, regions: [] };
      targets.set(referenceKey, target);
    }
    const { page, bbox } = action;
    if (page !== undefined && Number.isInteger(page) && page >= 1 && target.fallbackPage === undefined) {
      target.fallbackPage = page;
    }
    if (!page || !Number.isInteger(page) || page < 1 || !bbox || bbox.length !== 4
      || !bbox.every(Number.isFinite) || bbox[0] >= bbox[2] || bbox[1] >= bbox[3]) continue;
    if (!target.regions.some(region => region.page === page && region.bbox.every((value, index) => value === bbox[index]))) {
      target.regions.push({ page, bbox: [...bbox] });
    }
  }
  for (const target of targets.values()) {
    // Upstream regions already carry bibliography reading order. In a
    // two-column bibliography an entry can continue from the bottom of the
    // left column to the top of the right column, so sorting same-page boxes
    // by y would reverse its text and make navigation land on the continuation.
    // JavaScript's stable sort keeps that upstream order within each page.
    target.regions.sort((a, b) => a.page - b.page);
  }
  return [...targets.values()];
}

function hasContinuousCitationGeometry(rects: DOMRect[]): boolean {
  if (!rects.length) return false;
  const first = rects[0];
  const height = Math.min(...rects.map(rect => rect.bottom - rect.top));
  const centerY = (first.top + first.bottom) / 2;
  for (let index = 0; index < rects.length; index++) {
    const rect = rects[index];
    if (Math.abs((rect.top + rect.bottom) / 2 - centerY) > height * 0.35) return false;
    // Native ranges may include overlapping element and text rectangles. Allow
    // those duplicates while rejecting reversed text and separate columns.
    if (index && rect.left < rects[index - 1].left - height * 0.25) return false;
  }
  const ordered = [...rects].sort((a, b) => a.left - b.left);
  let right = ordered[0].right;
  for (const rect of ordered.slice(1)) {
    if (rect.left - right > height * 0.5) return false;
    right = Math.max(right, rect.right);
  }
  return true;
}

// Index text without wrapping or replacing PDF.js spans. The native Range reads
// current layout so resizing and zooming never leave stale citation hit boxes.
export function citationOccurrences(page: HTMLElement, targets: PdfCitationTarget[]): Array<{ target: PdfCitationTarget; rects: DOMRect[] }> {
  const occurrences: Array<{ target: PdfCitationTarget; rects: DOMRect[] }> = [];
  const numberedTargets = targets.filter(target => /^\d{1,4}$/.test(target.referenceKey));
  if (!numberedTargets.length) return occurrences;
  const targetsByKey = new Map(numberedTargets.map(target => [target.referenceKey, target]));
  const doc = page.ownerDocument;
  for (const layer of page.querySelectorAll(".react-pdf__Page__textContent")) {
    const nodes: Array<{ node: Text; start: number; end: number }> = [];
    const walker = doc.createTreeWalker(layer, NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT);
    let text = "";
    let node: Node | null;
    while ((node = walker.nextNode())) {
      if (node.nodeType === Node.ELEMENT_NODE) {
        // A non-whitespace boundary cannot be consumed by the marker grammar.
        if ((node as Element).tagName === "BR") text += "\0";
        continue;
      }
      const value = node.textContent || "";
      nodes.push({ node: node as Text, start: text.length, end: text.length + value.length });
      text += value;
    }
    for (const match of text.matchAll(/\[\s*\d{1,4}\s*\]|【\s*\d{1,4}\s*】/g)) {
      const key = numericKey(match[0].slice(1, -1));
      const target = key === undefined ? undefined : targetsByKey.get(key);
      if (!target) continue;
      const start = match.index!;
      const end = start + match[0].length;
      const first = nodes.find(item => item.start <= start && item.end > start);
      const last = nodes.find(item => item.start < end && item.end >= end);
      if (!first || !last) continue;
      if (first.node.parentElement?.closest("a") || last.node.parentElement?.closest("a")) continue;
      const range = doc.createRange();
      range.setStart(first.node, start - first.start);
      range.setEnd(last.node, end - last.start);
      const rects = Array.from(range.getClientRects()).filter(rect => rect.right > rect.left && rect.bottom > rect.top);
      if (hasContinuousCitationGeometry(rects)) occurrences.push({ target, rects });
    }
  }
  return occurrences;
}

export function citationAtPoint(page: HTMLElement, targets: PdfCitationTarget[], x: number, y: number): PdfCitationTarget | undefined {
  return citationOccurrences(page, targets).find(hit => hit.rects.some(rect => x >= rect.left && x <= rect.right && y >= rect.top && y <= rect.bottom))?.target;
}

interface CitationPointerEvent {
  clientX: number;
  clientY: number;
  button?: number;
  ctrlKey?: boolean;
  metaKey?: boolean;
  shiftKey?: boolean;
  altKey?: boolean;
}

export function citationForPointerActivation(
  page: HTMLElement,
  targets: PdfCitationTarget[],
  target: Element,
  event: CitationPointerEvent,
  selection: string,
  start?: { x: number; y: number },
): PdfCitationTarget | undefined {
  if (!start || selection || (event.button !== undefined && event.button !== 0)
    || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey
    || Math.hypot(event.clientX - start.x, event.clientY - start.y) > 4
    || target.closest("a,button,input,textarea,select")) return;
  return citationAtPoint(page, targets, event.clientX, event.clientY);
}
