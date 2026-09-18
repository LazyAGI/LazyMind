import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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
    expect(screen.queryByText("+1")).not.toBeInTheDocument();
  });

  it("shows one connected source capsule and pages through its sources", async () => {
    render(
      <MarkdownViewer
        sources={[
          {
            source_type: "external",
            index: "1.1",
            title: "Yuan Shikai",
            url: "https://en.wikipedia.org/wiki/Yuan_Shikai",
          },
          {
            source_type: "external",
            index: "2.1",
            title: "Xinhai Revolution",
            url: "https://en.wikipedia.org/wiki/Xinhai_Revolution",
          },
        ]}
      >
        {"袁世凯下台。[1](#source-1.1)[2](#source-2.1)"}
      </MarkdownViewer>,
    );

    const capsule = screen.getByRole("link", { name: "en.wikipedia.org" });
    expect(capsule).toHaveTextContent("en.wikipedia.org+1");
    const icon = capsule.querySelector(".md-source-chip-icon");
    const favicon = icon?.querySelector("img");
    expect(favicon).toBeInTheDocument();
    expect(getComputedStyle(capsule).height).toBe("18px");
    expect(getComputedStyle(icon as Element).width).toBe("14px");
    expect(getComputedStyle(favicon as Element).width).toBe("14px");

    fireEvent.mouseEnter(capsule);
    await waitFor(() => expect(screen.getByText("1/2")).toBeInTheDocument());
    expect(screen.getByText("Yuan Shikai")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Next source" }));
    expect(screen.getByText("2/2")).toBeInTheDocument();
    expect(screen.getByText("Xinhai Revolution")).toBeInTheDocument();
  });

  it("aggregates citations inside each table cell and keeps other cells independent", async () => {
    const markdown = [
      "| 价格 | 参数 |",
      "|---|---|",
      "| $1 [1](#source-1.1)[2](#source-2.1) | 32GB [3](#source-3.1) |",
    ].join("\n");

    render(
      <MarkdownViewer
        sources={[
          {
            source_type: "external",
            index: "1.1",
            title: "Price list",
            url: "https://price.example/list",
          },
          {
            source_type: "external",
            index: "2.1",
            title: "Vendor page",
            url: "https://vendor.example/page",
          },
          {
            source_type: "external",
            index: "3.1",
            title: "Spec sheet",
            url: "https://spec.example/sheet",
          },
        ]}
      >
        {markdown}
      </MarkdownViewer>,
    );

    const cells = screen.getAllByRole("cell");
    expect(cells).toHaveLength(2);
    expect(cells[0].querySelectorAll("a.md-source-chip")).toHaveLength(1);
    expect(cells[1].querySelectorAll("a.md-source-chip")).toHaveLength(1);

    const priceChip = cells[0].querySelector("a.md-source-chip") as HTMLAnchorElement;
    const specChip = cells[1].querySelector("a.md-source-chip") as HTMLAnchorElement;
    expect(priceChip).toHaveTextContent("price.example+1");
    expect(specChip).toHaveTextContent("spec.example");
    expect(specChip).not.toHaveTextContent("+1");

    fireEvent.mouseEnter(priceChip);
    await waitFor(() => expect(screen.getByText("1/2")).toBeInTheDocument());
    expect(screen.getByText("Price list")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Next source" }));
    expect(screen.getByText("2/2")).toBeInTheDocument();
    expect(screen.getByText("Vendor page")).toBeInTheDocument();

    fireEvent.mouseLeave(priceChip);
    fireEvent.mouseEnter(specChip);
    await waitFor(() => expect(screen.getByText("Spec sheet")).toBeInTheDocument());
    expect(screen.queryByText("1/2")).not.toBeInTheDocument();
  });
});
