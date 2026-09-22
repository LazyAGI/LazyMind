import { beforeEach, describe, expect, it } from "vitest";
import {
  CHAT_CONVERSATION_FILTER_KEY, CHAT_CONVERSATION_MODE_KEY, CHAT_CONVERSATION_SOURCES_KEY,
  CHAT_NEW_RUN_IN_BACKGROUND_KEY, readChatConversationFilters, revealChatConversation,
  selectChatConversationFilter, selectChatConversationSources,
} from "./chat";

describe("conversation mode and source persistence", () => {
  beforeEach(() => sessionStorage.clear());

  it.each([
    ["task", "task", null],
    ['["task"]', "task", null],
    ["normal", "normal", ["lazymind"]],
    ['["normal","agent:codex"]', "normal", ["lazymind", "codex"]],
    ['["task","agent:workbuddy"]', "task", ["workbuddy"]],
    ['["normal","task","agent:codex"]', "task", ["lazymind", "codex"]],
  ])("migrates legacy filter %s without mixing the two dimensions", (legacy, filter, sources) => {
    sessionStorage.setItem(CHAT_CONVERSATION_FILTER_KEY, legacy as string);
    sessionStorage.setItem(CHAT_NEW_RUN_IN_BACKGROUND_KEY, "1");
    expect(readChatConversationFilters()).toEqual({ filter, sources });
    selectChatConversationFilter(filter as "normal" | "task");
    sessionStorage.removeItem(CHAT_CONVERSATION_FILTER_KEY);
    expect(readChatConversationFilters()).toEqual({ filter, sources });
  });

  it("preserves the mode when source storage is corrupt", () => {
    sessionStorage.setItem(CHAT_CONVERSATION_MODE_KEY, "task");
    sessionStorage.setItem(CHAT_CONVERSATION_SOURCES_KEY, "{");
    expect(readChatConversationFilters()).toEqual({ filter: "task", sources: null });
  });

  it("ignores unsupported sources and refuses an empty selection", () => {
    selectChatConversationFilter("task");
    selectChatConversationSources(["codex", "invalid", "codex"]);
    selectChatConversationSources([]);
    expect(readChatConversationFilters()).toEqual({ filter: "task", sources: ["codex"] });
  });

  it("reveals a conversation by its binding source instead of its execution engine", () => {
    selectChatConversationSources(["lazymind"]);
    revealChatConversation({ is_task_conv: true, assistant: "workbuddy" });
    expect(readChatConversationFilters()).toEqual({ filter: "task", sources: ["lazymind", "workbuddy"] });
    revealChatConversation({ is_task_conv: false, assistant: "lazymind", chat_executor: "codex" } as Parameters<typeof revealChatConversation>[0]);
    expect(readChatConversationFilters()).toEqual({ filter: "normal", sources: ["lazymind", "workbuddy"] });
  });
});
