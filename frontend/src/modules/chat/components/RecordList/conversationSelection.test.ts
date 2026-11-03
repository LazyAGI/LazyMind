import { beforeEach, describe, expect, it, vi } from "vitest";
import { readConversationSelection } from "./conversationSelection";

const list = vi.hoisted(() => vi.fn());
vi.mock("@/modules/chat/utils/request", () => ({
  ChatServiceApi: () => ({ conversationServiceListConversations: list }),
}));

describe("conversation selection snapshot", () => {
  beforeEach(() => list.mockReset());

  it("preserves group, pin and lock metadata, deduplicates roots and excludes dependent children", async () => {
    const root = { conversation_id: "root", group_id: "g", pinned_at: "2026-10-20T00:00:00Z", organizing_run_id: "run" };
    const fork = { conversation_id: "fork", fork_origin: { source_conversation_id: "root" } };
    list.mockResolvedValue({ data: { selection_complete: true, conversations: [root, root, fork, { conversation_id: "child", parent_conversation_id: "root", relation_type: "sidechat" }] } });
    expect(await readConversationSelection("  中文  ", true, "codex,lazymind", "g")).toEqual([root, fork]);
    expect(list).toHaveBeenCalledTimes(1);
    expect(list).toHaveBeenCalledWith({ keyword: "中文" }, { params: {
      selection_snapshot: true, selection_group_id: "g", is_task_conv: "true", assistants: "codex,lazymind",
    } });
  });

  it.each([
    { conversations: [], next_page_token: "" },
    { conversations: [], selection_complete: true, next_page_token: "50" },
    { selection_complete: true },
  ])("rejects incomplete responses: %j", async data => {
    list.mockResolvedValue({ data });
    await expect(readConversationSelection("", false)).rejects.toThrow("Incomplete conversation selection snapshot");
  });

  it("accepts a complete empty result and propagates request failures", async () => {
    list.mockResolvedValueOnce({ data: { selection_complete: true, conversations: [] } });
    expect(await readConversationSelection("", false)).toEqual([]);
    list.mockRejectedValueOnce(new Error("offline"));
    await expect(readConversationSelection("", false)).rejects.toThrow("offline");
  });
});
