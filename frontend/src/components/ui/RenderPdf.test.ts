import { describe, expect, it } from "vitest";
import { extractPdfSelectionContext } from "./pdfSelectionContext";

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
