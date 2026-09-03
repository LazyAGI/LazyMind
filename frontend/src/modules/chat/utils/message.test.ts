import { describe, expect, it } from "vitest";
import { ChatConversationsResponseFinishReasonEnum } from "@/api/generated/chatbot-client";
import { RoleTypes } from "@/modules/chat/constants/common";
import {
  buildChatMessageListFromHistory,
  getCitationFromText,
  getCitationsFromText,
  getRegenerationInputs,
  isAskPendingReadOnly,
  mergeChatMessageLists,
  mergeConversationTrailIntoMessageList,
  normalizeMessageInputs,
  normalizeImportedUserText,
  stripAskUserReceipt,
  stripCitationFromText,
} from "./message";

describe("normalizeImportedUserText", () => {
  it("shows only the request body from an existing Codex transport envelope", () => {
    expect(normalizeImportedUserText(`# Files mentioned by the user:

## image.png: /tmp/image.png

## My request for Codex:
修复界面
<image name="Image #1" path="/tmp/image.png"></image>`)).toBe("修复界面");
  });
});

describe("isAskPendingReadOnly", () => {
  it("keeps the latest unanswered Ask interactive after history reload", () => {
    expect(isAskPendingReadOnly(false, true)).toBe(false);
  });

  it("disables answered or superseded Ask cards", () => {
    expect(isAskPendingReadOnly(true, true)).toBe(true);
    expect(isAskPendingReadOnly(false, false, true)).toBe(true);
  });

  it("keeps an unanswered Ask interactive when only assistant placeholders follow it", () => {
    expect(isAskPendingReadOnly(false, false, false)).toBe(false);
  });
});

describe("stripAskUserReceipt", () => {
  it("returns an empty string when the receipt pattern matches and an ask is pending", () => {
    const text = "Question sent to user (ask_id=abc). Waiting for answer on next turn.";
    expect(stripAskUserReceipt(text, true)).toBe("");
  });

  it("keeps the text unchanged when no ask is pending", () => {
    const text = "Question sent to user (ask_id=abc). Waiting for answer on next turn.";
    expect(stripAskUserReceipt(text, false)).toBe(text);
  });

  it("keeps unrelated text unchanged even when a ask is pending", () => {
    expect(stripAskUserReceipt("just a normal reply", true)).toBe("just a normal reply");
  });

  it("handles undefined input", () => {
    expect(stripAskUserReceipt(undefined, true)).toBe("");
  });
});

describe("normalizeMessageInputs", () => {
  it("returns an empty array for non-array input with no fallback text", () => {
    expect(normalizeMessageInputs(null)).toEqual([]);
  });

  it("clones and returns provided inputs unchanged when a text input already exists", () => {
    const inputs = [{ input_type: "text", text: "hi" }];
    const result = normalizeMessageInputs(inputs, "ignored fallback");
    expect(result).toEqual([{ input_type: "text", text: "hi" }]);
    expect(result[0]).not.toBe(inputs[0]);
  });

  it("prepends a fallback text input when there is no existing text input", () => {
    const inputs = [{ input_type: "image", input_base64: "abc" }];
    const result = normalizeMessageInputs(inputs as never, "fallback text");
    expect(result[0]).toEqual({ input_type: "text", text: "fallback text" });
    expect(result).toHaveLength(2);
  });

  it("filters out falsy items from the input array", () => {
    const inputs = [null, { input_type: "text", text: "ok" }, undefined];
    const result = normalizeMessageInputs(inputs as never);
    expect(result).toEqual([{ input_type: "text", text: "ok" }]);
  });

  it("does not prepend fallback text when the trimmed fallback is empty", () => {
    const result = normalizeMessageInputs([], "   ");
    expect(result).toEqual([]);
  });
});

describe("getRegenerationInputs", () => {
  it("returns an empty array when there is no user message", () => {
    expect(getRegenerationInputs(undefined)).toEqual([]);
  });

  it("derives inputs from delta when inputs are absent", () => {
    const result = getRegenerationInputs({ delta: "hello" });
    expect(result).toEqual([{ input_type: "text", text: "hello" }]);
  });

  it("preserves existing structured inputs", () => {
    const result = getRegenerationInputs({
      inputs: [{ input_type: "text", text: "hi" }],
      delta: "hi",
    });
    expect(result).toEqual([{ input_type: "text", text: "hi" }]);
  });
});

