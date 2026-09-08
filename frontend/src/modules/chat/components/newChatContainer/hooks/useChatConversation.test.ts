import { act, renderHook, waitFor } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Modal } from "antd";
import {
  ChatConversationsRequestActionEnum,
  ChatConversationsResponseFinishReasonEnum,
} from "@/api/generated/chatbot-client";
import { RoleTypes } from "@/modules/chat/constants/common";
import { CHAT_WORKFLOW_STEP_FEEDBACK_EVENT } from "@/modules/chat/constants/chat";
import type { ChatInputImperativeProps } from "../../ChatInput";
import { useChatConversation } from "./useChatConversation";
import { useTaskCenterStore } from "@/modules/chat/store/taskCenter";
import { buildChatMessageListFromHistory } from "@/modules/chat/utils/message";
import { streamManager } from "@/modules/chat/utils/StreamManager";

const { listConversationsMock, waitForRuntimeCapabilityMock } = vi.hoisted(() => ({
  listConversationsMock: vi.fn(),
  waitForRuntimeCapabilityMock: vi.fn(),
}));

vi.mock("@/runtime/readiness", () => ({
  waitForRuntimeCapability: waitForRuntimeCapabilityMock,
}));

vi.mock("antd", () => ({
  Button: "button",
  message: {
    error: vi.fn(),
    warning: vi.fn(),
  },
  Modal: { confirm: vi.fn() },
}));

vi.mock("react-router-dom", () => ({
  useNavigate: () => vi.fn(),
}));

vi.mock("../../ImageUpload", () => ({
  allowedImageTypes: [".png", ".jpg", ".jpeg"],
}));

vi.mock("@/modules/chat/utils/request", () => ({
  ChatServiceApi: () => ({
    conversationServiceListConversations: listConversationsMock,
  }),
}));

vi.mock("@/modules/chat/utils/conversationActivity", () => ({
  emitConversationActivity: vi.fn(),
}));

vi.mock("./useChatScroll", () => ({
  useChatScroll: () => ({
    chatContentRef: { current: null },
    isMouseScrollingRef: { current: false },
    showScrollButton: false,
    inputHeight: 120,
    scrollToEnd: vi.fn(),
    scrollToEndImmediately: vi.fn(),
    handleScroll: vi.fn(),
    handleToBottom: vi.fn(),
    handleInputHeightChange: vi.fn(),
  }),
}));

function renderConversation(
  overrides: Partial<Parameters<typeof useChatConversation>[0]> = {},
) {
  const options: Parameters<typeof useChatConversation>[0] = {
    canChat: true,
    onOpenSSE: vi.fn(),
    setIsChatContent: vi.fn(),
    clearStorePendingMessage: vi.fn(),
    clearCiteMessages: vi.fn(),
    chatInputRef: createRef<ChatInputImperativeProps>(),
    thinkingCollapseMap: new Map(),
    getUserEdit: () => undefined,
    t: (key) => key,
    ...overrides,
  };
  return renderHook(() => useChatConversation(options));
}

function createMockStream() {
  const listeners = new Map<string, (event: any) => void>();
  const stream = {
    addEventListener: vi.fn((type: string, listener: (event: any) => void) => {
      listeners.set(type, listener);
    }),
    removeEventListener: vi.fn(),
    close: vi.fn(),
  };
  return { stream, listeners };
}

function createPreparedStream(clientConversationId: string) {
  const { stream, listeners } = createMockStream();
  const onOpenSSE = vi.fn(
    (
      _input: unknown,
      _action: unknown,
      _callbacks: unknown,
      extras?: Record<string, unknown>,
    ) => {
      const prepareClientConversationId = extras?.__prepareClientConversationId;
      if (typeof prepareClientConversationId === "function") {
        prepareClientConversationId(clientConversationId);
      }
      return stream;
    },
  );
  return { listeners, onOpenSSE };
}

