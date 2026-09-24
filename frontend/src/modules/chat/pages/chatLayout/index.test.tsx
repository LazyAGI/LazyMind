import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { forwardRef, useImperativeHandle } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ChatLayout from "./index";
import { readChatConversationFilters, selectChatConversationSources } from "../../constants/chat";

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
  patchConversationSettings: vi.fn(),
  getConversationHistory: vi.fn(),
  listConversations: vi.fn(),
  replaceMessageList: vi.fn(),
  mergeHistoryPage: vi.fn(),
  openResumeSSE: vi.fn(),
  disconnectConversationStream: vi.fn(),
  createNewChat: vi.fn(),
  sendMessage: vi.fn(),
  setThinkingDepth: vi.fn(),
  messageError: vi.fn(),
  messageInfo: vi.fn(),
  clearPendingMessage: vi.fn(),
  sseConstructor: vi.fn(),
  pendingMessage: null as any,
  latestChatContainerProps: null as any,
  latestSideChatPanelProps: null as any,
  latestArtifactPanelProps: null as any,
  locationSearch: "",
  artifactsByConversation: {} as Record<string, Array<{ artifact_id: string }>>,
  tasksByConversation: {} as Record<string, Array<{ task_id: string; agent_type?: string }>>,
  taskLoading: {} as Record<string, boolean>,
  workflowSessions: {} as Record<string, any>,
  dismissedWorkflowSessions: {} as Record<string, Array<{ session_id: string; workflow_id: string }>>,
  forkBegin: vi.fn(),
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    i18n: { language: "zh-CN", resolvedLanguage: "zh-CN" },
    t: (key: string, params?: { parent?: string }) => {
      if (key === "chat.conversationSourceFrom") {
        return `来源：${params?.parent}`;
      }
      if (key === "chat.conversationForkedFrom") {
        return `分支来源：${params?.parent}`;
      }
      return key;
    },
  }),
}));

vi.mock("@/modules/chat/components/ForkConversation/ForkStatus", () => ({ default: () => null }));
vi.mock("@/modules/chat/components/ForkConversation/useForkConversation", () => ({
  useForkConversation: () => ({ begin: mocks.forkBegin, pending: false }),
}));

vi.mock("react-router-dom", () => ({
  useLocation: () => ({ key: "test", pathname: "/chat", search: mocks.locationSearch }),
  useNavigate: () => vi.fn(),
  Link: ({ to, children, ...props }: any) => (
    <a href={to} {...props}>
      {children}
    </a>
  ),
}));

vi.mock("antd", () => ({
  Badge: ({ children }: any) => <span>{children}</span>,
  Button: ({ children, loading, ...props }: any) => <button {...props} disabled={loading || props.disabled}>{children}</button>,
  Dropdown: ({ children, menu }: any) => (
    <div>
      {children}
      {menu?.items?.map((item: any) => item ? (
        <button
          key={item.key}
          type="button"
          data-testid={`conversation-menu-${item.key}`}
          onClick={() => menu.onClick?.({ key: item.key })}
        >
          {item.label}
        </button>
      ) : null)}
    </div>
  ),
  Space: ({ children, ...props }: any) => <div {...props}>{children}</div>,
  message: {
    error: mocks.messageError,
    info: mocks.messageInfo,
    warning: vi.fn(),
  },
}));

vi.mock("@ant-design/icons", () => ({
  MessageOutlined: () => null,
  CloseOutlined: () => null,
  UnorderedListOutlined: () => null,
  FileTextOutlined: () => null,
  MoreOutlined: () => null,
}));

vi.mock("@/components/request", () => ({
  localizeErrorCode: (code: string) => code,
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getAuthHeaders: () => ({}), getUserInfo: () => ({ userId: "test-user" }) },
  AUTH_USER_CHANGE_EVENT: "lazymind:user-change",
}));