describe("citation helpers", () => {
  it("extracts a single citation from text", () => {
    const text = "<cite_message>quoted part</cite_message>\nfollow up";
    expect(getCitationFromText(text)).toBe("quoted part");
  });

  it("returns an empty string when there is no citation", () => {
    expect(getCitationFromText("no citation here")).toBe("");
  });

  it("extracts multiple citations from text", () => {
    const text = "<cite_message>one</cite_message><cite_message>two</cite_message>body";
    expect(getCitationsFromText(text)).toEqual(["one", "two"]);
  });

  it("returns an empty array when there are no citations", () => {
    expect(getCitationsFromText("plain text")).toEqual([]);
  });

  it("strips all citations and trims the remaining text", () => {
    const text = "<cite_message>one</cite_message>  actual question  ";
    expect(stripCitationFromText(text)).toBe("actual question");
  });
});

describe("buildChatMessageListFromHistory", () => {
  it("uses a persisted image URI when imported history has no base64 payload", () => {
    const list = buildChatMessageListFromHistory([
      {
        id: "history-1",
        query: "hello",
        input: [
          { input_type: "text", text: "hello" },
          { input_type: "image", uri: "/static-files/imported/image.png", file_id: "image.png" },
        ],
      },
    ], { reverseHistory: false });

    expect(list[0].images).toEqual([
      { base64: "/static-files/imported/image.png", uid: "image.png" },
    ]);
  });
  it("returns an empty list for null/undefined history", () => {
    expect(buildChatMessageListFromHistory(null)).toEqual([]);
    expect(buildChatMessageListFromHistory(undefined)).toEqual([]);
  });

  it("builds a user/assistant pair per record and reverses history by default", () => {
    const history = [
      { id: "h1", query: "first question", result: "first answer", create_time: "t1" },
      { id: "h2", query: "second question", result: "second answer", create_time: "t2" },
    ];

    const list = buildChatMessageListFromHistory(history);

    // reverseHistory defaults to true, so record h2 is processed first.
    expect(list).toHaveLength(4);
    expect(list[0]).toMatchObject({ role: RoleTypes.USER, delta: "second question" });
    expect(list[1]).toMatchObject({ role: RoleTypes.ASSISTANT, delta: "second answer" });
    expect(list[2]).toMatchObject({ role: RoleTypes.USER, delta: "first question" });
    expect(list[3]).toMatchObject({ role: RoleTypes.ASSISTANT, delta: "first answer" });
  });

  it("keeps original order when reverseHistory is false", () => {
    const history = [
      { id: "h1", query: "q1", result: "a1" },
      { id: "h2", query: "q2", result: "a2" },
    ];

    const list = buildChatMessageListFromHistory(history, { reverseHistory: false });

    expect(list[0]).toMatchObject({ delta: "q1" });
    expect(list[2]).toMatchObject({ delta: "q2" });
  });

  it("restores the authoritative external execution projection", () => {
    const execution = {
      run_id: "run-1",
      history_id: "h1",
      provider: "codex",
      status: "completed" as const,
      host_online: false,
      claim_count: 2,
      recovery_count: 1,
      event_count: 4,
      invocation: { total: 1, running: 0, succeeded: 1, failed: 0, interrupted: 0, tools: ["workflow.state"] },
      workflows: [],
      artifact_count: 0,
      artifact_revision_count: 0,
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:01Z",
    };
    const list = buildChatMessageListFromHistory([
      { id: "h1", query: "q", result: "a", execution },
    ]);
    expect(list[1]).toMatchObject({ role: RoleTypes.ASSISTANT, execution });
  });

  it("renders observed external activity as the original user message only", () => {
    const list = buildChatMessageListFromHistory([
      { id: "h1", query: "查看有哪些知识库", result: "", external_user_only: true },
    ]);
    expect(list).toEqual([
      expect.objectContaining({ role: RoleTypes.USER, delta: "查看有哪些知识库" }),
    ]);
  });

  it("strips citation markers from the displayed user query by default", () => {
    const history = [
      { id: "h1", query: "<cite_message>ctx</cite_message>real question", result: "answer" },
    ];

    const list = buildChatMessageListFromHistory(history);

    expect(list[0]).toMatchObject({
      delta: "real question",
      cite_message: "ctx",
      cite_messages: ["ctx"],
    });
  });

  it("keeps the raw query when stripCitations is false", () => {
    const history = [
      { id: "h1", query: "<cite_message>ctx</cite_message>real question", result: "answer" },
    ];

    const list = buildChatMessageListFromHistory(history, { stripCitations: false });

    expect(list[0]!.delta).toBe("<cite_message>ctx</cite_message>real question");
  });

  it("marks the last assistant message as unfinished while isGenerating is true", () => {
    const history = [{ id: "h1", query: "q1", result: "a1" }];

    const list = buildChatMessageListFromHistory(history, { isGenerating: true });

    expect(list).toHaveLength(2);
    expect(list[1]).toMatchObject({
      role: RoleTypes.ASSISTANT,
      finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
    });
  });

  it("appends a placeholder user/assistant pair when isGenerating is true but there is no history yet", () => {
    const list = buildChatMessageListFromHistory([], { isGenerating: true });

    expect(list).toHaveLength(2);
    expect(list[0]).toMatchObject({ role: RoleTypes.USER, is_resumed: true });
    expect(list[1]).toMatchObject({
      role: RoleTypes.ASSISTANT,
      finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
    });
  });

  it("restores ask_pending state onto the assistant message", () => {
    const history = [
      {
        id: "h1",
        query: "q1",
        result: "a1",
        ask_pending: { question: "which one?" },
        ask_saved_answers: { a: "1" },
        ask_answered: true,
      },
    ];

    const list = buildChatMessageListFromHistory(history);
    const assistantMessage = list[1] as Record<string, unknown>;

    expect(assistantMessage.ask_pending).toEqual({ question: "which one?" });
    expect(assistantMessage.ask_saved_answers).toEqual({ a: "1" });
    expect(assistantMessage.ask_answered).toBe(true);
  });

  it("restores archived failure attempts before the latest answer", () => {
    const failedTerminal = {
      status: "failed",
      reason: "model_failure",
      code: "rate_limited",
      partial_output: true,
      diagnostic_id: "diagnostic-1",
    };
    const list = buildChatMessageListFromHistory([
      {
        id: "h1",
        query: "q1",
        result: "latest answer",
        failed_attempts: [
          {
            result: "partial answer",
            run_id: "run-failed-1",
            run_status: "failed",
            run_terminal: failedTerminal,
            model_route: {
              mode: "auto",
              provider_name: "Fast",
              model_name: "fast-free",
              reason: "simple_task",
            },
            create_time: "2026-09-01T00:00:00Z",
          },
        ],
      },
    ]);

    expect(list).toHaveLength(3);
    expect(list[1]).toMatchObject({
      role: RoleTypes.ASSISTANT,
      history_id: "h1:failed:run-failed-1",
      original_history_id: "h1",
      delta: "partial answer",
      run_terminal: failedTerminal,
      model_route: {
        mode: "auto",
        provider_name: "Fast",
        model_name: "fast-free",
        reason: "simple_task",
      },
      archived_failure: true,
    });
    expect(list[2]).toMatchObject({
      role: RoleTypes.ASSISTANT,
      history_id: "h1",
      delta: "latest answer",
    });
  });

  it("separates image and file inputs into their respective arrays", () => {
    const history = [
      {
        id: "h1",
        query: "with attachments",
        result: "ok",
        input: [
          { input_type: "text", text: "with attachments" },
          { input_type: "image", input_base64: "b64", file_id: "img1" },
          { input_type: "file", uri: "s3://bucket/report.pdf", file_id: "file1" },
        ],
      },
    ];

    const list = buildChatMessageListFromHistory(history);
    const userMessage = list[0] as Record<string, unknown>;

    expect(userMessage.images).toEqual([{ base64: "b64", uid: "img1" }]);
    expect(userMessage.files).toEqual([{ name: "report.pdf", uid: "file1" }]);
  });
});

