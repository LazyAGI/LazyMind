import { act, render, screen, waitFor } from "@testing-library/react";
import { forwardRef, useImperativeHandle } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ChatLayout from "./index";

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

const mocks = vi.hoisted(() => ({
  getChatStatus: vi.fn(),
  getConversationDetail: vi.fn(),
  getConversationHistory: vi.fn(),
  listConversations: vi.fn(),
  replaceMessageList: vi.fn(),
  openResumeSSE: vi.fn(),
  disconnectConversationStream: vi.fn(),
  createNewChat: vi.fn(),
  sendMessage: vi.fn(),
  setThinkingDepth: vi.fn(),
  messageError: vi.fn(),
  clearPendingMessage: vi.fn(),
  sseConstructor: vi.fn(),
  pendingMessage: null as any,
  latestChatContainerProps: null as any,
  latestSideChatPanelProps: null as any,
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    i18n: { language: "zh-CN", resolvedLanguage: "zh-CN" },
    t: (key: string, params?: { parent?: string }) => {
      if (key === "chat.conversationSourceFrom") {
        return `来源：${params?.parent}`;
      }
      if (key === "chat.conversationForkedFrom") {
        return `Fork自：${params?.parent}`;
      }
      return key;
    },
  }),
}));

vi.mock("react-router-dom", () => ({
  Link: ({ to, children, ...props }: any) => (
    <a href={to} {...props}>
      {children}
    </a>
  ),
}));

vi.mock("antd", () => ({
  message: {
    error: mocks.messageError,
    warning: vi.fn(),
  },
}));

vi.mock("@ant-design/icons", () => ({
  MessageOutlined: () => null,
  UnorderedListOutlined: () => null,
}));

vi.mock("@/components/request", () => ({
  localizeErrorCode: (code: string) => code,
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getAuthHeaders: () => ({}) },
}));

vi.mock("@/modules/chat/components/newChatContainer", () => ({
  default: forwardRef(function MockChatContainer(props: any, ref) {
    mocks.latestChatContainerProps = props;
    useImperativeHandle(ref, () => ({
      replaceMessageList: mocks.replaceMessageList,
      openResumeSSE: mocks.openResumeSSE,
      disconnectConversationStream: mocks.disconnectConversationStream,
      createNewChat: mocks.createNewChat,
      sendMessage: mocks.sendMessage,
    }));
    return <div data-testid="chat-container" data-session-id={props.sessionId} />;
  }),
}));

vi.mock("@/modules/chat/components/SideChatPanel", () => ({
  default: (props: any) => {
    mocks.latestSideChatPanelProps = props;
    return props.open ? (
      <div
        data-testid="side-chat-panel"
        data-parent-id={props.parentConversationId}
        data-selected-text={props.source?.selectedText || ""}
      />
    ) : null;
  },
}));

vi.mock("@/modules/chat/components/InitialCard", () => ({ default: () => null }));
vi.mock("@/modules/chat/components/TaskCenter", () => ({ default: () => null }));
vi.mock("@/modules/chat/components/TaskCenter/taskTimeline", () => ({
  taskCenterDisplayCount: () => 0,
}));
vi.mock("@/modules/chat/components/ImageUpload", () => ({
  allowedUploadTypes: [],
}));

vi.mock("@/modules/chat/utils/request", () => ({
  CHAT_RESUME_STREAM_URL: "/resume",
  CHAT_STREAM_URL: "/chat",
  ChatServiceApi: () => ({
    conversationServiceGetChatStatus: mocks.getChatStatus,
    conversationServiceGetConversationDetail: mocks.getConversationDetail,
    conversationServiceGetConversationHistory: mocks.getConversationHistory,
    conversationServiceListConversations: mocks.listConversations,
  }),
  parseConversationRuntimeSettings: (conversation: any) => conversation.settings,
  resolveConversationThinkingDepth: (conversation: any) => conversation.thinking_depth,
}));

vi.mock("@/modules/chat/utils/message", () => ({
  buildChatMessageListFromHistory: (history: any[]) => history,
}));

vi.mock("@/modules/chat/utils/sse", () => ({
  Method: { POST: "POST" },
  SSE: mocks.sseConstructor,
}));

