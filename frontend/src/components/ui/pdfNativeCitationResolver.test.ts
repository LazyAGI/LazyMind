import { describe, expect, it, vi } from "vitest";
import { groupPdfCitationTargets } from "./pdfCitationReferences";
import { createNativeCitationPageResolver, createNativeCitationResolver, type NativeCitationDocument } from "./pdfNativeCitationResolver";

const staleReference = groupPdfCitationTargets([{
  referenceId: "szepesvari",
  referenceKey: "",
  rawText: "Szepesvari, C. ́ Algorithms for reinforcement learning. Springer nature, 2022.",
  kind: "download",
  page: 11,
  bbox: [60.588, 88.263, 289.476, 89.3],
}])[0];

function documentWithDestination(includeSourceText: boolean): NativeCitationDocument {
  const viewport = {
    width: 612,
    height: 792,
    convertToViewportPoint: (x: number, y: number): [number, number] => [x, 792 - y],
  };
  return {
    getPage: vi.fn(async (number: number) => ({
      getAnnotations: async () => number === 1 ? [{ id: "citation", dest: "cite.szepesvari2022algorithms" }] : [],
      getViewport: () => viewport,
      getTextContent: async () => ({ items: includeSourceText ? [
        { str: "Szepesv´", transform: [1, 0, 0, 1, 307.44, 404.142], width: 33.3 },
        { str: "ari, C.", transform: [1, 0, 0, 1, 340.741, 404.142], width: 29 },
        { str: "Algorithms for reinforcement learning.", transform: [1, 0, 0, 1, 378.977, 404.142], width: 164 },
        { str: "Springer nature, 2022.", transform: [1, 0, 0, 1, 317.403, 392.187], width: 110 },
      ] : [] }),
    })),
    getDestination: vi.fn(async () => [{ num: 723, gen: 0 }, { name: "XYZ" }, 287.515, 416.097, null]),
    getPageIndex: vi.fn(async () => 10),
  };
}

describe("native PDF citation destinations", () => {
  it("uses a numeric link's named PDF destination as a page hint without needing a stored bbox", async () => {
    const pdf = documentWithDestination(false);
    const resolvePage = createNativeCitationPageResolver(pdf, 1);
    expect(await resolvePage("citation")).toBe(11);
    expect(await resolvePage("citation")).toBe(11);
    expect(pdf.getDestination).toHaveBeenCalledTimes(1);
  });

  it("accepts a page-only destination and rejects a destination beyond the PDF", async () => {
    const pdf = {
      ...documentWithDestination(false),
      numPages: 20,
      getDestination: vi.fn(async () => [{ num: 99, gen: 0 }, { name: "Fit" }]),
      getPageIndex: vi.fn(async () => 12),
    };
    expect(await createNativeCitationPageResolver(pdf, 1)("citation")).toBe(13);
    pdf.getPageIndex.mockResolvedValue(20);
    expect(await createNativeCitationPageResolver(pdf, 1)("citation")).toBeUndefined();
  });

  it("does not use an absent or malformed native destination as a page hint", async () => {
    const pdf = {
      ...documentWithDestination(false),
      getDestination: vi.fn(async () => [undefined, { name: "XYZ" }]),
    };
    const resolvePage = createNativeCitationPageResolver(pdf, 1);
    expect(await resolvePage("missing-annotation")).toBeUndefined();
    expect(await resolvePage("citation")).toBeUndefined();
  });

  it("recovers a reference whose stored bbox is stale only after checking the PDF text at the destination", async () => {
    const pdf = documentWithDestination(true);
    const target = await createNativeCitationResolver(pdf, 1, [staleReference])("citation");
    expect(target?.referenceId).toBe("szepesvari");
    expect(target?.regions[0].page).toBe(11);
    expect(target?.regions[0].bbox[0]).toBeGreaterThan(300);
    expect(target?.regions[0].bbox[1]).toBeGreaterThan(370);
  });

  it("does not guess from a citation key when the destination text disagrees", async () => {
    const pdf = documentWithDestination(false);
    expect(await createNativeCitationResolver(pdf, 1, [staleReference])("citation")).toBeUndefined();
  });
});