describe("mergeConversationTrailIntoMessageList", () => {
  it("returns the original list unchanged when there are no trail items", () => {
    const list = [{ history_id: "h1" }];
    expect(mergeConversationTrailIntoMessageList(list, null)).toBe(list);
    expect(mergeConversationTrailIntoMessageList(list, [])).toBe(list);
  });

  it("merges trail metadata onto matching user messages by history_id", () => {
    const list = [{ history_id: "h1", seq: 1, role: RoleTypes.USER }];
    const trail = [{ history_id: "h1", seq: 5, depth: 2, source: "search" }];

    const merged = mergeConversationTrailIntoMessageList(list, trail as never);

    expect(merged).not.toBe(list);
    expect(merged[0]).toMatchObject({ seq: 5, trail_depth: 2, trail_source: "search" });
  });

  it("leaves assistant messages untouched even when a matching trail entry exists", () => {
    const list = [{ history_id: "h1", seq: 1, role: RoleTypes.ASSISTANT }];
    const trail = [{ history_id: "h1", seq: 5, depth: 2 }];

    const merged = mergeConversationTrailIntoMessageList(list, trail as never);

    expect(merged[0]).toEqual(list[0]);
  });

  it("returns the same list reference when nothing actually changes", () => {
    const list = [{
      history_id: "h1",
      role: RoleTypes.USER,
      seq: 5,
      trail_depth: 0,
      trail_parent_history_id: "",
      trail_source: "",
      trail_summary: "",
      trail_question: "",
      cite_history_ids: undefined,
    }];
    const trail = [{ history_id: "h1", seq: 5 }];

    const merged = mergeConversationTrailIntoMessageList(list, trail as never);

    expect(merged).toBe(list);
  });

  it("leaves items without a matching trail entry untouched", () => {
    const list = [{ history_id: "h1", role: RoleTypes.USER }, { history_id: "h2", role: RoleTypes.USER }];
    const trail = [{ history_id: "h1", seq: 9 }];

    const merged = mergeConversationTrailIntoMessageList(list, trail as never);

    expect(merged[1]).toEqual({ history_id: "h2", role: RoleTypes.USER });
  });
});

