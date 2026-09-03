import { act, renderHook, waitFor } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  ChatConversationsRequestActionEnum,
  ChatConversationsResponseFinishReasonEnum,
} from "@/api/generated/chatbot-client";
import { RoleTypes } from "@/modules/chat/constants/common";
import type { ChatInputImperativeProps } from "../../ChatInput";
import { useChatConversation } from "./useChatConversation";

const { listConversationsMock, waitForRuntimeCapabilityMock } = vi.hoisted(() => ({
  listConversationsMock: vi.fn(),
  waitForRuntimeCapabilityMock: vi.fn(),
}));

vi.mock("@/runtime/readiness", () => ({
  waitForRuntimeCapability: waitForRuntimeCapabilityMock,
}));

vi.mock("antd", () => ({
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

describe("useChatConversation regeneration recovery", () => {
  beforeEach(() => {
    sessionStorage.clear();
    listConversationsMock.mockReset();
    listConversationsMock.mockResolvedValue({ data: { conversations: [] } });
    waitForRuntimeCapabilityMock.mockReset();
    waitForRuntimeCapabilityMock.mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("uses freshly loaded history instead of a stale per-conversation cache", () => {
    const { result } = renderHook(() =>
      useChatConversation({
        canChat: true,
        setIsChatContent: vi.fn(),
        clearStorePendingMessage: vi.fn(),
        clearCiteMessages: vi.fn(),
        chatInputRef: createRef<ChatInputImperativeProps>(),
        thinkingCollapseMap: new Map(),
        getUserEdit: () => undefined,
        t: (key) => key,
      }),
    );
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
    const { result } = renderHook(() =>
      useChatConversation({
        canChat: true,
        onOpenSSE,
        setIsChatContent: vi.fn(),
        clearStorePendingMessage: vi.fn(),
        clearCiteMessages: vi.fn(),
        chatInputRef: createRef<ChatInputImperativeProps>(),
        thinkingCollapseMap: new Map(),
        getUserEdit: () => undefined,
        t: (key) => key,
      }),
    );

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
    const { result } = renderHook(() =>
      useChatConversation({
        canChat: true,
        onOpenSSE,
        setIsChatContent: vi.fn(),
        clearStorePendingMessage: vi.fn(),
        clearCiteMessages: vi.fn(),
        chatInputRef: createRef<ChatInputImperativeProps>(),
        thinkingCollapseMap: new Map(),
        getUserEdit: () => undefined,
        t: (key) => key,
      }),
    );

    act(() => {
      result.current.replaceMessageList("conversation-1", messages);
    });

    let firstRequest: Promise<void> | undefined;
    act(() => {
      firstRequest = result.current.regenerate();
      void result.current.regenerate();
    });

    await waitFor(() => expect(onOpenSSE).toHaveBeenCalledTimes(1));

    const stream = {
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      close: vi.fn(),
    };
    await act(async () => {
      resolveOpen?.(stream);
      await firstRequest;
    });

    expect(onOpenSSE).toHaveBeenCalledTimes(1);
  });

  it("does not reopen regeneration before synchronous stream state renders", async () => {
    const stream = {
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      close: vi.fn(),
    };
    const onOpenSSE = vi.fn(() => stream);
    const { result } = renderHook(() =>
      useChatConversation({
        canChat: true,
        onOpenSSE,
        setIsChatContent: vi.fn(),
        clearStorePendingMessage: vi.fn(),
        clearCiteMessages: vi.fn(),
        chatInputRef: createRef<ChatInputImperativeProps>(),
        thinkingCollapseMap: new Map(),
        getUserEdit: () => undefined,
        t: (key) => key,
      }),
    );

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
    const stream = {
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      close: vi.fn(),
    };
    const onOpenSSE = vi.fn(() => stream);
    const { result } = renderHook(() =>
      useChatConversation({
        canChat: true,
        onOpenSSE,
        setIsChatContent: vi.fn(),
        clearStorePendingMessage: vi.fn(),
        clearCiteMessages: vi.fn(),
        chatInputRef: createRef<ChatInputImperativeProps>(),
        thinkingCollapseMap: new Map(),
        getUserEdit: () => undefined,
        isModelSelectionSaving: () => modelSelectionSaving,
        t: (key) => key,
      }),
    );

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
    const stream = {
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      close: vi.fn(),
    };
    const onOpenSSE = vi.fn(() => stream);
    const clearFiles = vi.fn();
    const { result } = renderHook(() =>
      useChatConversation({
        canChat: true,
        onOpenSSE,
        setIsChatContent: vi.fn(),
        clearStorePendingMessage: vi.fn(),
        clearCiteMessages: vi.fn(),
        chatInputRef: createRef<ChatInputImperativeProps>(),
        thinkingCollapseMap: new Map(),
        getUserEdit: () => undefined,
        t: (key) => key,
      }),
    );

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
    const stream = {
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      close: vi.fn(),
    };
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
    const { result } = renderHook(() =>
      useChatConversation({
        canChat: true,
        onOpenSSE,
        setIsChatContent: vi.fn(),
        clearStorePendingMessage: vi.fn(),
        clearCiteMessages: vi.fn(),
        chatInputRef: createRef<ChatInputImperativeProps>(),
        thinkingCollapseMap: new Map(),
        getUserEdit: () => undefined,
        t: (key) => key,
      }),
    );

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
    const stream = {
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      close: vi.fn(),
    };
    const onRequestPendingChange = vi.fn();
    const { result } = renderHook(() =>
      useChatConversation({
        canChat: true,
        onOpenSSE: vi.fn(() => stream),
        onRequestPendingChange,
        setIsChatContent: vi.fn(),
        clearStorePendingMessage: vi.fn(),
        clearCiteMessages: vi.fn(),
        chatInputRef: createRef<ChatInputImperativeProps>(),
        thinkingCollapseMap: new Map(),
        getUserEdit: () => undefined,
        t: (key) => key,
      }),
    );

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
    const stream = {
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      close: vi.fn(),
    };
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
    const { result } = renderHook(() =>
      useChatConversation({
        canChat: true,
        onOpenSSE,
        setIsChatContent: vi.fn(),
        clearStorePendingMessage: vi.fn(),
        clearCiteMessages: vi.fn(),
        chatInputRef: createRef<ChatInputImperativeProps>(),
        thinkingCollapseMap: new Map(),
        getUserEdit: () => undefined,
        t: (key) => key,
      }),
    );

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
    const listeners = new Map<string, (event: any) => void>();
    const stream = {
      addEventListener: vi.fn((type: string, listener: (event: any) => void) => {
        listeners.set(type, listener);
      }),
      removeEventListener: vi.fn(),
      close: vi.fn(),
    };
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
        return stream;
      },
    );
    const onOpenResumeSSE = vi.fn();
    const onConversationIdChange = vi.fn();
    const { result } = renderHook(() =>
      useChatConversation({
        canChat: true,
        onOpenSSE,
        onOpenResumeSSE,
        onConversationIdChange,
        setIsChatContent: vi.fn(),
        clearStorePendingMessage: vi.fn(),
        clearCiteMessages: vi.fn(),
        chatInputRef: createRef<ChatInputImperativeProps>(),
        thinkingCollapseMap: new Map(),
        getUserEdit: () => undefined,
        t: (key) => key,
      }),
    );

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
    const listeners = new Map<string, (event: any) => void>();
    const stream = {
      addEventListener: vi.fn((type: string, listener: (event: any) => void) => {
        listeners.set(type, listener);
      }),
      removeEventListener: vi.fn(),
      close: vi.fn(),
    };
    const onConversationIdChange = vi.fn();
    const { result, unmount } = renderHook(() =>
      useChatConversation({
        canChat: true,
        onOpenSSE: vi.fn(
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
            return stream;
          },
        ),
        onConversationIdChange,
        setIsChatContent: vi.fn(),
        clearStorePendingMessage: vi.fn(),
        clearCiteMessages: vi.fn(),
        chatInputRef: createRef<ChatInputImperativeProps>(),
        thinkingCollapseMap: new Map(),
        getUserEdit: () => undefined,
        t: (key) => key,
      }),
    );

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
    const listeners = new Map<string, (event: any) => void>();
    const stream = {
      addEventListener: vi.fn((type: string, listener: (event: any) => void) => {
        listeners.set(type, listener);
      }),
      removeEventListener: vi.fn(),
      close: vi.fn(),
    };
    const onConversationIdChange = vi.fn();
    const { result } = renderHook(() =>
      useChatConversation({
        canChat: true,
        onOpenSSE: vi.fn(
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
            return stream;
          },
        ),
        onConversationIdChange,
        setIsChatContent: vi.fn(),
        clearStorePendingMessage: vi.fn(),
        clearCiteMessages: vi.fn(),
        chatInputRef: createRef<ChatInputImperativeProps>(),
        thinkingCollapseMap: new Map(),
        getUserEdit: () => undefined,
        t: (key) => key,
      }),
    );

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
    const listeners = new Map<string, (event: any) => void>();
    const stream = {
      addEventListener: vi.fn((type: string, listener: (event: any) => void) => {
        listeners.set(type, listener);
      }),
      removeEventListener: vi.fn(),
      close: vi.fn(),
    };
    const onConversationIdChange = vi.fn();
    const { result, unmount } = renderHook(() =>
      useChatConversation({
        canChat: true,
        onOpenSSE: vi.fn(
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
            return stream;
          },
        ),
        onOpenResumeSSE: vi.fn(),
        onConversationIdChange,
        setIsChatContent: vi.fn(),
        clearStorePendingMessage: vi.fn(),
        clearCiteMessages: vi.fn(),
        chatInputRef: createRef<ChatInputImperativeProps>(),
        thinkingCollapseMap: new Map(),
        getUserEdit: () => undefined,
        t: (key) => key,
      }),
    );

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
