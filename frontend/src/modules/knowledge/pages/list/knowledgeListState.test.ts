import { describe, expect, it } from "vitest";
import { ALL_TAGS } from "../../constants/common";
import { getKnowledgeListPage, readKnowledgeListState, writeKnowledgeListState } from "./knowledgeListState";

describe("knowledge list URL state", () => {
  it("round trips independent mine and square filters without losing entry parameters", () => {
    const patch = {
      view: "mine", source: "official", scene: "academic_papers", keyword: "中文 & URL /", officialTag: "法律",
      tag: "团队", sort: "most_used", cloudSource: "feishu", page: 3, pageSize: 20,
      squareType: "evaluation", squareDomain: "中文测评", squareStatus: "installed", squareKeyword: "测试",
    } as const;
    const search = writeKnowledgeListState("?from=settings-knowledge&createSource=cloud-documents&provider=notion", patch);
    expect(readKnowledgeListState(search)).toEqual(patch);
    const params = new URLSearchParams(search);
    expect(params.get("from")).toBe("settings-knowledge");
    expect(params.get("createSource")).toBe("cloud-documents");
    expect(params.get("provider")).toBe("notion");
  });

  it("falls back for unknown enums and invalid pagination", () => {
    expect(readKnowledgeListState("?view=bad&source=unknown&sort=no&cloudSource=x&page=-1&pageSize=10000&squareType=x&squareStatus=x"))
      .toEqual(readKnowledgeListState(""));
  });

  it.each(["0", "NaN", "Infinity", "1.1", "9007199254740992", "1e309"])("rejects invalid page %s", (page) => {
    expect(readKnowledgeListState(`?page=${page}`).page).toBe(1);
  });

  it("removes defaults while preserving foreign parameters", () => {
    expect(writeKnowledgeListState("?source=local&page=1&tag=old&from=chat", { tag: ALL_TAGS }))
      .toBe("?from=chat");
  });

  it("clears the selected type without losing the paper reading entry mode", () => {
    expect(writeKnowledgeListState("?scene=academic_papers&showcase_entry=work&page=2", { scene: "", page: 1 }))
      .toBe("?showcase_entry=work");
    expect(readKnowledgeListState("").scene).toBe("");
  });

  it.each([[3, 10, 11, 2], [3, 10, 0, 1], [2, 10, 21, 2]])(
    "clamps page %s to remaining rows", (page, pageSize, total, expected) => {
      expect(getKnowledgeListPage(page, pageSize, total)).toBe(expected);
    },
  );
});
