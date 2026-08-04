import { describe, expect, it, vi } from "vitest";
import { renderWithProviders, screen, fireEvent } from "@/test/testUtils";
import ChatMessageContent from "./ChatMessageContent";
import { RoleTypes } from "@/modules/chat/constants/common";

vi.mock("@/modules/chat/components/MarkdownViewer", () => ({
  default: ({ children }: { children: string }) => (
    <div data-testid="markdown-viewer">{children}</div>
  ),
}));

describe("ChatMessageContent", () => {
  it("renders the message text via MarkdownViewer", () => {
    renderWithProviders(
      <ChatMessageContent
        item={{
          role: RoleTypes.ASSISTANT,
          delta: "Hello world",
          finish_reason: "FINISH_REASON_STOP",
        }}
        isThinkingCollapsed={() => true}
        onToggleThinkingCollapse={vi.fn()}
      />,
    );
    expect(screen.getByTestId("markdown-viewer")).toHaveTextContent("Hello world");
  });

  it("renders images and files attached to the message", () => {
    renderWithProviders(
      <ChatMessageContent
        item={{
          role: RoleTypes.ASSISTANT,
          delta: "with attachments",
          finish_reason: "FINISH_REASON_STOP",
          images: [{ uid: "img-1", url: "https://example.com/a.png" }],
          files: [{ uid: "file-1", name: "report.pdf" }],
        }}
        isThinkingCollapsed={() => true}
        onToggleThinkingCollapse={vi.fn()}
      />,
    );
    expect(screen.getByText("report.pdf")).toBeInTheDocument();
    expect(document.querySelector(".chat-images-item")).not.toBeNull();
  });

  it("shows a collapsible thinking section when reasoning_content is present", () => {
    const onToggle = vi.fn();
    renderWithProviders(
      <ChatMessageContent
        item={{
          role: RoleTypes.ASSISTANT,
          reasoning_content: "internal reasoning",
          delta: "final answer",
          finish_reason: "FINISH_REASON_STOP",
        }}
        uniqueKey="msg-1"
        isThinkingCollapsed={() => false}
        onToggleThinkingCollapse={onToggle}
      />,
    );
    expect(screen.getByText("chat.thinkingDone")).toBeInTheDocument();
    fireEvent.click(screen.getByText("chat.thinkingDone").closest(".chat-think-status")!);
    expect(onToggle).toHaveBeenCalledWith("msg-1", false);
  });

  it("shows the citation icon and tooltip content for a user message with cite messages", () => {
    renderWithProviders(
      <ChatMessageContent
        item={{
          role: RoleTypes.USER,
          delta: "quoted question",
          finish_reason: "FINISH_REASON_STOP",
          cite_messages: ["cited excerpt"],
        }}
        isThinkingCollapsed={() => true}
        onToggleThinkingCollapse={vi.fn()}
      />,
    );
    expect(screen.getByLabelText("chat.cite")).toBeInTheDocument();
  });

  it("does not show the citation icon for assistant messages", () => {
    renderWithProviders(
      <ChatMessageContent
        item={{
          role: RoleTypes.ASSISTANT,
          delta: "an answer",
          finish_reason: "FINISH_REASON_STOP",
          cite_messages: ["cited excerpt"],
        }}
        isThinkingCollapsed={() => true}
        onToggleThinkingCollapse={vi.fn()}
      />,
    );
    expect(screen.queryByLabelText("chat.cite")).not.toBeInTheDocument();
  });

  it("shows the conversation intent badge when intent_updated has conversation scope", () => {
    renderWithProviders(
      <ChatMessageContent
        item={{
          role: RoleTypes.ASSISTANT,
          delta: "updated intent",
          finish_reason: "FINISH_REASON_STOP",
          intent_updated: {
            scope: "conversation",
            intent_context: { goal: "Ship the feature" },
          },
        }}
        isThinkingCollapsed={() => true}
        onToggleThinkingCollapse={vi.fn()}
      />,
    );
    expect(screen.getByText("chat.intentUpdated")).toBeInTheDocument();
  });
});
