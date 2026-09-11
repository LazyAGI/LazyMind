import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { mergeChatStreamDelta } from "@/modules/chat/utils/streamDelta";
import MarkdownViewer from "./index";

describe("MarkdownViewer streamed tables", () => {
  it("renders a five-column GFM table after repeated live chunk boundaries", () => {
    const chunks = [
      "| 景点名称 | 推荐理由 | 门票价格 | 开放时间 | 交通方式 |\n|----------------|-------------------------|--------------------",
      "|---------------------------|---------------------------------|\n| 故宫博物院 | 秋季夜间特展 | 普通票40元 | 8:30-17:0",
      "0<br>夜间活动需预约 | 地铁1号线天安门东站 |\n| 香山公园 | 红叶 | 平日15元 | 6:0",
      "0-18:00 | 公交 |\n",
    ];
    const markdown = chunks.reduce(
      (current, chunk) => mergeChatStreamDelta(current, chunk),
      "",
    );

    render(<MarkdownViewer IS_STREAMING>{markdown}</MarkdownViewer>);

    expect(screen.getByRole("table")).toBeInTheDocument();
    expect(screen.getAllByRole("columnheader")).toHaveLength(5);
    expect(screen.getByText(/8:30-17:00/)).toBeInTheDocument();
    expect(screen.getByText("6:00-18:00")).toBeInTheDocument();
  });

  it("keeps GFM table columns when a row ends with a source chip", () => {
    const markdown = [
      "| 排名 | 模型 | 来源 |",
      "|---|---|---|",
      "| 1 | GPT-5 | [1](#source-1.1) |",
      "| 2 | Claude | [2](#source-2.1) |",
    ].join("\n");

    render(
      <MarkdownViewer
        sources={[
          { source_type: "external", index: "1.1", url: "https://en.wikipedia.org/wiki/GPT-5" },
          { source_type: "external", index: "2.1", url: "https://en.wikipedia.org/wiki/Claude" },
        ]}
      >
        {markdown}
      </MarkdownViewer>,
    );

    expect(screen.getByRole("table")).toBeInTheDocument();
    expect(screen.getAllByRole("columnheader")).toHaveLength(3);
    expect(screen.getAllByRole("cell")).toHaveLength(6);
    expect(screen.getAllByText("en.wikipedia.org")).toHaveLength(2);
  });
});
