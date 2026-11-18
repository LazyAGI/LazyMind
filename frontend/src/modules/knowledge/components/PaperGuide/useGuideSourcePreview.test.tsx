import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { webcrypto } from "node:crypto";
import { useGuideSourcePreview } from "./useGuideSourcePreview";
const hash = "df3f619804a92fdb4057192dc43dd748ea778adc52bc498ce80524c014b81119";
beforeEach(() => vi.stubGlobal("crypto", webcrypto));
afterEach(() => vi.unstubAllGlobals());

it("never opens current PDF bytes for a historical source hash", async () => {
  const read = vi.fn();
  const hook = renderHook(() =>
    useGuideSourcePreview("u/d/p", "old", true, "new", read),
  );
  expect(
    await hook.result.current.preview({
      id: "e",
      text: "",
      source_block_id: "b",
      source_type: "image",
      locator: { kind: "bbox", page: 0, bbox: [0, 0, 1, 1] },
    }),
  ).toBeUndefined();
  expect(read).not.toHaveBeenCalled();
});

const previewMocks = vi.hoisted(() => ({ get: vi.fn(), getDocument: vi.fn() }));
vi.mock("@/components/ui/pdfCitationPreview", () => ({
  createPdfCitationPreviewCache: () => ({
    get: previewMocks.get,
    dispose: () => {},
  }),
}));
vi.mock("react-pdf", () => ({
  pdfjs: { getDocument: previewMocks.getDocument },
}));
it("falls back to the original whole page when its source region cannot render", async () => {
  previewMocks.get
    .mockReset()
    .mockResolvedValueOnce(undefined)
    .mockResolvedValueOnce({ url: "blob:whole-page", width: 600, height: 800 });
  previewMocks.getDocument.mockReturnValue({
    promise: Promise.resolve({
      getPage: async () => ({
        getViewport: () => ({ width: 600, height: 800 }),
      }),
      destroy: async () => {},
    }),
  });
  const hook = renderHook(() =>
    useGuideSourcePreview(
      "u/d/p",
      hash,
      true,
      hash,
      () => new ArrayBuffer(4),
    ),
  );
  const result = await hook.result.current.preview({
    id: "e",
    text: "original",
    source_block_id: "b",
    source_type: "image",
    locator: { kind: "bbox", page: 2, bbox: [0.1, 0.2, 0.4, 0.6] },
  });
  expect(result).toMatchObject({ url: "blob:whole-page", mode: "page" });
  expect(previewMocks.getDocument).toHaveBeenCalledWith(expect.objectContaining({
    cMapUrl: expect.stringMatching(/pdfjs\/cmaps\/$/), cMapPacked: true,
  }));
  expect(previewMocks.get.mock.calls[0][1].regions).toEqual([
    { page: 3, bbox: [60, 160, 240, 480] },
  ]);
  expect(previewMocks.get.mock.calls[1][1].regions).toEqual([
    { page: 3, bbox: [0, 0, 600, 800] },
  ]);
});

it("uses the full physical page for a page-only table identity", async () => {
  previewMocks.get.mockReset().mockResolvedValue({ url: "blob:page", width: 600, height: 800 });
  previewMocks.getDocument.mockReturnValue({ promise: Promise.resolve({
    getPage: async () => ({ getViewport: () => ({ width: 600, height: 800 }) }),
    destroy: async () => {},
  }) });
  const hook = renderHook(() => useGuideSourcePreview("table", hash, true, hash, () => new ArrayBuffer(4)));
  await hook.result.current.preview({ id: "table", source_block_id: "parent", source_type: "table",
    text: "Measurement footnote outside the original body crop", locator: { kind: "page", page: 2 } });
  expect(previewMocks.get).toHaveBeenCalledTimes(1);
  expect(previewMocks.get.mock.calls[0][1].regions).toEqual([{ page: 3, bbox: [0, 0, 600, 800] }]);
});

