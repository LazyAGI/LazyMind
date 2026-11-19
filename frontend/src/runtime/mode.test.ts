import { describe, expect, it } from "vitest";
import { isVocabularyEnabled } from "./mode";

describe("isVocabularyEnabled", () => {
  it("is enabled by default", () => {
    expect(isVocabularyEnabled({})).toBe(true);
  });

  it.each(["1", "true", "TRUE", "yes", "on"])("accepts %s as enabled", (value) => {
    expect(isVocabularyEnabled({ VITE_VOCABULARY_ENABLED: value })).toBe(true);
  });

  it.each(["cloud", "local", "desktop"])("keeps vocabulary available in %s mode despite the legacy flag", (mode) => {
    expect(isVocabularyEnabled({ VITE_LAZYMIND_MODE: mode, VITE_VOCABULARY_ENABLED: "false" })).toBe(true);
  });
});
