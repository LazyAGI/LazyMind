import { renderHook } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { useGuideSourcePreview } from "./useGuideSourcePreview";
it("never opens current PDF bytes for a historical source hash", async () => {
  const read = vi.fn();
  const hook = renderHook(() =>
    useGuideSourcePreview("u/d/p", "old", true, "new", read),
  );
  expect(
    await hook.result.current({
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
      "same",
      true,
      "same",
      () => new ArrayBuffer(4),
    ),
  );
  const result = await hook.result.current({
    id: "e",
    text: "original",
    source_block_id: "b",
    source_type: "image",
    locator: { kind: "bbox", page: 2, bbox: [0.1, 0.2, 0.4, 0.6] },
  });
  expect(result?.url).toBe("blob:whole-page");
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
  const hook = renderHook(() => useGuideSourcePreview("table", "same", true, "same", () => new ArrayBuffer(4)));
  await hook.result.current({ id: "table", source_block_id: "parent", source_type: "table",
    text: "Measurement footnote outside the original body crop", locator: { kind: "page", page: 2 } });
  expect(previewMocks.get).toHaveBeenCalledTimes(1);
  expect(previewMocks.get.mock.calls[0][1].regions).toEqual([{ page: 3, bbox: [0, 0, 600, 800] }]);
});
