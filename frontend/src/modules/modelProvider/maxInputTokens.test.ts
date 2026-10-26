import { describe, expect, it } from "vitest";
import { formatContextWindow, isDefaultLlmMaxInputTokens, parseLlmMaxInputTokens, resolveLlmMaxInputTokens } from "./maxInputTokens";

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

// Formatting is display-only: the raw API values retain their original units.
describe("context window display", () => {
  it.each([
    ["1000000", "977K"], ["262144", "256K"], ["1024000", "1000K"],
    ["1024001", "1M"], ["1048576", "1M"], ["2097152", "2M"],
    [" 128k ", "128K"], ["2m", "2M"], ["512", "1K"],
    ["", null], ["unknown", null], ["0", null], [undefined, null],
  ])("formats %s as %s", (raw, expected) => {
    expect(formatContextWindow(raw)).toBe(expected);
  });
});
