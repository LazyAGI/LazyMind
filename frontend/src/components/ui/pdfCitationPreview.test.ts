import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PdfCitationTarget } from "./pdfReferenceGeometry";
import { createPdfCitationPreviewCache } from "./pdfCitationPreview";
import { groupPdfCitationTargets } from "./pdfCitationReferences";

const target = (key = "12", regions: PdfCitationTarget["regions"] = [
  { page: 1, bbox: [10, 20, 110, 60] },
]): PdfCitationTarget => ({ referenceId: key, referenceKey: key, rawText: `Reference ${key}`, kind: "open", regions });

function pdfDocument() {
  const page = {
    rotate: 0,
    getViewport: vi.fn(({ scale, rotation = page.rotate }: { scale: number; rotation?: number }): { width: number; height: number } => (
      rotation === 90 || rotation === 270
        ? { width: 800 * scale, height: 600 * scale }
        : { width: 600 * scale, height: 800 * scale }
    )),
    render: vi.fn((_options: { canvas: HTMLCanvasElement; canvasContext: CanvasRenderingContext2D; viewport: { width: number; height: number } }) => ({ promise: Promise.resolve() })),
    cleanup: vi.fn(),
  };
  return { getPage: vi.fn(async (_page: number) => page), cleanup: vi.fn(), page };
}

