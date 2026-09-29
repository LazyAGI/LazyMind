import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { RoleTypes } from "@/modules/chat/constants/common";
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

vi.mock("@/modules/chat/components/MailDraftCard", () => ({
  default: ({ draft }: { draft?: { subject?: string; draft_id?: string } }) => (
    <div>{draft?.subject || draft?.draft_id}</div>
  ),
}));

vi.mock("@/modules/chat/components/MailDraftCard/MailMailboxCard", () => ({
  default: () => null,
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

describe("MessageList resource references", () => {
  it.each([
    ["skill", "last30days-cn", "last30days-cn", "/memory-management/skills/resource-1", "thunderbolt"],
    ["workflow", "last30days-cn", "Last 30 Days Workflow", "/memory-management/workflows/resource-1", "appstore"],
    ["skill", "请汇总近期信息", "last30days-cn", "/memory-management/skills/resource-1", "thunderbolt"],
  ])("renders the persisted %s reference even when its name is not the question", (type, query, displayName, href, icon) => {
    const messages = buildChatMessageListFromHistory([{
      id: "referenced-history", query, result: "answer",
      mentions: [{ mention_id: "mention-1", type, resource_id: "resource-1", display_name: displayName }],
    }] as any);
    render(<MessageList messageList={messages} sendMessage={vi.fn()} regenerate={vi.fn()} stopGeneration={vi.fn()}
      updateAssistantMessage={vi.fn()} renderText={item => <span>{item.delta}</span>} />);

    const reference = screen.getByRole("link", { name: new RegExp(displayName) });
    expect(reference).toHaveAttribute("href", href);
    expect(reference.querySelector(`[data-icon="${icon}"]`)).toBeInTheDocument();
    expect(screen.getByText(query)).toBeInTheDocument();
  });

  it("does not infer a resource reference from ordinary message text", () => {
    render(<MessageList messageList={buildChatMessageListFromHistory([{ id: "plain-history", query: "last30days-cn", result: "answer" }])}
      sendMessage={vi.fn()} regenerate={vi.fn()} stopGeneration={vi.fn()} updateAssistantMessage={vi.fn()}
      renderText={item => <span>{item.delta}</span>} />);
    expect(screen.getByText("last30days-cn")).toBeInTheDocument();
    expect(document.querySelector(".chat-history-mention")).toBeNull();
  });
});

describe("MessageList side chat selection", () => {
  it("removes the entire failed reply on retry and shows a new failure if retry fails", () => {
    const failed = {
      result: "old partial reply",
      run_id: "failed-run-1",
      run_status: "failed",
      run_terminal: {
        status: "failed", reason: "model_failure", code: "transport_error", partial_output: true,
      },
    };
    const record = { id: "history-1", query: "question", ...failed };
    const props = {
      sendMessage: vi.fn(), stopGeneration: vi.fn(), updateAssistantMessage: vi.fn(),
      renderText: (item: any) => <span>{item.delta}</span>,
    };
    const retry = vi.fn(() => {
      rerender(<MessageList {...props} regenerate={retry} messageList={buildChatMessageListFromHistory([
        { ...record, result: "", run_status: undefined, run_terminal: undefined, failed_attempts: [failed] },
      ])} />);
    });
    const { rerender } = render(<MessageList {...props} regenerate={retry}
      messageList={buildChatMessageListFromHistory([record])} />);
    expect(screen.getByText("old partial reply")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "chat.tryAgain" }));
    expect(retry).toHaveBeenCalledOnce();
    expect(screen.queryByText("old partial reply")).not.toBeInTheDocument();
    expect(screen.queryByText("chat.runStatus.failed")).not.toBeInTheDocument();

    rerender(<MessageList {...props} regenerate={retry} messageList={buildChatMessageListFromHistory([
      { ...record, result: "new partial reply", run_id: "failed-run-2", failed_attempts: [failed] },
    ])} />);
    expect(screen.queryByText("old partial reply")).not.toBeInTheDocument();
    expect(screen.getByText("new partial reply")).toBeInTheDocument();
    expect(screen.getAllByText("chat.runStatus.failed")).toHaveLength(1);
  });

  it("hides retried failures restored from history while preserving attachments and the latest reply", () => {
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
    expect(screen.queryByText("failed partial answer")).not.toBeInTheDocument();
    expect(screen.queryByText("chat.runStatus.failed")).not.toBeInTheDocument();
    expect(document.querySelector('[data-chat-history-id="history-1:failed:failed-run-1"]')).toBeNull();
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

describe("MessageList capability configuration precedence", () => {
  const assistantWithAsk = {
    role: "assistant",
    delta: "",
    ask_pending: {
      ask_id: "redundant-ask",
      title: "缺少文生图模型",
      questions: [{
        text: "文生图模型还没配置，你希望怎么处理？",
        type: "single",
        choices: ["我去配置"],
      }],
    },
  };

  const renderMessages = (suppressAskPending: boolean) => render(
    <MessageList
      messageList={[assistantWithAsk]}
      suppressAskPending={suppressAskPending}
      capabilityConfigCard={<div>配置文生图模型</div>}
      sendMessage={vi.fn()}
      regenerate={vi.fn()}
      stopGeneration={vi.fn()}
      renderText={() => null}
      updateAssistantMessage={vi.fn()}
    />,
  );

  it("hides a redundant Ask card while capability configuration is required", () => {
    renderMessages(true);

    expect(screen.getByText("配置文生图模型")).toBeInTheDocument();
    expect(screen.queryByText("文生图模型还没配置，你希望怎么处理？"))
      .not.toBeInTheDocument();
  });

  it("keeps a mail confirmation card while capability configuration is required", () => {
    render(
      <MessageList
        messageList={[{
          role: RoleTypes.ASSISTANT,
          delta: "preview",
          ask_pending: {
            ask_id: "mail-ask",
            mail_draft: { draft_id: "draft_1", subject: "send preview", status: "draft" },
          },
        }]}
        suppressAskPending
        capabilityConfigCard={<div>配置文生图模型</div>}
        sendMessage={vi.fn()}
        regenerate={vi.fn()}
        stopGeneration={vi.fn()}
        renderText={() => null}
        updateAssistantMessage={vi.fn()}
      />,
    );

    expect(screen.getByText("send preview")).toBeInTheDocument();
  });
});

describe("MessageList render isolation", () => {
  it("keeps later messages when one assistant reply throws", () => {
    const errorSpy = vi.spyOn(console, "error").mockImplementation(() => undefined);
    render(
      <MessageList
        messageList={[
          { role: RoleTypes.ASSISTANT, delta: "explode table", id: "broken" },
          { role: RoleTypes.ASSISTANT, delta: "still here", id: "ok" },
        ]}
        sendMessage={vi.fn()}
        regenerate={vi.fn()}
        stopGeneration={vi.fn()}
        renderText={(item) => {
          if (String(item.delta || "").includes("explode")) {
            throw new Error("bad table");
          }
          return <span>{item.delta}</span>;
        }}
        updateAssistantMessage={vi.fn()}
      />,
    );

    expect(screen.getByText("chat.messageRenderFailed")).toBeInTheDocument();
    expect(screen.getByText("still here")).toBeInTheDocument();
    errorSpy.mockRestore();
  });
});
