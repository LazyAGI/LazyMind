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
  setThinkingDepth: vi.fn(),
  messageError: vi.fn(),
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    i18n: { language: "zh-CN", resolvedLanguage: "zh-CN" },
    t: (key: string) => key,
  }),
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
    useImperativeHandle(ref, () => ({
      replaceMessageList: mocks.replaceMessageList,
      openResumeSSE: mocks.openResumeSSE,
      disconnectConversationStream: mocks.disconnectConversationStream,
      createNewChat: mocks.createNewChat,
    }));
    return <div data-testid="chat-container" data-session-id={props.sessionId} />;
  }),
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
  SSE: vi.fn(),
}));

vi.mock("@/modules/chat/utils/environment", () => ({
  buildEnvironmentContext: () => ({}),
}));

vi.mock("@/utils/developerMode", () => ({
  DEVELOPER_ACTIVE_EVENT: "developer-active",
  isDeveloperModeActive: () => false,
}));

vi.mock("@/modules/chat/store/chatMessage", () => ({
  useChatMessageStore: () => ({ pendingMessage: null, clearPendingMessage: vi.fn() }),
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
    syncSessionSearchConfig: vi.fn(),
  };
  return {
    buildWorkflowSearchConfig: () => ({}),
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
