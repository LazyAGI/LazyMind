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

const { waitForRuntimeCapabilityMock } = vi.hoisted(() => ({
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
  ChatServiceApi: () => ({}),
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
    waitForRuntimeCapabilityMock.mockReset();
    waitForRuntimeCapabilityMock.mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.restoreAllMocks();
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
        undefined,
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
});
