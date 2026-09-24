import { describe, expect, it } from "vitest";
import { extractPdfSelectionContext } from "./pdfSelectionContext";
import { isLearningActionCompatible } from "./learningSelection";
import { referenceActionAtPoint, referenceActionForSelection, type PdfReferenceAction } from "./pdfReferenceGeometry";

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
