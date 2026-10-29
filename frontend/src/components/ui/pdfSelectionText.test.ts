import { describe, expect, it } from "vitest";
import { normalizePdfSelectionText } from "./pdfSelectionText";

describe("PDF paragraph selection", () => {
  it("joins layout line breaks without joining separate paragraphs", () => {
    expect(normalizePdfSelectionText("A long paragraph\nwraps across PDF lines.\n\nAnother\nparagraph."))
      .toBe("A long paragraph wraps across PDF lines.\n\nAnother paragraph.");
  });
  it("keeps compound hyphens and removes discretionary soft hyphens", () => {
    expect(normalizePdfSelectionText("long-\ncontext under\u00ad\nstanding"))
      .toBe("long-context understanding");
  });
  it("does not insert spaces into Chinese sentences", () => {
    expect(normalizePdfSelectionText("这个段落\n跨越多行。\n下一句话。"))
      .toBe("这个段落跨越多行。下一句话。");
  });
  it("preserves list boundaries and joins wrapped list items", () => {
    expect(normalizePdfSelectionText("1. First item\ncontinues here.\n2. Second item\n• Another item"))
      .toBe("1. First item continues here.\n2. Second item\n• Another item");
  });
});
