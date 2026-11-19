import type { PdfCitationRegion, PdfCitationTarget } from "./pdfReferenceGeometry";

interface PreviewViewport {
  width: number;
  height: number;
  convertToViewportPoint?(x: number, y: number): number[];
}
interface PreviewPage {
  rotate: number;
  getViewport(options: { scale: number; rotation?: number }): PreviewViewport;
  getTextContent?(): Promise<{ items: unknown[] }>;
  render(options: { canvas?: HTMLCanvasElement | null; canvasContext?: CanvasRenderingContext2D; viewport: PreviewViewport }): { promise: Promise<unknown> };
}
export interface PdfCitationPreviewDocument {
  numPages?: number;
  getPage(pageNumber: number): Promise<PreviewPage>;
}
export interface PdfCitationPreview { url: string; width: number; height: number; region?: PdfCitationRegion }
interface RenderedPreview extends PdfCitationPreview { bytes: number }
export interface PdfCitationPreviewOptions { maxEntries?: number; maxBytes?: number; renderScale?: number }

const PREVIEW_SCALE = 1.5;
const MAX_DIMENSION = 2048;
const PADDING = 8;
const GAP = 8;
const MAX_FALLBACK_LINES = 8;
const MAX_FALLBACK_HEIGHT = 128;

interface ReferenceLine { text: string; left: number; top: number; right: number; bottom: number; baseline: number }
interface ReferenceCandidate { number: number; text: string; region: PdfCitationRegion; inReferenceList: boolean }

function isTextItem(value: unknown): value is { str: string; transform: ArrayLike<number>; width: number; height: number } {
  if (!value || typeof value !== "object") return false;
  const item = value as { str?: unknown; transform?: unknown; width?: unknown; height?: unknown };
  const transform = item.transform;
  const sequence = Array.isArray(transform) || (ArrayBuffer.isView(transform) && !(transform instanceof DataView));
  return typeof item.str === "string" && item.str.length > 0 && sequence
    && (transform as ArrayLike<number>).length >= 6 && Array.from(transform as ArrayLike<number>).every(Number.isFinite)
    && typeof item.width === "number" && Number.isFinite(item.width)
    && typeof item.height === "number" && Number.isFinite(item.height);
}

function normalized(value: string): string {
  return value.normalize("NFKD").replace(/\p{M}/gu, "").toLowerCase().replace(/[^a-z0-9]/g, "");
}

function leadingReferenceNumber(value: string): number | undefined {
  const marker = /^\s*(?:\[\s*(\d{1,4})\s*\]|(\d{1,4})\s*[.)])/.exec(value);
  if (!marker || (marker[2] && /^(?:19|20)\d{2}$/.test(marker[2]))) return undefined;
  return Number(marker[1] || marker[2]);
}

function titleTerms(rawText: string): string[] {
  const sentences = rawText.split(/(?<=[.!?])\s+/);
  const afterParenthesizedYear = /\((?:19|20)\d{2}\)\s*[.,:]?\s*(.+)/.exec(rawText)?.[1];
  const title = (afterParenthesizedYear && (afterParenthesizedYear.match(/[a-zA-Z]{4,}/g) || []).length >= 3
    ? afterParenthesizedYear : undefined) || sentences.slice(1).find(sentence =>
    (sentence.match(/[a-zA-Z]{4,}/g) || []).length >= 3);
  return (title?.match(/[a-zA-Z]{4,}/g) || [])
    .map(word => normalized(word))
    .filter(word => word.length >= 4 && !["arxiv", "paper", "journal", "using", "with", "from", "about", "the", "and"].includes(word))
    .slice(0, 4);
}