it("uses the authorized original bounds independently from the citation and reports actual render mode", async () => {
  previewMocks.get.mockReset().mockResolvedValue({ url: "blob:actual-figure", width: 100, height: 100 });
  previewMocks.getDocument.mockReturnValue({ promise: Promise.resolve({
    getPage: async () => ({ getViewport: () => ({ width: 600, height: 800 }) }),
    destroy: async () => {},
  }) });
  const readOriginal = vi.fn(async () => new ArrayBuffer(4));
  const hook = renderHook(() => useGuideSourcePreview("asset", hash, true, hash, readOriginal));
  expect(readOriginal).not.toHaveBeenCalled();
  const result = await hook.result.current.preview({
    id: "figure", source_block_id: "image", source_type: "image", text: "A separate legend.",
    locator: { kind: "page", page: 2 },
  }, { kind: "figure", page: 2, bbox: [.2,.1,.4,.3] });
  expect(previewMocks.get.mock.calls[0][1].regions).toEqual([{ page: 3, bbox: [120, 80, 240, 240] }]);
  expect(result).toMatchObject({ mode: "region", url: "blob:actual-figure" });
  expect(readOriginal).toHaveBeenCalledTimes(1);
});

it("does not pass different bytes to PDF.js even when revision metadata claims the source is current", async () => {
  previewMocks.getDocument.mockClear();
  const hook = renderHook(() =>
    useGuideSourcePreview("wrong-bytes", hash, true, hash, () => Uint8Array.of(1, 2, 3).buffer),
  );
  expect(await hook.result.current.preview({
    id: "formula", source_block_id: "eq", source_type: "formula", text: "x = y",
    locator: { kind: "page", page: 2 },
  })).toBeUndefined();
  expect(previewMocks.getDocument).not.toHaveBeenCalled();
});

it("does not surface a completed render from an old revision sharing the same PDF", async () => {
  const getPdf = () => new ArrayBuffer(4);
  const destroy = vi.fn(async () => {});
  let finish!: (value: { url: string; width: number; height: number }) => void;
  let rendering!: () => void;
  const started = new Promise<void>(resolve => { rendering = resolve; });
  previewMocks.get.mockReset().mockImplementationOnce(async () => {
    rendering();
    return new Promise(resolve => { finish = resolve; });
  });
  previewMocks.getDocument.mockReturnValue({ promise: Promise.resolve({
    getPage: async () => ({ getViewport: () => ({ width: 600, height: 800 }) }),
    destroy,
  }) });
  const hook = renderHook(({ revision }) => useGuideSourcePreview(revision, hash, true, hash, getPdf),
    { initialProps: { revision: "old-revision" } });
  const old = hook.result.current.preview({
    id: "formula", source_block_id: "eq", source_type: "formula", text: "x = y",
    locator: { kind: "page", page: 2 },
  });
  await started;
  hook.rerender({ revision: "new-revision" });
  finish({ url: "blob:old-page", width: 600, height: 800 });
  expect(await old).toBeUndefined();
  expect(destroy).toHaveBeenCalledTimes(1);
});

it("preserves the legacy evidence region and reports unavailable when both paths fail", async () => {
  previewMocks.get.mockReset().mockRejectedValueOnce(new Error("bad region")).mockResolvedValueOnce(undefined);
  previewMocks.getDocument.mockReturnValue({ promise: Promise.resolve({
    getPage: async () => ({ getViewport: () => ({ width: 600, height: 800 }) }),
    destroy: async () => {},
  }) });
  const hook = renderHook(() => useGuideSourcePreview("legacy-v5", hash, true, hash, () => new ArrayBuffer(4)));
  const result = await hook.result.current.preview({
    id: "historical", source_block_id: "image", source_type: "image", text: "",
    locator: { kind: "figure_region", page: 2, bbox: [.2,.1,.4,.3] },
  });
  expect(result).toBeUndefined();
  expect(previewMocks.get.mock.calls.map(call => call[1].regions[0])).toEqual([
    { page: 3, bbox: [120, 80, 240, 240] }, { page: 3, bbox: [0, 0, 600, 800] },
  ]);
});
