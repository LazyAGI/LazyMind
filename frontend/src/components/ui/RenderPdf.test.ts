import { describe, expect, it } from "vitest";
import { extractPdfSelectionContext } from "./pdfSelectionContext";
import { isLearningActionCompatible } from "./learningSelection";

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
