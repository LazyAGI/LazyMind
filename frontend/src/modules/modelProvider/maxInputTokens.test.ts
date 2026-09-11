import { describe, expect, it } from "vitest";
import { isDefaultLlmMaxInputTokens, parseLlmMaxInputTokens, resolveLlmMaxInputTokens } from "./maxInputTokens";

describe("llm max input tokens", () => {
  it("defaults missing catalog values to 128K", () => {
    expect(resolveLlmMaxInputTokens(undefined)).toBe("128K");
    expect(resolveLlmMaxInputTokens("  ")).toBe("128K");
  });

  it("normalizes catalog values", () => {
    expect(parseLlmMaxInputTokens("200k")).toBe("200K");
    expect(parseLlmMaxInputTokens("1m")).toBe("1M");
    expect(parseLlmMaxInputTokens("512")).toBe("512");
  });

  it("treats 128K as the unresolved default", () => {
    expect(isDefaultLlmMaxInputTokens("128k")).toBe(true);
    expect(isDefaultLlmMaxInputTokens("1M")).toBe(false);
  });

  it("rejects invalid values", () => {
    expect(parseLlmMaxInputTokens("128KB")).toBeNull();
    expect(parseLlmMaxInputTokens("0K")).toBeNull();
    expect(parseLlmMaxInputTokens("999999999999999999999999K")).toBeNull();
  });
});
