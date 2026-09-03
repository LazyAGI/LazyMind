import { beforeEach, describe, expect, it, vi } from "vitest";

const requestMocks = vi.hoisted(() => ({
  request: vi.fn(),
  patch: vi.fn(),
}));

vi.mock("@/components/request", () => ({
  axiosInstance: { ...requestMocks, defaults: {} },
  BASE_URL: "/base",
}));

import {
  createSideChat, deleteSideChat, patchSideChatThinkingDepth, retainSideChat,
} from "./api";

describe("side-chat API", () => {
  beforeEach(() => {
    requestMocks.request.mockReset().mockResolvedValue({
      data: {
        conversation: {
          id: "child/1", relation_type: "sidechat", thinking_depth: "high",
        },
      },
    });
    requestMocks.patch.mockReset().mockResolvedValue({});
  });

  it("creates through the generated client with the source snapshot and inherited depth", async () => {
    const child = await createSideChat(
      "parent/1",
      { historyId: "history-1", sequence: 2, selectedText: " selected text " },
      "high",
    );

    expect(child).toMatchObject({ id: "child/1", thinkingDepth: "high" });
    expect(requestMocks.request).toHaveBeenCalledWith(
      expect.objectContaining({
        url: "/base/api/core/conversations/parent%2F1/sidechat",
        method: "POST", silentError: true,
        data: JSON.stringify({
          source_history_id: "history-1", source_seq: 2,
          selected_text: "selected text", thinking_depth: "high",
        }),
      }),
    );
  });

  it("retains and discards only the requested child", async () => {
    expect(await retainSideChat("child/1")).toMatchObject({ id: "child/1" });
    await deleteSideChat("child/1");
    for (const [index, method, suffix] of [
      [1, "POST", "retain"], [2, "DELETE", "sidechat"],
    ] as const) {
      expect(requestMocks.request).toHaveBeenNthCalledWith(index,
        expect.objectContaining({
          url: `/base/api/core/conversations/child%2F1/${suffix}`,
          method, silentError: true,
        }),
      );
    }
  });

  it("keeps thinking-depth updates scoped to the child", async () => {
    await patchSideChatThinkingDepth("child/1", "low");
    expect(requestMocks.patch).toHaveBeenCalledWith(
      "/base/api/core/conversations/child%2F1/settings",
      { thinking_depth: "low" },
      { silentError: true },
    );
  });

  it("propagates retention failures so the panel can keep content and offer retry", async () => {
    const failure = new Error("retention failed");
    requestMocks.request.mockRejectedValueOnce(failure);
    await expect(retainSideChat("child/1")).rejects.toBe(failure);
    expect(requestMocks.request).toHaveBeenCalledTimes(1);
  });
});
