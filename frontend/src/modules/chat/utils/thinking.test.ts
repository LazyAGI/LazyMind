import { describe, expect, it } from "vitest";
import { splitModelOutputContent, summarizeSearchToolsFromText } from "./thinking";

describe("model output across tool rounds", () => {
  it("keeps prior answer text visible when later tool previews arrive", () => {
    const split = splitModelOutputContent(
      "天津交通方便。<tp>查询预算</tp><trp>已找到票价</trp>两人预算1800元。",
      "正在比较",
    );
    expect(split.content).toBe("天津交通方便。两人预算1800元。");
    expect(split.reasoning_content).toBe("正在比较\n\n<tp>查询预算</tp>\n\n<trp>已找到票价</trp>");
    expect(split.reasoning_content).not.toContain("天津交通方便");
  });

  it("keeps visible text while a later tool preview is still streaming", () => {
    const split = splitModelOutputContent("天津交通方便。<tp>正在查预算");
    expect(split.content).toBe("天津交通方便。");
    expect(split.reasoning_content).toBe("<tp>正在查预算");
  });
});

describe("summarizeSearchToolsFromText", () => {
  it("lists search tools and queries, and skips read_file", () => {
    const raw = [
      '<tool_call>{"id":"1","name":"kb_tmp_search","arguments":{"semantic_query":"主营业务"}}</tool_call>',
      '<tool_call>{"id":"2","name":"read_file","arguments":{"target":"a.pdf","offset":1}}</tool_call>',
      '<tool_call>{"id":"3","name":"grep","arguments":{"target":"a.pdf","pattern":"芯片"}}</tool_call>',
    ].join("");
    expect(summarizeSearchToolsFromText(raw)).toBe(
      "kb_tmp_search「主营业务」 · grep「芯片」",
    );
  });
});
