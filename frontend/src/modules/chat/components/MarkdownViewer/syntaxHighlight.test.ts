import { describe, expect, it } from "vitest";
import {
  getLanguageFromClassName,
  getRawLanguageFromClassName,
  highlightCode,
} from "./syntaxHighlight";

describe("getRawLanguageFromClassName", () => {
  it("extracts the raw language token from a language-* class", () => {
    expect(getRawLanguageFromClassName("language-TypeScript")).toBe("typescript");
  });

  it("returns an empty string when there is no language class", () => {
    expect(getRawLanguageFromClassName("some-other-class")).toBe("");
  });

  it("returns an empty string for undefined input", () => {
    expect(getRawLanguageFromClassName(undefined)).toBe("");
  });
});

describe("getLanguageFromClassName", () => {
  it("maps aliased language names to their Prism grammar name", () => {
    expect(getLanguageFromClassName("language-js")).toBe("javascript");
    expect(getLanguageFromClassName("language-sh")).toBe("bash");
    expect(getLanguageFromClassName("language-yml")).toBe("yaml");
  });

  it("passes through languages that already match a Prism grammar name", () => {
    expect(getLanguageFromClassName("language-python")).toBe("python");
  });

  it("returns an empty string when there is no class name", () => {
    expect(getLanguageFromClassName()).toBe("");
  });
});

describe("highlightCode", () => {
  it("returns highlighted HTML markup for a known language", () => {
    const html = highlightCode("const a = 1;", "javascript");
    expect(html).toContain("token");
  });

  it("returns an empty string when the language has no grammar", () => {
    expect(highlightCode("some code", "not-a-real-language")).toBe("");
  });

  it("returns an empty string when no language is provided", () => {
    expect(highlightCode("some code", "")).toBe("");
  });
});
