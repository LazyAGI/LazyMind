import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import MarkdownViewer from "./index";

vi.mock("@/modules/chat/components/WorkflowPanel/MarkdownArtifactEditor", () => ({
  MarkdownArtifactEditor: ({ markdown }: { markdown: string }) => (
    <div data-testid="shared-markdown-editor">{markdown}</div>
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