vi.mock("@/modules/chat/utils/environment", () => ({
  buildEnvironmentContext: () => ({}),
}));

vi.mock("@/utils/developerMode", () => ({
  DEVELOPER_ACTIVE_EVENT: "developer-active",
  isDeveloperModeActive: () => false,
}));

vi.mock("@/modules/chat/store/chatMessage", () => ({
  useChatMessageStore: () => ({
    pendingMessage: mocks.pendingMessage,
    clearPendingMessage: mocks.clearPendingMessage,
  }),
}));

vi.mock("@/modules/chat/store/chatThink", () => ({
  useChatThinkStore: {
    getState: () => ({ thinkingDepth: "medium", setThinkingDepth: mocks.setThinkingDepth }),
  },
}));

vi.mock("@/modules/chat/store/chatInput", () => ({
  useChatInputStore: {
    getState: () => ({
      getArtifactRefs: () => [],
      clearArtifactRefs: vi.fn(),
    }),
  },
}));

vi.mock("@/modules/chat/store/workflowPanel", () => {
  const state = {
    autoRunningByConversation: {},
    sessionByConversation: {},
    workflowUIByWorkflow: {},
    focusedTabByConversation: {},
    focusedSortOrderByConversation: {},
    fetchWorkflowUI: vi.fn(),
    syncSessionSearchConfig: vi.fn(),
  };
  return {
    buildWorkflowSearchConfig: () => ({}),
    filterWorkflowTabs: (tabs: unknown[]) => tabs,
    draftStore: { flushAllDrafts: vi.fn() },
    useWorkflowStore: Object.assign(
      (selector: (value: typeof state) => unknown) => selector(state),
      { getState: () => state },
    ),
  };
});

vi.mock("@/modules/chat/store/taskCenter", () => {
  const state = {
    tasksByConversation: {},
    _loadingTasks: {},
    _taskLoadErrors: {},
    refreshConversationExecution: vi.fn(),
    subscribeConvEvents: vi.fn(),
    unsubscribeConvEvents: vi.fn(),
  };
  return {
    useTaskCenterStore: (selector: (value: typeof state) => unknown) => selector(state),
  };
});

