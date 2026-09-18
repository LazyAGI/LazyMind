import { act, fireEvent, render, screen } from "@testing-library/react";
import { forwardRef } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ChatContainerComponent from "./index";

const mocks = vi.hoisted(() => ({
  chatContentRef: { current: null as HTMLDivElement | null },
  messageScrollBy: vi.fn(),
  regenerate: vi.fn(),
  conversationSendMessage: vi.fn(() => Promise.resolve(true)),
  getHistory: vi.fn(),
  mergeHistoryPage: vi.fn(),
  error: vi.fn(),
  latestTrailProps: null as any,
  latestChatInputProps: null as any,
  latestConversationOptions: null as any,
  latestUserEditOptions: null as any,
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("@/i18n", () => ({
  default: { getResource: () => "" },
}));

vi.mock("antd", () => ({
  message: { info: vi.fn(), error: mocks.error },
  Drawer: ({ open, children, zIndex, onClose }: any) => open ? (
    <div role="dialog" data-z-index={zIndex}>
      <button onClick={onClose}>close sources drawer</button>{children}
    </div>
  ) : null,
}));

vi.mock("@/modules/chat/store/chatMessage", () => ({
  useChatMessageStore: () => ({ clearPendingMessage: vi.fn() }),
}));

vi.mock("../ChatInput", () => ({
  default: forwardRef(function MockChatInput(props: any, _ref) {
    mocks.latestChatInputProps = props;
    return (
      <div className="input-wrapper">
        <div className="input-top" data-testid="chat-input-top" />
        <button
          type="button"
          onClick={() => props.onModelSelectionSavingChange?.(true)}
        >
          begin model save
        </button>
        <button
          type="button"
          onClick={() => props.onModelSelectionSavingChange?.(false)}
        >
          finish model save
        </button>
        <button
          type="button"
          onClick={() => props.onWorkspacePermissionSavingChange?.(true)}
        >
          begin workspace save
        </button>
        <button
          type="button"
          onClick={() => props.onWorkspacePermissionSavingChange?.(false)}
        >
          finish workspace save
        </button>
        <button
          type="button"
          onClick={() => props.onSend?.({ text: "programmatic send" })}
        >
          send through container
        </button>
      </div>
    );
  }),
  SKILL_DEPOSIT_MIN_TOOL_CALL_TURNS: 1,
  SKILL_DEPOSIT_MIN_USER_TURNS: 1,
}));

vi.mock("./components/MessageList", () => ({
  default: ({
    chatContentRef,
    regenerate,
    regenerateDisabled,
    onOpenSources,
  }: {
    chatContentRef: typeof mocks.chatContentRef;
    regenerate: () => void;
    regenerateDisabled: boolean;
    onOpenSources?: (sources: any[]) => void;
  }) => (
    <>
      <div
        ref={(element) => {
          chatContentRef.current = element;
          if (element) {
            element.scrollBy = mocks.messageScrollBy;
          }
        }}
        data-testid="message-container"
      />
      <button
        onClick={() => onOpenSources?.([{ title: "Source one", url: "https://example.com/source" }])}
      >open sources</button>
      <button
        type="button"
        disabled={regenerateDisabled}
        onClick={regenerate}
      >
        retry failed message
      </button>
    </>
  ),
}));

vi.mock("../AssistantMessage", () => ({
  ChatSourcePanel: ({ onClose }: any) => <aside aria-label="references"><button onClick={onClose}>close sources</button></aside>,
}));
vi.mock("./components/ChatMessageContent", () => ({ default: () => null }));
vi.mock("./components/ScrollToBottomButton", () => ({ default: () => null }));
vi.mock("./components/ConversationTrail", () => ({ default: (props: any) => { mocks.latestTrailProps = props; return null; } }));
vi.mock("@/modules/chat/utils/request", () => ({ ChatServiceApi: () => ({ conversationServiceGetConversationHistory: mocks.getHistory }) }));
vi.mock("./components/StreamRecoveryBanner", () => ({ default: () => null }));
vi.mock("../CapabilityConfigCard", () => ({ default: () => null }));

vi.mock("./hooks/useChatConversation", () => ({
  useChatConversation: (options: any) => {
    mocks.latestConversationOptions = options;
    return {
    activeStreamRef: { current: false },
    appendAutoAdvanceTurn: vi.fn(),
    content: "",
    conversationMessagesCache: { current: new Map() },
    createNewChat: vi.fn(),
    currentConversationIdRef: { current: "conversation-1" },
    disconnectConversationStream: vi.fn(),
    ensureAutoAdvanceUserTurn: vi.fn(),
    isStreaming: false,
    loading: false,
    messageList: [],
    messageListRef: { current: [] },
    openResumeSSE: vi.fn(),
    openSSE: vi.fn(),
    regenerate: mocks.regenerate,
    mediaCapabilityDependency: null,
    mediaCapabilityChecking: false,
    continueAfterMediaCapabilityConfiguration: vi.fn(),
    replaceMessageList: vi.fn(),
    mergeHistoryPage: mocks.mergeHistoryPage,
    retryStreamRecovery: vi.fn(),
    runtimeWaiting: false,
    scroll: {
      chatContentRef: mocks.chatContentRef,
      handleInputHeightChange: vi.fn(),
      handleScroll: vi.fn(),
      handleToBottom: vi.fn(),
      inputHeight: 0,
      scrollToEnd: vi.fn(),
      showScrollButton: false,
      pauseFollowing: vi.fn(),
      unreadCount: 0,
    },
    sendMessage: mocks.conversationSendMessage,
    setContent: vi.fn(),
    setMessageList: vi.fn(),
    stopGeneration: vi.fn(),
    streamRecovery: {},
    updateAssistantMessage: vi.fn(),
    };
  },
}));

vi.mock("./hooks/useCiteMessagesInput", () => ({
  useCiteMessagesInput: () => ({
    citeMessages: [],
    citeHistoryIds: [],
    handleAddCiteMessage: vi.fn(),
    handleRemoveCiteMessage: vi.fn(),
    clearCiteMessages: vi.fn(),
  }),
}));

vi.mock("./hooks/useThinkingCollapse", () => ({
  useThinkingCollapse: () => ({
    thinkingCollapseMap: new Map(),
    toggleThinkingCollapse: vi.fn(),
    isThinkingCollapsed: vi.fn(),
    collapseAllThinking: vi.fn(),
  }),
}));

vi.mock("./hooks/useUserMessageEdit", () => ({
  useUserMessageEdit: (options: any) => {
    mocks.latestUserEditOptions = options;
    return {
    editingUserMessageIndex: null,
    editingUserMessageText: "",
    editingUserMessageCites: [],
    setEditingUserMessageText: vi.fn(),
    handleRemoveEditingUserMessageCite: vi.fn(),
    handleStartEditUserMessage: vi.fn(),
    handleCancelEditUserMessage: vi.fn(),
    handleResendEditedUserMessage: vi.fn(),
    handleCopyUserMessage: vi.fn(),
    };
  },
}));

vi.mock("./hooks/useConversationTrail", () => ({
  useConversationTrail: () => ({
    items: [],
    loading: false,
    error: null,
    retry: vi.fn(),
  }),
}));

describe("ChatContainerComponent wheel forwarding", () => {
  beforeEach(() => {
    mocks.chatContentRef.current = null;
    mocks.messageScrollBy.mockReset();
    mocks.regenerate.mockReset();
    mocks.conversationSendMessage.mockReset();
    mocks.conversationSendMessage.mockResolvedValue(true);
    mocks.getHistory.mockReset();
    mocks.mergeHistoryPage.mockReset();
    mocks.error.mockReset();
    mocks.latestChatInputProps = null;
    mocks.latestConversationOptions = null;
    mocks.latestUserEditOptions = null;
  });

  it("passes the side-chat mention restriction to its composer", () => {
    render(<ChatContainerComponent sessionId="side-chat" allowMentions={false}
      onOpenSSE={vi.fn()} parseErrorData={(data) => data}
      setIsChatContent={vi.fn()} setChatConfigFn={vi.fn()} />);
    expect(mocks.latestChatInputProps.allowMentions).toBe(false);
  });

  it("fetches an anchored history window for an earlier navigation target", async () => {
    const history = [{ id: "old", query: "早期问题", result: "早期回答" }];
    mocks.getHistory.mockResolvedValue({ data: { history } });
    render(<ChatContainerComponent sessionId="conversation-1" onOpenSSE={vi.fn()} parseErrorData={(data) => data} setIsChatContent={vi.fn()} setChatConfigFn={vi.fn()} />);
    await act(async () => { expect(await mocks.latestTrailProps.onLocate("old")).toBe(true); });
    expect(mocks.getHistory).toHaveBeenCalledWith({ name: "conversation-1", anchorHistoryId: "old" });
    expect(mocks.mergeHistoryPage).toHaveBeenCalledWith("conversation-1", history);
  });

  it("ignores a navigation response after switching conversations", async () => {
    let complete!: (value: any) => void;
    mocks.getHistory.mockImplementation(() => new Promise((resolve) => { complete = resolve; }));
    const props = { onOpenSSE: vi.fn(), parseErrorData: (data: string) => data, setIsChatContent: vi.fn(), setChatConfigFn: vi.fn() };
    const view = render(<ChatContainerComponent {...props} sessionId="conversation-1" />);
    const pending = mocks.latestTrailProps.onLocate("old");
    view.rerender(<ChatContainerComponent {...props} sessionId="conversation-2" />);
    await act(async () => { complete({ data: { history: [{ id: "old" }] } }); expect(await pending).toBe(false); });
    expect(mocks.mergeHistoryPage).not.toHaveBeenCalled();
  });

  it("reports a failed history lookup without changing the transcript", async () => {
    mocks.getHistory.mockRejectedValue(new Error("offline"));
    render(<ChatContainerComponent sessionId="conversation-1" onOpenSSE={vi.fn()} parseErrorData={(data) => data} setIsChatContent={vi.fn()} setChatConfigFn={vi.fn()} />);
    await act(async () => { expect(await mocks.latestTrailProps.onLocate("old")).toBe(false); });
    expect(mocks.error).toHaveBeenCalledWith("chat.fork.historyLoadFailed");
    expect(mocks.mergeHistoryPage).not.toHaveBeenCalled();
  });

  it.each([false, true])("opens references with side-chat overlay=%s and closes only references", (overlay) => {
    const { container } = render(<ChatContainerComponent
      onOpenSSE={vi.fn()} parseErrorData={(data) => data} setIsChatContent={vi.fn()}
      setChatConfigFn={vi.fn()} conversationTrailEnabled={false} sourcePanelOverlay={overlay}
    />);
    fireEvent.click(screen.getByRole("button", { name: "open sources" }));
    expect(screen.getByRole("complementary", { name: "references" })).toBeInTheDocument();
    if (overlay) {
      expect(screen.getByRole("dialog")).toHaveAttribute("data-z-index", "1100");
      expect(container.querySelector(".has-source-panel")).toBeNull();
    } else {
      expect(screen.queryByRole("dialog")).toBeNull();
      expect(container.querySelector(".has-source-panel")).not.toBeNull();
    }
    fireEvent.click(screen.getByRole("button", { name: "close sources" }));
    expect(screen.queryByRole("complementary", { name: "references" })).toBeNull();
    expect(screen.getByTestId("message-container")).toBeInTheDocument();
  });

  it("does not scroll the conversation when the wheel starts inside the chat input", () => {
    const { container } = render(
      <ChatContainerComponent
        onOpenSSE={vi.fn()}
        parseErrorData={(data) => data}
        setIsChatContent={vi.fn()}
        setChatConfigFn={vi.fn()}
        conversationTrailEnabled={false}
      />,
    );

    const chatContainer = container.querySelector(".chat-chat-container");
    expect(chatContainer).not.toBeNull();

    fireEvent.wheel(chatContainer!, { deltaY: 120 });
    expect(mocks.messageScrollBy).toHaveBeenCalledWith({
      top: 120,
      behavior: "auto",
    });

    mocks.messageScrollBy.mockClear();
    fireEvent.wheel(screen.getByTestId("chat-input-top"), { deltaY: 120 });

    expect(mocks.messageScrollBy).not.toHaveBeenCalled();
  });

  it("shares the model-save lock with failed-message retry actions", () => {
    render(
      <ChatContainerComponent
        onOpenSSE={vi.fn()}
        parseErrorData={(data) => data}
        setIsChatContent={vi.fn()}
        setChatConfigFn={vi.fn()}
        conversationTrailEnabled={false}
      />,
    );

    const retry = screen.getByRole("button", { name: "retry failed message" });
    expect(retry).toBeEnabled();

    fireEvent.click(screen.getByRole("button", { name: "begin model save" }));
    expect(mocks.latestConversationOptions.isModelSelectionSaving()).toBe(true);
    expect(retry).toBeDisabled();
    fireEvent.click(retry);
    expect(mocks.regenerate).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "finish model save" }));
    expect(mocks.latestConversationOptions.isModelSelectionSaving()).toBe(false);
    expect(retry).toBeEnabled();
    fireEvent.click(retry);
    expect(mocks.regenerate).toHaveBeenCalledOnce();
  });

  it("shares the workspace-save lock with every session execution entry point", async () => {
    render(
      <ChatContainerComponent
        onOpenSSE={vi.fn()}
        parseErrorData={(data) => data}
        setIsChatContent={vi.fn()}
        setChatConfigFn={vi.fn()}
        conversationTrailEnabled={false}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "begin workspace save" }));

    expect(mocks.latestConversationOptions.isWorkspacePermissionSaving()).toBe(true);
    expect(screen.getByRole("button", { name: "retry failed message" })).toBeDisabled();
    expect(mocks.latestUserEditOptions.canChat).toBe(false);
    expect(mocks.latestUserEditOptions.loading).toBe(true);

    fireEvent.click(screen.getByRole("button", { name: "retry failed message" }));
    fireEvent.click(screen.getByRole("button", { name: "send through container" }));
    expect(mocks.regenerate).not.toHaveBeenCalled();
    expect(mocks.conversationSendMessage).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "finish workspace save" }));
    expect(mocks.latestConversationOptions.isWorkspacePermissionSaving()).toBe(false);
    expect(screen.getByRole("button", { name: "retry failed message" })).toBeEnabled();
    expect(mocks.latestUserEditOptions.canChat).toBe(true);
    expect(mocks.latestUserEditOptions.loading).toBe(false);

    fireEvent.click(screen.getByRole("button", { name: "retry failed message" }));
    fireEvent.click(screen.getByRole("button", { name: "send through container" }));
    expect(mocks.regenerate).toHaveBeenCalledOnce();
    expect(mocks.conversationSendMessage).toHaveBeenCalledOnce();
  });
});