async function fallbackCandidatesOnPage(page: PreviewPage, pageNumber: number): Promise<ReferenceCandidate[]> {
  if (!page.getTextContent) return [];
  const viewport = page.getViewport({ scale: 1, rotation: 0 });
  if (!viewport.convertToViewportPoint) return [];
  const content = await page.getTextContent();
  const fragments = content.items.flatMap(item => {
    if (!isTextItem(item)) return [];
    const [x, baseline] = viewport.convertToViewportPoint!(item.transform[4], item.transform[5]);
    const left = Math.min(x, x + item.width);
    const right = Math.max(x, x + item.width);
    const top = baseline - Math.max(2, item.height);
    const bottom = baseline + 2;
    if (![left, right, top, bottom].every(Number.isFinite) || right <= left || bottom <= top
      || right < 0 || left > viewport.width || bottom < 0 || top > viewport.height) return [];
    return [{ text: item.str, left, top, right, bottom, baseline }];
  }).sort((a, b) => a.baseline - b.baseline || a.left - b.left);
  const lines: ReferenceLine[] = [];
  for (const fragment of fragments) {
    const line = lines.find(candidate => Math.abs(candidate.baseline - fragment.baseline) <= 3
      && fragment.left - candidate.right <= 24 && candidate.left - fragment.right <= 24);
    if (line) {
      line.text = fragment.left < line.left ? fragment.text + line.text : line.text + fragment.text;
      line.left = Math.min(line.left, fragment.left);
      line.right = Math.max(line.right, fragment.right);
      line.top = Math.min(line.top, fragment.top);
      line.bottom = Math.max(line.bottom, fragment.bottom);
    } else {
      lines.push({ ...fragment });
    }
  }
  lines.sort((a, b) => a.top - b.top || a.left - b.left);
  const candidates: ReferenceCandidate[] = [];
  for (let index = 0; index < lines.length; index++) {
    const start = lines[index];
    const number = leadingReferenceNumber(start.text);
    if (number === undefined) continue;
    const selected = [start];
    for (const line of lines.slice(index + 1)) {
      // A page can have two bibliography columns at nearly the same y. Only
      // lines aligned with this entry's hanging indent can continue it.
      if (line.left < start.left - 16 || line.left > start.left + 56) continue;
      if (selected.length >= MAX_FALLBACK_LINES || line.top - start.top > MAX_FALLBACK_HEIGHT) break;
      if (leadingReferenceNumber(line.text) !== undefined) break;
      const previous = selected[selected.length - 1];
      if (line.top - previous.bottom > 14 || line.right < start.left - 16) break;
      selected.push(line);
    }
    const left = Math.max(0, Math.min(...selected.map(line => line.left)) - 3);
    const top = Math.max(0, Math.min(...selected.map(line => line.top)) - 3);
    const right = Math.min(viewport.width, Math.max(...selected.map(line => line.right)) + 3);
    const bottom = Math.min(viewport.height, Math.max(...selected.map(line => line.bottom)) + 3);
    if (right <= left || bottom <= top || bottom - top > MAX_FALLBACK_HEIGHT
      || (right - left) * (bottom - top) > viewport.width * viewport.height * 0.2) continue;
    candidates.push({
      number,
      text: normalized(selected.map(line => line.text).join(" ")),
      region: { page: pageNumber, bbox: [left, top, right, bottom] },
      inReferenceList: false,
    });
  }
  for (const candidate of candidates) {
    candidate.inReferenceList = candidates.some(other => other !== candidate
      && Math.abs(other.number - candidate.number) <= 2
      && other.number !== candidate.number
      && Math.abs(other.region.bbox[0] - candidate.region.bbox[0]) <= 24
      && Math.abs(other.region.bbox[1] - candidate.region.bbox[1]) <= 160);
  }
  return candidates;
}

