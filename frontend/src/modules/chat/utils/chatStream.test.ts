import { describe, expect, it, vi } from "vitest";
import { createChatStream } from "./chatStream";

const mocks = vi.hoisted(() => ({ auth: vi.fn(), sse: vi.fn() }));
vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getAuthHeaders: mocks.auth },
}));
vi.mock("./sse", () => ({ Method: { POST: "POST" }, SSE: mocks.sse }));

describe("createChatStream", () => {
  it("shares transport options without sharing payloads, callbacks or stale credentials", () => {
    mocks.auth
      .mockReturnValueOnce({ Authorization: "first" })
      .mockReturnValueOnce({ Authorization: "second" });
    const mainCallbacks = { message: vi.fn() };
    const sideCallbacks = { message: vi.fn() };
    const main = { conversation_id: "main", input: [] };
    const side = {
      conversation_id: "child", history_id: "history-1", after_sequence: 2,
      basic_chat_only: true, use_memory: false,
    };
    createChatStream("/stream", main, mainCallbacks);
    createChatStream("/resume", side, sideCallbacks);
    for (const [index, url, payload, callbacks, authorization] of [
      [1, "/stream", main, mainCallbacks, "first"],
      [2, "/resume", side, sideCallbacks, "second"],
    ] as const) {
      expect(mocks.sse).toHaveBeenNthCalledWith(index, url, {
        method: "POST", timeout: 1_800_000,
        headers: {
          "Content-Type": "application/json",
          Accept: "text/event-stream",
          Authorization: authorization,
        },
        payload: JSON.stringify(payload), callbacks,
      });
    }
    expect(main).not.toHaveProperty("basic_chat_only");
  });
});
