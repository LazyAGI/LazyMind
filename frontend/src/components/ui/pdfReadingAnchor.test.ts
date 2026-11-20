import { describe, expect, it } from "vitest";
import { resolvePdfAnchorScrollTop } from "./pdfReadingAnchor";

describe("resolvePdfAnchorScrollTop", () => {
  it("preserves the last reading offset when a narrower reader clamps the viewport", () => {
    // Real browser reproduction: the PDF shrinks from 20,021 px to 12,804 px.
    expect(resolvePdfAnchorScrollTop({
      recordedTop: 19_173,
      liveTop: 11_956,
      previousMaxScrollTop: 19_173,
      maxScrollTop: 11_956,
    })).toBe(19_173);
  });

  it("preserves the old coordinate when a delayed shorter page reduces the scroll extent", () => {
    // Three 800 px pages with 20 px gaps and a 600 px viewport; page three
    // is later measured as 400 px. The browser clamps 1,700 to 1,440.
    expect(resolvePdfAnchorScrollTop({
      recordedTop: 1_700,
      liveTop: 1_440,
      previousMaxScrollTop: 1_840,
      maxScrollTop: 1_440,
    })).toBe(1_700);
  });

  it("uses newer compositor movement when the live viewport was not clamped", () => {
    expect(resolvePdfAnchorScrollTop({
      recordedTop: 1_000,
      liveTop: 1_100,
      previousMaxScrollTop: 1_840,
      maxScrollTop: 1_440,
    })).toBe(1_100);
  });

  it("keeps a genuine upward scroll below the new bottom", () => {
    expect(resolvePdfAnchorScrollTop({
      recordedTop: 1_700,
      liveTop: 1_200,
      previousMaxScrollTop: 1_840,
      maxScrollTop: 1_440,
    })).toBe(1_200);
  });

  it("does not resurrect an older offset after an upward scroll was recorded", () => {
    expect(resolvePdfAnchorScrollTop({
      recordedTop: 1_200,
      liveTop: 1_200,
      previousMaxScrollTop: 1_840,
      maxScrollTop: 1_440,
    })).toBe(1_200);
  });

  it("allows newer movement to reach the new bottom from a valid recorded offset", () => {
    expect(resolvePdfAnchorScrollTop({
      recordedTop: 1_400,
      liveTop: 1_440,
      previousMaxScrollTop: 1_840,
      maxScrollTop: 1_440,
    })).toBe(1_440);
  });

  it.each([1_440, 1_840])("does not infer a layout clamp if the extent stayed the same or grew from %i", previousMaxScrollTop => {
    expect(resolvePdfAnchorScrollTop({
      recordedTop: 1_900,
      liveTop: 1_840,
      previousMaxScrollTop,
      maxScrollTop: 1_840,
    })).toBe(1_840);
  });

  it("tolerates scrollHeight rounding at the bottom of fractional page geometry", () => {
    expect(resolvePdfAnchorScrollTop({
      recordedTop: 1_700.5,
      liveTop: 1_440,
      previousMaxScrollTop: 1_840.5,
      maxScrollTop: 1_440.4,
    })).toBe(1_700.5);
  });

  it("preserves the anchor when resized content fits completely inside the viewport", () => {
    expect(resolvePdfAnchorScrollTop({
      recordedTop: 300,
      liveTop: 0,
      previousMaxScrollTop: 500,
      maxScrollTop: 0,
    })).toBe(300);
  });
});
