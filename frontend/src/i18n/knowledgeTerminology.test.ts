import { describe, expect, it } from "vitest";
import { applyKnowledgeTerminology } from "./knowledgeTerminology";

describe("applyKnowledgeTerminology", () => {
  it("uses the user-facing library term in Chinese non-developer mode", () => {
    expect(applyKnowledgeTerminology("新建知识库并搜索知识库", "zh-CN", false))
      .toBe("新建资料库并搜索资料库");
  });

  it("keeps the technical term in developer mode", () => {
    expect(applyKnowledgeTerminology("知识库", "zh-CN", true)).toBe("知识库");
  });

  it("does not alter non-Chinese translations", () => {
    expect(applyKnowledgeTerminology("Knowledge Base", "en-US", false))
      .toBe("Knowledge Base");
  });
});
