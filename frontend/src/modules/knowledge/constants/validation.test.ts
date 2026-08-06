import { describe, expect, it } from "vitest";
import {
  KNOWLEDGE_BASE_NAME_MAX_LENGTH,
  KNOWLEDGE_BASE_NAME_PATTERN,
} from "./validation";

describe("KNOWLEDGE_BASE_NAME_PATTERN", () => {
  it("accepts Chinese characters, letters, digits, underscore, dot and dash", () => {
    expect(KNOWLEDGE_BASE_NAME_PATTERN.test("我的知识库_v1.0-beta")).toBe(true);
    expect(KNOWLEDGE_BASE_NAME_PATTERN.test("KnowledgeBase123")).toBe(true);
  });

  it("rejects empty strings", () => {
    expect(KNOWLEDGE_BASE_NAME_PATTERN.test("")).toBe(false);
  });

  it("rejects names containing disallowed special characters", () => {
    expect(KNOWLEDGE_BASE_NAME_PATTERN.test("bad/name")).toBe(false);
    expect(KNOWLEDGE_BASE_NAME_PATTERN.test("bad name")).toBe(false);
    expect(KNOWLEDGE_BASE_NAME_PATTERN.test("bad*name")).toBe(false);
  });

  it("rejects names longer than the max length", () => {
    const tooLong = "a".repeat(KNOWLEDGE_BASE_NAME_MAX_LENGTH + 1);
    expect(KNOWLEDGE_BASE_NAME_PATTERN.test(tooLong)).toBe(false);
  });

  it("accepts a name at exactly the max length", () => {
    const exact = "a".repeat(KNOWLEDGE_BASE_NAME_MAX_LENGTH);
    expect(KNOWLEDGE_BASE_NAME_PATTERN.test(exact)).toBe(true);
  });
});
