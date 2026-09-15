import { describe, expect, it } from "vitest";
import { bumpConversationToTop } from "@/modules/chat/utils/conversationActivity";
import { applyConversationOrder, sortConversationHistory, type SidebarConversation } from "./conversationHistory";

const searchConfig = { dataset_list: [], top_k: 5, confidence: 0, weight: 0.5 };
const rows: SidebarConversation[] = [
  { search_config: searchConfig, conversation_id: "a", update_time: "2026-09-08T00:00:00Z", history_order: 2 },
  { search_config: searchConfig, conversation_id: "b", update_time: "2026-09-01T00:00:00Z", history_order: 1 },
  { search_config: searchConfig, conversation_id: "p", update_time: "2026-08-01T00:00:00Z", pinned_at: "2026-08-01T00:00:00Z", history_order: 2 },
  { search_config: searchConfig, conversation_id: "q", update_time: "2026-08-02T00:00:00Z", pinned_at: "2026-08-02T00:00:00Z", history_order: 1 },
];
const ids = (items: SidebarConversation[]) => items.map((item) => item.conversation_id);

describe("conversation history ordering", () => {
  it("keeps manually ordered pinned and normal rows ahead of activity sorting", () => {
    expect(ids(sortConversationHistory(rows))).toEqual(["q", "p", "b", "a"]);
    expect(ids(sortConversationHistory(bumpConversationToTop(rows, "a")))).toEqual(["q", "p", "b", "a"]);
    expect(ids(sortConversationHistory([...rows, { search_config: searchConfig, conversation_id: "new", update_time: new Date().toISOString() }]))).toEqual(["q", "p", "new", "b", "a"]);
  });

  it("applies authoritative positions without dropping hidden or updated items", () => {
    const next = applyConversationOrder(rows, {
      conversation_id: "a", is_pinned: false, pinned_at: null, history_order: 1,
      order_updates: [{ conversation_id: "a", history_order: 1 }, { conversation_id: "hidden", history_order: 2 }, { conversation_id: "b", history_order: 3 }],
    });
    expect(ids(next)).toEqual(["q", "p", "a", "b"]);
    expect(next.find((item) => item.conversation_id === "a")?.update_time).toBe(rows[0].update_time);
  });

  it("clears the old position when unpinning to chronological history", () => {
    const next = applyConversationOrder(rows.map((item) => ({ ...item, history_order: null })), {
      conversation_id: "p", is_pinned: false, pinned_at: null, history_order: null, order_updates: [],
    });
    expect(ids(next)).toEqual(["q", "a", "b", "p"]);
  });
});
