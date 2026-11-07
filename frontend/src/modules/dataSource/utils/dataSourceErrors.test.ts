import { describe, expect, it } from "vitest";
import { isKnowledgeBaseNameDuplicatedError } from "./dataSourceErrors";

describe("data source name conflicts", () => {
  it.each([2001102, "2001102", "DATA_SOURCE_NAME_ALREADY_EXISTS"])("recognizes %s", (code) => {
    expect(isKnowledgeBaseNameDuplicatedError({ response: { data: { code } } })).toBe(true);
  });
  it("does not report an unrelated conflict as a duplicate name", () => {
    expect(isKnowledgeBaseNameDuplicatedError({ code: "IDEMPOTENCY_KEY_REUSED" })).toBe(false);
  });
});
