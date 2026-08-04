import { act, renderHook } from "@testing-library/react";
import { createRef, type ReactNode } from "react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatInputImperativeProps } from "../../ChatInput";
import { useChatConversation } from "./useChatConversation";

const mockListConversations = vi.fn();
const mockStopChatGeneration = vi.fn();

vi.mock("@/modules/chat/utils/request", () => ({
  ChatServiceApi: () => ({
    conversationServiceListConversations: (...args: unknown[]) =>
      mockListConversations(...args),
    conversationServiceStopChatGeneration: (...args: unknown[]) =>
      mockStopChatGeneration(...args),
    conversationServiceGetChatStatus: vi.fn().mockResolvedValue({ data: { is_generating: false } }),
    conversationServiceGetConversationHistory: vi.fn().mockResolvedValue({ data: { history: [] } }),
  }),
}));

function makeFakeSSE() {
  const listeners: Record<string, ((e: any) => void)[]> = {};
  return {
    addEventListener: (type: string, cb: (e: any) => void) => {
      listeners[type] = listeners[type] || [];
      listeners[type].push(cb);
    },
    removeEventListener: (type: string, cb: (e: any) => void) => {
      listeners[type] = (listeners[type] || []).filter((fn) => fn !== cb);
    },
    close: vi.fn(),
    readyState: 1,
    emit(type: string, detail: any) {
      (listeners[type] || []).forEach((cb) => cb(detail));
    },
  };
}

function setup(overrides: Partial<Parameters<typeof useChatConversation>[0]> = {}) {
  const chatInputRef = createRef<ChatInputImperativeProps>();
  const onOpenSSE = vi.fn();
  const options = {
    canChat: true,
    onOpenSSE,
    parseErrorData: (data: string) => data || "error",
    setIsChatContent: vi.fn(),
    clearStorePendingMessage: vi.fn(),
    clearCiteMessages: vi.fn(),
    chatInputRef,
    thinkingCollapseMap: new Map<string, boolean>(),
    getUserEdit: () => undefined,
    t: (key: string) => key,
    ...overrides,
  };
  const { result } = renderHook(() => useChatConversation(options as any), {
    wrapper: ({ children }: { children: ReactNode }) => (
      <MemoryRouter>{children}</MemoryRouter>
    ),
  });
  return { result, onOpenSSE };
}

