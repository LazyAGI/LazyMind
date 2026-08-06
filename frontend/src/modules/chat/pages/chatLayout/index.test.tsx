import { describe, expect, it, vi, beforeEach } from "vitest";
import { forwardRef } from "react";
import { renderWithProviders, screen } from "@/test/testUtils";
import ChatLayout from "./index";

const mockGetConversationHistory = vi.fn();
const mockGetConversationDetail = vi.fn();
const mockGetChatStatus = vi.fn();
const mockListConversations = vi.fn();

vi.mock("@/modules/chat/utils/request", () => ({
  ChatServiceApi: () => ({
    conversationServiceGetConversationHistory: (...args: unknown[]) =>
      mockGetConversationHistory(...args),
    conversationServiceGetConversationDetail: (...args: unknown[]) =>
      mockGetConversationDetail(...args),
    conversationServiceGetChatStatus: (...args: unknown[]) => mockGetChatStatus(...args),
    conversationServiceListConversations: (...args: unknown[]) => mockListConversations(...args),
  }),
  parseConversationPluginSettings: () => undefined,
  CHAT_STREAM_URL: "/api/chat/stream",
  CHAT_RESUME_STREAM_URL: "/api/chat/resume",
}));

vi.mock("@/modules/chat/utils/sse", () => ({
  Method: { POST: "POST" },
  SSE: vi.fn().mockImplementation(() => ({ close: vi.fn() })),
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getAuthHeaders: () => ({}) },
}));

vi.mock("@/modules/chat/components/newChatContainer", () => ({
  default: forwardRef((props: any, ref: any) => (
    <div data-testid="chat-container-stub">{props.sessionId}</div>
  )),
}));

vi.mock("@/modules/chat/components/InitialCard", () => ({
  default: () => <div data-testid="initial-card-stub" />,
}));

vi.mock("@/modules/chat/components/TaskCenter", () => ({
  default: ({ sessionId }: { sessionId: string }) => (
    <div data-testid="task-center-stub">{sessionId}</div>
  ),
}));

vi.mock("@/modules/chat/store/pluginPanel", () => ({
  usePluginStore: Object.assign(
    (selector: (state: unknown) => unknown) =>
      selector({
        autoRunningByConversation: {},
        sessionByConversation: {},
      }),
    { getState: () => ({ sessionByConversation: {}, focusedTabByConversation: {}, focusedSortOrderByConversation: {} }) },
  ),
  draftStore: { flushAllDrafts: vi.fn().mockResolvedValue(undefined) },
  buildPluginSearchConfig: vi.fn(() => ({})),
}));

vi.mock("@/modules/chat/store/taskCenter", () => ({
  useTaskCenterStore: (selector: (state: unknown) => unknown) =>
    selector({
      tasksByConversation: {},
      loadConversationTasks: vi.fn().mockResolvedValue(undefined),
      loadConversationArtifacts: vi.fn().mockResolvedValue(undefined),
      subscribeConvEvents: vi.fn(),
      unsubscribeConvEvents: vi.fn(),
    }),
}));

vi.mock("@/modules/chat/store/chatMessage", () => ({
  useChatMessageStore: () => ({
    pendingMessage: null,
    clearPendingMessage: vi.fn(),
  }),
}));

vi.mock("@/modules/chat/store/chatInput", () => ({
  useChatInputStore: {
    getState: () => ({ getArtifactRefs: () => [], clearArtifactRefs: vi.fn() }),
  },
}));

vi.mock("@/modules/chat/store/chatThink", () => ({
  useChatThinkStore: { getState: () => ({ thinkingDepth: "medium" }) },
}));

vi.mock("@/utils/developerMode", () => ({
  isDeveloperModeActive: () => false,
}));

function baseProps(overrides: Record<string, unknown> = {}) {
  return {
    setIsChatContent: vi.fn(),
    initchatConfig: {},
    setChatConfigFn: vi.fn(),
    canChat: true,
    ...overrides,
  };
}

describe("ChatLayout", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetConversationHistory.mockResolvedValue({ data: { history: [] } });
    mockGetConversationDetail.mockResolvedValue({ data: { conversation: {} } });
    mockGetChatStatus.mockResolvedValue({ data: { is_generating: false } });
    mockListConversations.mockResolvedValue({ data: { conversations: [] } });
    sessionStorage.clear();
  });

  it("renders the chat container without a task panel when there are no tasks", () => {
    renderWithProviders(<ChatLayout {...baseProps()} />);
    expect(screen.getByTestId("chat-container-stub")).toBeInTheDocument();
    expect(screen.queryByTestId("task-center-stub")).not.toBeInTheDocument();
  });

  it("does not attempt conversation resume when there is no stored resume key", () => {
    renderWithProviders(<ChatLayout {...baseProps()} />);
    expect(mockGetConversationHistory).not.toHaveBeenCalled();
  });
});
