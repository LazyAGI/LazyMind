import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import MarkdownViewer from "./index";

vi.mock("@/modules/chat/components/WorkflowPanel/MarkdownArtifactEditor", () => ({
  MarkdownArtifactEditor: ({
    markdown,
    onOpenSourceReference,
    sourceReferences,
  }: {
    markdown: string;
    onOpenSourceReference?: (citationId: string) => void;
    sourceReferences?: Array<{ citationId: string; label: string }>;
  }) => (
    <div data-testid="shared-markdown-editor">
      {markdown}
      {sourceReferences?.map((source) => (
        <span key={source.citationId} data-testid="source-label">{source.label}</span>
      ))}
      {onOpenSourceReference && (
        <button type="button" onClick={() => onOpenSourceReference("1.1")}>
          open source
        </button>
      )}
    </div>
  ),
}));

describe("MarkdownViewer editable writing blocks", () => {
  it("renders a completed editable fence with the shared Markdown editor", () => {
    render(
      <MarkdownViewer conversationId="conversation-1" historyId="history-1">
        {"```editable\n# 标题\n\n正文\n```"}
      </MarkdownViewer>,
    );

    expect(screen.getByTestId("editable-writing-block")).toBeInTheDocument();
    expect(screen.getByTestId("shared-markdown-editor")).toHaveTextContent("# 标题 正文");
  });

  it("keeps editable fence content byte-compatible with the persisted message", () => {
    render(
      <MarkdownViewer conversationId="conversation-1" historyId="history-1">
        {[
          "Outside https://outside.example",
          "",
          "```editable",
          "Inside https://inside.example [1](#user-content-source-1.1)",
          "```",
        ].join("\n")}
      </MarkdownViewer>,
    );

    expect(screen.getByTestId("shared-markdown-editor")).toHaveTextContent(
      "Inside https://inside.example [1](#user-content-source-1.1)",
    );
    expect(screen.getByRole("link", { name: "https://outside.example" }))
      .toHaveAttribute("href", "https://outside.example");
  });

  it("opens the source selected from an editable citation", () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    render(
      <MarkdownViewer
        conversationId="conversation-1"
        historyId="history-1"
        sources={[{
          source_type: "external",
          citation_id: "1.1",
          title: "Python documentation",
          url: "https://docs.python.org/3/",
        }]}
      >
        {"```editable\nEvidence [1](#user-content-source-1.1)\n```"}
      </MarkdownViewer>,
    );

    fireEvent.click(screen.getByRole("button", { name: "open source" }));

    expect(screen.getByTestId("source-label")).toHaveTextContent("docs.python.org");
    expect(open).toHaveBeenCalledWith(
      "https://docs.python.org/3/",
      "_blank",
      "noopener,noreferrer",
    );
  });

  it("keeps an editable fence read-only while the response is streaming", () => {
    render(
      <MarkdownViewer IS_STREAMING>
        {"```editable\n正在生成\n```"}
      </MarkdownViewer>,
    );

    expect(screen.queryByTestId("editable-writing-block")).not.toBeInTheDocument();
    expect(screen.getByText("正在生成")).toBeInTheDocument();
  });

  it("does not turn an ordinary text fence into an editor", () => {
    render(<MarkdownViewer>{"```text\n普通日志\n```"}</MarkdownViewer>);

    expect(screen.queryByTestId("editable-writing-block")).not.toBeInTheDocument();
    expect(screen.getByText("普通日志")).toBeInTheDocument();
  });

  it("does not enable editable blocks without a main-chat message identity", () => {
    render(<MarkdownViewer>{"```editable\n子任务内容\n```"}</MarkdownViewer>);

    expect(screen.queryByTestId("editable-writing-block")).not.toBeInTheDocument();
    expect(screen.getByText("子任务内容")).toBeInTheDocument();
  });
});
