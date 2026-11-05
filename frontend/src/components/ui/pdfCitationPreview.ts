import type { PdfCitationTarget } from "./pdfReferenceGeometry";

interface PreviewViewport { width: number; height: number }
interface PreviewPage {
  rotate: number;
  getViewport(options: { scale: number; rotation?: number }): PreviewViewport;
  render(options: { canvas?: HTMLCanvasElement | null; canvasContext?: CanvasRenderingContext2D; viewport: PreviewViewport }): { promise: Promise<unknown> };
}
export interface PdfCitationPreviewDocument {
  getPage(pageNumber: number): Promise<PreviewPage>;
}
export interface PdfCitationPreview { url: string; width: number; height: number }
interface RenderedPreview extends PdfCitationPreview { bytes: number }
export interface PdfCitationPreviewOptions { maxEntries?: number; maxBytes?: number }

const PREVIEW_SCALE = 1.5;
const MAX_DIMENSION = 2048;
const PADDING = 8;
const GAP = 8;

async function renderPreview(pdf: PdfCitationPreviewDocument, target: PdfCitationTarget): Promise<RenderedPreview | undefined> {
  const pages = new Map<number, { page: PreviewPage; base: PreviewViewport; viewport: PreviewViewport; scale: number }>();
  for (const region of target.regions) {
    if (pages.has(region.page)) continue;
    const page = await pdf.getPage(region.page);
    const base = page.getViewport({ scale: 1, rotation: 0 });
    const scale = Math.min(PREVIEW_SCALE, MAX_DIMENSION / Math.max(base.width, base.height));
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
    const x = Math.max(0, Math.floor((left - PADDING) * scale));
    const y = Math.max(0, Math.floor((top - PADDING) * scale));
    const width = Math.min(Math.ceil(viewport.width), Math.ceil((right + PADDING) * scale)) - x;
    const height = Math.min(Math.ceil(viewport.height), Math.ceil((bottom + PADDING) * scale)) - y;
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
    const preview = await new Promise<RenderedPreview>((resolve, reject) => {
      composition.toBlob(blob => {
        if (!blob) { reject(new Error("PDF preview encoding failed")); return; }
        try {
          // Deliver the owned URL with the encoding result, before cache validity is checked.
          resolve({ url: URL.createObjectURL(blob), width, height, bytes: blob.size });
        } catch (error) {
          reject(error);
        }
      }, "image/png");
    });
    return preview;
  } finally {
    composition.width = 0;
    composition.height = 0;
  }
}

export function createPdfCitationPreviewCache(options: PdfCitationPreviewOptions = {}) {
  interface DocumentState {
    active: boolean;
    resolved: Map<string, Entry>;
    inflight: Map<string, Promise<PdfCitationPreview | undefined>>;
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
    if (!entry.state.resolved.size && !entry.state.inflight.size) liveStates.delete(entry.state);
  }
  function disposeState(state: DocumentState) {
    state.active = false;
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
  function get(pdf: PdfCitationPreviewDocument, target: PdfCitationTarget): Promise<PdfCitationPreview | undefined> {
    if (disposed || !target.regions.length) return Promise.resolve(undefined);
    let state = documents.get(pdf);
    if (!state) {
      state = { active: true, resolved: new Map(), inflight: new Map() };
      documents.set(pdf, state);
    }
    const key = JSON.stringify([target.referenceKey, target.regions]);
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
      if (!requestState.resolved.size && !requestState.inflight.size) liveStates.delete(requestState);
    }
    const pending = renderPreview(pdf, target).then(rendered => {
      try {
        if (!rendered) return undefined;
        // A detached render can finish after its reader was closed. Never expose a stale URL.
        if (!requestState.active || maxEntries < 1 || rendered.bytes > maxBytes) {
          URL.revokeObjectURL(rendered.url);
          return undefined;
        }
        const { url, width, height, bytes } = rendered;
        const preview = { url, width, height };
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