describe("useChatConversation", () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["setTimeout"] });
    mockListConversations.mockReset().mockResolvedValue({ data: { conversations: [] } });
    mockStopChatGeneration.mockReset().mockResolvedValue({});
  });

  afterEach(async () => {
    await act(async () => {
      vi.runAllTimers();
      await Promise.resolve();
    });
    vi.useRealTimers();
  });

  it("starts empty and not streaming", () => {
    const { result } = setup();
    expect(result.current.messageList).toEqual([]);
    expect(result.current.isStreaming).toBe(false);
    expect(result.current.loading).toBe(false);
  });

  it("sendMessage appends a user + placeholder assistant message and opens the SSE stream", async () => {
    const sse = makeFakeSSE();
    const { result, onOpenSSE } = setup();
    onOpenSSE.mockReturnValue(sse);

    await act(async () => {
      await result.current.sendMessage({ text: "hello there" });
    });

    expect(result.current.messageList).toHaveLength(2);
    expect(result.current.messageList[0].delta).toBe("hello there");
    expect(result.current.isStreaming).toBe(true);
    expect(onOpenSSE).toHaveBeenCalled();
  });

  it("does nothing when the message text is blank", async () => {
    const { result, onOpenSSE } = setup();
    await act(async () => {
      await result.current.sendMessage({ text: "   " });
    });
    expect(result.current.messageList).toHaveLength(0);
    expect(onOpenSSE).not.toHaveBeenCalled();
  });

  it("shows a warning and skips sending when chat is disabled", async () => {
    const { result, onOpenSSE } = setup({ canChat: false, disabledReason: "chat.disabled" });
    await act(async () => {
      await result.current.sendMessage({ text: "hello" });
    });
    expect(result.current.messageList).toHaveLength(0);
    expect(onOpenSSE).not.toHaveBeenCalled();
  });

  it("applies incoming stream chunks to the last assistant message", async () => {
    const sse = makeFakeSSE();
    const { result, onOpenSSE } = setup();
    onOpenSSE.mockReturnValue(sse);

    await act(async () => {
      await result.current.sendMessage({ text: "hi" });
    });

    act(() => {
      sse.emit("message", {
        type: "message",
        data: JSON.stringify({
          result: { delta: "Hello", finish_reason: "FINISH_REASON_UNSPECIFIED" },
        }),
      });
    });

    const assistantMessage = result.current.messageList[result.current.messageList.length - 1];
    expect(assistantMessage.delta).toBe("Hello");
  });

  it("marks the stream as finished when finish_reason is FINISH_REASON_STOP", async () => {
    const sse = makeFakeSSE();
    const { result, onOpenSSE } = setup();
    onOpenSSE.mockReturnValue(sse);

    await act(async () => {
      await result.current.sendMessage({ text: "hi" });
    });

    act(() => {
      sse.emit("message", {
        type: "message",
        data: JSON.stringify({
          result: { delta: "done", finish_reason: "FINISH_REASON_STOP" },
        }),
      });
    });

    expect(result.current.isStreaming).toBe(false);
    expect(result.current.loading).toBe(false);
  });

  it("handles error events by recording an error message and stopping the stream", async () => {
    const sse = makeFakeSSE();
    const { result, onOpenSSE } = setup();
    onOpenSSE.mockReturnValue(sse);

    await act(async () => {
      await result.current.sendMessage({ text: "hi" });
    });

    act(() => {
      sse.emit("error", { type: "error", data: "network failure" });
    });

    const assistantMessage = result.current.messageList[result.current.messageList.length - 1];
    expect(assistantMessage.errMessage).toBe("network failure");
    expect(result.current.isStreaming).toBe(false);
  });

  it("createNewChat resets the conversation state", async () => {
    const sse = makeFakeSSE();
    const { result, onOpenSSE } = setup();
    onOpenSSE.mockReturnValue(sse);

    await act(async () => {
      await result.current.sendMessage({ text: "hi" });
    });
    expect(result.current.messageList.length).toBeGreaterThan(0);

    act(() => {
      result.current.createNewChat();
    });

    expect(result.current.messageList).toEqual([]);
    expect(result.current.currentConversationIdRef.current).toBe("");
  });

  it("stopGeneration calls the stop API and marks the assistant message as stopped", async () => {
    const sse = makeFakeSSE();
    const { result, onOpenSSE } = setup();
    onOpenSSE.mockReturnValue(sse);

    await act(async () => {
      await result.current.sendMessage({ text: "hi" });
    });
    act(() => {
      result.current.currentConversationIdRef.current = "conv-1";
    });

    act(() => {
      result.current.stopGeneration();
    });

    expect(sse.close).toHaveBeenCalled();
    expect(result.current.isStreaming).toBe(false);
    const assistantMessage = result.current.messageList[result.current.messageList.length - 1];
    expect(assistantMessage.finish_reason).toBe("FINISH_REASON_STOP");
  });

  it("regenerate does nothing when there is no prior user message", () => {
    const { result, onOpenSSE } = setup();
    act(() => {
      result.current.regenerate();
    });
    expect(onOpenSSE).not.toHaveBeenCalled();
  });

  it("replaceMessageList swaps in a new conversation id and message list", () => {
    const { result } = setup();
    const newList = [{ role: "User", delta: "restored" }];
    act(() => {
      result.current.replaceMessageList("conv-42", newList);
    });
    expect(result.current.currentConversationIdRef.current).toBe("conv-42");
    expect(result.current.messageList).toEqual(newList);
  });
});
