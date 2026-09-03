import { describe, expect, it } from "vitest";

import {
  canStartSidechat,
  CONVERSATION_RELATION_FORK,
  CONVERSATION_RELATION_SIDECHAT,
  getConversationRelation,
} from "./conversationRelation";

describe("conversation relation helpers", () => {
  it("normalizes a sidechat relation and prevents nested sidechat", () => {
    const conversation = {
      conversation_id: "child",
      search_config: {},
      parent_conversation_id: "parent",
      relation_type: "sidechat",
      parent_display_name: "主会话",
    };

    expect(getConversationRelation(conversation)).toEqual({
      parentConversationId: "parent",
      parentDisplayName: "主会话",
      relationType: CONVERSATION_RELATION_SIDECHAT,
    });
    expect(canStartSidechat(conversation)).toBe(false);
  });

  it("keeps fork distinct and allows a root conversation to start sidechat", () => {
    expect(
      getConversationRelation({
        parent_conversation_id: "parent",
        relation_type: "fork",
      }),
    ).toEqual(
      expect.objectContaining({ relationType: CONVERSATION_RELATION_FORK }),
    );
    expect(canStartSidechat({})).toBe(true);
  });

  it("keeps an unknown relation generic but still blocks nested sidechat", () => {
    const conversation = {
      parent_conversation_id: "parent",
      relation_type: "future-relation",
      parent_display_name: "主会话",
    };

    expect(getConversationRelation(conversation)).toEqual({
      parentConversationId: "parent",
      parentDisplayName: "主会话",
      relationType: null,
    });
    expect(canStartSidechat(conversation)).toBe(false);
  });
});
