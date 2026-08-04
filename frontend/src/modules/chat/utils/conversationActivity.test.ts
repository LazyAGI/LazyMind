import { afterEach, describe, expect, it, vi } from "vitest";
import {
  bumpConversationToTop,
  emitConversationActivity,
} from "./conversationActivity";
import { CHAT_CONVERSATION_ACTIVITY_EVENT } from "@/modules/chat/constants/chat";
import type { Conversation } from "@/api/generated/chatbot-client";

describe("emitConversationActivity", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("dispatches the activity event for a real conversation id", () => {
    const dispatchSpy = vi.spyOn(window, "dispatchEvent");

    emitConversationActivity({ conversationId: " conv-1 " });

    expect(dispatchSpy).toHaveBeenCalledTimes(1);
    const event = dispatchSpy.mock.calls[0]![0] as CustomEvent;
    expect(event.type).toBe(CHAT_CONVERSATION_ACTIVITY_EVENT);
    expect(event.detail).toEqual({ conversationId: "conv-1" });
  });

  it("does nothing for an empty conversation id", () => {
    const dispatchSpy = vi.spyOn(window, "dispatchEvent");

    emitConversationActivity({ conversationId: "   " });

    expect(dispatchSpy).not.toHaveBeenCalled();
  });

  it("does nothing for temp_ prefixed conversation ids", () => {
    const dispatchSpy = vi.spyOn(window, "dispatchEvent");

    emitConversationActivity({ conversationId: "temp_123" });

    expect(dispatchSpy).not.toHaveBeenCalled();
  });
});

describe("bumpConversationToTop", () => {
  it("moves an existing conversation to the top and refreshes update_time", () => {
    const list: Conversation[] = [
      { conversation_id: "a", display_name: "A", update_time: "2020-01-01T00:00:00.000Z" },
      { conversation_id: "b", display_name: "B", update_time: "2021-01-01T00:00:00.000Z" },
    ];

    const result = bumpConversationToTop(list, "b");

    expect(result[0]!.conversation_id).toBe("b");
    expect(result[1]!.conversation_id).toBe("a");
    expect(new Date(result[0]!.update_time!).getTime()).toBeGreaterThan(
      new Date("2021-01-01T00:00:00.000Z").getTime() - 1000,
    );
  });

  it("applies a new display name to the bumped conversation when provided", () => {
    const list: Conversation[] = [
      { conversation_id: "a", display_name: "Old name", update_time: "2020-01-01T00:00:00.000Z" },
    ];

    const result = bumpConversationToTop(list, "a", { displayName: "New name" });

    expect(result[0]!.display_name).toBe("New name");
  });

  it("returns the list unchanged when the conversation is missing and no display name given", () => {
    const list: Conversation[] = [
      { conversation_id: "a", display_name: "A", update_time: "2020-01-01T00:00:00.000Z" },
    ];

    const result = bumpConversationToTop(list, "missing");

    expect(result).toBe(list);
  });

  it("inserts a placeholder at the top when the conversation is missing but a display name is given", () => {
    const list: Conversation[] = [
      { conversation_id: "a", display_name: "A", update_time: "2020-01-01T00:00:00.000Z" },
    ];

    const result = bumpConversationToTop(list, "new-conv", { displayName: "New conv" });

    expect(result).toHaveLength(2);
    expect(result[0]).toMatchObject({
      conversation_id: "new-conv",
      display_name: "New conv",
      search_config: {},
    });
    expect(result[1]!.conversation_id).toBe("a");
  });
});