async function findFallbackRegion(
  pdf: PdfCitationPreviewDocument,
  target: PdfCitationTarget,
  candidatesOnPage: (pageNumber: number) => Promise<ReferenceCandidate[]>,
  shouldContinue: () => boolean,
): Promise<PdfCitationRegion | undefined> {
  if (!/^\d{1,4}$/.test(target.referenceKey)) return undefined;
  // Old records may have neither a trustworthy page nor a bbox. Check whether
  // their metadata can identify a bibliography entry before reading any pages.
  const author = normalized(target.rawText).slice(0, 16);
  const terms = titleTerms(target.rawText);
  if (author.length < 8 || terms.length < 2) return undefined;
  const count = pdf.numPages;
  const pages: number[] = [];
  if (target.fallbackPage && Number.isInteger(target.fallbackPage) && target.fallbackPage > 0
    && (!count || target.fallbackPage <= count)) pages.push(target.fallbackPage);
  if (count && Number.isInteger(count) && count > 0) {
    // References can precede a long appendix, so a fixed tail-page window is
    // insufficient. Each page's compact candidates are cached per PDF below.
    for (let page = count; page >= 1; page--) if (page !== target.fallbackPage) pages.push(page);
  }
  let weakMatch: PdfCitationRegion | undefined;
  for (const pageNumber of pages) {
    if (!shouldContinue()) return undefined;
    const candidates = await candidatesOnPage(pageNumber);
    if (!shouldContinue()) return undefined;
    const match = candidates.find(candidate => candidate.number === Number(target.referenceKey)
      && candidate.text.includes(author) && terms.filter(term => candidate.text.includes(term)).length >= 2);
    if (match) {
      // A bibliography page normally has neighboring numbered entries. A lone
      // copy in an appendix is weaker evidence; keep scanning for the list.
      if (pageNumber === target.fallbackPage || match.inReferenceList) return match.region;
      weakMatch = match.region;
    }
  }
  // Single-entry bibliography pages still work when no stronger list is found.
  return weakMatch;
}

// For an isolated figure the producer establishes the page strip, while the PDF
// itself establishes the extent of its ink, including detached legends and labels.
type MarginTrim = "horizontal" | "all";

function trimWhiteMargins(canvas: HTMLCanvasElement, context: CanvasRenderingContext2D, mode: MarginTrim) {
  const { width, height } = canvas;
  const pixels = context.getImageData(0, 0, width, height).data;
  let left = width, top = height, right = -1, bottom = -1;
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      const i = 4 * (y * width + x);
      if (pixels[i] === 255 && pixels[i + 1] === 255 && pixels[i + 2] === 255) continue;
      left = Math.min(left, x);
      right = Math.max(right, x);
      top = Math.min(top, y);
      bottom = y;
    }
  }
  if (right < left) return undefined;
  const border = 3;
  left = Math.max(0, left - border);
  top = mode === "horizontal" ? 0 : Math.max(0, top - border);
  right = Math.min(width - 1, right + border);
  bottom = mode === "horizontal" ? height - 1 : Math.min(height - 1, bottom + border);
  if (left === 0 && top === 0 && right === width - 1 && bottom === height - 1) return canvas;
  const trimmed = document.createElement("canvas");
  trimmed.width = right - left + 1;
  trimmed.height = bottom - top + 1;
  const target = trimmed.getContext("2d");
  if (!target) return undefined;
  target.drawImage(canvas, left, top, trimmed.width, trimmed.height, 0, 0, trimmed.width, trimmed.height);
  return trimmed;
}

