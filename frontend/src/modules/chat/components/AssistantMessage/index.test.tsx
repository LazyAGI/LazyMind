import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import AssistantMessage from "./index";
import { ChatConversationsResponseFinishReasonEnum, FeedBackChatHistoryRequestTypeEnum } from "@/api/generated/chatbot-client";

const mockFeedBackChatHistory = vi.fn();
const mockSetChatHistory = vi.fn();
const mockSaveAskAnswers = vi.fn();
const mockDecideToolLimit = vi.fn();
const mockGetUserInfo = vi.fn();
const mockUsePluginStore = vi.fn();

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: () => mockGetUserInfo() },
}));

vi.mock("@/modules/chat/utils/request", () => ({
  ChatServiceApi: () => ({
    conversationServiceFeedBackChatHistory: mockFeedBackChatHistory,
    conversationServiceSetChatHistory: mockSetChatHistory,
    conversationServiceSaveAskAnswers: mockSaveAskAnswers,
  }),
  decideToolLimit: (...args: unknown[]) => mockDecideToolLimit(...args),
}));

vi.mock("@/modules/chat/store/pluginPanel", () => ({
  usePluginStore: (selector: (s: unknown) => unknown) => mockUsePluginStore(selector),
}));

vi.mock("@/modules/chat/components/PluginPanel", () => ({
  PluginPanel: ({ conversationId }: { conversationId: string }) => (
    <div data-testid="plugin-panel">{conversationId}</div>
  ),
}));

vi.mock("../MultiAnswerDisplay", () => ({
  default: ({ answers }: { answers: unknown[] }) => (
    <div data-testid="multi-answer-display">{answers.length}</div>
  ),
}));

vi.mock("../FeedbackModal", () => ({
  default: ({ visible, onSubmit }: { visible: boolean; onSubmit: (r: string[], c: string) => void }) =>
    visible ? (
      <button data-testid="feedback-submit" onClick={() => onSubmit(["reason"], "comment")}>
        submit-feedback
      </button>
    ) : null,
}));

vi.mock("@/modules/chat/components/AskCard", () => ({
  default: ({ onSubmit }: { onSubmit: (payload: { text: string; structured?: unknown }) => void }) => (
    <button data-testid="ask-card" onClick={() => onSubmit({ text: "ask answer" })}>
      ask-card
    </button>
  ),
}));

vi.mock("@/modules/chat/components/ToolLimitCard", () => ({
  default: ({ onDecision }: { onDecision: (action: string) => void }) => (
    <button data-testid="tool-limit-card" onClick={() => onDecision("continue")}>
      tool-limit-card
    </button>
  ),
}));

vi.mock("@/modules/chat/components/ArtifactCollectorCard/ArtifactDownloadButton", () => ({
  default: () => <div data-testid="artifact-download-button" />,
}));

const FinishReasonStop = ChatConversationsResponseFinishReasonEnum.FinishReasonStop;
const FinishReasonUnspecified = ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified;
const FinishReasonUnknown = ChatConversationsResponseFinishReasonEnum.FinishReasonUnknown;

function baseItem(overrides: Record<string, unknown> = {}) {
  return {
    id: "msg-1",
    history_id: "h1",
    delta: "Hello world",
    finish_reason: FinishReasonStop,
    sources: [],
    ...overrides,
  };
}

function renderMessage(props: Record<string, unknown> = {}) {
  const defaultProps = {
    item: baseItem(),
    index: 0,
    length: 1,
    sendMessage: vi.fn(),
    regenerate: vi.fn(),
    stopGeneration: vi.fn(),
    renderText: (item: { delta?: string }) => <div data-testid="rendered-text">{item.delta}</div>,
    updateMessage: vi.fn(),
    sessionId: "session-1",
  };
  return render(<AssistantMessage {...defaultProps} {...props} />);
}

