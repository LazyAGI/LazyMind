import { beforeEach, describe, expect, it, vi } from "vitest";
import { useChatMessageStore } from "./chatMessage";

describe("useChatMessageStore", () => {
  beforeEach(() => {
    useChatMessageStore.setState({ pendingMessage: null });
  });

  it("defaults pendingMessage to null", () => {
    expect(useChatMessageStore.getState().pendingMessage).toBeNull();
  });

  it("setPendingMessage stores the provided message", () => {
    const message = { conversationId: "conv-1", text: "hi" } as never;
    useChatMessageStore.getState().setPendingMessage(message);

    expect(useChatMessageStore.getState().pendingMessage).toBe(message);
  });

  it("clearPendingMessage resets to null", () => {
    useChatMessageStore.getState().setPendingMessage({ text: "hi" } as never);
    useChatMessageStore.getState().clearPendingMessage();

    expect(useChatMessageStore.getState().pendingMessage).toBeNull();
  });

  it("notifies subscribers when pendingMessage changes", () => {
    const listener = vi.fn();
    const unsubscribe = useChatMessageStore.subscribe(
      (state) => state.pendingMessage,
      listener,
    );

    useChatMessageStore.getState().setPendingMessage({ text: "hi" } as never);

    expect(listener).toHaveBeenCalledTimes(1);
    unsubscribe();
  });
});
