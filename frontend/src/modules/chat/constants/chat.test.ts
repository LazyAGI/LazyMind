import { afterEach, describe, expect, it, vi } from "vitest";
import {
  CHAT_CONVERSATION_FILTER_EVENT,
  CHAT_CONVERSATION_FILTER_KEY,
  selectChatConversationFilter,
} from "./chat";

describe("selectChatConversationFilter", () => {
  afterEach(() => {
    sessionStorage.clear();
    vi.restoreAllMocks();
  });

  it("persists the filter in sessionStorage and dispatches an event", () => {
    const listener = vi.fn();
    window.addEventListener(CHAT_CONVERSATION_FILTER_EVENT, listener);

    selectChatConversationFilter("task");

    expect(sessionStorage.getItem(CHAT_CONVERSATION_FILTER_KEY)).toBe("task");
    expect(listener).toHaveBeenCalledTimes(1);
    const event = listener.mock.calls[0][0] as CustomEvent;
    expect(event.detail).toEqual({ filter: "task" });

    window.removeEventListener(CHAT_CONVERSATION_FILTER_EVENT, listener);
  });

  it("still dispatches the event even if sessionStorage throws", () => {
    const setItemSpy = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("storage disabled");
      });
    const listener = vi.fn();
    window.addEventListener(CHAT_CONVERSATION_FILTER_EVENT, listener);

    expect(() => selectChatConversationFilter("normal")).not.toThrow();
    expect(listener).toHaveBeenCalledTimes(1);

    window.removeEventListener(CHAT_CONVERSATION_FILTER_EVENT, listener);
    setItemSpy.mockRestore();
  });
});