describe("ChatLayout conversation loading", () => {
  beforeEach(() => {
    window.sessionStorage.clear();
    vi.clearAllMocks();
    mocks.pendingMessage = null;
    mocks.latestChatContainerProps = null;
    mocks.latestSideChatPanelProps = null;
    mocks.getChatStatus.mockResolvedValue({ data: { is_generating: false } });
    mocks.listConversations.mockResolvedValue({ data: { conversations: [] } });
    mocks.getConversationHistory.mockImplementation(({ name }: { name: string }) =>
      Promise.resolve({ data: { history: [{ conversation: name }] } }),
    );
  });

  it("does not reset a newly mounted chat before it receives a real id", () => {
    render(
      <ChatLayout
        setIsChatContent={vi.fn()}
        initchatConfig={{}}
        setChatConfigFn={vi.fn()}
        canChat
      />,
    );

    expect(mocks.createNewChat).not.toHaveBeenCalled();
    expect(mocks.disconnectConversationStream).not.toHaveBeenCalled();
  });

  it("sends an initial model selection only for the first new-conversation request", async () => {
    const initialModelSelection = { mode: "fixed", model_id: "model-1" };
    mocks.pendingMessage = {
      text: "hello",
      initial_model_selection: initialModelSelection,
    };

    const { unmount } = render(
      <ChatLayout
        setIsChatContent={vi.fn()}
        initchatConfig={{}}
        setChatConfigFn={vi.fn()}
        canChat
      />,
    );

    await waitFor(() => {
      expect(mocks.sendMessage).toHaveBeenCalledWith(mocks.pendingMessage);
    });

    const prepareFirstConversationId = vi.fn();
    await act(async () => {
      await mocks.latestChatContainerProps.onOpenSSE(
        [],
        "chat_action_next",
        {},
        { __prepareClientConversationId: prepareFirstConversationId },
      );
    });
    const firstCall = mocks.sseConstructor.mock.calls[0];
    const firstPayload = JSON.parse(firstCall[1].payload);
    expect(firstPayload.conversation_id).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i,
    );
    expect(prepareFirstConversationId).toHaveBeenCalledWith(
      firstPayload.conversation_id,
    );
    expect(firstPayload.initial_model_selection).toEqual(initialModelSelection);

    const prepareRetryConversationId = vi.fn();
    await act(async () => {
      await mocks.latestChatContainerProps.onOpenSSE(
        [],
        "chat_action_next",
        {},
        { __prepareClientConversationId: prepareRetryConversationId },
      );
    });
    const secondCall = mocks.sseConstructor.mock.calls[1];
    const secondPayload = JSON.parse(secondCall[1].payload);
    expect(secondPayload.conversation_id).toBe(firstPayload.conversation_id);
    expect(prepareRetryConversationId).toHaveBeenCalledWith(
      firstPayload.conversation_id,
    );
    expect(secondPayload).not.toHaveProperty("initial_model_selection");

    act(() => {
      mocks.latestChatContainerProps.onConversationIdChange(
        "different-conversation",
      );
    });
    expect(screen.getByTestId("chat-container")).toHaveAttribute(
      "data-session-id",
      "",
    );

    mocks.getConversationDetail.mockResolvedValue({
      data: {
        conversation: {
          conversation_id: firstPayload.conversation_id,
          thinking_depth: "medium",
          search_config: {},
          settings: { chat_executor: "lazymind" },
        },
      },
    });
    act(() => {
      mocks.latestChatContainerProps.onConversationIdChange(
        firstPayload.conversation_id,
      );
    });
    expect(screen.getByTestId("chat-container")).toHaveAttribute(
      "data-session-id",
      firstPayload.conversation_id,
    );

    unmount();
    mocks.pendingMessage = null;
    mocks.sseConstructor.mockClear();
    mocks.getConversationDetail.mockResolvedValue({
      data: {
        conversation: {
          conversation_id: "conversation-history",
          thinking_depth: "medium",
          search_config: {},
          settings: { chat_executor: "lazymind" },
        },
      },
    });

    render(
      <ChatLayout
        conversationId="conversation-history"
        setIsChatContent={vi.fn()}
        initchatConfig={{}}
        setChatConfigFn={vi.fn()}
        canChat
      />,
    );

    await waitFor(() => {
      expect(screen.getByTestId("chat-container")).toHaveAttribute(
        "data-session-id",
        "conversation-history",
      );
    });
    await act(async () => {
      await mocks.latestChatContainerProps.onOpenSSE(
        [],
        "chat_action_next",
        {},
      );
    });
    const historicalCall = mocks.sseConstructor.mock.calls[0];
    const historicalPayload = JSON.parse(historicalCall[1].payload);
    expect(historicalPayload).not.toHaveProperty("initial_model_selection");
  });

  it("shows the parent source and return action for a child conversation", async () => {
    mocks.getConversationDetail.mockResolvedValue({
      data: {
        conversation: {
          conversation_id: "child-conversation",
          thinking_depth: "medium",
          search_config: {},
          settings: { chat_executor: "lazymind" },
          parent_conversation_id: "parent-conversation",
          parent_display_name: "主会话标题",
          relation_type: "sidechat",
        },
      },
    });

    render(
      <ChatLayout
        conversationId="child-conversation"
        setIsChatContent={vi.fn()}
        initchatConfig={{}}
        setChatConfigFn={vi.fn()}
        canChat
      />,
    );

    expect(
      await screen.findByRole("region", {
        name: "chat.conversationRelationBannerLabel",
      }),
    ).toHaveTextContent("来源：主会话标题");
    expect(
      screen.getByRole("link", {
        name: "chat.returnToParentConversation",
      }),
    ).toHaveAttribute(
      "href",
      "/agent/chat/home/parent-conversation",
    );
    expect(mocks.latestChatContainerProps.showConversationConfig).toBe(false);
    expect(mocks.latestChatContainerProps.showSkillDeposit).toBe(false);
    expect(mocks.latestChatContainerProps.allowKnowledgeBaseSelection).toBe(false);
    expect(mocks.latestChatContainerProps.onOpenSideChat).toBeUndefined();
  });

  it("opens one side panel from the shared root-conversation callback and refreshes after retain", async () => {
    mocks.getConversationDetail.mockResolvedValue({
      data: {
        conversation: {
          conversation_id: "root-conversation",
          thinking_depth: "medium",
          search_config: {},
          settings: { chat_executor: "lazymind" },
        },
      },
    });
    const refreshed = vi.fn();
    window.addEventListener(
      "lazymind:chat-conversation-list-refresh",
      refreshed,
    );

    const view = render(
      <ChatLayout
        conversationId="root-conversation"
        setIsChatContent={vi.fn()}
        initchatConfig={{}}
        setChatConfigFn={vi.fn()}
        canChat
      />,
    );

    await waitFor(() => {
      expect(mocks.latestChatContainerProps.onOpenSideChat).toEqual(
        expect.any(Function),
      );
    });
    act(() => {
      mocks.latestChatContainerProps.onOpenSideChat({
        selectedText: "选中的回答",
        historyId: "history-1",
      });
    });
    expect(screen.getByTestId("side-chat-panel")).toHaveAttribute(
      "data-parent-id",
      "root-conversation",
    );
    expect(screen.getByTestId("side-chat-panel")).toHaveAttribute(
      "data-selected-text",
      "选中的回答",
    );

    act(() => {
      mocks.latestSideChatPanelProps.onRetained({ id: "child-1" });
    });
    expect(refreshed).toHaveBeenCalledTimes(1);
    view.rerender(
      <ChatLayout
        conversationId="next-conversation"
        setIsChatContent={vi.fn()}
        initchatConfig={{}}
        setChatConfigFn={vi.fn()}
        canChat
      />,
    );
    await waitFor(() => {
      expect(screen.queryByTestId("side-chat-panel")).not.toBeInTheDocument();
    });
    window.removeEventListener(
      "lazymind:chat-conversation-list-refresh",
      refreshed,
    );
  });

  it("keeps execution configuration available for a fork child", async () => {
    mocks.getConversationDetail.mockResolvedValue({
      data: {
        conversation: {
          conversation_id: "fork-conversation",
          thinking_depth: "medium",
          search_config: {},
          settings: { chat_executor: "lazymind" },
          parent_conversation_id: "parent-conversation",
          parent_display_name: "主会话标题",
          relation_type: "fork",
        },
      },
    });

    render(
      <ChatLayout
        conversationId="fork-conversation"
        setIsChatContent={vi.fn()}
        initchatConfig={{}}
        setChatConfigFn={vi.fn()}
        canChat
      />,
    );

    expect(await screen.findByText("Fork自：主会话标题")).toBeInTheDocument();
    expect(mocks.latestChatContainerProps.showConversationConfig).toBe(true);
    expect(mocks.latestChatContainerProps.showSkillDeposit).toBe(true);
    expect(mocks.latestChatContainerProps.allowKnowledgeBaseSelection).toBe(true);
  });

  it("does not let a late route load overwrite a newer route selection", async () => {
    const routeDetail = deferred<any>();
    mocks.getConversationDetail.mockImplementation(
      ({ conversation }: { conversation: string }) => {
        if (conversation === "conversation-a") {
          return routeDetail.promise;
        }
        return Promise.resolve({
          data: {
            conversation: {
              conversation_id: conversation,
              thinking_depth: "high",
              search_config: {},
              settings: { chat_executor: "lazymind" },
            },
          },
        });
      },
    );

    const setIsChatContent = vi.fn();
    const setChatConfigFn = vi.fn();
    const { rerender } = render(
      <ChatLayout
        conversationId="conversation-a"
        setIsChatContent={setIsChatContent}
        initchatConfig={{}}
        setChatConfigFn={setChatConfigFn}
        canChat
      />,
    );

    await waitFor(() => {
      expect(mocks.getConversationDetail).toHaveBeenCalledWith({
        conversation: "conversation-a",
      });
    });

    rerender(
      <ChatLayout
        conversationId="conversation-b"
        setIsChatContent={setIsChatContent}
        initchatConfig={{}}
        setChatConfigFn={setChatConfigFn}
        canChat
      />,
    );

    await waitFor(() => {
      expect(mocks.replaceMessageList).toHaveBeenCalledWith(
        "conversation-b",
        [{ conversation: "conversation-b" }],
      );
      expect(screen.getByTestId("chat-container")).toHaveAttribute(
        "data-session-id",
        "conversation-b",
      );
    });

    await act(async () => {
      routeDetail.resolve({
        data: {
          conversation: {
            conversation_id: "conversation-a",
            thinking_depth: "low",
            search_config: {},
            settings: { chat_executor: "lazymind" },
          },
        },
      });
      await routeDetail.promise;
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(mocks.replaceMessageList).toHaveBeenCalledTimes(1);
    expect(mocks.replaceMessageList).not.toHaveBeenCalledWith(
      "conversation-a",
      expect.anything(),
    );
    expect(mocks.setThinkingDepth).toHaveBeenLastCalledWith("high");
    expect(screen.getByTestId("chat-container")).toHaveAttribute(
      "data-session-id",
      "conversation-b",
    );
    expect(mocks.messageError).not.toHaveBeenCalled();
  });

  it("clears the previous conversation while the next route is loading", async () => {
    const nextHistory = deferred<any>();
    mocks.getConversationDetail.mockImplementation(
      ({ conversation }: { conversation: string }) => Promise.resolve({
        data: {
          conversation: {
            conversation_id: conversation,
            thinking_depth: "high",
            search_config: {},
            settings: { chat_executor: "lazymind" },
          },
        },
      }),
    );
    mocks.getConversationHistory.mockImplementation(
      ({ name }: { name: string }) => name === "conversation-b"
        ? nextHistory.promise
        : Promise.resolve({ data: { history: [{ conversation: name }] } }),
    );

    const { rerender } = render(
      <ChatLayout
        conversationId="conversation-a"
        setIsChatContent={vi.fn()}
        initchatConfig={{}}
        setChatConfigFn={vi.fn()}
        canChat
      />,
    );

    await waitFor(() => {
      expect(mocks.replaceMessageList).toHaveBeenCalledWith(
        "conversation-a",
        [{ conversation: "conversation-a" }],
      );
    });
    mocks.replaceMessageList.mockClear();

    rerender(
      <ChatLayout
        conversationId="conversation-b"
        setIsChatContent={vi.fn()}
        initchatConfig={{}}
        setChatConfigFn={vi.fn()}
        canChat
      />,
    );

    await waitFor(() => {
      expect(mocks.disconnectConversationStream)
        .toHaveBeenCalledWith("conversation-a");
      expect(mocks.replaceMessageList)
        .toHaveBeenCalledWith("conversation-b", []);
      expect(screen.getByTestId("chat-container"))
        .toHaveAttribute("data-session-id", "conversation-b");
    });

    await act(async () => {
      nextHistory.resolve({
        data: { history: [{ conversation: "conversation-b" }] },
      });
      await nextHistory.promise;
    });

    await waitFor(() => {
      expect(mocks.replaceMessageList).toHaveBeenLastCalledWith(
        "conversation-b",
        [{ conversation: "conversation-b" }],
      );
    });
  });

  it("invalidates the initial route request when the layout unmounts", async () => {
    const routeDetail = deferred<any>();
    mocks.getConversationDetail.mockReturnValue(routeDetail.promise);

    const { unmount } = render(
      <ChatLayout
        conversationId="conversation-a"
        setIsChatContent={vi.fn()}
        initchatConfig={{}}
        setChatConfigFn={vi.fn()}
        canChat
      />,
    );

    await waitFor(() => {
      expect(mocks.getConversationDetail).toHaveBeenCalledWith({
        conversation: "conversation-a",
      });
    });
    mocks.setThinkingDepth.mockClear();

    unmount();
    await act(async () => {
      routeDetail.resolve({
        data: {
          conversation: {
            conversation_id: "conversation-a",
            thinking_depth: "low",
            search_config: {},
            settings: { chat_executor: "lazymind" },
          },
        },
      });
      await routeDetail.promise;
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(mocks.setThinkingDepth).not.toHaveBeenCalled();
    expect(mocks.replaceMessageList).not.toHaveBeenCalled();
    expect(mocks.messageError).not.toHaveBeenCalled();
  });
});