vi.mock("@/modules/chat/components/newChatContainer", () => ({
  default: forwardRef(function MockChatContainer(props: any, ref) {
    mocks.latestChatContainerProps = props;
    useImperativeHandle(ref, () => ({
      replaceMessageList: mocks.replaceMessageList,
      mergeHistoryPage: mocks.mergeHistoryPage,
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
    return props.open && props.visible !== false ? (
      <div>
        <div
          data-testid="side-chat-panel"
          data-visible={String(props.visible !== false)}
          data-parent-id={props.parentConversationId}
          data-selected-text={props.source?.selectedText || ""}
        />
        <button type="button" onClick={props.onClose}>Close side chat</button>
      </div>
    ) : null;
  },
}));

vi.mock("@/modules/chat/components/AssistantMessage", () => ({ ChatSourcePanel: () => <div>sources</div> }));

vi.mock("@/modules/chat/components/InitialCard", () => ({ default: () => null }));
vi.mock("@/modules/chat/components/TaskCenter", () => ({
  default: () => <div data-testid="task-center" />,
}));
vi.mock("@/modules/chat/components/ArtifactPanel", () => ({
  default: (props: { sessionId: string }) => {
    mocks.latestArtifactPanelProps = props;
    return <div data-testid="artifact-panel" data-session-id={props.sessionId} />;
  },
}));
vi.mock("@/modules/chat/components/TaskCenter/taskTimeline", () => ({
  taskCenterDisplayCount: (tasks: unknown[]) => (Array.isArray(tasks) ? tasks.length : 0),
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
  ConversationSettingsApi: () => ({ patchConversationSettings: mocks.patchConversationSettings }),
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
    sessionByConversation: mocks.workflowSessions,
    dismissedSessionsByConversation: mocks.dismissedWorkflowSessions,
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
    setViewMode: vi.fn(),
    tasksByConversation: mocks.tasksByConversation,
    artifactsByConversation: mocks.artifactsByConversation,
    _loadingTasks: mocks.taskLoading,
    _taskLoadErrors: {},
    refreshConversationExecution: vi.fn(),
    subscribeConvEvents: vi.fn(),
    unsubscribeConvEvents: vi.fn(),
  };
  return {
    useTaskCenterStore: Object.assign((selector: (value: typeof state) => unknown) => selector(state), { getState: () => state }),
  };
});

describe("ChatLayout conversation loading", () => {
  beforeEach(() => {
    window.sessionStorage.clear();
    window.localStorage.clear();
    vi.clearAllMocks();
    mocks.pendingMessage = null;
    mocks.latestChatContainerProps = null;
    mocks.latestSideChatPanelProps = null;
    mocks.locationSearch = "";
    Object.keys(mocks.artifactsByConversation).forEach((key) => {
      delete mocks.artifactsByConversation[key];
    });
    Object.keys(mocks.tasksByConversation).forEach((key) => {
      delete mocks.tasksByConversation[key];
    });
    Object.keys(mocks.taskLoading).forEach((key) => {
      delete mocks.taskLoading[key];
    });
    Object.keys(mocks.workflowSessions).forEach((key) => {
      delete mocks.workflowSessions[key];
    });
    Object.keys(mocks.dismissedWorkflowSessions).forEach((key) => {
      delete mocks.dismissedWorkflowSessions[key];
    });
    for (const id of ["source", "ordinary"]) {
      mocks.workflowSessions[id] = null;
      mocks.dismissedWorkflowSessions[id] = [];
      mocks.tasksByConversation[id] = [];
    }
    mocks.getChatStatus.mockResolvedValue({ data: { is_generating: false } });
    mocks.patchConversationSettings.mockReset().mockResolvedValue({ data: null });
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

  it.each(["codex", "workbuddy"])("aligns a directly opened %s task with the sidebar mode and source", async (assistant) => {
    selectChatConversationSources(["lazymind"]);
    mocks.getConversationDetail.mockResolvedValue({ data: { conversation: {
      conversation_id: "external-task", is_task_conv: true, assistant,
      search_config: {}, settings: {},
    } } });
    render(<ChatLayout conversationId="external-task" setIsChatContent={vi.fn()}
      initchatConfig={{}} setChatConfigFn={vi.fn()} canChat />);
    await waitFor(() => expect(readChatConversationFilters()).toEqual({
      filter: "task", sources: ["lazymind", assistant],
    }));
    expect(mocks.latestChatContainerProps.runInBackground).toBe(true);
  });

  it("merges only the arriving history page after locating the latest reply", async () => {
    const initial = { id: "h2", seq: 2, query: "initial question", result: "initial answer" };
    const older = { id: "h1", seq: 1, query: "older question", result: "older answer" };
    const page = deferred<any>();
    mocks.locationSearch = "?anchor_history_id=h2";
    mocks.getConversationDetail.mockResolvedValue({ data: { conversation: { conversation_id: "source", thinking_depth: "medium", settings: {} } } });
    mocks.getConversationHistory.mockImplementation(({ anchorPageToken }: { anchorPageToken?: string }) => anchorPageToken
      ? page.promise
      : Promise.resolve({ data: { history: [initial], older_page_token: "older-token", newer_page_token: "" } }));
    render(<ChatLayout conversationId="source" setIsChatContent={vi.fn()} initchatConfig={{}} setChatConfigFn={vi.fn()} canChat />);

    fireEvent.click(await screen.findByRole("button", { name: "chat.fork.older" }));
    expect(mocks.latestChatContainerProps.canChat).toBe(true);
    expect(mocks.getConversationHistory).toHaveBeenLastCalledWith({ name: "source", anchorPageToken: "older-token" });
    await act(async () => {
      page.resolve({ data: { history: [older], older_page_token: "", newer_page_token: "newer-token" } });
    });

    expect(mocks.mergeHistoryPage).toHaveBeenCalledWith("source", [older]);
    expect(mocks.replaceMessageList).toHaveBeenCalledTimes(1);
    expect(mocks.replaceMessageList).toHaveBeenCalledWith("source", [initial], true);
    expect(mocks.latestChatContainerProps.canChat).toBe(true);
  });

  it("loads settings and Fork capability with one detail request for a new conversation", async () => {
    mocks.getConversationDetail.mockResolvedValue({
      data: {
        conversation: {
          conversation_id: "new-conversation",
          thinking_depth: "high",
          settings: { chat_executor: "lazymind" },
          fork_capability: { supported: true },
        },
      },
    });
    render(
      <ChatLayout
        setIsChatContent={vi.fn()}
        initchatConfig={{}}
        setChatConfigFn={vi.fn()}
        canChat
      />,
    );

    await act(async () => {
      mocks.latestChatContainerProps.onConversationIdChange("new-conversation");
    });

    expect(mocks.getConversationDetail).toHaveBeenCalledTimes(1);
    expect(mocks.getConversationDetail).toHaveBeenCalledWith({
      conversation: "new-conversation",
    });
    expect(mocks.latestChatContainerProps.onFork).toEqual(expect.any(Function));
    expect(mocks.setThinkingDepth).toHaveBeenLastCalledWith("high");
  });

  it("ignores late Fork capability from the previous conversation detail request", async () => {
    const previousDetail = deferred<any>();
    mocks.getConversationDetail.mockImplementation(
      ({ conversation }: { conversation: string }) => conversation === "previous-conversation"
        ? previousDetail.promise
        : Promise.resolve({
          data: {
            conversation: {
              conversation_id: conversation,
              thinking_depth: "medium",
              settings: { chat_executor: "lazymind" },
              fork_capability: { supported: false },
            },
          },
        }),
    );
    render(
      <ChatLayout
        setIsChatContent={vi.fn()}
        initchatConfig={{}}
        setChatConfigFn={vi.fn()}
        canChat
      />,
    );

    act(() => {
      mocks.latestChatContainerProps.onConversationIdChange("previous-conversation");
    });
    await act(async () => {
      mocks.latestChatContainerProps.onConversationIdChange("current-conversation");
    });
    expect(mocks.latestChatContainerProps.onFork).toBeUndefined();

    await act(async () => {
      previousDetail.resolve({
        data: {
          conversation: {
            conversation_id: "previous-conversation",
            thinking_depth: "high",
            settings: { chat_executor: "lazymind" },
            fork_capability: { supported: true },
          },
        },
      });
    });

    expect(mocks.latestChatContainerProps.onFork).toBeUndefined();
    expect(mocks.setThinkingDepth).toHaveBeenLastCalledWith("medium");
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
    expect(secondPayload).not.toHaveProperty("basic_chat_only");
    mocks.latestChatContainerProps.onOpenResumeSSE(
      firstPayload.conversation_id, {}, { historyId: "history-1", afterSequence: 2 },
    );
    const resumeCall = mocks.sseConstructor.mock.calls[2];
    expect(JSON.parse(resumeCall[1].payload)).toEqual({
      conversation_id: firstPayload.conversation_id,
      history_id: "history-1",
      after_sequence: 2,
    });

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

  it("restores the same sidechat after visiting another conversation", async () => {
    mocks.getConversationDetail.mockImplementation(async ({ conversation }: { conversation: string }) => ({
      data: { conversation: { conversation_id: conversation, thinking_depth: "medium", search_config: {}, settings: { chat_executor: "lazymind" } } },
    }));
    const props = { setIsChatContent: vi.fn(), initchatConfig: {}, setChatConfigFn: vi.fn(), canChat: true };
    const view = render(<ChatLayout {...props} conversationId="parent" />);
    await waitFor(() => expect(mocks.latestChatContainerProps.onOpenSideChat).toBeTypeOf("function"));
    act(() => mocks.latestChatContainerProps.onOpenSideChat({ selectedText: "excerpt", historyId: "h1" }));
    expect(screen.getByTestId("side-chat-panel")).toHaveAttribute("data-selected-text", "excerpt");
    view.rerender(<ChatLayout {...props} conversationId="other" />);
    await waitFor(() => expect(screen.queryByTestId("side-chat-panel")).not.toBeInTheDocument());
    view.rerender(<ChatLayout {...props} conversationId="parent" />);
    await waitFor(() => expect(screen.getByTestId("side-chat-panel")).toHaveAttribute("data-selected-text", "excerpt"));
    expect(mocks.latestSideChatPanelProps.parentConversationId).toBe("parent");
  });

  it("keeps fork thinking depth local and clears it when starting a new conversation", async () => {
    mocks.getConversationDetail.mockResolvedValue({ data: { conversation: { conversation_id: "fork", thinking_depth: "high", search_config: {}, settings: { chat_executor: "lazymind" }, fork_origin: { source_conversation_id: "source", source_history_id: "h1", source_status: "available", can_locate: true } } } });
    const props = { setIsChatContent: vi.fn(), initchatConfig: {}, setChatConfigFn: vi.fn(), canChat: true };
    const { rerender } = render(<ChatLayout {...props} conversationId="fork" />);
    await waitFor(() => expect(mocks.latestChatContainerProps.thinkingDepth).toBe("high"));
    expect(mocks.setThinkingDepth).not.toHaveBeenCalled();
    await act(async () => mocks.latestChatContainerProps.onThinkingDepthChange("low"));
    expect(mocks.latestChatContainerProps.thinkingDepth).toBe("low");
    expect(mocks.setThinkingDepth).not.toHaveBeenCalled();
    rerender(<ChatLayout {...props} conversationId="" />);
    await waitFor(() => expect(mocks.latestChatContainerProps.thinkingDepth).toBeUndefined());
  });

  it("persists fork thinking depth and restores it on reopening", async () => {
    let storedDepth = "medium";
    mocks.getConversationDetail.mockImplementation(() => Promise.resolve({ data: { conversation: {
      conversation_id: "fork", thinking_depth: storedDepth, search_config: {}, settings: {},
      fork_origin: { source_conversation_id: "source", source_history_id: "h1", source_status: "available", can_locate: true },
    } } }));
    mocks.patchConversationSettings.mockImplementation(async (_id, settings) => {
      storedDepth = settings.thinking_depth;
      return { data: null };
    });
    const props = { conversationId: "fork", setIsChatContent: vi.fn(), initchatConfig: {}, setChatConfigFn: vi.fn(), canChat: true };
    const view = render(<ChatLayout {...props} />);
    await waitFor(() => expect(mocks.latestChatContainerProps.thinkingDepth).toBe("medium"));
    await act(async () => mocks.latestChatContainerProps.onThinkingDepthChange("high"));
    expect(mocks.patchConversationSettings).toHaveBeenCalledWith("fork", { thinking_depth: "high" }, expect.anything());
    expect(mocks.setThinkingDepth).not.toHaveBeenCalled();
    view.unmount();
    render(<ChatLayout {...props} />);
    await waitFor(() => expect(mocks.latestChatContainerProps.thinkingDepth).toBe("high"));
  });

  it("keeps the saved fork depth and reports a failed save", async () => {
    mocks.getConversationDetail.mockResolvedValue({ data: { conversation: {
      conversation_id: "fork", thinking_depth: "medium", search_config: {}, settings: {},
      fork_origin: { source_conversation_id: "source", source_history_id: "h1", source_status: "available", can_locate: true },
    } } });
    mocks.patchConversationSettings.mockRejectedValueOnce(new Error("save failed"));
    render(<ChatLayout conversationId="fork" setIsChatContent={vi.fn()} initchatConfig={{}} setChatConfigFn={vi.fn()} canChat />);
    await waitFor(() => expect(mocks.latestChatContainerProps.thinkingDepth).toBe("medium"));
    await act(async () => mocks.latestChatContainerProps.onThinkingDepthChange("high"));
    expect(mocks.latestChatContainerProps.thinkingDepth).toBe("medium");
    expect(mocks.messageError).toHaveBeenCalledWith("settingsPage.saveFailed");
    expect(mocks.latestChatContainerProps.canChat).toBe(true);
  });

  it("blocks duplicate depth saves and ignores a response after changing conversations", async () => {
    const saving = deferred<any>();
    const otherSaving = deferred<any>();
    mocks.patchConversationSettings.mockReturnValueOnce(saving.promise).mockReturnValueOnce(otherSaving.promise);
    mocks.getConversationDetail.mockImplementation(({ conversation }) => Promise.resolve({ data: { conversation: {
      conversation_id: conversation, thinking_depth: conversation === "fork" ? "medium" : "low", search_config: {}, settings: {},
      fork_origin: { source_conversation_id: "source", source_history_id: "h1", source_status: "available", can_locate: true },
    } } }));
    const props = { setIsChatContent: vi.fn(), initchatConfig: {}, setChatConfigFn: vi.fn(), canChat: true };
    const view = render(<ChatLayout {...props} conversationId="fork" />);
    await waitFor(() => expect(mocks.latestChatContainerProps.thinkingDepth).toBe("medium"));
    act(() => {
      void mocks.latestChatContainerProps.onThinkingDepthChange("high");
      void mocks.latestChatContainerProps.onThinkingDepthChange("max");
    });
    expect(mocks.patchConversationSettings).toHaveBeenCalledTimes(1);
    expect(mocks.latestChatContainerProps.canChat).toBe(false);
    expect(mocks.latestChatContainerProps.thinkingDepth).toBe("medium");
    view.rerender(<ChatLayout {...props} conversationId="other-fork" />);
    await waitFor(() => expect(mocks.latestChatContainerProps.thinkingDepth).toBe("low"));
    expect(mocks.latestChatContainerProps.canChat).toBe(true);
    act(() => { void mocks.latestChatContainerProps.onThinkingDepthChange("max"); });
    expect(mocks.patchConversationSettings).toHaveBeenCalledTimes(2);
    expect(mocks.patchConversationSettings).toHaveBeenLastCalledWith("other-fork", { thinking_depth: "max" }, expect.anything());
    expect(mocks.latestChatContainerProps.canChat).toBe(false);
    await act(async () => saving.resolve({ data: null }));
    expect(mocks.latestChatContainerProps.thinkingDepth).toBe("low");
    expect(mocks.latestChatContainerProps.canChat).toBe(false);
    act(() => { void mocks.latestChatContainerProps.onThinkingDepthChange("high"); });
    expect(mocks.patchConversationSettings).toHaveBeenCalledTimes(2);
    await act(async () => otherSaving.resolve({ data: null }));
    expect(mocks.latestChatContainerProps.thinkingDepth).toBe("max");
    expect(mocks.latestChatContainerProps.canChat).toBe(true);
  });

  it("reuses restored Fork capability and configuration without a second detail request", async () => {
    mocks.getConversationDetail.mockRejectedValue(new Error("second detail request failed")).mockResolvedValueOnce({
      data: {
        conversation: {
          conversation_id: "fork-conversation",
          thinking_depth: "medium",
          search_config: {},
          settings: { chat_executor: "lazymind" },
          parent_conversation_id: "parent-conversation",
          parent_display_name: "主会话标题",
          relation_type: "fork",
          fork_capability: { supported: true },
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

    expect(await screen.findByText("分支来源：主会话标题")).toBeInTheDocument();
    expect(mocks.latestChatContainerProps.showConversationConfig).toBe(true);
    expect(mocks.latestChatContainerProps.showSkillDeposit).toBe(true);
    expect(mocks.latestChatContainerProps.allowKnowledgeBaseSelection).toBe(true);
    expect(mocks.latestChatContainerProps.onFork).toEqual(expect.any(Function));
    expect(mocks.getConversationDetail).toHaveBeenCalledTimes(1);
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

  it("does not auto-open the artifact rail when files appear", async () => {
    mocks.artifactsByConversation.source = [{ artifact_id: "a1" }];
    mocks.getConversationDetail.mockResolvedValue({
      data: {
        conversation: {
          conversation_id: "source",
          thinking_depth: "medium",
          settings: {},
        },
      },
    });
    render(
      <ChatLayout
        conversationId="source"
        setIsChatContent={vi.fn()}
        initchatConfig={{}}
        setChatConfigFn={vi.fn()}
        canChat
      />,
    );

    await screen.findByTestId("chat-container");
    expect(screen.queryByTestId("artifact-panel")).not.toBeInTheDocument();
  });

  it("opens the artifact rail on demand even when there are no files", async () => {
    mocks.getConversationDetail.mockResolvedValue({
      data: {
        conversation: {
          conversation_id: "source",
          thinking_depth: "medium",
          settings: {},
        },
      },
    });
    render(
      <ChatLayout
        conversationId="source"
        setIsChatContent={vi.fn()}
        initchatConfig={{}}
        setChatConfigFn={vi.fn()}
        canChat
      />,
    );

    await screen.findByTestId("chat-container");
    await act(async () => {
      window.dispatchEvent(
        new CustomEvent("lazymind:chat-open-artifact-panel", {
          detail: { conversationId: "source" },
        }),
      );
    });

    expect(await screen.findByTestId("artifact-panel")).toHaveAttribute(
      "data-session-id",
      "source",
    );
  });

  it("offers the conversation files action from the top-right overflow menu", async () => {
    mocks.getConversationDetail.mockResolvedValue({
      data: { conversation: { conversation_id: "source", settings: {} } },
    });
    render(
      <ChatLayout
        conversationId="source"
        setIsChatContent={vi.fn()}
        initchatConfig={{}}
        setChatConfigFn={vi.fn()}
        canChat
      />,
    );

    expect(await screen.findByRole("button", { name: "chat.conversationMoreActions" })).toBeInTheDocument();
  });

  it("keeps only conversation and milestone tabs when the workflow is expanded", async () => {
    mocks.workflowSessions.source = { session_id: "wf-1", status: "active", steps: [] };
    mocks.getConversationDetail.mockResolvedValue({ data: { conversation: { conversation_id: "source" } } });
    const { container } = render(<ChatLayout conversationId="source" setIsChatContent={vi.fn()} initchatConfig={{}} setChatConfigFn={vi.fn()} canChat />);
    await screen.findByTestId("conversation-menu-conversation-files");

    act(() => window.dispatchEvent(new CustomEvent("lazymind:workflow-panel-expanded", {
      detail: { conversationId: "source", expanded: true },
    })));

    expect(screen.getAllByRole("tab")).toHaveLength(2);
    expect(screen.getByRole("tab", { name: "chat.workflowRailConversation" })).toHaveAttribute("aria-selected", "true");
    expect(screen.queryByRole("tab", { name: "chat.artifactPanelTitle" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId("conversation-menu-conversation-files"));
    expect(mocks.messageInfo).toHaveBeenCalledWith("chat.artifactPanelWorkflowHint");
    expect(container.querySelector(".detail-container--workflow-expanded")).not.toBeNull();
    fireEvent.click(screen.getByRole("tab", { name: "taskCenter.panelTitle" }));
    expect(screen.getByRole("tab", { name: "taskCenter.panelTitle" })).toHaveAttribute("aria-selected", "true");
  });

  it.each(["active", "completed", "dismissed", "step-task"])(
    "directs files to the workflow page for a conversation with a %s workflow",
    async (status) => {
      if (status === "dismissed") {
        mocks.dismissedWorkflowSessions.source = [{ session_id: "wf-1", workflow_id: "writer" }];
      } else if (status === "step-task") {
        mocks.tasksByConversation.source = [{ task_id: "step-1", agent_type: "workflow_step" }];
      } else {
        mocks.workflowSessions.source = { session_id: "wf-1", status, steps: [] };
      }
      mocks.artifactsByConversation.source = [{ artifact_id: "internal-material" }];
      mocks.getConversationDetail.mockResolvedValue({ data: { conversation: { conversation_id: "source" } } });
      render(<ChatLayout conversationId="source" setIsChatContent={vi.fn()} initchatConfig={{}} setChatConfigFn={vi.fn()} canChat />);

      fireEvent.click(await screen.findByTestId("conversation-menu-conversation-files"));

      expect(mocks.messageInfo).toHaveBeenCalledWith("chat.artifactPanelWorkflowHint");
      expect(screen.queryByTestId("artifact-panel")).not.toBeInTheDocument();
    },
  );

  it.each([false, true])("waits for workflow metadata on reload (dismissed: %s)", async (dismissed) => {
    delete mocks.workflowSessions.source;
    delete mocks.dismissedWorkflowSessions.source;
    mocks.artifactsByConversation.source = [{ artifact_id: "possibly-internal-file" }];
    mocks.getConversationDetail.mockResolvedValue({ data: { conversation: { conversation_id: "source" } } });
    const props = { conversationId: "source", setIsChatContent: vi.fn(), initchatConfig: {}, setChatConfigFn: vi.fn(), canChat: true };
    const { rerender } = render(<ChatLayout {...props} />);

    fireEvent.click(await screen.findByTestId("conversation-menu-conversation-files"));
    expect(screen.queryByTestId("artifact-panel")).not.toBeInTheDocument();

    mocks.workflowSessions.source = null;
    rerender(<ChatLayout {...props} />);
    expect(screen.queryByTestId("artifact-panel")).not.toBeInTheDocument();

    mocks.dismissedWorkflowSessions.source = dismissed ? [{ session_id: "wf-1", workflow_id: "writer" }] : [];
    rerender(<ChatLayout {...props} />);
    if (dismissed) {
      expect(screen.queryByTestId("artifact-panel")).not.toBeInTheDocument();
      expect(mocks.messageInfo).toHaveBeenCalledWith("chat.artifactPanelWorkflowHint");
    } else {
      expect(screen.getByTestId("artifact-panel")).toBeInTheDocument();
    }
  });

  it("applies the workflow restriction after a new task creates its conversation", async () => {
    render(<ChatLayout setIsChatContent={vi.fn()} initchatConfig={{}} setChatConfigFn={vi.fn()} canChat />);
    mocks.workflowSessions.source = { session_id: "wf-1", status: "active", steps: [] };
    act(() => mocks.latestChatContainerProps.onConversationIdChange("source"));
    fireEvent.click(await screen.findByTestId("conversation-menu-conversation-files"));
    expect(mocks.messageInfo).toHaveBeenCalledWith("chat.artifactPanelWorkflowHint");
    expect(screen.queryByTestId("artifact-panel")).not.toBeInTheDocument();
  });

  it("keeps the file panel mounted during background task refreshes", async () => {
    mocks.getConversationDetail.mockResolvedValue({ data: { conversation: { conversation_id: "source" } } });
    const props = { conversationId: "source", setIsChatContent: vi.fn(), initchatConfig: {}, setChatConfigFn: vi.fn(), canChat: true };
    const { rerender } = render(<ChatLayout {...props} />);
    fireEvent.click(await screen.findByTestId("conversation-menu-conversation-files"));
    const panel = screen.getByTestId("artifact-panel");
    mocks.taskLoading.source = true;
    rerender(<ChatLayout {...props} />);
    expect(screen.getByTestId("artifact-panel")).toBe(panel);
    mocks.taskLoading.source = false;
    rerender(<ChatLayout {...props} />);
    expect(screen.getByTestId("artifact-panel")).toBe(panel);
  });

  it("closes an open file panel when a workflow starts and keeps the restriction scoped to that conversation", async () => {
    mocks.getConversationDetail.mockImplementation(({ conversation }: { conversation: string }) =>
      Promise.resolve({ data: { conversation: { conversation_id: conversation } } }));
    const props = { setIsChatContent: vi.fn(), initchatConfig: {}, setChatConfigFn: vi.fn(), canChat: true };
    const { rerender } = render(<ChatLayout {...props} conversationId="source" />);
    fireEvent.click(await screen.findByTestId("conversation-menu-conversation-files"));
    expect(screen.getByTestId("artifact-panel")).toBeInTheDocument();
    expect(document.querySelector(".right-box")).not.toHaveStyle({ width: "700px" });

    mocks.workflowSessions.source = { session_id: "wf-1", status: "active", steps: [] };
    mocks.tasksByConversation.source = [{ task_id: "step-1", agent_type: "workflow_step" }];
    rerender(<ChatLayout {...props} conversationId="source" />);
    expect(screen.queryByTestId("artifact-panel")).not.toBeInTheDocument();
    expect(document.querySelector(".right-box")).not.toHaveStyle({ width: "700px" });

    rerender(<ChatLayout {...props} conversationId="ordinary" />);
    await waitFor(() => expect(screen.getByTestId("chat-container")).toHaveAttribute("data-session-id", "ordinary"));
    fireEvent.click(screen.getByTestId("conversation-menu-conversation-files"));
    expect(screen.getByTestId("artifact-panel")).toHaveAttribute("data-session-id", "ordinary");
  });

  it("replaces the task rail instead of stacking conversation files beside it", async () => {
    mocks.tasksByConversation.source = [{ task_id: "t1" }];
    mocks.getConversationDetail.mockResolvedValue({
      data: { conversation: { conversation_id: "source", settings: {} } },
    });
    render(
      <ChatLayout
        conversationId="source"
        setIsChatContent={vi.fn()}
        initchatConfig={{}}
        setChatConfigFn={vi.fn()}
        canChat
      />,
    );

    expect(await screen.findByTestId("task-center")).toBeInTheDocument();
    await act(async () => {
      window.dispatchEvent(
        new CustomEvent("lazymind:chat-open-artifact-panel", {
          detail: { conversationId: "source" },
        }),
      );
    });

    expect(await screen.findByTestId("artifact-panel")).toBeInTheDocument();
    expect(screen.queryByTestId("task-center")).not.toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "taskCenter.panelTitle" })).not.toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "chat.artifactPanelTitle" })).not.toBeInTheDocument();
  });

  it("hides the task rail when references open", async () => {
    mocks.tasksByConversation.source = [{ task_id: "t1" }];
    mocks.getConversationDetail.mockResolvedValue({
      data: { conversation: { conversation_id: "source", settings: {} } },
    });
    render(
      <ChatLayout
        conversationId="source"
        setIsChatContent={vi.fn()}
        initchatConfig={{}}
        setChatConfigFn={vi.fn()}
        canChat
      />,
    );

    expect(await screen.findByTestId("task-center")).toBeInTheDocument();
    await act(async () => {
      mocks.latestChatContainerProps.onOpenSources(
        [{ title: "doc", url: "https://example.com" }],
        "summary",
      );
    });

    expect(screen.getByRole("complementary", { name: "chat.contextPanel.title" })).toBeInTheDocument();
    expect(screen.queryByTestId("task-center")).not.toBeInTheDocument();
  });

  it("hides conversation files when references open", async () => {
    mocks.getConversationDetail.mockResolvedValue({
      data: { conversation: { conversation_id: "source", settings: {} } },
    });
    render(
      <ChatLayout
        conversationId="source"
        setIsChatContent={vi.fn()}
        initchatConfig={{}}
        setChatConfigFn={vi.fn()}
        canChat
      />,
    );

    await screen.findByTestId("chat-container");
    await act(async () => {
      window.dispatchEvent(
        new CustomEvent("lazymind:chat-open-artifact-panel", {
          detail: { conversationId: "source" },
        }),
      );
    });
    expect(await screen.findByTestId("artifact-panel")).toBeInTheDocument();

    await act(async () => {
      mocks.latestChatContainerProps.onOpenSources(
        [{ title: "doc", url: "https://example.com" }],
        "summary",
      );
    });

    expect(screen.queryByTestId("artifact-panel")).not.toBeInTheDocument();
    expect(screen.getByRole("complementary", { name: "chat.contextPanel.title" })).toBeInTheDocument();
  });

  it("replaces conversation files with side chat and closes the entire sidebar", async () => {
    mocks.getConversationDetail.mockResolvedValue({
      data: { conversation: { conversation_id: "source", settings: {} } },
    });
    render(
      <ChatLayout
        conversationId="source"
        setIsChatContent={vi.fn()}
        initchatConfig={{}}
        setChatConfigFn={vi.fn()}
        canChat
      />,
    );

    await screen.findByTestId("chat-container");
    await act(async () => {
      window.dispatchEvent(
        new CustomEvent("lazymind:chat-open-artifact-panel", {
          detail: { conversationId: "source" },
        }),
      );
    });
    expect(await screen.findByTestId("artifact-panel")).toBeInTheDocument();

    fireEvent.click(screen.getByTestId("conversation-menu-open-side-chat"));
    expect(screen.queryByTestId("artifact-panel")).not.toBeInTheDocument();
    expect(screen.getByTestId("side-chat-panel")).toHaveAttribute("data-visible", "true");

    fireEvent.click(screen.getByRole("button", { name: "Close side chat" }));
    expect(screen.queryByTestId("side-chat-panel")).not.toBeInTheDocument();
    expect(document.querySelector(".right-box")).toBeNull();
  });

  it("replaces side chat with conversation files instead of stacking both panels", async () => {
    mocks.getConversationDetail.mockResolvedValue({
      data: { conversation: { conversation_id: "source", settings: {} } },
    });
    render(
      <ChatLayout
        conversationId="source"
        setIsChatContent={vi.fn()}
        initchatConfig={{}}
        setChatConfigFn={vi.fn()}
        canChat
      />,
    );

    await screen.findByTestId("chat-container");
    fireEvent.click(screen.getByTestId("conversation-menu-open-side-chat"));
    expect(await screen.findByTestId("side-chat-panel")).toBeInTheDocument();

    await act(async () => {
      window.dispatchEvent(
        new CustomEvent("lazymind:chat-open-artifact-panel", {
          detail: { conversationId: "source" },
        }),
      );
    });

    expect(await screen.findByTestId("artifact-panel")).toBeInTheDocument();
    expect(screen.queryByTestId("side-chat-panel")).not.toBeInTheDocument();
  });

  it("does not offer a branch action from the conversation menu", async () => {
    mocks.getConversationDetail.mockResolvedValue({
      data: {
        conversation: {
          conversation_id: "source",
          settings: { chat_executor: "lazymind" },
          fork_capability: { supported: true },
        },
      },
    });
    render(
      <ChatLayout
        conversationId="source"
        setIsChatContent={vi.fn()}
        initchatConfig={{}}
        setChatConfigFn={vi.fn()}
        canChat
      />,
    );

    await waitFor(() => {
      expect(screen.getByTestId("conversation-menu-conversation-files")).toBeInTheDocument();
      expect(screen.getByTestId("conversation-menu-open-side-chat")).toBeInTheDocument();
      expect(screen.queryByTestId("conversation-menu-fork-from-latest")).not.toBeInTheDocument();
    });
    expect(mocks.latestChatContainerProps.onFork).toEqual(expect.any(Function));
  });
});
