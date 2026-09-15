import { describe, expect, it } from "vitest";
import { isVocabularyEnabled } from "./mode";

describe("isVocabularyEnabled", () => {
  it("is disabled by default", () => {
    expect(isVocabularyEnabled({})).toBe(false);
  });

  it.each(["1", "true", "TRUE", "yes", "on"])("accepts %s as enabled", (value) => {
    expect(isVocabularyEnabled({ VITE_VOCABULARY_ENABLED: value })).toBe(true);
  });

  it.each(["0", "false", "off", "unexpected"])("rejects %s", (value) => {
    expect(isVocabularyEnabled({ VITE_VOCABULARY_ENABLED: value })).toBe(false);
  });
});
