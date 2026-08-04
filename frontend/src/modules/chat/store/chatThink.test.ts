import { beforeEach, describe, expect, it } from "vitest";
import { useChatThinkStore } from "./chatThink";

describe("useChatThinkStore", () => {
  beforeEach(() => {
    useChatThinkStore.setState({ think: false, thinkingDepth: "medium" });
  });

  it("has the default think/thinkingDepth values", () => {
    const state = useChatThinkStore.getState();
    expect(state.think).toBe(false);
    expect(state.thinkingDepth).toBe("medium");
  });

  it("setThink toggles the think flag", () => {
    useChatThinkStore.getState().setThink(true);
    expect(useChatThinkStore.getState().think).toBe(true);

    useChatThinkStore.getState().setThink(false);
    expect(useChatThinkStore.getState().think).toBe(false);
  });

  it("setThinkingDepth updates to any allowed depth", () => {
    useChatThinkStore.getState().setThinkingDepth("high");
    expect(useChatThinkStore.getState().thinkingDepth).toBe("high");

    useChatThinkStore.getState().setThinkingDepth("max");
    expect(useChatThinkStore.getState().thinkingDepth).toBe("max");
  });
});