async function renderPreview(pdf: PdfCitationPreviewDocument, target: PdfCitationTarget, padding: number, requestedScale = PREVIEW_SCALE, trimWhitespace?: MarginTrim): Promise<RenderedPreview | undefined> {
  const pages = new Map<number, { page: PreviewPage; base: PreviewViewport; viewport: PreviewViewport; scale: number }>();
  for (const region of target.regions) {
    if (pages.has(region.page)) continue;
    const page = await pdf.getPage(region.page);
    const base = page.getViewport({ scale: 1, rotation: 0 });
    const scale = Math.min(requestedScale, MAX_DIMENSION / Math.max(base.width, base.height));
    pages.set(region.page, { page, base, viewport: page.getViewport({ scale }), scale });
  }
  const crops = target.regions.map(region => {
    const { page, base, viewport, scale } = pages.get(region.page)!;
    const [sourceLeft, sourceTop, sourceRight, sourceBottom] = region.bbox;
    const rotation = ((page.rotate % 360) + 360) % 360;
    const corners = [
      [sourceLeft, sourceTop], [sourceRight, sourceTop],
      [sourceRight, sourceBottom], [sourceLeft, sourceBottom],
    ].map(([x, y]) => {
      switch (rotation) {
        case 90: return [base.height - y, x];
        case 180: return [base.width - x, base.height - y];
        case 270: return [y, base.width - x];
        default: return [x, y];
      }
    });
    const left = Math.min(...corners.map(point => point[0]));
    const top = Math.min(...corners.map(point => point[1]));
    const right = Math.max(...corners.map(point => point[0]));
    const bottom = Math.max(...corners.map(point => point[1]));
    const x = Math.max(0, Math.floor((left - padding) * scale));
    const y = Math.max(0, Math.floor((top - padding) * scale));
    const width = Math.min(Math.ceil(viewport.width), Math.ceil((right + padding) * scale)) - x;
    const height = Math.min(Math.ceil(viewport.height), Math.ceil((bottom + padding) * scale)) - y;
    return { page: region.page, x, y, width, height, destinationY: 0 };
  }).filter(crop => crop.width > 0 && crop.height > 0);
  if (!crops.length) return undefined;
  const naturalWidth = Math.max(...crops.map(crop => crop.width));
  let naturalHeight = 0;
  for (const crop of crops) {
    crop.destinationY = naturalHeight;
    naturalHeight += crop.height + GAP;
  }
  naturalHeight -= GAP;
  const compositionScale = Math.min(1, MAX_DIMENSION / Math.max(naturalWidth, naturalHeight));
  const width = Math.ceil(naturalWidth * compositionScale);
  const height = Math.ceil(naturalHeight * compositionScale);
  const composition = document.createElement("canvas");
  let output = composition;
  try {
    composition.width = width;
    composition.height = height;
    const context = composition.getContext("2d");
    if (!context) throw new Error("PDF preview canvas is unavailable");
    context.fillStyle = "white";
    context.fillRect(0, 0, width, height);
    for (const [pageNumber, { page, viewport }] of pages) {
      const canvas = document.createElement("canvas");
      try {
        canvas.width = Math.ceil(viewport.width);
        canvas.height = Math.ceil(viewport.height);
        const canvasContext = canvas.getContext("2d");
        if (!canvasContext) throw new Error("PDF preview canvas is unavailable");
        await page.render({ canvas, canvasContext, viewport }).promise;
        for (const crop of crops.filter(crop => crop.page === pageNumber)) {
          context.drawImage(canvas, crop.x, crop.y, crop.width, crop.height,
            0, crop.destinationY * compositionScale, crop.width * compositionScale, crop.height * compositionScale);
        }
      } finally {
        canvas.width = 0;
        canvas.height = 0;
      }
    }
    if (trimWhitespace) {
      const trimmed = trimWhiteMargins(composition, context, trimWhitespace);
      if (!trimmed) return undefined;
      output = trimmed;
    }
    const preview = await new Promise<RenderedPreview>((resolve, reject) => {
      output.toBlob(blob => {
        if (!blob) { reject(new Error("PDF preview encoding failed")); return; }
        try {
          // Deliver the owned URL with the encoding result, before cache validity is checked.
          resolve({ url: URL.createObjectURL(blob), width: output.width, height: output.height, bytes: blob.size });
        } catch (error) {
          reject(error);
        }
      }, "image/png");
    });
    return preview;
  } finally {
    if (output !== composition) { output.width = 0; output.height = 0; }
    composition.width = 0;
    composition.height = 0;
  }
}

