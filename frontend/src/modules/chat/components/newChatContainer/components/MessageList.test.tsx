import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { buildChatMessageListFromHistory } from "@/modules/chat/utils/message";
import ChatMessageContent from "./ChatMessageContent";
import MessageList from "./MessageList";

vi.mock("react-i18next", () => ({
  initReactI18next: {
    type: "3rdParty",
    init: () => undefined,
  },
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("@/modules/chat/store/workflowPanel", () => ({
  useWorkflowStore: () => null,
}));

vi.mock("@/modules/chat/components/WorkflowPanel", () => ({
  WorkflowPanel: () => null,
}));

vi.mock("@/modules/identityAvatar", () => ({
  IdentityAvatar: () => null,
}));

vi.mock("@/modules/chat/components/MarkdownViewer", () => ({
  default: ({ children }: { children?: React.ReactNode }) => <span>{children}</span>,
}));

function selectMessageText(text: string) {
  const selected = screen.getByText(text);
  const range = document.createRange();
  range.selectNodeContents(selected);
  Object.defineProperty(range, "getClientRects", {
    value: () => [{ top: 80, left: 100, right: 220, width: 120, height: 20 }],
  });
  const selection = window.getSelection();
  selection?.removeAllRanges();
  selection?.addRange(range);
  fireEvent.mouseUp(selected);
}

describe("MessageList side chat selection", () => {
  it("preserves failed attempts and attachments while only allowing side chat from real history", () => {
    const onOpenSideChat = vi.fn();
    const onCiteMessage = vi.fn();
    const messageList = buildChatMessageListFromHistory([
      {
        id: "history-1",
        query: "explain this report",
        input: [
          { input_type: "text", text: "explain this report" },
          { input_type: "file", uri: "/uploads/report.pdf", file_id: "file-1" },
        ],
        result: "latest answer",
        failed_attempts: [
          {
            result: "failed partial answer",
            run_id: "failed-run-1",
            run_status: "failed",
            run_terminal: {
              status: "failed",
              reason: "model_failure",
              code: "rate_limited",
              partial_output: true,
            },
          },
        ],
      },
    ]);

    render(
      <MessageList
        messageList={messageList}
        sendMessage={vi.fn()}
        regenerate={vi.fn()}
        stopGeneration={vi.fn()}
        renderText={(item) => (
          <ChatMessageContent
            item={item}
            isThinkingCollapsed={() => false}
            onToggleThinkingCollapse={vi.fn()}
          />
        )}
        updateAssistantMessage={vi.fn()}
        onCiteMessage={onCiteMessage}
        onOpenSideChat={onOpenSideChat}
      />,
    );

    expect(screen.getByText("explain this report")).toBeInTheDocument();
    expect(screen.getByText("report.pdf")).toBeInTheDocument();
    expect(screen.getByText("failed partial answer")).toBeInTheDocument();
    expect(screen.getByText("chat.runStatus.failed")).toBeInTheDocument();

    selectMessageText("failed partial answer");
    expect(
      screen.queryByRole("button", { name: "chat.sideChat.askFromSelection" }),
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "chat.cite" }));
    expect(onCiteMessage).toHaveBeenCalledWith(
      "failed partial answer",
      "history-1:failed:failed-run-1",
    );
    expect(onOpenSideChat).not.toHaveBeenCalled();

    selectMessageText("latest answer");
    fireEvent.click(
      screen.getByRole("button", { name: "chat.sideChat.askFromSelection" }),
    );
    expect(onOpenSideChat).toHaveBeenCalledOnce();
    expect(onOpenSideChat).toHaveBeenCalledWith({
      selectedText: "latest answer",
      historyId: "history-1",
      sequence: undefined,
    });
  });
});
