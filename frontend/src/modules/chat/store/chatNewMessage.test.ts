import { beforeEach, describe, expect, it } from "vitest";
import { useChatNewMessageStore } from "./chatNewMessage";

describe("useChatNewMessageStore", () => {
  beforeEach(() => {
    useChatNewMessageStore.setState({ newMessage: true });
  });

  it("defaults newMessage to true", () => {
    expect(useChatNewMessageStore.getState().newMessage).toBe(true);
  });

  it("setNewMessage updates the flag", () => {
    useChatNewMessageStore.getState().setNewMessage(false);
    expect(useChatNewMessageStore.getState().newMessage).toBe(false);

    useChatNewMessageStore.getState().setNewMessage(true);
    expect(useChatNewMessageStore.getState().newMessage).toBe(true);
  });
});
