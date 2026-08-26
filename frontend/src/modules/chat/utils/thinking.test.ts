import { describe, expect, it } from "vitest";
import { summarizeSearchToolsFromText } from "./thinking";

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
