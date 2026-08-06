import { createRef } from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderWithProviders, screen } from "@/test/testUtils";
import ChatContainerComponent, { type ChatImperativeProps } from "./index";
import { RoleTypes } from "@/modules/chat/constants/common";

const mockSendMessage = vi.fn();
const mockCreateNewChat = vi.fn();
const mockReplaceMessageList = vi.fn();
const mockDisconnectConversationStream = vi.fn();
const mockOpenResumeSSE = vi.fn();
const mockAppendAutoAdvanceTurn = vi.fn();
const mockEnsureAutoAdvanceUserTurn = vi.fn();
const mockUploadFiles = vi.fn();

let mockMessageList: any[] = [];
let mockIsStreaming = false;

vi.mock("./hooks/useChatConversation", () => ({
  useChatConversation: () => ({
    messageList: mockMessageList,
    messageListRef: { current: mockMessageList },
    setMessageList: vi.fn(),
    isStreaming: mockIsStreaming,
    loading: false,
    runtimeWaiting: false,
    content: "",
    setContent: vi.fn(),
    sendMessage: mockSendMessage,
    regenerate: vi.fn(),
    stopGeneration: vi.fn(),
    updateAssistantMessage: vi.fn(),
    createNewChat: mockCreateNewChat,
    replaceMessageList: mockReplaceMessageList,
    disconnectConversationStream: mockDisconnectConversationStream,
    openResumeSSE: mockOpenResumeSSE,
    appendAutoAdvanceTurn: mockAppendAutoAdvanceTurn,
    ensureAutoAdvanceUserTurn: mockEnsureAutoAdvanceUserTurn,
    activeStreamRef: { current: false },
    currentConversationIdRef: { current: "" },
    conversationMessagesCache: { current: new Map() },
    scroll: {
      chatContentRef: { current: null },
      showScrollButton: false,
      inputHeight: 120,
      handleScroll: vi.fn(),
      handleToBottom: vi.fn(),
      handleInputHeightChange: vi.fn(),
      scrollToEnd: vi.fn(),
    },
  }),
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
    isThinkingCollapsed: () => true,
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

vi.mock("./components/MessageList", () => ({
  default: ({ messageList }: { messageList: any[] }) => (
    <div data-testid="message-list-stub">{messageList.length}</div>
  ),
}));

vi.mock("./components/ChatMessageContent", () => ({
  default: () => <div data-testid="chat-message-content-stub" />,
}));

vi.mock("./components/ScrollToBottomButton", () => ({
  default: () => <div data-testid="scroll-to-bottom-stub" />,
}));

vi.mock("./components/ConversationTrail", () => ({
  default: () => <div data-testid="conversation-trail-stub" />,
}));

vi.mock("../ChatInput", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  return {
    SKILL_DEPOSIT_MIN_TOOL_CALL_TURNS: 1,
    SKILL_DEPOSIT_MIN_USER_TURNS: 1,
    default: React.forwardRef((props: any, ref: any) => {
      React.useImperativeHandle(ref, () => ({
        uploadFiles: mockUploadFiles,
        focus: vi.fn(),
      }));
      return <div data-testid="chat-input-stub" />;
    }),
  };
});

function baseProps(overrides: Record<string, unknown> = {}) {
  return {
    sessionId: "conv-1",
    onOpenSSE: vi.fn(),
    parseErrorData: (data: string) => data,
    ...overrides,
  };
}

describe("ChatContainerComponent", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockMessageList = [];
    mockIsStreaming = false;
  });

  it("renders the message list and chat input", () => {
    renderWithProviders(<ChatContainerComponent {...baseProps()} />);
    expect(screen.getByTestId("message-list-stub")).toBeInTheDocument();
    expect(screen.getByTestId("chat-input-stub")).toBeInTheDocument();
  });

  it("does not render the scroll-to-bottom button when the message list is empty", () => {
    renderWithProviders(<ChatContainerComponent {...baseProps()} />);
    expect(screen.queryByTestId("scroll-to-bottom-stub")).not.toBeInTheDocument();
  });

  it("renders the scroll-to-bottom button once there are messages", () => {
    mockMessageList = [{ role: RoleTypes.USER, delta: "hi" }];
    renderWithProviders(<ChatContainerComponent {...baseProps()} />);
    expect(screen.getByTestId("scroll-to-bottom-stub")).toBeInTheDocument();
  });

  it("exposes sendMessage, createNewChat and replaceMessageList via the imperative ref", () => {
    const ref = createRef<ChatImperativeProps>();
    renderWithProviders(<ChatContainerComponent ref={ref} {...baseProps()} />);

    ref.current?.sendMessage({ text: "hello" });
    expect(mockSendMessage).toHaveBeenCalledWith(
      expect.objectContaining({ text: "hello" }),
    );

    ref.current?.createNewChat();
    expect(mockCreateNewChat).toHaveBeenCalled();

    ref.current?.replaceMessageList("conv-2", []);
    expect(mockReplaceMessageList).toHaveBeenCalledWith("conv-2", []);
  });

  it("forwards uploadFiles from the imperative ref to the chat input", () => {
    const ref = createRef<ChatImperativeProps>();
    renderWithProviders(<ChatContainerComponent ref={ref} {...baseProps()} />);
    const files = [new File(["x"], "a.txt")];
    ref.current?.uploadFiles(files);
    expect(mockUploadFiles).toHaveBeenCalledWith(files);
  });

  it("only exposes openResumeSSE and appendAutoAdvanceTurn when onOpenResumeSSE is provided", () => {
    const refWithout = createRef<ChatImperativeProps>();
    renderWithProviders(<ChatContainerComponent ref={refWithout} {...baseProps()} />);
    expect(refWithout.current?.openResumeSSE).toBeUndefined();

    const refWith = createRef<ChatImperativeProps>();
    renderWithProviders(
      <ChatContainerComponent ref={refWith} {...baseProps({ onOpenResumeSSE: vi.fn() })} />,
    );
    expect(typeof refWith.current?.openResumeSSE).toBe("function");
  });
});
