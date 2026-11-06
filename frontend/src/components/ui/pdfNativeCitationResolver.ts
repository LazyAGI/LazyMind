import type { PdfCitationTarget } from "./pdfReferenceGeometry";

interface NativeLinkAnnotation {
  id?: string;
  dest?: string | unknown[];
}

interface NativeCitationPage {
  getAnnotations(options: { intent: "display" }): Promise<NativeLinkAnnotation[]>;
  getViewport(options: { scale: number; rotation: number }): {
    width: number;
    height: number;
    convertToViewportPoint(x: number, y: number): [number, number];
  };
  getTextContent?(): Promise<{ items: Array<{ str: string; transform: number[]; width?: number }> }>;
}

export interface NativeCitationDocument {
  numPages?: number;
  getPage(pageNumber: number): Promise<NativeCitationPage>;
  getDestination(name: string): Promise<unknown[] | null>;
  getPageIndex(reference: unknown): Promise<number>;
}

function createDestinationLookup(pdf: NativeCitationDocument, sourcePage: number) {
  const annotations = pdf.getPage(sourcePage).then(page => page.getAnnotations({ intent: "display" })).catch(() => [] as NativeLinkAnnotation[]);
  const destinations = new Map<string, Promise<unknown[] | null>>();
  return async (annotationId: string): Promise<{ destination: unknown[]; name?: string } | undefined> => {
    const annotation = (await annotations).find(item => item.id === annotationId);
    if (!annotation?.dest) return undefined;
    let destination: unknown[] | null;
    if (typeof annotation.dest === "string") {
      let request = destinations.get(annotation.dest);
      if (!request) {
        request = pdf.getDestination(annotation.dest);
        destinations.set(annotation.dest, request);
      }
      destination = await request;
    } else {
      destination = annotation.dest;
    }
    return Array.isArray(destination) && destination.length >= 2 && destination[0] != null
      ? { destination, name: typeof annotation.dest === "string" ? annotation.dest : undefined }
      : undefined;
  };
}

async function destinationPage(pdf: NativeCitationDocument, destination: unknown[]): Promise<number | undefined> {
  const pageIndex = typeof destination[0] === "number"
    ? destination[0]
    : await pdf.getPageIndex(destination[0]);
  return Number.isInteger(pageIndex) && pageIndex >= 0 && (pdf.numPages === undefined || pageIndex < pdf.numPages)
    ? pageIndex + 1
    : undefined;
}

/** Native PDF links can locate a candidate bibliography page even when the stored reference has no bbox. */
export function createNativeCitationPageResolver(pdf: NativeCitationDocument, sourcePage: number) {
  const lookup = createDestinationLookup(pdf, sourcePage);
  const resolved = new Map<string, Promise<number | undefined>>();
  return (annotationId: string): Promise<number | undefined> => {
    if (!annotationId) return Promise.resolve(undefined);
    const existing = resolved.get(annotationId);
    if (existing) return existing;
    const pending = lookup(annotationId).then(value => value && destinationPage(pdf, value.destination)).catch(() => undefined);
    resolved.set(annotationId, pending);
    return pending;
  };
}

function targetAtDestination(targets: PdfCitationTarget[], page: number, x: number, y: number): PdfCitationTarget | undefined {
  const candidates = targets.flatMap(target => {
    const scores = target.regions
      .filter(region => region.page === page)
      .map(region => {
        const dx = Math.abs(region.bbox[0] - x);
        const dy = Math.abs(region.bbox[1] - y);
        return dx <= 48 && dy <= 18 ? dy + dx * 0.15 : Infinity;
      });
    return scores.length ? [{ target, score: Math.min(...scores) }] : [];
  }).filter(candidate => Number.isFinite(candidate.score)).sort((a, b) => a.score - b.score);
  if (!candidates.length || (candidates[1] && candidates[1].score - candidates[0].score < 4)) return undefined;
  return candidates[0].target;
}

function plain(value: string): string {
  return value.normalize("NFKD").replace(/\p{M}/gu, "").toLowerCase().replace(/[^a-z0-9]/g, "");
}

