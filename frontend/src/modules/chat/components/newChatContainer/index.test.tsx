import { fireEvent, render, screen } from "@testing-library/react";
import { forwardRef } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ChatContainerComponent from "./index";

const mocks = vi.hoisted(() => ({
  chatContentRef: { current: null as HTMLDivElement | null },
  messageScrollBy: vi.fn(),
  regenerate: vi.fn(),
  latestChatInputProps: null as any,
  latestConversationOptions: null as any,
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("@/i18n", () => ({
  default: { getResource: () => "" },
}));

vi.mock("antd", () => ({
  message: { info: vi.fn() },
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
  }: {
    chatContentRef: typeof mocks.chatContentRef;
    regenerate: () => void;
    regenerateDisabled: boolean;
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
        type="button"
        disabled={regenerateDisabled}
        onClick={regenerate}
      >
        retry failed message
      </button>
    </>
  ),
}));

vi.mock("../AssistantMessage", () => ({ ChatSourcePanel: () => null }));
vi.mock("./components/ChatMessageContent", () => ({ default: () => null }));
vi.mock("./components/ScrollToBottomButton", () => ({ default: () => null }));
vi.mock("./components/ConversationTrail", () => ({ default: () => null }));
vi.mock("./components/StreamRecoveryBanner", () => ({ default: () => null }));

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
    replaceMessageList: vi.fn(),
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
    },
    sendMessage: vi.fn(),
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
  useUserMessageEdit: () => ({
    editingUserMessageIndex: null,
    editingUserMessageText: "",
    editingUserMessageCites: [],
    setEditingUserMessageText: vi.fn(),
    handleRemoveEditingUserMessageCite: vi.fn(),
    handleStartEditUserMessage: vi.fn(),
    handleCancelEditUserMessage: vi.fn(),
    handleResendEditedUserMessage: vi.fn(),
    handleCopyUserMessage: vi.fn(),
  }),
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
    mocks.latestChatInputProps = null;
    mocks.latestConversationOptions = null;
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
});