describe("AssistantMessage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetUserInfo.mockReturnValue({ chatUnlikeSwitch: false });
    mockUsePluginStore.mockImplementation((selector: (s: unknown) => unknown) =>
      selector({ sessionByConversation: {}, loadActiveSession: vi.fn() }),
    );
    mockFeedBackChatHistory.mockResolvedValue({});
    mockSetChatHistory.mockResolvedValue({});
  });

  it("renders the assistant's text content when finished", () => {
    renderMessage();
    expect(screen.getByTestId("rendered-text")).toHaveTextContent("Hello world");
  });

  it("shows a loading indicator while still streaming with no content", () => {
    renderMessage({
      item: baseItem({ delta: "", finish_reason: FinishReasonUnspecified }),
    });
    expect(screen.getByText("chat.generatingAnswer")).toBeInTheDocument();
  });

  it("shows the stop-generation button while streaming", () => {
    const stopGeneration = vi.fn();
    renderMessage({
      item: baseItem({ delta: "partial", finish_reason: FinishReasonUnspecified }),
      stopGeneration,
    });
    fireEvent.click(screen.getByText("chat.stopGenerate"));
    expect(stopGeneration).toHaveBeenCalled();
  });

  it("shows an error and lets the user regenerate on unknown finish reason", () => {
    const regenerate = vi.fn();
    renderMessage({
      item: baseItem({ delta: "", finish_reason: FinishReasonUnknown, errMessage: "boom" }),
      regenerate,
    });
    expect(screen.getByText("boom")).toBeInTheDocument();
    fireEvent.click(screen.getByText("chat.regenerate"));
    expect(regenerate).toHaveBeenCalled();
  });

  it("submits a like feedback and calls updateMessage with the updated item", async () => {
    const updateMessage = vi.fn();
    renderMessage({ updateMessage });

    const likeIcon = document.querySelector(".anticon-like") as Element;
    fireEvent.click(likeIcon);

    await waitFor(() =>
      expect(mockFeedBackChatHistory).toHaveBeenCalledWith(
        expect.objectContaining({
          feedBackChatHistoryRequest: expect.objectContaining({
            history_id: "h1",
            type: FeedBackChatHistoryRequestTypeEnum.FeedBackTypeLike,
          }),
        }),
      ),
    );
    await waitFor(() => expect(updateMessage).toHaveBeenCalled());
  });

  it("opens the feedback modal on dislike when chatUnlikeSwitch is enabled", async () => {
    mockGetUserInfo.mockReturnValue({ chatUnlikeSwitch: true });
    renderMessage();

    const dislikeIcon = document.querySelector(".anticon-dislike") as Element;
    fireEvent.click(dislikeIcon);

    expect(await screen.findByTestId("feedback-submit")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("feedback-submit"));

    await waitFor(() =>
      expect(mockFeedBackChatHistory).toHaveBeenCalledWith(
        expect.objectContaining({
          feedBackChatHistoryRequest: expect.objectContaining({
            reason: "reason",
            expected_answer: "comment",
          }),
        }),
      ),
    );
  });

  it("dislikes immediately (no modal) when chatUnlikeSwitch is disabled", async () => {
    mockGetUserInfo.mockReturnValue({ chatUnlikeSwitch: false });
    renderMessage();

    const dislikeIcon = document.querySelector(".anticon-dislike") as Element;
    fireEvent.click(dislikeIcon);

    await waitFor(() =>
      expect(mockFeedBackChatHistory).toHaveBeenCalledWith(
        expect.objectContaining({
          feedBackChatHistoryRequest: expect.objectContaining({
            type: FeedBackChatHistoryRequestTypeEnum.FeedBackTypeUnlike,
          }),
        }),
      ),
    );
    expect(screen.queryByTestId("feedback-submit")).not.toBeInTheDocument();
  });

  it("renders knowledge base sources and opens the source link on click", () => {
    const openSpy = vi.spyOn(window, "open").mockImplementation(() => null);
    renderMessage({
      item: baseItem({
        sources: [
          {
            dataset_id: "ds1",
            document_id: "doc1",
            file_name: "report.pdf",
            index: "1",
          },
        ],
      }),
    });
    fireEvent.click(screen.getByText("report.pdf"));
    expect(openSpy).toHaveBeenCalledWith(expect.stringContaining("ds1/doc1"), "_blank");
    openSpy.mockRestore();
  });

  it("renders MultiAnswerDisplay when the item has multiple unselected answers", () => {
    renderMessage({
      item: baseItem({
        answers: [
          { history_id: "a1", content: "answer one" },
          { history_id: "a2", content: "answer two" },
        ],
        selected_answer_index: undefined,
      }),
    });
    expect(screen.getByTestId("multi-answer-display")).toHaveTextContent("2");
  });

  it("renders an ask card and forwards the submitted payload to sendMessage", () => {
    const sendMessage = vi.fn();
    const updateMessage = vi.fn();
    renderMessage({
      item: baseItem({ ask_pending: { ask_id: "ask-1" } }),
      sendMessage,
      updateMessage,
    });
    fireEvent.click(screen.getByTestId("ask-card"));
    expect(updateMessage).toHaveBeenCalledWith(
      expect.objectContaining({ ask_answered: true }),
    );
    expect(sendMessage).toHaveBeenCalledWith("ask answer", undefined, expect.anything());
  });

  it("renders a tool limit card and resolves it via decideToolLimit + updateMessage", async () => {
    mockDecideToolLimit.mockResolvedValue({});
    const updateMessage = vi.fn();
    renderMessage({
      item: baseItem({
        finish_reason: FinishReasonUnspecified,
        tool_limit_pending: { decision_id: "d1", timeout_seconds: 10 },
      }),
      updateMessage,
    });
    fireEvent.click(screen.getByTestId("tool-limit-card"));
    await waitFor(() => expect(mockDecideToolLimit).toHaveBeenCalledWith("session-1", "d1", "continue"));
    await waitFor(() =>
      expect(updateMessage).toHaveBeenCalledWith(
        expect.objectContaining({ resolved_tool_limit_decision_id: "d1" }),
      ),
    );
  });

  it("renders the PluginPanel when this is the last message and a plugin session exists", () => {
    mockUsePluginStore.mockImplementation((selector: (s: unknown) => unknown) =>
      selector({
        sessionByConversation: { "session-1": { session_id: "s1", status: "active" } },
        loadActiveSession: vi.fn(),
      }),
    );
    renderMessage({ index: 0, length: 1 });
    expect(screen.getByTestId("plugin-panel")).toHaveTextContent("session-1");
  });
});