describe("mergeChatMessageLists", () => {
  it("prefers the cached list when it has content and no persisted overlap", () => {
    const api = [{ id: "api-1" }];
    const cached = [{ id: "cache-1" }];
    expect(mergeChatMessageLists(api, cached)).toBe(cached);
  });

  it("falls back to the api list when the cache is empty", () => {
    const api = [{ id: "api-1" }];
    expect(mergeChatMessageLists(api, [])).toBe(api);
    expect(mergeChatMessageLists(api, null)).toBe(api);
  });

  it("defaults to an empty array when both inputs are missing", () => {
    expect(mergeChatMessageLists(undefined, undefined)).toEqual([]);
  });

  it("deduplicates persisted ask_user turns while keeping unpersisted tail messages", () => {
    const api = [
      { role: RoleTypes.USER, history_id: "h1", delta: "run workflow" },
      {
        role: RoleTypes.ASSISTANT,
        history_id: "h1",
        delta: "need approval",
        ask_pending: { question: "approve?" },
      },
    ];
    const cached = [
      { role: RoleTypes.USER, history_id: "h1", delta: "run workflow" },
      {
        role: RoleTypes.ASSISTANT,
        history_id: "h1",
        delta: "need approval",
        ask_pending: { question: "approve?" },
      },
      { role: RoleTypes.USER, delta: "draft answer" },
    ];

    expect(mergeChatMessageLists(api, cached)).toEqual([
      ...api,
      { role: RoleTypes.USER, delta: "draft answer" },
    ]);
  });

  it("drops unkeyed cached duplicates once the same turn is present in api history", () => {
    const api = [
      { role: RoleTypes.USER, history_id: "h1", delta: "run workflow" },
      {
        role: RoleTypes.ASSISTANT,
        history_id: "h1",
        delta: "need approval",
        ask_pending: { question: "approve?" },
      },
    ];
    const cached = [
      { role: RoleTypes.USER, history_id: "h1", delta: "run workflow" },
      { role: RoleTypes.ASSISTANT, history_id: "h1", delta: "need approval" },
      { role: RoleTypes.USER, delta: "run workflow" },
      {
        role: RoleTypes.ASSISTANT,
        delta: "need approval",
        ask_pending: { question: "approve?" },
      },
    ];

    expect(mergeChatMessageLists(api, cached)).toEqual(api);
  });
});