let canvases: HTMLCanvasElement[];
let draws: ReturnType<typeof vi.fn>[];
let blobSizes: number[];
beforeEach(() => {
  canvases = [];
  draws = [];
  blobSizes = [];
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(function (this: HTMLCanvasElement) {
    if (!canvases.includes(this)) canvases.push(this);
    const drawImage = vi.fn();
    draws.push(drawImage);
    return { drawImage, fillRect: vi.fn(), fillStyle: "" } as unknown as CanvasRenderingContext2D;
  });
  vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation(function (callback) {
    callback(new Blob([new Uint8Array(blobSizes.shift() ?? 64)], { type: "image/png" }));
  });
  let urlId = 0;
  vi.stubGlobal("URL", Object.assign(class extends URL {}, {
    createObjectURL: vi.fn(() => `blob:preview-${++urlId}`),
    revokeObjectURL: vi.fn(),
  }));
});
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("PDF citation preview", () => {
  it("deduplicates concurrent and repeated requests for the same document and target", async () => {
    const document = pdfDocument();
    const cache = createPdfCitationPreviewCache();
    const first = cache.get(document, target());
    const second = cache.get(document, target());
    expect(first).toBe(second);
    const [a, b] = await Promise.all([first, second]);
    expect(a?.url).toBe("blob:preview-1");
    expect(a).toBe(b);
    expect(await cache.get(document, target())).toBe(a);
    expect(document.getPage).toHaveBeenCalledTimes(1);
    expect(URL.createObjectURL).toHaveBeenCalledWith(expect.any(Blob));
    cache.dispose();
  });

  it("returns the text fallback without touching PDF pages when regions are absent", async () => {
    const document = pdfDocument();
    expect(await createPdfCitationPreviewCache().get(document, target("empty", []))).toBeUndefined();
    expect(document.getPage).not.toHaveBeenCalled();
    expect(canvases).toHaveLength(0);
  });

  it("crops every region in input order, clamps padding, and renders each distinct page once", async () => {
    const document = pdfDocument();
    const preview = await createPdfCitationPreviewCache().get(document, target("multiple", [
      { page: 2, bbox: [0, 0, 100, 40] },
      { page: 1, bbox: [10, 20, 110, 60] },
      { page: 2, bbox: [590, 790, 600, 800] },
    ]));
    expect(document.getPage.mock.calls).toEqual([[2], [1]]);
    expect(document.page.render).toHaveBeenCalledTimes(2);
    const crops = draws.flatMap(draw => draw.mock.calls).sort((a, b) => a[6] - b[6]);
    expect(crops).toHaveLength(3);
    // 8pt padding, 1.5x rendering; first crop is clamped at the top-left edge.
    expect(crops[0].slice(1, 5)).toEqual([0, 0, 162, 72]);
    expect(crops[1].slice(1, 5)).toEqual([3, 18, 174, 84]);
    expect(crops[2].slice(1, 5)).toEqual([873, 1173, 27, 27]);
    expect(preview?.width).toBe(174);
    expect(preview?.height).toBeGreaterThanOrEqual(183);
    expect(canvases.every(canvas => canvas.width === 0 && canvas.height === 0)).toBe(true);
    expect(canvases.every(canvas => !canvas.isConnected)).toBe(true);
  });

  it("composes a grouped cross-column bibliography entry in upstream reading order", async () => {
    const document = pdfDocument();
    const grouped = groupPdfCitationTargets([
      { referenceId: "r12", referenceKey: "12", rawText: "Paper", kind: "download", page: 1, bbox: [50, 700, 280, 730] },
      { referenceId: "r12", referenceKey: "12", rawText: "Paper", kind: "download", page: 1, bbox: [320, 60, 550, 95] },
    ])[0];
    await createPdfCitationPreviewCache().get(document, grouped);
    expect(draws[0].mock.calls.map(call => call.slice(1, 5))).toEqual([
      [63, 1038, 369, 69],
      [468, 78, 369, 77],
    ]);
  });

  it.each([
    { rotation: 90, crop: [108, 138, 54, 174], viewport: { width: 1200, height: 900 } },
    { rotation: 180, crop: [588, 108, 174, 54], viewport: { width: 900, height: 1200 } },
    { rotation: 270, crop: [1038, 588, 54, 174], viewport: { width: 1200, height: 900 } },
  ])("transforms unrotated top-left regions into the reader's $rotation degree viewport", async ({ rotation, crop, viewport }) => {
    const document = pdfDocument();
    document.page.rotate = rotation;
    const cache = createPdfCitationPreviewCache();
    // In the 90-degree viewport this source bbox becomes [80,100,100,200] before padding/scaling.
    const preview = await cache.get(document, target("rotated", [
      { page: 1, bbox: [100, 700, 200, 720] },
      { page: 1, bbox: [10, 20, 110, 60] },
    ]));
    const crops = draws.flatMap(draw => draw.mock.calls);
    expect(preview).toBeDefined();
    expect(crops).toHaveLength(2);
    expect(crops[0].slice(1, 5)).toEqual(crop);
    expect(document.getPage).toHaveBeenCalledTimes(1);
    expect(document.page.getViewport).toHaveBeenCalledWith({ scale: 1, rotation: 0 });
    expect(document.page.render.mock.calls[0][0].viewport).toEqual(viewport);
    cache.dispose();
  });

  it("evicts the least recently used entry across documents and refreshes recency on hits", async () => {
    const document = pdfDocument();
    const other = pdfDocument();
    const cache = createPdfCitationPreviewCache({ maxEntries: 2, maxBytes: 1024 });
    await cache.get(document, target("12"));
    await cache.get(other, target("13"));
    await cache.get(document, target("12"));
    await cache.get(document, target("14"));
    expect(vi.mocked(URL.revokeObjectURL).mock.calls).toEqual([["blob:preview-2"]]);
    await cache.get(other, target("13"));
    expect(other.getPage).toHaveBeenCalledTimes(2);
    cache.dispose();
  });

  it("uses Blob byte sizes to enforce the memory budget and falls back for oversized previews", async () => {
    const cache = createPdfCitationPreviewCache({ maxEntries: 40, maxBytes: 100 });
    const document = pdfDocument();
    blobSizes.push(60, 60, 101);
    await cache.get(document, target("12"));
    await cache.get(document, target("13"));
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:preview-1");
    expect(await cache.get(document, target("large"))).toBeUndefined();
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:preview-3");
    expect((await cache.get(document, target("13")))?.url).toBe("blob:preview-2");
    cache.dispose();
  });

  it("defaults to 40 entries and a 16 MiB Blob budget", async () => {
    const document = pdfDocument();
    const cache = createPdfCitationPreviewCache();
    for (let index = 0; index < 41; index++) await cache.get(document, target(String(index)));
    expect(vi.mocked(URL.revokeObjectURL).mock.calls).toEqual([["blob:preview-1"]]);
    cache.dispose();
    vi.mocked(URL.revokeObjectURL).mockClear();
    const bytesCache = createPdfCitationPreviewCache();
    blobSizes.push(9 * 1024 * 1024, 9 * 1024 * 1024);
    await bytesCache.get(document, target("a"));
    await bytesCache.get(document, target("b"));
    expect(vi.mocked(URL.revokeObjectURL).mock.calls).toEqual([["blob:preview-42"]]);
    bytesCache.dispose();
  });

  it("retries a failed request on the same document and never calls PDF cleanup", async () => {
    const document = pdfDocument();
    document.page.render.mockImplementationOnce(() => ({ promise: Promise.reject(new Error("render failed")) }));
    const cache = createPdfCitationPreviewCache();
    await expect(cache.get(document, target())).rejects.toThrow("render failed");
    expect(canvases.every(canvas => canvas.width === 0 && canvas.height === 0)).toBe(true);
    expect((await cache.get(document, target()))?.url).toBe("blob:preview-1");
    cache.disposeDocument(document);
    cache.dispose();
    expect(document.getPage).toHaveBeenCalledTimes(2);
    expect(document.cleanup).not.toHaveBeenCalled();
    expect(document.page.cleanup).not.toHaveBeenCalled();
  });

  it("revokes only the disposed document's URLs and keys previews by both geometry and document", async () => {
    const document = pdfDocument();
    const other = pdfDocument();
    const cache = createPdfCitationPreviewCache();
    await cache.get(document, target());
    await cache.get(other, target());
    await cache.get(document, target("12", [{ page: 2, bbox: [1, 2, 20, 30] }]));
    expect(URL.createObjectURL).toHaveBeenCalledTimes(3);
    cache.disposeDocument(document);
    expect(vi.mocked(URL.revokeObjectURL).mock.calls).toEqual([["blob:preview-1"], ["blob:preview-3"]]);
    expect((await cache.get(other, target()))?.url).toBe("blob:preview-2");
    expect((await cache.get(document, target()))?.url).toBe("blob:preview-4");
    cache.dispose();
    cache.dispose();
    expect(URL.revokeObjectURL).toHaveBeenCalledTimes(4);
  });

  it.each(["document", "cache"])("does not leak late URLs after %s disposal during rendering", async mode => {
    const document = pdfDocument();
    let finish!: () => void;
    document.page.render.mockReturnValueOnce({ promise: new Promise<void>(resolve => { finish = resolve; }) });
    const cache = createPdfCitationPreviewCache();
    const pending = cache.get(document, target());
    await vi.waitFor(() => expect(document.page.render).toHaveBeenCalledTimes(1));
    if (mode === "document") cache.disposeDocument(document);
    else cache.dispose();
    finish();
    expect(await pending).toBeUndefined();
    expect(vi.mocked(URL.revokeObjectURL).mock.calls).toEqual(vi.mocked(URL.createObjectURL).mock.results.map(result => [result.value]));
    expect(canvases.every(canvas => canvas.width === 0 && canvas.height === 0)).toBe(true);
  });

  it.each(["document", "cache"])("returns undefined when nested URL microtasks trigger %s disposal before delivery", async mode => {
    const document = pdfDocument();
    const cache = createPdfCitationPreviewCache();
    vi.mocked(URL.createObjectURL).mockImplementationOnce(() => {
      queueMicrotask(() => queueMicrotask(() => {
        if (mode === "document") cache.disposeDocument(document);
        else cache.dispose();
      }));
      return "blob:microtask-race";
    });
    const pending = cache.get(document, target());
    expect(cache.get(document, target())).toBe(pending);
    expect(await pending).toBeUndefined();
    expect(vi.mocked(URL.revokeObjectURL).mock.calls).toEqual([["blob:microtask-race"]]);
    expect(canvases.every(canvas => canvas.width === 0 && canvas.height === 0)).toBe(true);
  });

  it("releases canvases after PNG encoding fails and allows retry", async () => {
    vi.mocked(HTMLCanvasElement.prototype.toBlob).mockImplementationOnce(callback => callback(null));
    const document = pdfDocument();
    const cache = createPdfCitationPreviewCache();
    await expect(cache.get(document, target())).rejects.toThrow();
    expect(canvases.every(canvas => canvas.width === 0 && canvas.height === 0)).toBe(true);
    expect(await cache.get(document, target())).toBeDefined();
    cache.dispose();
  });

  it("bounds page and composed canvas dimensions for large pages and many regions", async () => {
    const document = pdfDocument();
    document.page.getViewport.mockImplementation(({ scale }) => ({ width: 10000 * scale, height: 20000 * scale }));
    const sizes: number[] = [];
    vi.mocked(HTMLCanvasElement.prototype.toBlob).mockImplementationOnce(function (this: HTMLCanvasElement, callback) {
      sizes.push(this.width, this.height);
      callback(new Blob(["png"]));
    });
    document.page.render.mockImplementation(options => {
      sizes.push(options.canvas.width, options.canvas.height);
      return { promise: Promise.resolve() };
    });
    const cache = createPdfCitationPreviewCache();
    const preview = await cache.get(document, target("large", Array.from({ length: 10 }, () => ({ page: 1, bbox: [0, 0, 10000, 20000] }))));
    expect(preview).toBeDefined();
    expect(sizes.every(size => size > 0 && size <= 2048)).toBe(true);
    expect(draws.flatMap(draw => draw.mock.calls)).toHaveLength(10);
    cache.dispose();
  });
});
