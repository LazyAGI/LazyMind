import { describe, expect, it } from "vitest";
import { exportCitationMarkdown } from "./chatExportCitations";

const sources = [
  { citation_id: "1.1", source_type: "external" as const, title: "Web report", url: "https://example.com/report", content: "Web evidence" },
  { citation_id: "2.1", file_name: "Internal report", dataset_id: "kb", document_id: "doc", content: "Internal evidence" },
];

describe("portable export citations", () => {
  it("converts grouped citations, lists only cited sources and preserves code and block formatting", () => {
    const code = "```md\n[1](#source-1.1)\n```";
    const input = `${code}\n\n> 结论[1](#source-1.1,2.1)\n> 下一行\n\n- 再次引用[1](#source-1.1)\n\n\`[1](#source-1.1)\``;
    const result = exportCitationMarkdown(input, [...sources, { citation_id: "unused", title: "unused" }], "https://lazymind.example");
    expect(result).toContain(code);
    expect(result).toContain("`[1](#source-1.1)`");
    expect(result).toContain("> 结论[1](https://example.com/report) [1](https://lazymind.example/lib/knowledge/knowledge/kb/doc?");
    expect(result).toContain("> 下一行");
    expect(result).toContain("- 再次引用[1](https://example.com/report)");
    expect(result).toContain("Internal evidence");
    expect(result).not.toContain("unused");
    expect(result.match(/- 1\.1：/g)).toHaveLength(1);
  });
  it("does not modify ordinary Markdown and marks missing sources without unsafe URLs", () => {
    const plain = "# Report\n\n[a](https://example.com)\n";
    expect(exportCitationMarkdown(plain, sources, "https://local.example")).toBe(plain);
    const result = exportCitationMarkdown("[1](#source-missing) [2](#source-bad)", [{ citation_id: "bad", url: "javascript:alert(1)", title: "Bad" }], "https://local.example");
    expect(result).toContain("来源信息缺失");
    expect(result).not.toContain("javascript:");
    expect(result).not.toContain("#source-");
  });
});