describe("useChatConversation regeneration recovery", () => {
  beforeEach(() => {
    sessionStorage.clear();
    listConversationsMock.mockReset();
    listConversationsMock.mockResolvedValue({ data: { conversations: [] } });
    waitForRuntimeCapabilityMock.mockReset();
    waitForRuntimeCapabilityMock.mockResolvedValue(undefined);
    vi.mocked(Modal.confirm).mockClear();
    useTaskCenterStore.setState({
      activeConversationId: "",
      tasksByConversation: {},
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("uses freshly loaded history instead of a stale per-conversation cache", () => {
    const { result } = renderConversation();
    const first = [{ role: RoleTypes.ASSISTANT, delta: "cached answer" }];
    const second = [{ role: RoleTypes.ASSISTANT, delta: "server answer" }];

    act(() => {
      result.current.replaceMessageList("conversation-1", first);
      result.current.replaceMessageList("conversation-2", []);
      result.current.replaceMessageList("conversation-1", second);
    });

    expect(result.current.messageList).toEqual(second);
    expect(result.current.conversationMessagesCache.current.get("conversation-1"))
      .toEqual(second);
  });

  it("keeps a completed new turn when an older history page arrives", async () => {
    const initial = { id: "h2", seq: 2, query: "original question", result: "original answer" };
    const older = { id: "h1", seq: 1, query: "older question", result: "older answer" };
    const { stream, listeners } = createMockStream();
    const { result } = renderConversation({ onOpenSSE: vi.fn(() => stream) });
    act(() => result.current.replaceMessageList("conversation-1", buildChatMessageListFromHistory([initial])));
    await act(async () => {
      await result.current.sendMessage({ text: "new question" });
    });
    act(() => listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "conversation-1", history_id: "h3", seq: 3,
      delta: "new answer", finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
    } }) }));
    act(() => listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "conversation-1", history_id: "h3", seq: 3,
      finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
    } }) }));
    const liveTurn = result.current.messageList.slice(-2);

    act(() => result.current.mergeHistoryPage("conversation-1", [older]));

    expect(result.current.messageList.map((item) => item.delta)).toEqual([
      "older question", "older answer", "original question", "original answer", "new question", "new answer",
    ]);
    expect(result.current.messageList.slice(-2)).toEqual(liveTurn);
    expect(result.current.conversationMessagesCache.current.get("conversation-1")).toEqual(result.current.messageList);
    expect(streamManager.getStreamState("conversation-1")?.messageList).toEqual(result.current.messageList);
  });

  it("merges a pending history page without losing live deltas or resetting the active stream", async () => {
    const initial = { id: "h2", seq: 2, query: "original question", result: "original answer" };
    const older = { id: "h1", seq: 1, query: "older question", result: "older answer" };
    const { stream, listeners } = createMockStream();
    const { result } = renderConversation({ onOpenSSE: vi.fn(() => stream) });
    act(() => result.current.replaceMessageList("conversation-1", buildChatMessageListFromHistory([initial])));
    await act(async () => {
      await result.current.sendMessage({ text: "new question" });
    });
    let resolvePage!: () => void;
    const pendingPage = new Promise<void>((resolve) => { resolvePage = resolve; }).then(() => {
      result.current.mergeHistoryPage("conversation-1", [older]);
    });
    const emitDelta = (delta: string) => listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "conversation-1", history_id: "h3", seq: 3, delta, delta_mode: "append",
      finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
    } }) });
    act(() => emitDelta("first part"));
    await act(async () => {
      resolvePage();
      await pendingPage;
    });

    expect(result.current.messageList.map((item) => item.delta)).toEqual([
      "older question", "older answer", "original question", "original answer", "new question", "first part",
    ]);
    expect(result.current.loading).toBe(true);
    expect(result.current.isStreaming).toBe(true);
    expect(result.current.activeStreamRef.current).toBe(true);
    expect(stream.close).not.toHaveBeenCalled();
    act(() => emitDelta(" and second part"));
    expect(result.current.messageList.at(-1)?.delta).toBe("first part and second part");
    expect(result.current.messageList.at(-2)).toMatchObject({ role: RoleTypes.USER, history_id: "h3", delta: "new question" });
    expect(result.current.conversationMessagesCache.current.get("conversation-1")).toEqual(result.current.messageList);
  });

  it("deduplicates overlapping history by role and identity while preserving optimistic messages", async () => {
    const initial = { id: "h2", seq: 2, query: "same question", result: "current answer" };
    const newer = { id: "h3", seq: 3, query: "same question", result: "newer answer" };
    const { stream } = createMockStream();
    const { result } = renderConversation({ onOpenSSE: vi.fn(() => stream) });
    act(() => result.current.replaceMessageList("conversation-1", buildChatMessageListFromHistory([initial])));
    await act(async () => {
      await result.current.sendMessage({ text: "optimistic question" });
    });
    const optimistic = result.current.messageList.slice(-2);
    expect(optimistic.every((item) => !item.history_id)).toBe(true);
    const page = [newer, { ...initial, result: "stale answer" }];
    act(() => {
      result.current.mergeHistoryPage("conversation-1", page);
      result.current.mergeHistoryPage("conversation-1", page);
    });

    expect(result.current.messageList.map((item) => item.delta)).toEqual([
      "same question", "current answer", "same question", "newer answer", "optimistic question", "",
    ]);
    expect(result.current.messageList.slice(-2)).toEqual(optimistic);
    expect(result.current.isStreaming).toBe(true);
    expect(streamManager.getStreamState("conversation-1")?.messageList).toEqual(result.current.messageList);
  });

  it("keeps archived attempts between their user message and final reply when paging", () => {
    const initial = { id: "h2", seq: 2, query: "current question", result: "current answer" };
    const older = {
      id: "h1", seq: 1, query: "older question", result: "final answer",
      failed_attempts: [{ run_id: "run-1", result: "partial failed answer", run_status: "failed" }],
    };
    const { result } = renderConversation();
    act(() => {
      result.current.replaceMessageList("conversation-1", buildChatMessageListFromHistory([initial]));
      result.current.mergeHistoryPage("conversation-1", [older]);
      result.current.mergeHistoryPage("conversation-1", [older]);
    });

    expect(result.current.messageList.map((item) => item.delta)).toEqual([
      "older question", "partial failed answer", "final answer", "current question", "current answer",
    ]);
    expect(result.current.messageList[1]).toMatchObject({ archived_failure: true, history_id: "h1:failed:run-1" });
  });

  it("ignores a history page for a conversation that is no longer active", () => {
    const current = buildChatMessageListFromHistory([{ id: "current", query: "current question", result: "current answer" }]);
    const { result } = renderConversation();
    act(() => {
      result.current.replaceMessageList("conversation-2", current);
      result.current.mergeHistoryPage("conversation-1", [{ id: "previous", query: "previous question", result: "previous answer" }]);
    });
    expect(result.current.currentConversationIdRef.current).toBe("conversation-2");
    expect(result.current.messageList).toEqual(current);
    expect(result.current.conversationMessagesCache.current.has("conversation-1")).toBe(false);
  });

  it("appends every completed workflow step to its assistant chat message once", () => {
    const { result } = renderConversation();
    const messages = [
      { role: RoleTypes.USER, delta: "生成 PPT", history_id: "history-1" },
      {
        role: RoleTypes.ASSISTANT,
        delta: "PPT 工作流已启动。",
        raw_delta: "PPT 工作流已启动。",
        history_id: "history-1",
        finish_reason:
          ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
      },
    ];
    const detail = {
      conversationId: "conversation-1",
      feedbackId: "task-outline",
      historyId: "history-1",
      message: "步骤「生成大纲」已完成：已生成 10 页大纲。",
      status: "succeeded",
    };

    act(() => {
      result.current.replaceMessageList("conversation-1", messages);
      window.dispatchEvent(
        new CustomEvent(CHAT_WORKFLOW_STEP_FEEDBACK_EVENT, { detail }),
      );
      window.dispatchEvent(
        new CustomEvent(CHAT_WORKFLOW_STEP_FEEDBACK_EVENT, { detail }),
      );
    });

    expect(result.current.messageList[1]).toMatchObject({
      delta:
        "PPT 工作流已启动。\n\n步骤「生成大纲」已完成：已生成 10 页大纲。",
      workflow_step_feedback_ids: ["task-outline"],
    });
  });

  it("restores the previous answer and clears busy state when opening SSE rejects", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const onOpenSSE = vi.fn().mockRejectedValue(new Error("open failed"));
    const originalMessages = [
      {
        role: RoleTypes.USER,
        delta: "hello",
        inputs: [{ input_type: "text", text: "hello" }],
      },
      {
        role: RoleTypes.ASSISTANT,
        delta: "previous answer",
        history_id: "history-1",
        finish_reason:
          ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
      },
    ];
    const { result } = renderConversation({
      onOpenSSE,
    });

    await act(async () => {
      result.current.replaceMessageList("conversation-1", originalMessages);
      await result.current.regenerate();
    });

    await waitFor(() => {
      expect(onOpenSSE).toHaveBeenCalledWith(
        originalMessages[0].inputs,
        ChatConversationsRequestActionEnum.ChatActionRegeneration,
        {},
        expect.objectContaining({
          __prepareClientConversationId: expect.any(Function),
        }),
      );
      expect(result.current.loading).toBe(false);
      expect(result.current.isStreaming).toBe(false);
      expect(result.current.activeStreamRef.current).toBe(false);
      expect(result.current.messageList).toEqual(originalMessages);
      expect(result.current.messageListRef.current).toEqual(originalMessages);
    });

    await act(async () => {
      await result.current.regenerate();
    });

    await waitFor(() => expect(onOpenSSE).toHaveBeenCalledTimes(2));
  });

  it("shows a setup card when a persisted workflow task contains a capability failure", async () => {
    renderHook(() =>
      useChatConversation({
        canChat: true,
        onOpenSSE: vi.fn(),
        setIsChatContent: vi.fn(),
        clearStorePendingMessage: vi.fn(),
        clearCiteMessages: vi.fn(),
        chatInputRef: createRef<ChatInputImperativeProps>(),
        thinkingCollapseMap: new Map(),
        getUserEdit: () => undefined,
        t: (key) => key,
      }),
    );
    const failure =
      'MEDIA_CAPABILITY_DEPENDENCY_MISSING '
      + '{"status":"blocked","workflow":"CREATE_ANIMATED_MEME",'
      + '"required":["video_generator"],"missing":[{"id":"video_generator",'
      + '"label":"视频生成模型","available":false,"settings_url":"/settings?section=models",'
      + '"reason":"尚未配置视频生成模型。"}],"message":"缺少视频生成模型。"}';

    act(() => {
      useTaskCenterStore.setState({
        activeConversationId: "conversation-capability",
        tasksByConversation: {
          "conversation-capability": [{
            task_id: "task-capability",
            conversation_id: "conversation-capability",
            title: "image-workflow:analyze_subject",
            agent_type: "workflow_step",
            mode: "auto",
            status: "failed",
            progress_pct: 100,
            artifacts: [],
            sources: [],
            artifact_streams: [],
            execution_log: [{ type: "tool_results", content: "", tool_results: [{
              tool_call_id: "tool-1",
              name: "check_image_workflow_capabilities",
              result: failure,
            }] }],
          }],
        },
      });
    });

    await waitFor(() => expect(Modal.confirm).toHaveBeenCalledTimes(1));
    expect(vi.mocked(Modal.confirm).mock.calls[0]?.[0]?.title)
      .toBe("chat.mediaCapabilitiesRequiredTitle");
  });

  it("does not open parallel regeneration requests", async () => {
    let resolveOpen: ((stream: unknown) => void) | undefined;
    const onOpenSSE = vi.fn(
      () =>
        new Promise((resolve) => {
          resolveOpen = resolve;
        }),
    );
    const messages = [
      {
        role: RoleTypes.USER,
        delta: "hello",
        inputs: [{ input_type: "text", text: "hello" }],
      },
      {
        role: RoleTypes.ASSISTANT,
        delta: "previous answer",
        finish_reason:
          ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
      },
    ];
    const { result } = renderConversation({
      onOpenSSE,
    });

    act(() => {
      result.current.replaceMessageList("conversation-1", messages);
    });

    let firstRequest: Promise<void> | undefined;
    act(() => {
      firstRequest = result.current.regenerate();
      void result.current.regenerate();
    });

    await waitFor(() => expect(onOpenSSE).toHaveBeenCalledTimes(1));

    const { stream } = createMockStream();
    await act(async () => {
      resolveOpen?.(stream);
      await firstRequest;
    });

    expect(onOpenSSE).toHaveBeenCalledTimes(1);
  });

  it("does not reopen regeneration before synchronous stream state renders", async () => {
    const { stream } = createMockStream();
    const onOpenSSE = vi.fn(() => stream);
    const { result } = renderConversation({
      onOpenSSE,
    });

    act(() => {
      result.current.replaceMessageList("conversation-1", [
        {
          role: RoleTypes.USER,
          delta: "hello",
          inputs: [{ input_type: "text", text: "hello" }],
        },
        {
          role: RoleTypes.ASSISTANT,
          delta: "failed",
          run_status: "failed",
        },
      ]);
    });

    await act(async () => {
      await result.current.regenerate();
      await result.current.regenerate();
    });

    expect(onOpenSSE).toHaveBeenCalledTimes(1);
  });

  it("does not retry while a model PATCH is pending and retries after release", async () => {
    let modelSelectionSaving = true;
    const { stream } = createMockStream();
    const onOpenSSE = vi.fn(() => stream);
    const { result } = renderConversation({
      onOpenSSE,
      isModelSelectionSaving: () => modelSelectionSaving,
    });

    act(() => {
      result.current.replaceMessageList("conversation-1", [
        {
          role: RoleTypes.USER,
          delta: "retry me",
          inputs: [{ input_type: "text", text: "retry me" }],
        },
        {
          role: RoleTypes.ASSISTANT,
          delta: "",
          run_status: "failed",
          run_terminal: {
            status: "failed",
            reason: "model_failure",
            code: "rate_limited",
            partial_output: false,
          },
        },
      ]);
    });

    await act(async () => {
      await result.current.regenerate();
    });
    expect(onOpenSSE).not.toHaveBeenCalled();

    modelSelectionSaving = false;
    await act(async () => {
      await result.current.regenerate();
    });
    expect(onOpenSSE).toHaveBeenCalledTimes(1);
    expect(onOpenSSE).toHaveBeenCalledWith(
      [{ input_type: "text", text: "retry me" }],
      ChatConversationsRequestActionEnum.ChatActionRegeneration,
      {},
      expect.objectContaining({
        __prepareClientConversationId: expect.any(Function),
      }),
    );
  });

  it("keeps a first submitted attachment retryable when runtime readiness fails", async () => {
    waitForRuntimeCapabilityMock
      .mockRejectedValueOnce(new Error("runtime unavailable"))
      .mockResolvedValue(undefined);
    const { stream } = createMockStream();
    const onOpenSSE = vi.fn(() => stream);
    const clearFiles = vi.fn();
    const { result } = renderConversation({
      onOpenSSE,
    });

    await act(async () => {
      await result.current.sendMessage({
        text: "review the attachment",
        fileList: [
          {
            uid: "file-1",
            name: "brief.pdf",
            base64: "",
            suffix: ".pdf",
            size: "1 KB",
          },
        ],
        fileListRef: {
          current: { clear: clearFiles },
        } as any,
        files: [
          { uid: "file-1", name: "brief.pdf", uri: "/uploads/brief.pdf" },
        ] as any,
      });
    });

    expect(onOpenSSE).not.toHaveBeenCalled();
    expect(clearFiles).toHaveBeenCalledTimes(1);
    expect(result.current.messageList[0]).toMatchObject({
      role: RoleTypes.USER,
      delta: "review the attachment",
      fileList: [
        expect.objectContaining({
          uid: "file-1",
          name: "brief.pdf",
        }),
      ],
      files: [
        expect.objectContaining({
          uid: "file-1",
          name: "brief.pdf",
        }),
      ],
      inputs: expect.arrayContaining([
        expect.objectContaining({
          input_type: "file",
          uri: "/uploads/brief.pdf",
        }),
      ]),
    });
    expect(result.current.messageList[1]).toMatchObject({
      role: RoleTypes.ASSISTANT,
      run_status: "failed",
      run_terminal: {
        status: "failed",
        reason: "model_failure",
        code: "service_unavailable",
        partial_output: false,
      },
    });

    await act(async () => {
      await result.current.regenerate();
    });

    expect(onOpenSSE).toHaveBeenCalledWith(
      expect.arrayContaining([
        expect.objectContaining({
          input_type: "file",
          uri: "/uploads/brief.pdf",
        }),
      ]),
      ChatConversationsRequestActionEnum.ChatActionRegeneration,
      {},
      expect.objectContaining({
        __prepareClientConversationId: expect.any(Function),
      }),
    );
    expect(
      result.current.messageList.filter((item) => item.role === RoleTypes.USER),
    ).toHaveLength(1);
  });

  it("keeps a first submitted turn retryable when opening SSE rejects", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const clientConversationId = "11111111-1111-4111-8111-111111111111";
    const { stream } = createMockStream();
    let attempt = 0;
    const onOpenSSE = vi.fn(
      (
        _input: unknown,
        _action: unknown,
        _callbacks: unknown,
        extras?: Record<string, unknown>,
      ) => {
        const prepareClientConversationId =
          extras?.__prepareClientConversationId;
        if (typeof prepareClientConversationId === "function") {
          prepareClientConversationId(clientConversationId);
        }
        attempt += 1;
        return attempt === 1
          ? Promise.reject(new Error("open failed"))
          : stream;
      },
    );
    const { result } = renderConversation({
      onOpenSSE,
    });

    await act(async () => {
      await result.current.sendMessage({ text: "keep this turn" });
    });

    expect(result.current.currentConversationIdRef.current).toBe(
      clientConversationId,
    );
    expect(result.current.messageList[0]).toMatchObject({
      role: RoleTypes.USER,
      delta: "keep this turn",
      inputs: [{ input_type: "text", text: "keep this turn" }],
    });
    expect(result.current.messageList[1]).toMatchObject({
      role: RoleTypes.ASSISTANT,
      run_terminal: expect.objectContaining({
        status: "failed",
        code: "transport_error",
      }),
    });

    await act(async () => {
      await result.current.regenerate();
    });

    expect(onOpenSSE).toHaveBeenCalledTimes(2);
    expect(
      result.current.messageList.filter((item) => item.role === RoleTypes.USER),
    ).toHaveLength(1);
  });

  it("reports a submitted request while runtime startup is pending", async () => {
    let resolveRuntime: (() => void) | undefined;
    waitForRuntimeCapabilityMock.mockImplementationOnce(
      () => new Promise<void>((resolve) => { resolveRuntime = resolve; }),
    );
    const { stream } = createMockStream();
    const onRequestPendingChange = vi.fn();
    const { result } = renderConversation({
      onOpenSSE: vi.fn(() => stream),
      onRequestPendingChange,
    });

    let send: Promise<void> | undefined;
    act(() => {
      send = result.current.sendMessage({ text: "pending question" });
    });
    await waitFor(() =>
      expect(onRequestPendingChange).toHaveBeenCalledWith(true),
    );
    expect(result.current.messageList[0]).toMatchObject({
      role: RoleTypes.USER,
      delta: "pending question",
    });

    await act(async () => {
      resolveRuntime?.();
      await send;
    });
  });

  it("keeps the failed attempt visible while retrying the same user turn", async () => {
    const { stream } = createMockStream();
    const onOpenSSE = vi.fn(() => stream);
    const failedMessages = [
      {
        role: RoleTypes.USER,
        delta: "hello",
        inputs: [{ input_type: "text", text: "hello" }],
        history_id: "history-1",
      },
      {
        role: RoleTypes.ASSISTANT,
        delta: "partial",
        history_id: "history-1",
        run_id: "run-failed-1",
        run_status: "failed",
        run_terminal: {
          status: "failed",
          reason: "model_failure",
          code: "rate_limited",
          partial_output: true,
        },
      },
    ];
    const { result } = renderConversation({
      onOpenSSE,
    });

    await act(async () => {
      result.current.replaceMessageList("conversation-1", failedMessages);
      await result.current.regenerate();
    });

    expect(onOpenSSE).toHaveBeenCalledTimes(1);
    expect(result.current.messageList).toHaveLength(3);
    expect(result.current.messageList[1]).toMatchObject({
      delta: "partial",
      archived_failure: true,
      original_history_id: "history-1",
      run_status: "failed",
    });
    expect(result.current.messageList[2]).toMatchObject({
      role: RoleTypes.ASSISTANT,
      history_id: "history-1",
      delta: "",
    });
    expect(result.current.messageList[2].run_status).toBeUndefined();
  });

  it("confirms the prepared conversation after a mapped 503 so model switching can target it", async () => {
    const clientConversationId = "44444444-4444-4444-8444-444444444444";
    const { listeners, onOpenSSE } = createPreparedStream(clientConversationId);
    const onOpenResumeSSE = vi.fn();
    const onConversationIdChange = vi.fn();
    const { result } = renderConversation({
      onOpenSSE,
      onOpenResumeSSE,
      onConversationIdChange,
    });

    await act(async () => {
      await result.current.sendMessage({ text: "keep this question", clearInput: false });
    });
    act(() => {
      listeners.get("error")?.({
        type: "error",
        status: 503,
        data: JSON.stringify({
          code: 2001597,
          message: "provider-secret: model config unavailable",
        }),
      });
    });

    expect(onOpenResumeSSE).not.toHaveBeenCalled();
    expect(onConversationIdChange).toHaveBeenCalledOnce();
    expect(onConversationIdChange).toHaveBeenCalledWith(clientConversationId);
    expect(result.current.loading).toBe(false);
    expect(result.current.isStreaming).toBe(false);
    expect(result.current.streamRecovery.status).toBe("idle");
    expect(result.current.messageList[0]).toMatchObject({
      role: RoleTypes.USER,
      delta: "keep this question",
    });
    expect(result.current.messageList[1]).toMatchObject({
      role: RoleTypes.ASSISTANT,
      run_status: "failed",
      run_terminal: {
        status: "failed",
        reason: "model_failure",
        code: "not_found",
        partial_output: false,
      },
    });
    expect(JSON.stringify(result.current.messageList)).not.toContain(
      "provider-secret",
    );
  });

  it("confirms only the prepared client conversation id from the first SSE event", async () => {
    vi.useFakeTimers();
    listConversationsMock.mockResolvedValue({
      data: { conversations: [{ conversation_id: "old-conversation" }] },
    });
    const clientConversationId = "22222222-2222-4222-8222-222222222222";
    const { listeners, onOpenSSE } = createPreparedStream(clientConversationId);
    const onConversationIdChange = vi.fn();
    const { result, unmount } = renderConversation({
      onOpenSSE,
      onConversationIdChange,
    });

    await act(async () => {
      await result.current.sendMessage({ text: "keep this question" });
    });
    expect(result.current.currentConversationIdRef.current).toBe(
      clientConversationId,
    );
    act(() => {
      vi.advanceTimersByTime(400);
    });

    expect(listConversationsMock).not.toHaveBeenCalled();
    expect(onConversationIdChange).not.toHaveBeenCalled();

    act(() => {
      listeners.get("message")?.({
        type: "message",
        data: JSON.stringify({
          result: {
            conversation_id: clientConversationId,
            delta: "answer",
          },
        }),
      });
    });

    expect(onConversationIdChange).toHaveBeenCalledWith(clientConversationId);
    expect(result.current.currentConversationIdRef.current).toBe(
      clientConversationId,
    );
    unmount();
  });

  it("ignores a first SSE event with a different conversation id", async () => {
    const clientConversationId = "33333333-3333-4333-8333-333333333333";
    const { listeners, onOpenSSE } = createPreparedStream(clientConversationId);
    const onConversationIdChange = vi.fn();
    const { result } = renderConversation({
      onOpenSSE,
      onConversationIdChange,
    });

    await act(async () => {
      await result.current.sendMessage({ text: "keep this question" });
    });
    expect(result.current.currentConversationIdRef.current).toBe(
      clientConversationId,
    );
    act(() => {
      listeners.get("message")?.({
        type: "message",
        data: JSON.stringify({
          result: {
            conversation_id: "different-conversation",
            delta: "must not attach",
          },
        }),
      });
    });

    expect(onConversationIdChange).not.toHaveBeenCalled();
    expect(result.current.messageList[1]).toMatchObject({
      role: RoleTypes.ASSISTANT,
      delta: "",
    });
    expect(result.current.messageList[1].run_terminal).toBeUndefined();
    expect(JSON.stringify(result.current.messageList)).not.toContain(
      "must not attach",
    );
  });

  it("keeps status-zero failures on the existing stream recovery path", async () => {
    const clientConversationId = "55555555-5555-4555-8555-555555555555";
    const { listeners, onOpenSSE } = createPreparedStream(clientConversationId);
    const onConversationIdChange = vi.fn();
    const { result, unmount } = renderConversation({
      onOpenSSE,
      onOpenResumeSSE: vi.fn(),
      onConversationIdChange,
    });

    await act(async () => {
      await result.current.sendMessage({ text: "network test", clearInput: false });
    });
    act(() => {
      listeners.get("error")?.({
        type: "error",
        status: 0,
        data: JSON.stringify({ code: 2001597, message: "model config unavailable" }),
      });
    });

    expect(result.current.streamRecovery.status).toBe("resuming");
    expect(onConversationIdChange).not.toHaveBeenCalled();
    expect(result.current.messageList[1].run_terminal).toBeUndefined();
    unmount();
  });
});
