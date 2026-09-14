import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import MarkdownViewer from "./index";

const sources = [
  {
    source_type: "external",
    index: "1.1",
    title: "First source",
    url: "https://first.example/source",
  },
  {
    source_type: "external",
    index: "2.1",
    title: "Second source",
    url: "https://second.example/source",
  },
];

function renderMarkdown(markdown: string) {
  return render(<MarkdownViewer sources={sources}>{markdown}</MarkdownViewer>);
}

describe("MarkdownViewer source citation AST transform", () => {
  it("keeps a heading citation in the heading without requiring a blank line", () => {
    const { container } = renderMarkdown(
      "## 结论 [1](#source-1.1)\n这里是解释",
    );

    expect(container.querySelector("h2 .md-source-chip")).toBeInTheDocument();
    expect(container.querySelector("p .md-source-chip")).not.toBeInTheDocument();
  });

  it("relocates citations within blockquote paragraph boundaries", () => {
    const { container } = renderMarkdown(
      "> 引用 [1](#source-1.1)。继续。\n\n外部段落",
    );

    const quoteParagraph = container.querySelector("blockquote p");
    expect(quoteParagraph).toHaveTextContent("引用。继续。");
    expect(quoteParagraph?.lastElementChild).toHaveClass("md-source-chip");
    expect(container.querySelector(":scope > p .md-source-chip")).not.toBeInTheDocument();
  });

  it("preserves a two-space hard break while moving the citation", () => {
    const { container } = renderMarkdown("A[1](#source-1.1)  \nB");

    const paragraph = container.querySelector("p");
    expect(paragraph?.querySelector("br")).toBeInTheDocument();
    expect(paragraph?.lastElementChild).toHaveClass("md-source-chip");
  });

  it("handles continuation and nested list paragraphs independently", () => {
    const { container } = renderMarkdown(
      [
        "- Parent [1](#source-1.1)",
        "  continuation",
        "  - Child [2](#source-2.1)",
      ].join("\n"),
    );

    const items = container.querySelectorAll("li");
    expect(items).toHaveLength(2);
    expect(items[0].querySelector(":scope > .md-source-chip, :scope > p > .md-source-chip"))
      .toBeInTheDocument();
    expect(items[1].querySelector(":scope > .md-source-chip, :scope > p > .md-source-chip"))
      .toBeInTheDocument();
  });

  it("aggregates citations inside each table cell without moving them across cells", () => {
    const { container } = renderMarkdown(
      [
        "| 模型 | 价格 |",
        "|---|---|",
        "| A [1](#source-1.1)[2](#source-2.1) | $1 [2](#source-2.1) |",
      ].join("\n"),
    );

    const cells = container.querySelectorAll("tbody td");
    expect(cells).toHaveLength(2);
    expect(cells[0].querySelector(".md-source-chip")).toHaveTextContent("+1");
    expect(cells[1].querySelector(".md-source-chip")).toHaveTextContent("second.example");
    expect(cells[1].querySelector(".md-source-chip")).not.toHaveTextContent("+1");
  });

  it("does not turn fenced or inline code links into source chips", () => {
    const { container } = renderMarkdown(
      [
        "```md",
        "[1](#source-1.1)",
        "```",
        "",
        "`[2](#source-2.1)`",
      ].join("\n"),
    );

    expect(container.querySelectorAll("code")).toHaveLength(2);
    expect(container.querySelector(".md-source-chip")).not.toBeInTheDocument();
    expect(container).toHaveTextContent("[1](#source-1.1)");
    expect(container).toHaveTextContent("[2](#source-2.1)");
  });

  it("aggregates ordinary paragraph citations for the existing +N chip", () => {
    const { container } = renderMarkdown(
      "第一句[1](#source-1.1)。第二句[2](#source-2.1)。",
    );

    const paragraph = container.querySelector("p");
    expect(paragraph).toHaveTextContent("第一句。第二句。");
    expect(paragraph?.querySelectorAll(".md-source-chip")).toHaveLength(1);
    expect(paragraph?.querySelector(".md-source-chip")).toHaveTextContent("+1");
  });
});
