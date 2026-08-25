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
    window.sessionStorage.setItem("chat_resume_conversation_id", "conversation-a");
    vi.clearAllMocks();
    mocks.getChatStatus.mockResolvedValue({ data: { is_generating: false } });
    mocks.listConversations.mockResolvedValue({ data: { conversations: [] } });
    mocks.getConversationHistory.mockImplementation(({ name }: { name: string }) =>
      Promise.resolve({ data: { history: [{ conversation: name }] } }),
    );
  });

  it("does not let a late initial resume overwrite a newer sidebar selection", async () => {
    const resumeDetail = deferred<any>();
    mocks.getConversationDetail.mockImplementation(
      ({ conversation }: { conversation: string }) => {
        if (conversation === "conversation-a") {
          return resumeDetail.promise;
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

    render(
      <ChatLayout
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

    act(() => {
      window.dispatchEvent(new CustomEvent("lazymind:chat-select-conversation", {
        detail: { conversationId: "conversation-b", source: "sidebar" },
      }));
    });

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
      resumeDetail.resolve({
        data: {
          conversation: {
            conversation_id: "conversation-a",
            thinking_depth: "low",
            search_config: {},
            settings: { chat_executor: "lazymind" },
          },
        },
      });
      await resumeDetail.promise;
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

  it("invalidates the initial resume request when the layout unmounts", async () => {
    const resumeDetail = deferred<any>();
    mocks.getConversationDetail.mockReturnValue(resumeDetail.promise);

    const { unmount } = render(
      <ChatLayout
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
      resumeDetail.resolve({
        data: {
          conversation: {
            conversation_id: "conversation-a",
            thinking_depth: "low",
            search_config: {},
            settings: { chat_executor: "lazymind" },
          },
        },
      });
      await resumeDetail.promise;
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(mocks.setThinkingDepth).not.toHaveBeenCalled();
    expect(window.sessionStorage.getItem("chat_resume_conversation_id")).toBe(
      "conversation-a",
    );
    expect(mocks.messageError).not.toHaveBeenCalled();
  });
});
