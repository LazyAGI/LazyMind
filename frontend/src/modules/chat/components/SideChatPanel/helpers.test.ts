import { describe, expect, it } from "vitest";
import {
  buildSideChatCreateBody,
  buildSideChatStreamPayload,
  chatConfigFromSideChat,
  normalizeSideChatConversation,
  sideChatSourceText,
} from "./helpers";

describe("side-chat helpers", () => {
  it("maps and Unicode-truncates the source request", () => {
    const selectedText = `${"问".repeat(15_999)}😀尾部`;
    const body = buildSideChatCreateBody(
      {
        historyId: " history-1 ",
        sequence: 7,
        selectedText,
      },
      "max",
    );

    expect(body.source_history_id).toBe("history-1");
    expect(body.source_seq).toBe(7);
    expect(body.thinking_depth).toBe("max");
    expect(Array.from(body.selected_text ?? "")).toHaveLength(16_000);
    expect(body.selected_text?.endsWith("😀")).toBe(true);
  });

  it("normalizes inherited child settings", () => {
    const conversation = normalizeSideChatConversation({
      data: {
        conversation: {
          id: "child-1",
          parent_conversation_id: "parent-1",
          relation_type: "sidechat",
          thinking_depth: "high",
          search_config: {
            dataset_list: [{ id: "kb-1" }, "kb-2"],
            database_ids: ["db-1"],
            creators: ["user-1"],
            tags: ["tag-1"],
          },
        },
      },
    });

    expect(conversation.thinkingDepth).toBe("high");
    expect(chatConfigFromSideChat(conversation)).toEqual({
      knowledgeBaseId: ["kb-1", "kb-2"],
      databaseBaseId: "db-1",
      creators: ["user-1"],
      tags: ["tag-1"],
    });
  });

  it("builds a basic-chat-only request without workflow or memory fields", () => {
    const payload = buildSideChatStreamPayload({
      conversationId: "child-1",
      action: "CHAT_ACTION_NEXT",
      input: [{ input_type: "text", text: "hello" }],
      thinkingDepth: "max",
      chatConfig: { knowledgeBaseId: ["kb-1"] },
      modelLabel: "LazyMind",
      locale: "zh-CN",
      createTime: "2026-09-02T00:00:00.000Z",
      clientRequestId: "request-1",
    });

    expect(payload).toMatchObject({
      conversation_id: "child-1",
      basic_chat_only: true,
      use_memory: false,
      thinking_depth: "max",
      client_request_id: "request-1",
      conversation: {
        search_config: { dataset_list: [{ id: "kb-1" }] },
      },
    });
    expect(payload).not.toHaveProperty("workflow_context");
    expect(payload).not.toHaveProperty("mentions");
    expect(payload).not.toHaveProperty("initial_model_selection");
    expect(payload).not.toHaveProperty("initial_conversation_settings");
  });

  it("reads a page-entry preview from the structured source snapshot", () => {
    const conversation = normalizeSideChatConversation({
      conversation: {
        id: "child-1",
        parent_conversation_id: "parent-1",
        relation_type: "sidechat",
        source_context: {
          messages: [
            { role: "user", content: "older" },
            { role: "assistant", content: "latest completed answer" },
          ],
        },
      },
    });

    expect(conversation.sourceContext).toEqual({
      messages: [
        { role: "user", content: "older" },
        { role: "assistant", content: "latest completed answer" },
      ],
    });
    // No selected excerpt means the source card uses the last snapshotted message.
    expect(sideChatSourceText(null, conversation)).toBe(
      "latest completed answer",
    );
  });
});
