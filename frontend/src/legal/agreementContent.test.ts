import { describe, expect, it } from "vitest";
import { getUserAgreementMarkdown } from "./agreementContent";

describe("getUserAgreementMarkdown", () => {
  it("returns the English agreement for en-prefixed languages", () => {
    const text = getUserAgreementMarkdown("en-US");
    expect(text.length).toBeGreaterThan(0);
    expect(text).not.toMatch(/^\s|\s$/);
  });

  it("returns the Chinese agreement by default", () => {
    const zh = getUserAgreementMarkdown("zh-CN");
    const fallback = getUserAgreementMarkdown(undefined);
    expect(zh).toBe(fallback);
    expect(zh.length).toBeGreaterThan(0);
  });

  it("is case-insensitive when matching the language prefix", () => {
    expect(getUserAgreementMarkdown("EN-us")).toBe(getUserAgreementMarkdown("en-US"));
  });
});
