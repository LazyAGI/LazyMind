import { describe, expect, it } from "vitest";
import {
  buildAutoInteractionMessagesFromEvents,
  dedupeAndSortChatMessages,
  getDialogueEventAgentLabel,
  getHistoryAssistantDeltaContent,
  getHistoryMessageContent,
  getThreadListItemTitle,
  normalizeHistoryEventMessages,
  normalizeThreadListPayload,
  normalizeThreadMessagesPayload,
} from "./threadHistory";
import type { ChatMessage, NormalizedThreadEvent } from "./types";

function makeEvent(overrides: Partial<NormalizedThreadEvent>): NormalizedThreadEvent {
  return { key: "k", type: "x", ...overrides };
}

describe("getThreadListItemTitle", () => {
  it("prefers a title directly on the item", () => {
    expect(getThreadListItemTitle({ title: "My Session" }, "thread-1")).toBe("My Session");
  });

  it("falls back to a generated session title using the thread id prefix", () => {
    const title = getThreadListItemTitle({}, "abcdef1234567890");
    expect(title).toContain("abcdef12");
  });
});

describe("normalizeThreadListPayload", () => {
  it("normalizes a list of thread records with ids and titles", () => {
    const entries = normalizeThreadListPayload({
      threads: [{ thread_id: "t1", title: "First", updated_at: "2026-01-01T00:00:00Z", status: "active" }],
    });
    expect(entries).toHaveLength(1);
    expect(entries[0]).toMatchObject({ threadId: "t1", title: "First", status: "active" });
  });

  it("skips records without a resolvable thread id", () => {
    const entries = normalizeThreadListPayload({ threads: [{ title: "No id" }] });
    expect(entries).toEqual([]);
  });
});

describe("getDialogueEventAgentLabel", () => {
  it("labels autooperator events", () => {
    expect(getDialogueEventAgentLabel(makeEvent({ type: "autooperator.step" }))).toBe("AutoOperator");
  });

  it("labels user/assistant message events distinctly", () => {
    const userLabel = getDialogueEventAgentLabel(makeEvent({ type: "message.user" }));
    const assistantLabel = getDialogueEventAgentLabel(makeEvent({ type: "message.assistant" }));
    expect(userLabel).not.toBe(assistantLabel);
  });

  it("returns undefined for unrelated event types", () => {
    expect(getDialogueEventAgentLabel(makeEvent({ type: "dataset.progress" }))).toBeUndefined();
  });
});

describe("buildAutoInteractionMessagesFromEvents", () => {
  it("builds chat messages from dialogue-labeled events with content, sorted by time", () => {
    const events: NormalizedThreadEvent[] = [
      makeEvent({ key: "e2", type: "message.assistant", content: "reply", sequence: 2 }),
      makeEvent({ key: "e1", type: "message.user", content: "hello", sequence: 1 }),
    ];
    const messages = buildAutoInteractionMessagesFromEvents(events);
    expect(messages).toHaveLength(2);
    expect(messages[0].content).toBe("hello");
    expect(messages[1].content).toBe("reply");
  });

  it("filters out events without a dialogue label or content", () => {
    const events: NormalizedThreadEvent[] = [makeEvent({ type: "dataset.progress" })];
    expect(buildAutoInteractionMessagesFromEvents(events)).toEqual([]);
  });
});

describe("getHistoryMessageContent", () => {
  it("extracts a string field from a record payload", () => {
    expect(getHistoryMessageContent({ content: "hi there" })).toBe("hi there");
  });

  it("joins string items from an array payload", () => {
    expect(getHistoryMessageContent(["a", "b"])).toBe("ab");
  });

  it("returns undefined for empty or unresolvable payloads", () => {
    expect(getHistoryMessageContent("  ")).toBeUndefined();
    expect(getHistoryMessageContent({ foo: "bar" })).toBeUndefined();
  });
});

describe("getHistoryAssistantDeltaContent", () => {
  it("extracts a delta from an answer_delta record", () => {
    expect(getHistoryAssistantDeltaContent({ type: "answer_delta", delta: "chunk" })).toBe("chunk");
  });

  it("parses SSE-formatted data lines and joins matching deltas", () => {
    const raw = 'data: {"type":"answer_delta","delta":"He"}\ndata: {"type":"answer_delta","delta":"llo"}\n';
    expect(getHistoryAssistantDeltaContent(raw)).toBe("Hello");
  });

  it("returns undefined for content with no matching deltas", () => {
    expect(getHistoryAssistantDeltaContent("plain text with no data: lines")).toBeUndefined();
  });
});

describe("normalizeHistoryEventMessages", () => {
  it("normalizes message records with a role and content", () => {
    const messages = normalizeHistoryEventMessages({
      messages: [{ role: "user", content: "hi", event_type: "message" }],
    });
    expect(messages).toHaveLength(1);
    expect(messages[0]).toMatchObject({ role: "user", content: "hi" });
  });

  it("returns an empty array when there are no recognizable message records", () => {
    expect(normalizeHistoryEventMessages({})).toEqual([]);
  });
});

describe("dedupeAndSortChatMessages", () => {
  it("removes duplicate messages by role/content/time key", () => {
    const messages: ChatMessage[] = [
      { id: "a", role: "user", content: "hi", time: "t1", sortTime: 1 },
      { id: "b", role: "user", content: "hi", time: "t1", sortTime: 1 },
    ];
    expect(dedupeAndSortChatMessages(messages)).toHaveLength(1);
  });

  it("sorts messages by sortTime ascending", () => {
    const messages: ChatMessage[] = [
      { id: "a", role: "assistant", content: "second", time: "t2", sortTime: 2 },
      { id: "b", role: "user", content: "first", time: "t1", sortTime: 1 },
    ];
    const sorted = dedupeAndSortChatMessages(messages);
    expect(sorted[0].content).toBe("first");
  });
});

describe("normalizeThreadMessagesPayload", () => {
  it("merges and dedupes messages from multiple sources into a sorted list", () => {
    const messages = normalizeThreadMessagesPayload({
      items: [{ role: "user", content: "hi from items" }],
    });
    expect(messages.some((message) => message.content === "hi from items")).toBe(true);
  });

  it("returns an empty array for a payload without any messages", () => {
    expect(normalizeThreadMessagesPayload({})).toEqual([]);
  });
});