export function createPdfCitationPreviewCache(options: PdfCitationPreviewOptions = {}) {
  const renderScale = Number.isFinite(options.renderScale) && options.renderScale! > 0 ? Math.min(options.renderScale!, 4) : PREVIEW_SCALE;
  interface DocumentState {
    active: boolean;
    resolved: Map<string, Entry>;
    inflight: Map<string, Promise<PdfCitationPreview | undefined>>;
    missing: Set<string>;
    fallbackPages: Map<number, Promise<ReferenceCandidate[]>>;
  }
  interface Entry { state: DocumentState; key: string; preview: PdfCitationPreview; bytes: number }
  const maxEntries = options.maxEntries ?? 40;
  const maxBytes = options.maxBytes ?? 16 * 1024 * 1024;
  const documents = new WeakMap<PdfCitationPreviewDocument, DocumentState>();
  const liveStates = new Set<DocumentState>();
  // Map insertion order records recency across all documents owned by this cache.
  const lru = new Map<Entry, true>();
  let totalBytes = 0;
  let disposed = false;

  function release(entry: Entry) {
    lru.delete(entry);
    entry.state.resolved.delete(entry.key);
    totalBytes -= entry.bytes;
    URL.revokeObjectURL(entry.preview.url);
    if (!entry.state.resolved.size && !entry.state.inflight.size && !entry.state.fallbackPages.size) liveStates.delete(entry.state);
  }
  function disposeState(state: DocumentState) {
    state.active = false;
    state.fallbackPages.clear();
    state.missing.clear();
    for (const entry of state.resolved.values()) release(entry);
    state.inflight.clear();
    liveStates.delete(state);
  }
  function disposeDocument(pdf: PdfCitationPreviewDocument) {
    const state = documents.get(pdf);
    if (state) disposeState(state);
    documents.delete(pdf);
  }
  function dispose() {
    disposed = true;
    for (const state of liveStates) disposeState(state);
  }
  function get(pdf: PdfCitationPreviewDocument, target: PdfCitationTarget, options?: { exact?: boolean; trimWhitespace?: MarginTrim }): Promise<PdfCitationPreview | undefined> {
    if (disposed || (!target.regions.length && !/^\d{1,4}$/.test(target.referenceKey))) return Promise.resolve(undefined);
    let state = documents.get(pdf);
    if (!state) {
      state = { active: true, resolved: new Map(), inflight: new Map(), missing: new Set(), fallbackPages: new Map() };
      documents.set(pdf, state);
    }
    const key = JSON.stringify([target.referenceKey, target.regions, target.regions.length ? undefined : target.rawText, target.fallbackPage, options?.exact === true, options?.trimWhitespace]);
    if (state.missing.has(key)) return Promise.resolve(undefined);
    const existing = state.resolved.get(key);
    if (existing) {
      lru.delete(existing);
      lru.set(existing, true);
      return Promise.resolve(existing.preview);
    }
    const inflight = state.inflight.get(key);
    if (inflight) return inflight;
    const requestState = state;
    liveStates.add(requestState);
    function finishRequest() {
      requestState.inflight.delete(key);
      if (!requestState.resolved.size && !requestState.inflight.size && !requestState.fallbackPages.size) liveStates.delete(requestState);
    }
    function candidatesOnPage(pageNumber: number) {
      const existing = requestState.fallbackPages.get(pageNumber);
      if (existing) return existing;
      const pending = pdf.getPage(pageNumber).then(page => fallbackCandidatesOnPage(page, pageNumber));
      requestState.fallbackPages.set(pageNumber, pending);
      void pending.catch(() => {
        if (requestState.fallbackPages.get(pageNumber) === pending) requestState.fallbackPages.delete(pageNumber);
      });
      return pending;
    }
    const pending = (async () => {
      const region = target.regions.length ? undefined : await findFallbackRegion(pdf, target, candidatesOnPage, () => requestState.active);
      if (!requestState.active) return undefined;
      if (!target.regions.length && !region) {
        requestState.missing.add(key);
        return undefined;
      }
      const rendered = await renderPreview(pdf, region ? { ...target, regions: [region] } : target, options?.exact ? 0 : PADDING, renderScale, options?.trimWhitespace);
      return rendered && region ? { ...rendered, region } : rendered;
    })().then(rendered => {
      try {
        if (!rendered) return undefined;
        // A detached render can finish after its reader was closed. Never expose a stale URL.
        if (!requestState.active || maxEntries < 1 || rendered.bytes > maxBytes) {
          URL.revokeObjectURL(rendered.url);
          return undefined;
        }
        const { url, width, height, bytes, region } = rendered;
        const preview = { url, width, height, region };
        const entry = { state: requestState, key, preview, bytes };
        requestState.resolved.set(key, entry);
        lru.set(entry, true);
        totalBytes += bytes;
        while (lru.size > maxEntries || totalBytes > maxBytes) release(lru.keys().next().value!);
        return preview;
      } finally {
        finishRequest();
      }
    }, error => {
      finishRequest();
      throw error;
    });
    requestState.inflight.set(key, pending);
    return pending;
  }
  return { get, disposeDocument, dispose };
}
