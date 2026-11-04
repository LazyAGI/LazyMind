import { describe, expect, it } from "vitest";
import { extractPdfSelectionContext } from "./pdfSelectionContext";
import { isLearningActionCompatible, visibleLearningSelectionActions } from "./learningSelection";
import { referenceActionAtPoint, referenceActionForSelection, type PdfReferenceAction } from "./pdfReferenceGeometry";
import { groupPdfCitationTargets } from "./pdfCitationReferences";

describe("extractPdfSelectionContext", () => {
  it("returns the sentence containing the selected text", () => {
    expect(extractPdfSelectionContext(
      "Previous sentence. batch size is commonly reported as batch size tokens. Next sentence.",
      "batch size tokens",
    )).toBe("batch size is commonly reported as batch size tokens.");
  });

  it("falls back to the selection when page text cannot locate it", () => {
    expect(extractPdfSelectionContext("Other page text", "selected words"))
      .toBe("selected words");
  });
});

describe("learning action compatibility", () => {
  it("filters capabilities by language and selection granularity", () => {
    expect(isLearningActionCompatible({ key: "english", label: "English", languages: ["en"], subjectKinds: ["word"] }, "evidence")).toBe(true);
    expect(isLearningActionCompatible({ key: "english", label: "English", languages: ["en"], subjectKinds: ["word"] }, "求索")).toBe(false);
    expect(isLearningActionCompatible({ key: "translation", label: "Translate", languages: ["*"], subjectKinds: ["sentence"] }, "学而时习之，不亦说乎？")).toBe(true);
  });

  it("hides the extra learning translation when the reader already offers machine translation", () => {
    const actions = [{ key: "translation", label: "翻译" }, { key: "explanation", label: "解释" }];
    expect(visibleLearningSelectionActions(actions, "selected text", true).map((action) => action.key)).toEqual(["explanation"]);
    expect(visibleLearningSelectionActions(actions, "selected text", false).map((action) => action.key)).toEqual(["translation", "explanation"]);
  });
});

describe("reference bbox matching", () => {
  const actions: PdfReferenceAction[] = [{
    referenceId: "ref-1",
    rawText: "Reza Yazdani Aminabadi et al. DeepSpeed Inference: Enabling Efficient Inference, 2022.",
    kind: "download",
    page: 3,
    bbox: [100, 200, 400, 260],
  }];

  it("matches a pointer inside the reference bbox", () => {
    expect(referenceActionAtPoint(actions, 3, 220, 230)?.referenceId).toBe("ref-1");
  });

  it("does not match the same coordinates on another page", () => {
    expect(referenceActionAtPoint(actions, 2, 220, 230)).toBeUndefined();
  });

  it("matches a selection intersecting the reference bbox", () => {
    expect(referenceActionForSelection(actions, 3, [350, 230, 450, 280])?.referenceId).toBe("ref-1");
  });
});

describe("citation target metadata", () => {
  it("normalizes numeric keys, sorts pages, and preserves upstream reading order within each page", () => {
    const actions: PdfReferenceAction[] = [
      { referenceId: "r12", referenceKey: " 0012 ", rawText: "Full paper", kind: "external", href: "https://example.com", page: 9, bbox: [20, 10, 100, 30] },
      { referenceId: "r12", referenceKey: "12", rawText: "Continuation", kind: "download", page: 8, bbox: [40, 60, 100, 80] },
      { referenceId: "r12", referenceKey: "12", rawText: "Continuation", kind: "download", page: 8, bbox: [20, 60, 100, 80] },
      { referenceId: "r12", referenceKey: "12", rawText: "Continuation", kind: "download", page: 8, bbox: [20, 10, 100, 30] },
      { referenceId: "r12", referenceKey: "12", rawText: "Continuation", kind: "download", page: 8, bbox: [20, 10, 100, 30] },
    ];
    const before = structuredClone(actions);
    expect(groupPdfCitationTargets(actions)).toEqual([{
      referenceId: "r12", referenceKey: "12", rawText: "Full paper", kind: "external", href: "https://example.com",
      fallbackPage: 9,
      regions: [
        { page: 8, bbox: [40, 60, 100, 80] },
        { page: 8, bbox: [20, 60, 100, 80] },
        { page: 8, bbox: [20, 10, 100, 30] },
        { page: 9, bbox: [20, 10, 100, 30] },
      ],
    }]);
    expect(actions).toEqual(before);
  });

  it("ignores nonnumeric keys and excludes invalid geometry without losing reference metadata", () => {
    const action: PdfReferenceAction = { referenceId: "r12", referenceKey: "12", rawText: "Paper", kind: "download" };
    const targets = groupPdfCitationTargets([
      { ...action, referenceKey: "author2020" },
      { ...action, referenceKey: "" },
      { ...action, referenceKey: undefined },
      { ...action, page: 0, bbox: [20, 60, 100, 80] },
      { ...action, page: 1.5, bbox: [20, 60, 100, 80] },
      { ...action, page: 8, bbox: [100, 60, 20, 80] },
      { ...action, page: 8, bbox: [20, 60, 20, 80] },
      { ...action, page: 8, bbox: [20, 60, 100, Number.NaN] },
      { ...action, page: 8 },
    ]);
    expect(targets).toEqual([{ ...action, fallbackPage: 8, regions: [] }]);
  });

  it("keeps the first valid page even when its crop is absent", () => {
    const action: PdfReferenceAction = { referenceId: "r12", referenceKey: "12", rawText: "Paper", kind: "download" };
    expect(groupPdfCitationTargets([
      { ...action, page: -1 }, { ...action, page: 2.5 }, { ...action, page: 7 },
      { ...action, page: 8, bbox: [20, 60, 100, 80] },
    ])[0]).toMatchObject({ fallbackPage: 7, regions: [{ page: 8, bbox: [20, 60, 100, 80] }] });
  });
});
