import { act, renderHook } from "@testing-library/react";
import { createRef, type MutableRefObject } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { RoleTypes } from "@/modules/chat/constants/common";
import { ChatConversationsRequestActionEnum } from "@/api/generated/chatbot-client";
import { useUserMessageEdit } from "./useUserMessageEdit";

const { warningMock, successMock, errorMock } = vi.hoisted(() => ({
  warningMock: vi.fn(),
  successMock: vi.fn(),
  errorMock: vi.fn(),
}));

vi.mock("antd", () => ({
  message: { warning: warningMock, success: successMock, error: errorMock },
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

const { saveMessageListMock } = vi.hoisted(() => ({
  saveMessageListMock: vi.fn(),
}));
vi.mock("@/modules/chat/utils/StreamManager", () => ({
  streamManager: { saveMessageList: saveMessageListMock },
}));

const { emitConversationActivityMock } = vi.hoisted(() => ({
  emitConversationActivityMock: vi.fn(),
}));
vi.mock("@/modules/chat/utils/conversationActivity", () => ({
  emitConversationActivity: emitConversationActivityMock,
}));

function makeRef<T>(value: T): MutableRefObject<T> {
  return { current: value };
}

function setup(overrides: Partial<Parameters<typeof useUserMessageEdit>[0]> = {}) {
  const openSSE = vi.fn();
  const scrollToEnd = vi.fn();
  const setMessageList = vi.fn();
  const messageList = overrides.messageList ?? [
    { role: RoleTypes.USER, delta: "hello", inputs: [{ input_type: "text", text: "hello" }] },
    { role: RoleTypes.ASSISTANT, delta: "hi there" },
  ];
  const messageListRef = overrides.messageListRef ?? makeRef(messageList);
  const currentConversationIdRef =
    overrides.currentConversationIdRef ?? makeRef("conv-1");
  const conversationMessagesCache =
    overrides.conversationMessagesCache ?? makeRef(new Map<string, any[]>());
  const activeStreamRef = overrides.activeStreamRef ?? makeRef(false);

  const { result, rerender } = renderHook(
    (props: any) =>
      useUserMessageEdit({
        canChat: true,
        loading: false,
        activeStreamRef,
        messageList,
        messageListRef,
        setMessageList,
        currentConversationIdRef,
        conversationMessagesCache,
        openSSE,
        scrollToEnd,
        ...props,
      }),
    { initialProps: overrides },
  );

  return {
    result,
    rerender,
    openSSE,
    scrollToEnd,
    setMessageList,
    messageListRef,
    currentConversationIdRef,
    conversationMessagesCache,
    activeStreamRef,
  };
}

describe("useUserMessageEdit", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
  });

  it("handleStartEditUserMessage populates edit state from the message when there is no draft", () => {
    const { result } = setup();

    act(() => {
      result.current.handleStartEditUserMessage(
        { delta: "hello", cite_message: "" },
        0,
      );
    });

    expect(result.current.editingUserMessageIndex).toBe(0);
    expect(result.current.editingUserMessageText).toBe("hello");
    expect(result.current.editingUserMessageCites).toEqual([]);
  });

  it("handleStartEditUserMessage restores a persisted draft instead of the item text", () => {
    const { result, currentConversationIdRef } = setup();
    localStorage.setItem(
      "userMsgEditDraft:conv-1",
      JSON.stringify({ text: "draft text", cites: ["c1"] }),
    );

    act(() => {
      result.current.handleStartEditUserMessage({ delta: "hello" }, 0);
    });

    expect(currentConversationIdRef.current).toBe("conv-1");
    expect(result.current.editingUserMessageText).toBe("draft text");
    expect(result.current.editingUserMessageCites).toEqual(["c1"]);
  });

  it("handleStartEditUserMessage warns and does nothing when chat is disabled", () => {
    const { result } = setup({ canChat: false, disabledReason: "disabled" } as never);

    act(() => {
      result.current.handleStartEditUserMessage({ delta: "hello" }, 0);
    });

    expect(warningMock).toHaveBeenCalledWith("disabled");
    expect(result.current.editingUserMessageIndex).toBeNull();
  });

  it("handleStartEditUserMessage does nothing while a stream is active", () => {
    const activeStreamRef = makeRef(true);
    const { result } = setup({ activeStreamRef } as never);

    act(() => {
      result.current.handleStartEditUserMessage({ delta: "hello" }, 0);
    });

    expect(result.current.editingUserMessageIndex).toBeNull();
  });

  it("handleCancelEditUserMessage clears the draft and resets edit state", () => {
    const { result } = setup();
    act(() => {
      result.current.handleStartEditUserMessage({ delta: "hello" }, 0);
    });
    localStorage.setItem("userMsgEditDraft:conv-1", JSON.stringify({ text: "x", cites: [] }));

    act(() => {
      result.current.handleCancelEditUserMessage();
    });

    expect(result.current.editingUserMessageIndex).toBeNull();
    expect(result.current.editingUserMessageText).toBe("");
    expect(localStorage.getItem("userMsgEditDraft:conv-1")).toBeNull();
  });

  it("handleRemoveEditingUserMessageCite removes the cite at the given index", () => {
    const { result } = setup();
    act(() => {
      result.current.handleStartEditUserMessage(
        { delta: "hello", cite_message: "first\n\nsecond" },
        0,
      );
    });
    expect(result.current.editingUserMessageCites).toEqual(["first", "second"]);

    act(() => {
      result.current.handleRemoveEditingUserMessageCite(0);
    });

    expect(result.current.editingUserMessageCites).toEqual(["second"]);
  });

  it("handleResendEditedUserMessage truncates the list, saves it and opens a regeneration SSE call", () => {
    const { result, openSSE, scrollToEnd, setMessageList, conversationMessagesCache } = setup();

    act(() => {
      result.current.handleResendEditedUserMessage(0, "  edited text  ");
    });

    expect(setMessageList).toHaveBeenCalledTimes(1);
    const newList = setMessageList.mock.calls[0][0];
    expect(newList).toHaveLength(2);
    expect(newList[0].delta).toBe("edited text");
    expect(newList[0].inputs[0]).toEqual({ input_type: "text", text: "edited text" });
    expect(newList[1].role).toBe(RoleTypes.ASSISTANT);

    expect(scrollToEnd).toHaveBeenCalledTimes(1);
    expect(saveMessageListMock).toHaveBeenCalledWith("conv-1", newList);
    expect(conversationMessagesCache.current.get("conv-1")).toBe(newList);
    expect(openSSE).toHaveBeenCalledWith(
      newList[0].inputs,
      ChatConversationsRequestActionEnum.ChatActionRegeneration,
      undefined,
    );
    expect(emitConversationActivityMock).toHaveBeenCalledWith({ conversationId: "conv-1" });
  });

  it("handleResendEditedUserMessage does nothing when the resend text is blank", () => {
    const { result, openSSE, setMessageList } = setup();

    act(() => {
      result.current.handleResendEditedUserMessage(0, "   ");
    });

    expect(openSSE).not.toHaveBeenCalled();
    expect(setMessageList).not.toHaveBeenCalled();
  });

  it("handleResendEditedUserMessage does nothing when the target message is not a user message", () => {
    const { result, openSSE } = setup();

    act(() => {
      result.current.handleResendEditedUserMessage(1, "edited");
    });

    expect(openSSE).not.toHaveBeenCalled();
  });

  it("handleResendEditedUserMessage skips emitConversationActivity for temp_ conversations", () => {
    const currentConversationIdRef = makeRef("temp_123");
    const { result } = setup({ currentConversationIdRef } as never);

    act(() => {
      result.current.handleResendEditedUserMessage(0, "edited");
    });

    expect(emitConversationActivityMock).not.toHaveBeenCalled();
  });

  it("handleCopyUserMessage copies via navigator.clipboard when available", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });
    const { result } = setup();

    await act(async () => {
      await result.current.handleCopyUserMessage({ delta: "copy me" });
    });

    expect(writeText).toHaveBeenCalledWith("copy me");
    expect(successMock).toHaveBeenCalledWith("chat.copySuccess");
  });

  it("handleCopyUserMessage is a no-op for blank text", async () => {
    const writeText = vi.fn();
    Object.assign(navigator, { clipboard: { writeText } });
    const { result } = setup();

    await act(async () => {
      await result.current.handleCopyUserMessage({ delta: "   " });
    });

    expect(writeText).not.toHaveBeenCalled();
  });
});