/** A named citation and the PDF text must both identify the same entry before replacing a stale stored bbox. */
async function targetFromDestinationText(
  page: NativeCitationPage,
  destinationName: string,
  targets: PdfCitationTarget[],
  pageNumber: number,
  x: number,
  y: number,
): Promise<PdfCitationTarget | undefined> {
  const key = /^cite\.([a-z][a-z0-9]*?)((?:19|20)\d{2})[a-z]?([a-z][a-z0-9]*)$/i.exec(destinationName);
  if (!key || key[1].length < 4 || key[3].length < 5 || !page.getTextContent) return undefined;
  const author = plain(key[1]);
  const year = key[2];
  const titleHint = plain(key[3]).slice(0, 8);
  const candidates = targets.filter(target => {
    if (target.fallbackPage !== pageNumber && !target.regions.some(region => region.page === pageNumber)) return false;
    const text = plain(target.rawText);
    return text.slice(0, 96).includes(author) && text.includes(year) && text.includes(titleHint);
  });
  if (candidates.length !== 1) return undefined;

  const viewport = page.getViewport({ scale: 1, rotation: 0 });
  const content = await page.getTextContent();
  const nearby = content.items.flatMap(item => {
    if (!item.str || item.transform.length < 6) return [];
    const [left, top] = viewport.convertToViewportPoint(item.transform[4], item.transform[5]);
    if (left < x - 10 || left > x + 260 || top < y - 4 || top > y + 75) return [];
    const normalized = plain(item.str);
    return normalized ? [{ left, top, normalized }] : [];
  }).sort((a, b) => a.top - b.top || a.left - b.left);
  let text = "";
  const spans = nearby.map(item => {
    const start = text.length;
    text += item.normalized;
    return { ...item, start, end: text.length };
  });
  const authorAt = text.indexOf(author);
  const yearAt = authorAt < 0 ? -1 : text.indexOf(year, authorAt + author.length);
  if (yearAt < 0 || !text.slice(authorAt, yearAt).includes(titleHint)) return undefined;
  const first = spans.find(item => item.start <= authorAt && item.end > authorAt);
  const last = spans.find(item => item.start <= yearAt && item.end >= yearAt + year.length);
  if (!first || !last || last.top < first.top || last.top - first.top > 70) return undefined;
  const bbox: [number, number, number, number] = [
    Math.max(0, first.left - 2),
    Math.max(0, first.top - 8),
    Math.min(viewport.width, x + 260),
    Math.min(viewport.height, last.top + 8),
  ];
  if (bbox[0] >= bbox[2] || bbox[1] >= bbox[3]) return undefined;
  return { ...candidates[0], fallbackPage: pageNumber, regions: [{ page: pageNumber, bbox }] };
}

/** Resolve only hovered native links; PDF.js keeps their real destination out of the DOM. */
export function createNativeCitationResolver(pdf: NativeCitationDocument, sourcePage: number, targets: PdfCitationTarget[]) {
  const lookup = createDestinationLookup(pdf, sourcePage);
  const resolved = new Map<string, Promise<PdfCitationTarget | undefined>>();
  return (annotationId: string): Promise<PdfCitationTarget | undefined> => {
    if (!annotationId) return Promise.resolve(undefined);
    const existing = resolved.get(annotationId);
    if (existing) return existing;
    const pending = (async () => {
      const value = await lookup(annotationId);
      if (!value) return undefined;
      const { destination, name } = value;
      // A page-only destination cannot identify a particular bibliography entry.
      if ((destination[1] as { name?: string } | undefined)?.name !== "XYZ"
        || !Number.isFinite(destination[2]) || !Number.isFinite(destination[3])) return undefined;
      const pageNumber = await destinationPage(pdf, destination);
      if (!pageNumber) return undefined;
      const page = await pdf.getPage(pageNumber);
      const [x, y] = page.getViewport({ scale: 1, rotation: 0 }).convertToViewportPoint(destination[2] as number, destination[3] as number);
      if (!Number.isFinite(x) || !Number.isFinite(y)) return undefined;
      const nearby = targetAtDestination(targets, pageNumber, x, y);
      if (nearby) return nearby;
      return name
        ? targetFromDestinationText(page, name, targets, pageNumber, x, y)
        : undefined;
    })().catch(() => undefined);
    resolved.set(annotationId, pending);
    return pending;
  };
}
