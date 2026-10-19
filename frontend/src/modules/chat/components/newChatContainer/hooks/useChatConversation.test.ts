import { act, fireEvent, render, renderHook, screen, waitFor } from "@testing-library/react";
import { createElement, createRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ConfigProvider, message, Modal } from "antd";
import {
  ChatConversationsRequestActionEnum,
  ChatConversationsResponseFinishReasonEnum,
} from "@/api/generated/chatbot-client";
import { RoleTypes } from "@/modules/chat/constants/common";
import { CHAT_WORKFLOW_STEP_FEEDBACK_EVENT } from "@/modules/chat/constants/chat";
import type { ChatInputImperativeProps } from "../../ChatInput";
import { useChatConversation } from "./useChatConversation";
import { useTaskCenterStore } from "@/modules/chat/store/taskCenter";
import { ordinary } from "@/modules/chat/components/TaskCenter/ordinaryTestFixtures";
import { buildChatMessageListFromHistory } from "@/modules/chat/utils/message";
import { streamManager } from "@/modules/chat/utils/StreamManager";
import { emitConversationActivity, emitConversationListRefresh } from "@/modules/chat/utils/conversationActivity";
import type { ChatModelSwitchRecord } from "@/api/generated/core-client";
import zhCN from "@/i18n/locales/zh-CN";
import enUS from "@/i18n/locales/en-US";

const {
  listConversationsMock,
  getHistoryMock,
  listToolAssetsMock,
  waitForRuntimeCapabilityMock,
  scrollMock,
} = vi.hoisted(() => ({
  listConversationsMock: vi.fn(),
  getHistoryMock: vi.fn(),
  listToolAssetsMock: vi.fn(),
  waitForRuntimeCapabilityMock: vi.fn(),
  scrollMock: {
    chatContentRef: { current: null },
    isMouseScrollingRef: { current: false },
    showScrollButton: false,
    inputHeight: 120,
    unreadCount: 0,
    trackNewContent: vi.fn(),
    resetUnread: vi.fn(),
    pauseFollowing: vi.fn(),
    scrollToEnd: vi.fn(),
    scrollToEndImmediately: vi.fn(),
    handleScroll: vi.fn(),
    handleToBottom: vi.fn(),
    handleInputHeightChange: vi.fn(),
  },
}));

vi.mock("@/runtime/readiness", () => ({
  waitForRuntimeCapability: waitForRuntimeCapabilityMock,
}));

vi.mock("@/modules/memory/toolApi", () => ({
  listToolAssets: listToolAssetsMock,
}));

vi.mock("antd", async (importOriginal) => ({
  notification: (await importOriginal<typeof import("antd")>()).notification,
  ConfigProvider: (await importOriginal<typeof import("antd")>()).ConfigProvider,
  Button: "button",
  message: {
    error: vi.fn(),
    warning: vi.fn(),
  },
  Modal: { confirm: vi.fn() },
}));

vi.mock("react-router-dom", () => ({
  useNavigate: () => vi.fn(),
}));

vi.mock("../../ImageUpload", () => ({
  allowedImageTypes: [".png", ".jpg", ".jpeg"],
}));

vi.mock("@/modules/chat/utils/request", () => ({
  ChatServiceApi: () => ({
    conversationServiceListConversations: listConversationsMock,
    conversationServiceGetConversationHistory: getHistoryMock,
  }),
}));

vi.mock("@/modules/chat/utils/conversationActivity", () => ({
  emitConversationActivity: vi.fn(),
  emitConversationListRefresh: vi.fn(),
}));

vi.mock("./useChatScroll", () => ({ useChatScroll: () => scrollMock }));

function renderConversation(
  overrides: Partial<Parameters<typeof useChatConversation>[0]> = {},
) {
  const options: Parameters<typeof useChatConversation>[0] = {
    canChat: true,
    onOpenSSE: vi.fn(),
    setIsChatContent: vi.fn(),
    clearStorePendingMessage: vi.fn(),
    clearCiteMessages: vi.fn(),
    chatInputRef: createRef<ChatInputImperativeProps>(),
    thinkingCollapseMap: new Map(),
    getUserEdit: () => undefined,
    t: (key) => key,
    ...overrides,
  };
  return renderHook(() => useChatConversation(options));
}

function createMockStream() {
  const listeners = new Map<string, (event: any) => void>();
  const stream = {
    addEventListener: vi.fn((type: string, listener: (event: any) => void) => {
      listeners.set(type, listener);
    }),
    removeEventListener: vi.fn(),
    close: vi.fn(),
  };
  return { stream, listeners };
}

function modelSwitch(overrides: Partial<ChatModelSwitchRecord> = {}): ChatModelSwitchRecord {
  return {
    event_id: "switch-1", switch_index: 1,
    reason_code: "rate_limited", context_compacted: false,
    from_max_input_tokens: null, to_max_input_tokens: null,
    from: { candidate_id: "own:a", source: "own", model_id: "a", model_name: "原模型 A", provider_group_id: "provider-a", provider_name: "服务 A" },
    to: { candidate_id: "own:b", source: "own", model_id: "b", model_name: "替代模型 B", provider_group_id: "provider-b", provider_name: "服务 B" },
    ...overrides,
  };
}

function modelFallbackTranslate(messages: typeof zhCN | typeof enUS) {
  return (key: string, values: Record<string, unknown> = {}) => {
    const value = key.split(".").reduce<unknown>((result, part) =>
      result && typeof result === "object" ? (result as Record<string, unknown>)[part] : undefined, messages);
    return String(typeof value === "string" ? value : values.defaultValue ?? key)
      .replace(/{{(\w+)}}/g, (_, name: string) => String(values[name] ?? ""));
  };
}

function createPreparedStream(clientConversationId: string) {
  const { stream, listeners } = createMockStream();
  const onOpenSSE = vi.fn(
    (
      _input: unknown,
      _action: unknown,
      _callbacks: unknown,
      extras?: Record<string, unknown>,
    ) => {
      const prepareClientConversationId = extras?.__prepareClientConversationId;
      if (typeof prepareClientConversationId === "function") {
        prepareClientConversationId(clientConversationId);
      }
      return stream;
    },
  );
  return { listeners, onOpenSSE };
}

describe("useChatConversation regeneration recovery", () => {
  it("replays the exact mail confirmation only on explicit failed-run retry", async () => {
    const { stream, listeners } = createMockStream();
    const onOpenSSE = vi.fn(() => stream);
    const { result } = renderConversation({ onOpenSSE });
    const confirmation = { mail_draft_confirm_id: "draft-retry", mail_draft_confirm_revision: 3,
      mail_draft_patch: { subject: "Edited subject", body: "Edited body", to: "recipient@example.com",
        attachment_paths: ["/workspace/report.pdf"], attachments: [{ filename: "local.txt", content_base64: "bG9jYWw=" }] } };
    const original = structuredClone(confirmation);
    act(() => result.current.replaceMessageList("mail-retry-conversation", []));
    await act(async () => { expect(await result.current.sendMessage({ text: "Confirm mail", ...confirmation })).toBe(true); });
    await act(async () => {
      listeners.get("message")?.({ data: JSON.stringify({ result: {
        conversation_id: "mail-retry-conversation", history_id: "mail-history", seq: 1,
        finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
        runtime_event: { type: "run_finished", run_id: "mail-run", data: { status: "failed", reason: "model_failure", code: "transport_error", partial_output: false } },
      } }) });
    });
    expect(onOpenSSE).toHaveBeenCalledTimes(1);
    expect(result.current.messageList.at(-1)?.run_status).toBe("failed");
    expect(JSON.stringify(result.current.messageList)).not.toContain("bG9jYWw=");
    confirmation.mail_draft_patch.subject = "Unconfirmed later edit";
    const originalMessages = result.current.messageList;
    act(() => result.current.replaceMessageList("different-conversation", originalMessages));
    await act(async () => { expect(await result.current.regenerate()).toBe(false); });
    expect(onOpenSSE).toHaveBeenCalledTimes(1);
    for (const changedUser of [
      { ...originalMessages[0], delta: "Changed confirmation text" },
      { ...originalMessages[0], mail_confirmation: { ...originalMessages[0].mail_confirmation, mail_draft_confirm_revision: 4 } },
    ]) {
      act(() => result.current.replaceMessageList("mail-retry-conversation", [changedUser, originalMessages[1]]));
      await act(async () => { expect(await result.current.regenerate()).toBe(false); });
      expect(onOpenSSE).toHaveBeenCalledTimes(1);
    }
    act(() => result.current.replaceMessageList("mail-retry-conversation", originalMessages));
    await act(async () => { expect(await result.current.regenerate()).toBe(true); });
    expect(onOpenSSE).toHaveBeenLastCalledWith(expect.any(Array),
      ChatConversationsRequestActionEnum.ChatActionRegeneration, {}, expect.objectContaining(original));
  });

  it("drops a locally refused mail confirmation and returns control to the card", async () => {
    const { stream, listeners } = createMockStream();
    const onOpenSSE = vi.fn(() => stream);
    const onMailSubmissionRefused = vi.fn();
    const { result } = renderConversation({ onOpenSSE });
    const card = { role: RoleTypes.ASSISTANT, history_id: "card", ask_pending: { ask_id: "mail-card" } };
    act(() => result.current.replaceMessageList("mail-offline", [card]));
    await act(async () => { expect(await result.current.sendMessage({ text: "Confirm mail",
      mail_draft_confirm_id: "draft", mail_draft_confirm_revision: 1, onMailSubmissionRefused })).toBe(true); });
    expect(result.current.messageList).toHaveLength(3);
    await act(async () => listeners.get("error")?.({ type: "error", status: 0, reason: "offline" }));
    expect(onMailSubmissionRefused).toHaveBeenCalledWith("offline");
    expect(result.current.messageList).toEqual([card]);
    expect(result.current.loading).toBe(false);
    expect(getHistoryMock).not.toHaveBeenCalled();
    expect(onOpenSSE).toHaveBeenCalledTimes(1);
  });

  it.each(["failed", "interrupted", "completed"])("does not replay a restored mail confirmation without its payload (%s)", async (status) => {
    const onOpenSSE = vi.fn(() => createMockStream().stream);
    const { result } = renderConversation({ onOpenSSE });
    const restored = buildChatMessageListFromHistory([{ id: "mail-history", query: "Confirm mail",
      run_status: status, mail_confirmation: { mail_draft_confirm_id: "draft-retry", mail_draft_confirm_revision: 3 },
    }] as any);
    act(() => result.current.replaceMessageList("mail-restored", restored));
    await act(async () => { expect(await result.current.regenerate()).toBe(false); });
    expect(onOpenSSE).not.toHaveBeenCalled();
    expect(message.warning).toHaveBeenCalledWith("chat.mailDraft.retryNeedsPreview");
  });

  beforeEach(() => {
    sessionStorage.clear();
    getHistoryMock.mockReset();
    scrollMock.isMouseScrollingRef.current = false;
    scrollMock.scrollToEnd.mockClear();
    scrollMock.scrollToEndImmediately.mockClear();
    scrollMock.trackNewContent.mockClear();
    listConversationsMock.mockReset();
    listConversationsMock.mockResolvedValue({ data: { conversations: [] } });
    listToolAssetsMock.mockReset();
    listToolAssetsMock.mockResolvedValue([{
      id: "image_generator",
      isAvailable: true,
    }]);
    waitForRuntimeCapabilityMock.mockReset();
    waitForRuntimeCapabilityMock.mockResolvedValue(undefined);
    vi.mocked(Modal.confirm).mockClear();
    useTaskCenterStore.setState({
      activeConversationId: "",
      tasksByConversation: {},
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it.each([zhCN, enUS])("reuses the five-second notice for live task switches without announcing snapshot history", async (messages) => {
    vi.useFakeTimers();
    const { result } = renderConversation({ t: modelFallbackTranslate(messages) });
    render(createElement(ConfigProvider, { theme: { token: { motion: false } } }, result.current.modelFallbackNotificationHolder));
    act(() => result.current.replaceMessageList("task-notice", []));
    const emit = (record: ChatModelSwitchRecord, live: boolean, conversationId = "task-notice") =>
      window.dispatchEvent(new CustomEvent("lazymind:chat-model-switch", { detail: { conversationId, runId: "execution", record, live } }));
    const record = modelSwitch({ from_max_input_tokens: 131072, to_max_input_tokens: 65536, context_compacted: true });
    act(() => { emit(record, false); emit(record, true); });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    const next = { ...record, event_id: "task-switch-2", switch_index: 2 };
    act(() => { emit(next, true, "other-conversation"); });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    await act(async () => { emit(next, true); });
    expect(screen.getByRole("status")).toHaveTextContent("原模型 A (128K)");
    expect(screen.getByRole("status")).toHaveTextContent("替代模型 B (64K)");
    expect(screen.getByRole("status")).toHaveTextContent(messages.chat.modelFallbackSucceeded);
    await act(async () => { await vi.advanceTimersByTimeAsync(4000); emit(next, true); emit(next, false); });
    await act(async () => { await vi.advanceTimersByTimeAsync(999); });
    expect(screen.getByRole("status")).toBeInTheDocument();
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    act(() => result.current.replaceMessageList("other-conversation", []));
    act(() => { emit({ ...next, event_id: "late-task-switch" }, true); });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("keeps committed SDK thinking and tool previews consistent through rollback and export", async () => {
    const live = createMockStream();
    const cached = createMockStream();
    const cold = createMockStream();
    const { result } = renderConversation({ onOpenSSE: vi.fn(() => live.stream),
      onOpenResumeSSE: vi.fn().mockReturnValueOnce(cached.stream).mockReturnValueOnce(cold.stream) });
    act(() => result.current.replaceMessageList("thinking-output", []));
    await act(async () => { await result.current.sendMessage({ text: "task" }); });
    const emit = (frame: object) => live.listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "thinking-output", history_id: "thinking-history", ...frame,
    } }) });
    const cursor = { run_id: "thinking-run", call_index: 1 };
    const prefix = "KEPT PREFIX<tp>COMMITTED TOOL</tp><trp>COMMITTED RESULT</trp>";
    await act(async () => {
      emit({ model_output: { ...cursor, call_index: 0, phase: "commit" } });
      emit({ model_output: { ...cursor, phase: "begin" } });
      emit({ model_output: cursor, reasoning_content: "COMMITTED SDK THINK" });
      emit({ model_output: cursor, delta: "KEPT PREFIX" });
      emit({ model_output: { ...cursor, phase: "commit" } });
      emit({ delta: "<tp>COMMITTED TOOL</tp><trp>COMMITTED RESULT</trp>\nKEPT BODY" });
      emit({ model_output: { ...cursor, call_index: 2, phase: "begin" } });
      emit({ model_output: { ...cursor, call_index: 2 }, reasoning_content: "BAD THINK", delta: "BAD BODY" });
      emit({ model_output: { ...cursor, call_index: 2, phase: "rollback" }, delta_mode: "replace",
        delta: prefix + "\nKEPT BODY", reasoning_content: "COMMITTED SDK THINK", sources: [], exports: [] });
    });
    expect(result.current.messageList[1].delta.trim()).toBe("KEPT BODY");
    expect(result.current.messageList[1].reasoning_content).toContain("COMMITTED SDK THINK");
    expect(result.current.messageList[1].reasoning_content).toContain("COMMITTED TOOL");
    await act(async () => {
      emit({ model_output: { ...cursor, call_index: 3, phase: "begin" } });
      emit({ model_output: { ...cursor, call_index: 3 }, reasoning_content: "\nB SDK THINK" });
      emit({ model_output: { ...cursor, call_index: 3 }, delta: " B BODY" });
      emit({ model_output: { ...cursor, call_index: 3, phase: "commit" } });
      emit({ reasoning_content: "\nFINAL THINK" });
      emit({ delta_mode: "replace", delta: prefix + "FINAL BODY", exports: [] });
    });
    const finished = result.current.messageList[1];
    expect(finished.delta).toBe("FINAL BODY");
    expect(finished.reasoning_content).not.toContain("BAD");
    for (const marker of ["COMMITTED SDK THINK", "B SDK THINK", "FINAL THINK", "COMMITTED TOOL", "COMMITTED RESULT"]) {
      expect(finished.reasoning_content.split(marker)).toHaveLength(2);
    }
    const metadata = { ...cursor, call_index: 3, phase: "commit" as const };
    const restored = buildChatMessageListFromHistory([{ id: "thinking-history", query: "task", result: prefix + "FINAL BODY",
      reasoning_content: "COMMITTED SDK THINK\nB SDK THINK\nFINAL THINK", model_output: metadata, sources: [], exports: [] }]);
    expect(finished.delta).toBe(restored[1].delta);
    expect(finished.reasoning_content).toBe(restored[1].reasoning_content);
    act(() => result.current.replaceMessageList("thinking-output", restored));
    for (const resumed of [cached, cold]) {
      await act(async () => { await result.current.openResumeSSE("thinking-output"); });
      await act(async () => { resumed.listeners.get("message")?.({ data: JSON.stringify({ result: {
        conversation_id: "thinking-output", history_id: "thinking-history", model_output: metadata, delta_mode: "replace",
        delta: prefix + "FINAL BODY", reasoning_content: "COMMITTED SDK THINK\nB SDK THINK\nFINAL THINK", sources: [], exports: [],
      } }) }); });
      expect(result.current.messageList[1].delta).toBe(finished.delta);
      expect(result.current.messageList[1].reasoning_content).toBe(finished.reasoning_content);
    }
  });

  it("matches Core thinking block boundaries for think, text, then think within one Host call", async () => {
    const { stream, listeners } = createMockStream();
    const { result } = renderConversation({ onOpenSSE: vi.fn(() => stream) });
    act(() => result.current.replaceMessageList("thinking-blocks", []));
    await act(async () => { await result.current.sendMessage({ text: "task" }); });
    const cursor = { run_id: "thinking-run", call_index: 1 };
    const emit = (frame: object) => listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "thinking-blocks", history_id: "h", ...frame,
    } }) });
    await act(async () => {
      emit({ model_output: { ...cursor, call_index: 0, phase: "commit" } });
      emit({ model_output: { ...cursor, phase: "begin" } });
      emit({ model_output: cursor, reasoning_content: "FIRST" });
      emit({ model_output: cursor, reasoning_content: "  MORE" });
      emit({ model_output: cursor, delta: "body" });
      emit({ model_output: cursor, reasoning_content: "\nSECOND" });
      emit({ model_output: cursor, reasoning_content: "  TAIL" });
    });
    expect(result.current.messageList[1].reasoning_content).toBe("FIRST  MORE\nSECOND  TAIL");
  });

  it.each(["  MORE", "\nSECOND"])("continues canonical thinking from an open snapshot without inferring block state (%j)", async (delta) => {
    const { stream, listeners } = createMockStream();
    const { result } = renderConversation({ onOpenResumeSSE: vi.fn(() => stream) });
    const metadata = { run_id: "thinking-run", call_index: 1, phase: "begin" as const };
    act(() => result.current.replaceMessageList("thinking-resume", buildChatMessageListFromHistory([{
      id: "h", query: "task", result: "body", reasoning_content: "FIRST", model_output: metadata,
    }])));
    await act(async () => { await result.current.openResumeSSE("thinking-resume"); });
    const emit = (frame: object) => listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "thinking-resume", history_id: "h", ...frame,
    } }) });
    await act(async () => {
      emit({ model_output: metadata, delta_mode: "replace", delta: "body", reasoning_content: "FIRST", sources: [], exports: [] });
      emit({ model_output: { run_id: metadata.run_id, call_index: 1 }, reasoning_content: delta });
    });
    expect(result.current.messageList[1].delta).toBe("body");
    expect(result.current.messageList[1].raw_reasoning_content).toBe("FIRST" + delta);
    expect(result.current.messageList[1].reasoning_content).toBe("FIRST" + delta);
  });

  it.each([false, true])("restores only the failed model projection and keeps committed tools and artifacts (%s)", async (hasCommittedOutput) => {
    const { stream, listeners } = createMockStream();
    const { result } = renderConversation({ onOpenSSE: vi.fn(() => stream) });
    const artifact = { artifact_id: "real-artifact", history_id: "output-history", content_type: "text", value: { text: "saved tool result" } };
    act(() => {
      useTaskCenterStore.setState({ artifactsByConversation: { "output-conversation": [artifact as any] } });
      result.current.replaceMessageList("output-conversation", []);
    });
    await act(async () => { await result.current.sendMessage({ text: "task" }); });
    const emit = (frame: object) => listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "output-conversation", history_id: "output-history", ...frame,
    } }) });
    const cursor = { run_id: "output-run", call_index: 1 };
    const body = hasCommittedOutput ? "completed answer" : "";
    const think = hasCommittedOutput ? "completed tool preview" : "";
    const sources = hasCommittedOutput ? [{ title: "committed reference" }] : [];
    await act(async () => {
      emit({ model_output: { ...cursor, call_index: 0, phase: "commit" } });
      if (hasCommittedOutput) emit({ delta: body, reasoning_content: think, sources });
      emit({ model_output: { ...cursor, phase: "begin" } });
      emit({ model_output: cursor, reasoning_content: "BAD THINK" });
      emit({ model_output: cursor, delta: "BAD DRAFT [1](#source-bad)", sources: [{ title: "bad reference" }],
        exports: [{ export_id: "bad-preview", start: 0, end: 3 }] });
    });
    expect(result.current.messageList[1].delta).toContain("BAD DRAFT");
    const rollback = { model_output: { ...cursor, phase: "rollback" }, delta_mode: "replace", delta: body,
      reasoning_content: think, sources, exports: [] };
    await act(async () => { emit(rollback); });
    expect(result.current.messageList[1]).toMatchObject({ raw_delta: body, delta: body, reasoning_content: think, sources, exports: [] });
    await act(async () => {
      emit({ delta: " tool fact" });
      emit(rollback);
      emit({ model_output: cursor, delta: "LATE OLD" });
      emit({ model_output: { ...cursor, call_index: 2, phase: "begin" } });
      emit({ model_output: { ...cursor, call_index: 2 }, reasoning_content: (think ? "\n" : "") + "replacement reasoning" });
      emit({ model_output: { ...cursor, call_index: 2 }, delta: " replacement" });
      emit({ model_output: { ...cursor, call_index: 2, phase: "commit" } });
      emit(rollback);
    });
    expect(result.current.messageList[1]).toMatchObject({ delta: `${body} tool fact replacement`, reasoning_content: [think, "replacement reasoning"].filter(Boolean).join("\n"), sources, exports: [] });
    const finalExports = [{ export_id: "committed-export", start: 0, end: 5 }];
    await act(async () => { emit({ delta_mode: "replace", delta: "final committed export", exports: finalExports }); });
    expect(result.current.messageList[1]).toMatchObject({ delta: "final committed export", exports: finalExports, sources });
    expect(useTaskCenterStore.getState().artifactsByConversation["output-conversation"]).toEqual([artifact]);
  });

  it("keeps metadata-less replacement behavior on the legacy stream", async () => {
    const { stream, listeners } = createMockStream();
    const { result } = renderConversation({ onOpenSSE: vi.fn(() => stream) });
    act(() => result.current.replaceMessageList("legacy-output", [
      { role: RoleTypes.USER, delta: "task", history_id: "h" },
      { role: RoleTypes.ASSISTANT, delta: "old", raw_delta: "old", reasoning_content: "legacy reasoning", sources: [{ title: "legacy source" }] },
    ]));
    await act(async () => { await result.current.openSSE([], ChatConversationsRequestActionEnum.ChatActionNext); });
    await act(async () => { listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "legacy-output", delta_mode: "replace", delta: "legacy replacement", sources: [], reasoning_content: "",
    } }) }); });
    expect(result.current.messageList[1]).toMatchObject({ delta: "legacy replacement", reasoning_content: "legacy reasoning", sources: [{ title: "legacy source" }] });
  });

  it("refreshes model selection only for a new successful main conversation switch", async () => {
    const { stream, listeners } = createMockStream();
    const { result } = renderConversation({ onOpenSSE: vi.fn(() => stream) });
    render(createElement(ConfigProvider, { theme: { token: { motion: false } } }, result.current.modelFallbackNotificationHolder));
    act(() => result.current.replaceMessageList("selection-sync", []));
    await act(async () => { await result.current.sendMessage({ text: "task" }); });
    const refreshed = vi.fn();
    window.addEventListener("lazymind:chat-model-selection-changed", refreshed);
    try {
      const child = modelSwitch({ event_id: "child-switch" });
      act(() => window.dispatchEvent(new CustomEvent("lazymind:chat-model-switch", {
        detail: { conversationId: "selection-sync", runId: "child-run", record: child, live: true },
      })));
      expect(refreshed).not.toHaveBeenCalled();
      const record = modelSwitch();
      const emit = (type: string) => listeners.get("message")?.({ data: JSON.stringify({ result: {
        conversation_id: "selection-sync", history_id: "history", model_route: { mode: "fixed", model_id: "a", source: "own", selection_version: 1 },
        model_switches: [], runtime_event: { type, run_id: "run", event_id: record.event_id,
          data: { model_call_id: "call-a", attempt_count: 1, kind: "finish", has_semantic_output: true, finish: "stop" } },
      } }) });
      await act(async () => { emit("model_call_finished"); });
      expect(refreshed).not.toHaveBeenCalled();
      const success = modelSwitch({ event_id: "main-success" });
      const frame = { result: {
        conversation_id: "selection-sync", history_id: "history", model_route: { mode: "fixed", model_id: "b", source: "own", selection_version: 1 },
        model_switches: [success], runtime_event: { type: "model_switched", run_id: "run", event_id: success.event_id, data: success },
      } };
      await act(async () => {
        listeners.get("message")?.({ data: JSON.stringify(frame) });
        listeners.get("message")?.({ data: JSON.stringify(frame) });
      });
      expect(refreshed).toHaveBeenCalledTimes(1);
      expect(refreshed.mock.calls[0][0].detail).toEqual({ conversationId: "selection-sync" });
    } finally {
      window.removeEventListener("lazymind:chat-model-selection-changed", refreshed);
    }
  });

  it("refreshes a newly confirmed main selection from a silent resume snapshot once", async () => {
    const { stream, listeners } = createMockStream();
    const { result } = renderConversation({ onOpenResumeSSE: vi.fn(() => stream) });
    render(createElement(ConfigProvider, { theme: { token: { motion: false } } }, result.current.modelFallbackNotificationHolder));
    act(() => result.current.replaceMessageList("selection-resume", [
      { role: RoleTypes.USER, delta: "task", history_id: "history" },
      { role: RoleTypes.ASSISTANT, delta: "partial", history_id: "history", model_switches: [modelSwitch()] },
    ]));
    await act(async () => { await result.current.openResumeSSE("selection-resume"); });
    const refreshed = vi.fn();
    window.addEventListener("lazymind:chat-model-selection-changed", refreshed);
    try {
      const frame = { result: { conversation_id: "selection-resume", history_id: "history", delta_mode: "replace", delta: "partial",
        model_route: { mode: "fixed", model_id: "b", source: "own", selection_version: 1 }, model_switches: [modelSwitch()],
      } };
      await act(async () => {
        listeners.get("message")?.({ data: JSON.stringify(frame) });
        listeners.get("message")?.({ data: JSON.stringify(frame) });
      });
      expect(refreshed).toHaveBeenCalledTimes(1);
      expect(screen.queryByRole("status")).not.toBeInTheDocument();
    } finally {
      window.removeEventListener("lazymind:chat-model-selection-changed", refreshed);
    }
  });

  it.each([zhCN, enUS])("announces only successful Host switches in one five-second notification", async (messages) => {
    vi.useFakeTimers();
    const { stream, listeners } = createMockStream();
    const { result } = renderConversation({ onOpenSSE: vi.fn(() => stream), t: modelFallbackTranslate(messages) });
    render(createElement(ConfigProvider, { theme: { token: { motion: false } } }, result.current.modelFallbackNotificationHolder));
    act(() => result.current.replaceMessageList("fallback-conversation", []));
    await act(async () => { await result.current.sendMessage({ text: "continue the task" }); });
    const emit = (frame: object) => listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "fallback-conversation", history_id: "fallback-history", ...frame,
    } }) });
    const record = modelSwitch({ from_max_input_tokens: 131072, to_max_input_tokens: 65536, context_compacted: true });
    const originalRoute = { mode: "fixed", model_id: "a", model_name: "原模型 A" };
    await act(async () => { emit({ model_route: originalRoute, model_switches: [{ ...record, event_id: "saved-switch" }],
      runtime_event: { type: "model_call_finished", run_id: "fallback-run", event_id: "sdk-finish", data: { model_call_id: "sdk-call", kind: "finish" } } }); });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(result.current.messageList[result.current.messageList.length - 1]?.model_route).toEqual(originalRoute);

    const successfulRoute = { mode: "fixed", model_id: "b", model_name: "替代模型 B" };
    const confirmed = { model_switches: [record], model_route: successfulRoute,
      runtime_event: { type: "model_switched", run_id: "fallback-run", event_id: record.event_id, data: record } };
    await act(async () => { emit(confirmed); });
    expect(result.current.isStreaming).toBe(true);
    expect(result.current.messageList[result.current.messageList.length - 1]?.model_route).toEqual(successfulRoute);
    expect(screen.getByRole("status")).toHaveTextContent(messages.chat.modelFallbackSucceeded);
    expect(screen.getByRole("status").closest(".ant-notification-bottomRight")).not.toBeNull();
    expect(screen.getByRole("status")).toHaveTextContent("原模型 A");
    expect(screen.getByRole("status")).toHaveTextContent("替代模型 B");
    expect(screen.getByRole("status")).toHaveTextContent("原模型 A (128K)");
    expect(screen.getByRole("status")).toHaveTextContent("替代模型 B (64K)");
    const adaptedText = messages === zhCN ? "上下文已适配" : "Context adapted";
    expect(screen.getByRole("status")).toHaveTextContent(adaptedText);
    expect(screen.getByRole("status")).toHaveTextContent(messages.chat.runStatus.codes.rate_limited);

    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    const second = modelSwitch({ event_id: "switch-2", switch_index: 2, from: record.to,
      from_max_input_tokens: 65536, to_max_input_tokens: 32768,
      to: { candidate_id: "own:c", source: "own", model_id: "c", model_name: "替代模型 C", provider_group_id: "provider-c" } });
    const nextSwitch = { model_switches: [record, second],
      runtime_event: { type: "model_switched", run_id: "fallback-run", event_id: second.event_id, data: second } };
    await act(async () => { emit(nextSwitch); });
    expect(screen.getAllByRole("status")).toHaveLength(1);
    expect(screen.getByRole("status")).toHaveTextContent(messages.chat.modelFallbackSucceeded);
    expect(screen.getByRole("status")).toHaveTextContent("替代模型 B");
    expect(screen.getByRole("status")).toHaveTextContent("替代模型 C");
    expect(screen.getByRole("status")).not.toHaveTextContent(adaptedText);
    fireEvent.mouseEnter(screen.getByRole("status"));
    await act(async () => { emit(confirmed); });
    expect(screen.getByRole("status")).toHaveTextContent("替代模型 C");
    await act(async () => { await vi.advanceTimersByTimeAsync(4000); emit(nextSwitch); });
    await act(async () => { await vi.advanceTimersByTimeAsync(999); });
    expect(screen.getByRole("status")).toHaveTextContent(messages.chat.modelFallbackSucceeded);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("restores history and snapshots silently and announces a new successful switch without SDK fields", async () => {
    vi.useFakeTimers();
    const { stream, listeners } = createMockStream();
    const { result } = renderConversation({ onOpenResumeSSE: vi.fn(() => stream), t: modelFallbackTranslate(zhCN) });
    render(createElement(ConfigProvider, { theme: { token: { motion: false } } }, result.current.modelFallbackNotificationHolder));
    const prior = modelSwitch({ from_max_input_tokens: 131072, to_max_input_tokens: 65536, context_compacted: true });
    act(() => result.current.replaceMessageList("fallback-resume", [
      { role: RoleTypes.USER, delta: "question", history_id: "history" },
      { role: RoleTypes.ASSISTANT, delta: "partial", history_id: "history", model_switches: [prior] },
    ]));
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    await act(async () => { await result.current.openResumeSSE("fallback-resume"); });
    const emit = (frame: object) => listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "fallback-resume", history_id: "history", ...frame,
    } }) });
    await act(async () => { emit({ delta_mode: "replace", delta: "partial", model_switches: [prior] }); });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    await act(async () => { emit({ model_switches: [prior], runtime_event: {
      type: "model_switched", run_id: "old-run", event_id: prior.event_id, data: prior,
    } }); });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    const record = modelSwitch({ event_id: "initial-switch", reason_code: "model_config_unavailable",
      from: { candidate_id: "own:removed", source: "own", model_id: "removed-model", provider_group_id: "" } });
    await act(async () => { emit({ model_switches: [prior, record], runtime_event: {
      type: "model_switched", run_id: "new-run", event_id: record.event_id, data: record,
    } }); });
    expect(result.current.isStreaming).toBe(true);
    expect(screen.getByRole("status")).toHaveTextContent("removed-model");
    expect(screen.getByRole("status")).toHaveTextContent(zhCN.chat.modelFallbackReason.model_config_unavailable);
    expect(screen.getAllByRole("status")).toHaveLength(1);
    expect(screen.getByRole("status")).toHaveTextContent(zhCN.chat.modelFallbackSucceeded);
  });

  it.each([zhCN, enUS])("shows unknown and exact declared windows without claiming adaptation", async (messages) => {
    const { stream, listeners } = createMockStream();
    const { result } = renderConversation({ onOpenSSE: vi.fn(() => stream), t: modelFallbackTranslate(messages) });
    render(createElement(ConfigProvider, { theme: { token: { motion: false } } }, result.current.modelFallbackNotificationHolder));
    act(() => result.current.replaceMessageList("window-conversation", []));
    await act(async () => { await result.current.sendMessage({ text: "task" }); });
    const unknown = messages === zhCN ? "窗口未知" : "window unknown";
    const cases = [
      { from: null, to: 65536, fromLabel: unknown, toLabel: "64K" },
      { from: 131072, to: null, fromLabel: "128K", toLabel: unknown },
      { from: undefined, to: undefined, fromLabel: unknown, toLabel: unknown },
      { from: 131073, to: 65535, fromLabel: "131073", toLabel: "65535" },
    ];
    for (const [index, window] of cases.entries()) {
      const record = modelSwitch({ event_id: `window-${index}`, switch_index: index + 1,
        from_max_input_tokens: window.from, to_max_input_tokens: window.to });
      await act(async () => { listeners.get("message")?.({ data: JSON.stringify({ result: {
        conversation_id: "window-conversation", history_id: "history", model_switches: [record],
        runtime_event: { type: "model_switched", run_id: "window-run", event_id: record.event_id, data: record },
      } }) }); });
      expect(screen.getByRole("status")).toHaveTextContent(`原模型 A (${window.fromLabel})`);
      expect(screen.getByRole("status")).toHaveTextContent(`替代模型 B (${window.toLabel})`);
      expect(screen.getByRole("status")).not.toHaveTextContent(messages === zhCN ? "上下文已适配" : "Context adapted");
    }
  });

  it.each([false, true])("retains exhaustion after zero or more successful switches (%s)", async (hasSwitch) => {
    const { stream, listeners } = createMockStream();
    const { result } = renderConversation({ onOpenSSE: vi.fn(() => stream), t: modelFallbackTranslate(zhCN) });
    render(createElement(ConfigProvider, { theme: { token: { motion: false } } }, result.current.modelFallbackNotificationHolder));
    act(() => result.current.replaceMessageList("exhausted-conversation", []));
    await act(async () => { await result.current.sendMessage({ text: "task" }); });
    const emit = (frame: object) => listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "exhausted-conversation", history_id: "history", ...frame,
    } }) });
    const records = hasSwitch ? [modelSwitch()] : [];
    if (hasSwitch) {
      await act(async () => { emit({ model_switches: records, runtime_event: {
        type: "model_switched", run_id: "run", event_id: records[0].event_id, data: records[0],
      } }); });
      expect(screen.getByRole("status")).toHaveTextContent(zhCN.chat.modelFallbackSucceeded);
    }
    await act(async () => { emit({ model_switches: records, runtime_event: {
      type: "run_finished", run_id: "run", event_id: "exhausted", data: {
        status: "failed", reason: "runtime_failure", code: "model_fallback_exhausted", partial_output: false,
      },
    } }); });
    expect(result.current.isStreaming).toBe(false);
    expect(result.current.messageList[result.current.messageList.length - 1]).toMatchObject({
      model_switches: records, run_terminal: { code: "model_fallback_exhausted" },
    });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it.each([false, true])("clears the old notice and ignores late frames when switching conversations (%s)", async (newChat) => {
    const { stream, listeners } = createMockStream();
    const { result } = renderConversation({ onOpenSSE: vi.fn(() => stream), t: modelFallbackTranslate(zhCN) });
    render(createElement(ConfigProvider, { theme: { token: { motion: false } } }, result.current.modelFallbackNotificationHolder));
    act(() => result.current.replaceMessageList("conversation-a", []));
    await act(async () => { await result.current.sendMessage({ text: "task" }); });
    const emit = (record: ChatModelSwitchRecord) => listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "conversation-a", history_id: "history", model_switches: [record],
      runtime_event: { type: "model_switched", run_id: "run-a", event_id: record.event_id, data: record },
    } }) });
    await act(async () => { emit(modelSwitch()); });
    expect(screen.getByRole("status")).toHaveTextContent(zhCN.chat.modelFallbackSucceeded);
    act(() => {
      if (newChat) result.current.createNewChat();
      else result.current.replaceMessageList("conversation-b", []);
    });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    await act(async () => { emit(modelSwitch({ event_id: "late-switch" })); });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it.each([false, true])("restores the authoritative deletion decision (%s) after HTTP rejection", async (answered) => {
    const { stream, listeners } = createMockStream();
    const pending = {
      ask_id: "delete-card", user_env_delete: { id: "env", name: "test_api_key" },
      questions: [{ text: "Delete?", type: "boolean", choices: ["__ask_user_yes__", "__ask_user_no__"] }],
    };
    const savedAnswers = { 0: { type: "boolean", value: "__ask_user_no__" } };
    getHistoryMock.mockResolvedValue({ data: { history: [{
      id: "history-card", query: "Delete variable", answer: "", seq: 1,
      ask_pending: pending, ask_answered: answered,
    }] } });
    const { result } = renderConversation({ onOpenSSE: vi.fn().mockReturnValue(stream) });
    act(() => result.current.replaceMessageList("delete-conversation", [{
      role: RoleTypes.ASSISTANT, history_id: "history-card", ask_pending: pending, ask_saved_answers: savedAnswers,
    }]));
    await act(async () => { await result.current.sendMessage({
      text: "No", ask_answers_structured: { ask_id: pending.ask_id, questions: [] },
    }); });
    expect(result.current.messageList).toHaveLength(3);
    await act(async () => {
      listeners.get("error")?.({ type: "error", status: 409, data: "rejected" });
    });
    expect(getHistoryMock).toHaveBeenCalledWith({ name: "delete-conversation" });
    const card = result.current.messageList[result.current.messageList.length - 1];
    expect(card.ask_pending.ask_id).toBe("delete-card");
    expect(Boolean(card.ask_answered)).toBe(answered);
    if (!answered) expect(card.ask_saved_answers).toEqual(savedAnswers);
    expect(result.current.messageList).toHaveLength(2);
    expect(result.current.loading).toBe(false);
    expect(message.error).toHaveBeenCalled();
  });

  it.each([false, true])("preserves older loaded history after deletion rejection (consumed=%s)", async (answered) => {
    const { stream, listeners } = createMockStream();
    const pending = { ask_id: "delete-card", user_env_delete: { id: "env", name: "test_api_key" }, questions: [] };
    const history = Array.from({ length: 40 }, (_, index) => ({
      id: `history-${index + 1}`, seq: index + 1,
      query: `question-${index + 1}`, result: `answer-${index + 1}`,
      ...(index === 39 ? { ask_pending: pending } : {}),
    }));
    const previousMessages = buildChatMessageListFromHistory([...history].reverse());
    const latest = history.map((record) => record.id === "history-40"
      ? { ...record, ask_answered: answered }
      : record);
    getHistoryMock.mockResolvedValue({ data: { history: latest.slice(-20).reverse(), next_page_token: "20" } });
    const { result } = renderConversation({ onOpenSSE: vi.fn().mockReturnValue(stream) });
    act(() => result.current.replaceMessageList("long-delete-chat", previousMessages));
    await act(async () => { await result.current.sendMessage({
      text: "No", ask_answers_structured: { ask_id: pending.ask_id, questions: [] },
    }); });
    expect(result.current.messageList).toHaveLength(82);
    await act(async () => listeners.get("error")?.({ type: "error", status: 409 }));
    expect(result.current.messageList).toHaveLength(80);
    expect(result.current.messageList.map((item) => `${item.role}:${item.history_id}`))
      .toEqual(previousMessages.map((item) => `${item.role}:${item.history_id}`));
    expect(result.current.messageList.slice(0, 40)).toEqual(previousMessages.slice(0, 40));
    expect(Boolean(result.current.messageList[79].ask_answered)).toBe(answered);
    expect(result.current.conversationMessagesCache.current.get("long-delete-chat"))
      .toEqual(result.current.messageList);
  });

  it.each([
    ["http", "before_error"],
    ["http", "during_recovery"],
    ["open_failure", "before_error"],
    ["open_failure", "during_recovery"],
  ])("retains pages loaded %s/%s without keeping the failed submission", async (failure, timing) => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const { stream, listeners } = createMockStream();
    const pending = { ask_id: "delete-card", user_env_delete: { id: "env", name: "test_api_key" }, questions: [] };
    const history = Array.from({ length: 40 }, (_, index) => ({
      id: `history-${index + 1}`, seq: index + 1,
      query: `question-${index + 1}`, result: `answer-${index + 1}`,
      ...(index === 39 ? { ask_pending: pending } : {}),
    }));
    let resolveHistory!: (value: unknown) => void;
    getHistoryMock.mockImplementation(() => new Promise((resolve) => { resolveHistory = resolve; }));
    let rejectOpen!: (error: Error) => void;
    const onOpenSSE = vi.fn(() => failure === "http"
      ? stream : new Promise<typeof stream>((_, reject) => { rejectOpen = reject; }));
    const { result } = renderConversation({ onOpenSSE });
    act(() => result.current.replaceMessageList("concurrent-pagination", buildChatMessageListFromHistory(history.slice(-20).reverse())));
    let submitted!: Promise<boolean>;
    act(() => { submitted = result.current.sendMessage({
      text: "No", ask_answers_structured: { ask_id: pending.ask_id, questions: [] },
    }); });
    await waitFor(() => expect(onOpenSSE).toHaveBeenCalledOnce());
    if (failure === "http") await act(async () => { await submitted; });
    const loadOlderPage = () => {
      act(() => result.current.mergeHistoryPage("concurrent-pagination", history.slice(0, 20).reverse()));
      expect(result.current.messageList).toHaveLength(82);
    };
    if (timing === "before_error") loadOlderPage();
    act(() => {
      if (failure === "http") listeners.get("error")?.({ type: "error", status: 409 });
      else rejectOpen(new Error("request failed"));
    });
    await waitFor(() => expect(getHistoryMock).toHaveBeenCalledOnce());
    if (timing === "during_recovery") loadOlderPage();
    await act(async () => {
      resolveHistory({ data: { history: history.slice(-20).reverse(), next_page_token: "20" } });
      await submitted;
    });
    expect(result.current.messageList).toHaveLength(80);
    expect(result.current.messageList.map((item) => `${item.role}:${item.history_id}`))
      .toEqual(buildChatMessageListFromHistory([...history].reverse()).map((item) => `${item.role}:${item.history_id}`));
    expect(result.current.messageList[79].ask_answered).not.toBe(true);
    expect(result.current.conversationMessagesCache.current.get("concurrent-pagination"))
      .toEqual(result.current.messageList);
    expect(streamManager.getStreamState("concurrent-pagination")?.messageList)
      .toEqual(result.current.messageList);
  });

  it("keeps a server-persisted failed submission while removing its optimistic duplicate", async () => {
    const { stream, listeners } = createMockStream();
    const pending = { ask_id: "delete-card", user_env_delete: { id: "env", name: "test_api_key" }, questions: [] };
    const history = Array.from({ length: 40 }, (_, index) => ({
      id: `history-${index + 1}`, seq: index + 1,
      query: `question-${index + 1}`, result: `answer-${index + 1}`,
      ...(index === 39 ? { ask_pending: pending } : {}),
    }));
    const persisted = {
      id: "history-41", seq: 41, query: "No", result: "", run_status: "failed" as const,
    };
    getHistoryMock.mockResolvedValue({ data: { history: [
      persisted,
      ...history.slice(-19).reverse().map((record) => ({ ...record, ask_answered: true })),
    ], next_page_token: "20" } });
    const { result } = renderConversation({ onOpenSSE: vi.fn().mockReturnValue(stream) });
    act(() => result.current.replaceMessageList("persisted-delete-failure", buildChatMessageListFromHistory([...history].reverse())));
    await act(async () => { await result.current.sendMessage({
      text: "No", ask_answers_structured: { ask_id: pending.ask_id, questions: [] },
    }); });
    await act(async () => listeners.get("error")?.({ type: "error", status: 503 }));
    expect(result.current.messageList).toHaveLength(82);
    expect(result.current.messageList.map((item) => item.history_id))
      .toEqual(Array.from({ length: 41 }, (_, index) => [`history-${index + 1}`, `history-${index + 1}`]).flat());
    expect(result.current.messageList[79].ask_answered).toBe(true);
    expect(result.current.messageList[81].run_status).toBe("failed");
  });

  it("restores an unsubmitted deletion card when opening the request fails", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const pending = { ask_id: "delete-card", user_env_delete: { id: "env", name: "test_api_key" }, questions: [] };
    getHistoryMock.mockResolvedValue({ data: { history: [{ id: "history-card", seq: 1, query: "Delete", ask_pending: pending }] } });
    const { result } = renderConversation({
      onOpenSSE: vi.fn().mockRejectedValue(new Error("unavailable")),
    });
    act(() => result.current.replaceMessageList("delete-open-failure", [{ role: RoleTypes.ASSISTANT, history_id: "history-card", ask_pending: pending }]));
    await act(async () => {
      expect(await result.current.sendMessage({ text: "No", ask_answers_structured: { ask_id: "delete-card", questions: [] } })).toBe(false);
    });
    const card = result.current.messageList[result.current.messageList.length - 1];
    expect(card.ask_pending.ask_id).toBe("delete-card");
    expect(card.ask_answered).not.toBe(true);
  });

  it.each(["unavailable", "newer_turn"])("does not restore stale local decisions when history is %s", async (scenario) => {
    const { stream, listeners } = createMockStream();
    const pending = { ask_id: "delete-card", user_env_delete: { id: "env", name: "test_api_key" }, questions: [] };
    let resolve!: (value: unknown) => void;
    let reject!: (reason: Error) => void;
    getHistoryMock.mockImplementation(() => new Promise((res, rej) => { resolve = res; reject = rej; }));
    const { result } = renderConversation({ onOpenSSE: vi.fn().mockReturnValue(stream) });
    act(() => result.current.replaceMessageList("delete-late-history", [{ role: RoleTypes.ASSISTANT, ask_pending: pending }]));
    await act(async () => { await result.current.sendMessage({ text: "No", ask_answers_structured: { ask_id: "delete-card", questions: [] } }); });
    act(() => listeners.get("error")?.({ type: "error", status: 409 }));
    if (scenario === "unavailable") {
      await act(async () => reject(new Error("history unavailable")));
      expect(result.current.messageList).toHaveLength(3);
    } else {
      const newer = [{ role: RoleTypes.ASSISTANT, delta: "newer turn" }];
      act(() => result.current.replaceMessageList("delete-late-history", newer));
      await act(async () => resolve({ data: { history: [{ id: "old-card", seq: 1, query: "Delete", ask_pending: pending }] } }));
      expect(result.current.messageList).toEqual(newer);
    }
  });

  it("loads authoritative group and parent metadata before showing a new conversation in history", async () => {
    const { listeners, onOpenSSE } = createPreparedStream("new-in-group");
    const { result } = renderConversation({ onOpenSSE });
    vi.mocked(emitConversationActivity).mockClear();
    vi.mocked(emitConversationListRefresh).mockClear();
    await act(async () => { await result.current.sendMessage({ text: "new grouped chat" }); });
    act(() => listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "new-in-group", history_id: "h1", seq: 1, delta: "answer",
    } }) }));
    expect(emitConversationActivity).not.toHaveBeenCalledWith(expect.objectContaining({ displayName: expect.any(String) }));
    expect(emitConversationListRefresh).toHaveBeenCalledOnce();
  });

  it("keeps unretained sidechat activity out of the main history", async () => {
    const { listeners, onOpenSSE } = createPreparedStream("ephemeral-sidechat");
    const { result } = renderConversation({ onOpenSSE, concurrentStream: true });
    vi.mocked(emitConversationActivity).mockClear();
    vi.mocked(emitConversationListRefresh).mockClear();
    await act(async () => { await result.current.sendMessage({ text: "side question" }); });
    act(() => listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "ephemeral-sidechat", history_id: "side-h1", seq: 1, delta: "side answer",
    } }) }));
    expect(emitConversationActivity).not.toHaveBeenCalled();
    expect(emitConversationListRefresh).not.toHaveBeenCalled();
  });

  it("uses freshly loaded history instead of a stale per-conversation cache", () => {
    const { result } = renderConversation();
    const first = [{ role: RoleTypes.ASSISTANT, delta: "cached answer" }];
    const second = [{ role: RoleTypes.ASSISTANT, delta: "server answer" }];

    act(() => {
      result.current.replaceMessageList("conversation-1", first);
      result.current.replaceMessageList("conversation-2", []);
      result.current.replaceMessageList("conversation-1", second);
    });

    expect(result.current.messageList).toEqual(second);
    expect(result.current.conversationMessagesCache.current.get("conversation-1"))
      .toEqual(second);
  });

  it("does not resume following when a streamed answer completes while reading history", async () => {
    const { stream, listeners } = createMockStream();
    const { result } = renderConversation({ onOpenSSE: vi.fn(() => stream) });
    act(() => result.current.replaceMessageList("scroll-conversation", []));
    await act(async () => { await result.current.sendMessage({ text: "question" }); });
    scrollMock.isMouseScrollingRef.current = false;
    scrollMock.scrollToEnd.mockClear();
    act(() => listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "scroll-conversation", history_id: "new-answer", delta: "new content",
      finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
    } }) }));
    act(() => listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "scroll-conversation", history_id: "new-answer",
      finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
    } }) }));
    expect(scrollMock.isMouseScrollingRef.current).toBe(false);
    expect(scrollMock.scrollToEnd).not.toHaveBeenCalled();
    expect(scrollMock.trackNewContent).toHaveBeenCalledWith(expect.any(Array), expect.arrayContaining([
      expect.objectContaining({ history_id: "new-answer", delta: "new content" }),
    ]));
  });

  it("keeps the reading position during workflow feedback and same-conversation refresh", () => {
    const { result } = renderConversation();
    const list = [{ role: RoleTypes.ASSISTANT, history_id: "h1", delta: "old" }];
    act(() => result.current.replaceMessageList("conversation-1", list));
    scrollMock.isMouseScrollingRef.current = false;
    scrollMock.scrollToEndImmediately.mockClear();
    act(() => window.dispatchEvent(new CustomEvent(CHAT_WORKFLOW_STEP_FEEDBACK_EVENT, { detail: {
      conversationId: "conversation-1", feedbackId: "step", historyId: "h1", message: "new result", status: "succeeded",
    } })));
    expect(scrollMock.isMouseScrollingRef.current).toBe(false);
    act(() => result.current.replaceMessageList("conversation-1", result.current.messageList));
    expect(scrollMock.scrollToEndImmediately).not.toHaveBeenCalled();
    expect(scrollMock.isMouseScrollingRef.current).toBe(false);
  });

  it("resets unread and enables following for a new empty conversation", () => {
    const { result } = renderConversation();
    act(() => result.current.replaceMessageList("old-conversation", [{ role: RoleTypes.ASSISTANT, delta: "old" }]));
    scrollMock.isMouseScrollingRef.current = false;
    scrollMock.scrollToEndImmediately.mockClear();
    scrollMock.resetUnread.mockClear();
    act(() => result.current.createNewChat());
    expect(result.current.messageList).toEqual([]);
    expect(scrollMock.resetUnread).toHaveBeenCalledOnce();
    expect(scrollMock.scrollToEndImmediately).toHaveBeenCalledOnce();
  });

  it.each(["succeeded", "failed"])("hydrates persisted %s workflow feedback by history id and deduplicates notices", async (status) => {
    const { result } = renderConversation();
    const feedback = status === "succeeded" ? "步骤已完成：结果已保存。" : "步骤未完成：请重试。";
    const initial = buildChatMessageListFromHistory([{ id: "h1", query: "question", result: "original" }]);
    const live = { role: RoleTypes.ASSISTANT, history_id: "live", delta: "streaming text" };
    act(() => result.current.replaceMessageList("conversation-1", [...initial, live]));
    getHistoryMock.mockResolvedValue({ data: { history: [{ id: "h1", result:
      `original\n\n<!-- workflow-step-feedback:task-1 -->\n${feedback}\n\n<!-- workflow-step-feedback:task-2 -->\nother feedback`,
    }] } });
    const notify = () => window.dispatchEvent(new CustomEvent(CHAT_WORKFLOW_STEP_FEEDBACK_EVENT, { detail: {
      conversationId: "conversation-1", feedbackId: "task-1", historyId: "h1", status,
    } }));
    await act(async () => { notify(); });
    await act(async () => { notify(); });
    expect(getHistoryMock).toHaveBeenCalledWith({ name: "conversation-1", anchorHistoryId: "h1" });
    expect(result.current.messageList[1].delta).toBe(`original\n\n${feedback}`);
    expect(result.current.messageList[1].workflow_step_feedback_ids).toEqual(["task-1"]);
    expect(result.current.messageList[2]).toEqual(live);
  });

  it("does not insert late workflow feedback into another conversation", async () => {
    let resolveHistory!: (value: unknown) => void;
    getHistoryMock.mockReturnValue(new Promise((resolve) => { resolveHistory = resolve; }));
    const { result } = renderConversation();
    act(() => result.current.replaceMessageList("conversation-1", buildChatMessageListFromHistory([
      { id: "h1", query: "first", result: "answer" },
    ])));
    act(() => window.dispatchEvent(new CustomEvent(CHAT_WORKFLOW_STEP_FEEDBACK_EVENT, { detail: {
      conversationId: "conversation-1", feedbackId: "task-1", historyId: "h1",
    } })));
    const other = [{ role: RoleTypes.ASSISTANT, history_id: "h2", delta: "other" }];
    act(() => result.current.replaceMessageList("conversation-2", other));
    await act(async () => { resolveHistory({ data: { history: [{ id: "h1", result:
      "answer\n\n<!-- workflow-step-feedback:task-1 -->\nfinished",
    }] } }); });
    expect(result.current.messageList).toEqual(other);
    expect(result.current.conversationMessagesCache.current.get("conversation-1")?.[1].delta).toBe("answer\n\nfinished");
  });

  it.each(["offline", "missing feedback"])("keeps chat messages when feedback hydration has %s", async (failure) => {
    const { result } = renderConversation();
    const messages = [{ role: RoleTypes.ASSISTANT, history_id: "h1", delta: "live content" }];
    act(() => result.current.replaceMessageList("conversation-1", messages));
    if (failure === "offline") getHistoryMock.mockRejectedValue(new Error("offline"));
    else getHistoryMock.mockResolvedValue({ data: { history: [{ id: "h1", result: "older content" }] } });
    await act(async () => { window.dispatchEvent(new CustomEvent(CHAT_WORKFLOW_STEP_FEEDBACK_EVENT, { detail: {
      conversationId: "conversation-1", feedbackId: "task-1", historyId: "h1",
    } })); });
    expect(getHistoryMock).toHaveBeenCalledOnce();
    expect(result.current.messageList).toEqual(messages);
  });

  it("discards a feedback history response after unmount", async () => {
    let resolveHistory!: (value: unknown) => void;
    getHistoryMock.mockReturnValue(new Promise((resolve) => { resolveHistory = resolve; }));
    const { result, unmount } = renderConversation();
    act(() => result.current.replaceMessageList("conversation-1", []));
    act(() => window.dispatchEvent(new CustomEvent(CHAT_WORKFLOW_STEP_FEEDBACK_EVENT, { detail: {
      conversationId: "conversation-1", feedbackId: "task-1", historyId: "h1",
    } })));
    unmount();
    const save = vi.spyOn(streamManager, "saveMessageList");
    await act(async () => { resolveHistory({ data: { history: [{ id: "h1", result:
      "<!-- workflow-step-feedback:task-1 -->\nfinished",
    }] } }); });
    expect(save).not.toHaveBeenCalled();
  });

  it("keeps a completed new turn when an older history page arrives", async () => {
    const initial = { id: "h2", seq: 2, query: "original question", result: "original answer" };
    const older = { id: "h1", seq: 1, query: "older question", result: "older answer" };
    const { stream, listeners } = createMockStream();
    const { result } = renderConversation({ onOpenSSE: vi.fn(() => stream) });
    act(() => result.current.replaceMessageList("conversation-1", buildChatMessageListFromHistory([initial])));
    await act(async () => {
      await result.current.sendMessage({ text: "new question" });
    });
    act(() => listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "conversation-1", history_id: "h3", seq: 3,
      delta: "new answer", finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
    } }) }));
    act(() => listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "conversation-1", history_id: "h3", seq: 3,
      finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
    } }) }));
    const liveTurn = result.current.messageList.slice(-2);

    act(() => result.current.mergeHistoryPage("conversation-1", [older]));

    expect(result.current.messageList.map((item) => item.delta)).toEqual([
      "older question", "older answer", "original question", "original answer", "new question", "new answer",
    ]);
    expect(result.current.messageList.slice(-2)).toEqual(liveTurn);
    expect(result.current.conversationMessagesCache.current.get("conversation-1")).toEqual(result.current.messageList);
    expect(streamManager.getStreamState("conversation-1")?.messageList).toEqual(result.current.messageList);
  });

  it("merges a pending history page without losing live deltas or resetting the active stream", async () => {
    const initial = { id: "h2", seq: 2, query: "original question", result: "original answer" };
    const older = { id: "h1", seq: 1, query: "older question", result: "older answer" };
    const { stream, listeners } = createMockStream();
    const { result } = renderConversation({ onOpenSSE: vi.fn(() => stream) });
    act(() => result.current.replaceMessageList("conversation-1", buildChatMessageListFromHistory([initial])));
    await act(async () => {
      await result.current.sendMessage({ text: "new question" });
    });
    let resolvePage!: () => void;
    const pendingPage = new Promise<void>((resolve) => { resolvePage = resolve; }).then(() => {
      result.current.mergeHistoryPage("conversation-1", [older]);
    });
    const emitDelta = (delta: string) => listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "conversation-1", history_id: "h3", seq: 3, delta, delta_mode: "append",
      finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
    } }) });
    act(() => emitDelta("first part"));
    await act(async () => {
      resolvePage();
      await pendingPage;
    });

    expect(result.current.messageList.map((item) => item.delta)).toEqual([
      "older question", "older answer", "original question", "original answer", "new question", "first part",
    ]);
    expect(result.current.loading).toBe(true);
    expect(result.current.isStreaming).toBe(true);
    expect(result.current.activeStreamRef.current).toBe(true);
    expect(stream.close).not.toHaveBeenCalled();
    act(() => emitDelta(" and second part"));
    expect(result.current.messageList.at(-1)?.delta).toBe("first part and second part");
    expect(result.current.messageList.at(-2)).toMatchObject({ role: RoleTypes.USER, history_id: "h3", delta: "new question" });
    expect(result.current.conversationMessagesCache.current.get("conversation-1")).toEqual(result.current.messageList);
  });

  it("deduplicates overlapping history by role and identity while preserving optimistic messages", async () => {
    const initial = { id: "h2", seq: 2, query: "same question", result: "current answer" };
    const newer = { id: "h3", seq: 3, query: "same question", result: "newer answer" };
    const { stream } = createMockStream();
    const { result } = renderConversation({ onOpenSSE: vi.fn(() => stream) });
    act(() => result.current.replaceMessageList("conversation-1", buildChatMessageListFromHistory([initial])));
    await act(async () => {
      await result.current.sendMessage({ text: "optimistic question" });
    });
    const optimistic = result.current.messageList.slice(-2);
    expect(optimistic.every((item) => !item.history_id)).toBe(true);
    const page = [newer, { ...initial, result: "stale answer" }];
    act(() => {
      result.current.mergeHistoryPage("conversation-1", page);
      result.current.mergeHistoryPage("conversation-1", page);
    });

    expect(result.current.messageList.map((item) => item.delta)).toEqual([
      "same question", "current answer", "same question", "newer answer", "optimistic question", "",
    ]);
    expect(result.current.messageList.slice(-2)).toEqual(optimistic);
    expect(result.current.isStreaming).toBe(true);
    expect(streamManager.getStreamState("conversation-1")?.messageList).toEqual(result.current.messageList);
  });

  it("keeps archived attempts between their user message and final reply when paging", () => {
    const initial = { id: "h2", seq: 2, query: "current question", result: "current answer" };
    const older = {
      id: "h1", seq: 1, query: "older question", result: "final answer",
      failed_attempts: [{ run_id: "run-1", result: "partial failed answer", run_status: "failed" }],
    };
    const { result } = renderConversation();
    act(() => {
      result.current.replaceMessageList("conversation-1", buildChatMessageListFromHistory([initial]));
      result.current.mergeHistoryPage("conversation-1", [older]);
      result.current.mergeHistoryPage("conversation-1", [older]);
    });

    expect(result.current.messageList.map((item) => item.delta)).toEqual([
      "older question", "partial failed answer", "final answer", "current question", "current answer",
    ]);
    expect(result.current.messageList[1]).toMatchObject({ archived_failure: true, history_id: "h1:failed:run-1" });
  });

  it("ignores a history page for a conversation that is no longer active", () => {
    const current = buildChatMessageListFromHistory([{ id: "current", query: "current question", result: "current answer" }]);
    const { result } = renderConversation();
    act(() => {
      result.current.replaceMessageList("conversation-2", current);
      result.current.mergeHistoryPage("conversation-1", [{ id: "previous", query: "previous question", result: "previous answer" }]);
    });
    expect(result.current.currentConversationIdRef.current).toBe("conversation-2");
    expect(result.current.messageList).toEqual(current);
    expect(result.current.conversationMessagesCache.current.has("conversation-1")).toBe(false);
  });

  it("appends every completed workflow step to its assistant chat message once", () => {
    const { result } = renderConversation();
    const messages = [
      { role: RoleTypes.USER, delta: "生成 PPT", history_id: "history-1" },
      {
        role: RoleTypes.ASSISTANT,
        delta: "PPT 工作流已启动。",
        raw_delta: "PPT 工作流已启动。",
        history_id: "history-1",
        finish_reason:
          ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
      },
    ];
    const detail = {
      conversationId: "conversation-1",
      feedbackId: "task-outline",
      historyId: "history-1",
      message: "步骤「生成大纲」已完成：已生成 10 页大纲。",
      status: "succeeded",
    };

    act(() => {
      result.current.replaceMessageList("conversation-1", messages);
      window.dispatchEvent(
        new CustomEvent(CHAT_WORKFLOW_STEP_FEEDBACK_EVENT, { detail }),
      );
      window.dispatchEvent(
        new CustomEvent(CHAT_WORKFLOW_STEP_FEEDBACK_EVENT, { detail }),
      );
    });

    expect(result.current.messageList[1]).toMatchObject({
      delta:
        "PPT 工作流已启动。\n\n步骤「生成大纲」已完成：已生成 10 页大纲。",
      workflow_step_feedback_ids: ["task-outline"],
    });
  });

  it("restores the previous answer and clears busy state when opening SSE rejects", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const onOpenSSE = vi.fn().mockRejectedValue(new Error("open failed"));
    const originalMessages = [
      {
        role: RoleTypes.USER,
        delta: "hello",
        inputs: [{ input_type: "text", text: "hello" }],
      },
      {
        role: RoleTypes.ASSISTANT,
        delta: "previous answer",
        history_id: "history-1",
        finish_reason:
          ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
      },
    ];
    const { result } = renderConversation({
      onOpenSSE,
    });

    await act(async () => {
      result.current.replaceMessageList("conversation-1", originalMessages);
      await result.current.regenerate();
    });

    await waitFor(() => {
      expect(onOpenSSE).toHaveBeenCalledWith(
        originalMessages[0].inputs,
        ChatConversationsRequestActionEnum.ChatActionRegeneration,
        {},
        expect.objectContaining({
          __prepareClientConversationId: expect.any(Function),
        }),
      );
      expect(result.current.loading).toBe(false);
      expect(result.current.isStreaming).toBe(false);
      expect(result.current.activeStreamRef.current).toBe(false);
      expect(result.current.messageList).toEqual(originalMessages);
      expect(result.current.messageListRef.current).toEqual(originalMessages);
    });

    await act(async () => {
      await result.current.regenerate();
    });

    await waitFor(() => expect(onOpenSSE).toHaveBeenCalledTimes(2));
  });

  it("exposes a persistent setup card when a workflow task contains a capability failure", async () => {
    const { result } = renderHook(() =>
      useChatConversation({
        canChat: true,
        onOpenSSE: vi.fn(),
        setIsChatContent: vi.fn(),
        clearStorePendingMessage: vi.fn(),
        clearCiteMessages: vi.fn(),
        chatInputRef: createRef<ChatInputImperativeProps>(),
        thinkingCollapseMap: new Map(),
        getUserEdit: () => undefined,
        t: (key) => key,
      }),
    );
    const failure =
      'MEDIA_CAPABILITY_DEPENDENCY_MISSING '
      + '{"status":"blocked","workflow":"CREATE_ANIMATED_MEME",'
      + '"required":["video_generator"],"missing":[{"id":"video_generator",'
      + '"label":"视频生成模型","available":false,"settings_url":"/settings?section=models",'
      + '"reason":"尚未配置视频生成模型。"}],"message":"缺少视频生成模型。"}';

    act(() => {
      useTaskCenterStore.setState({
        activeConversationId: "conversation-capability",
        tasksByConversation: {
          "conversation-capability": [{
            task_id: "task-capability",
            conversation_id: "conversation-capability",
            title: "image-workflow:analyze_subject",
            agent_type: "workflow_step",
            mode: "auto",
            status: "failed",
            progress_pct: 100,
            artifacts: [],
            sources: [],
            artifact_streams: [],
            execution_log: [{ type: "tool_results", content: "", tool_results: [{
              tool_call_id: "tool-1",
              name: "check_image_workflow_capabilities",
              result: failure,
            }] }],
          }],
        },
      });
    });

    await waitFor(() => {
      expect(result.current.mediaCapabilityDependency).toMatchObject({
        failure_id: "task-capability",
        conversation_id: "conversation-capability",
        missing: [expect.objectContaining({ id: "video_generator" })],
      });
    });
  });

  it("shows a setup card from the structured direct-chat stream field", async () => {
    const { stream, listeners } = createMockStream();
    const { result } = renderConversation({ onOpenSSE: vi.fn(() => stream) });
    await act(async () => {
      await result.current.sendMessage({ text: "生成一张柯基犬" });
    });

    act(() => listeners.get("message")?.({
      data: JSON.stringify({
        result: {
          conversation_id: "conversation-capability",
          capability_dependency: {
            status: "blocked",
            workflow: "DIRECT_CHAT",
            required: ["image_generator"],
            missing: [{
              id: "image_generator",
              label: "文生图模型",
              available: false,
              settings_url: "/settings?section=models&target=image_generator",
              reason: "尚未配置文生图模型。",
            }],
            message: "当前任务缺少：文生图模型。",
          },
        },
      }),
    }));

    expect(result.current.mediaCapabilityDependency).toMatchObject({
      conversation_id: "conversation-capability",
      workflow: "DIRECT_CHAT",
      missing: [expect.objectContaining({ id: "image_generator" })],
    });
  });

  it("restores a failed ordinary task's setup and continue actions with empty session storage", async () => {
    const onOpenSSE = vi.fn(() => createMockStream().stream);
    const { result } = renderConversation({ onOpenSSE });
    act(() => result.current.replaceMessageList("ordinary-capability", [
      { role: RoleTypes.USER, delta: "generate image" },
    ]));
    expect(sessionStorage.length).toBe(0);
    act(() => {
      useTaskCenterStore.setState({ viewMode: "ordinary", activeConversationId: "ordinary-capability" });
      useTaskCenterStore.getState().applyOrdinarySnapshot("ordinary-capability", ordinary("failed-task", {
        status: "failed", agent_type: "workflow_step",
        capability_dependency: { status: "blocked", required: ["image_generator"], missing: [{
          id: "image_generator", label: "文生图模型", available: false,
          settings_url: "/settings?section=models&target=image_generator", reason: "请配置文生图模型。",
        }], message: "请完成配置后继续。" },
      }));
    });
    expect(useTaskCenterStore.getState().getTasks("ordinary-capability")[0].summary).toBeUndefined();
    expect(result.current.mediaCapabilityDependency).toMatchObject({
      failure_id: "failed-task", conversation_id: "ordinary-capability",
      missing: [expect.objectContaining({ settings_url: "/settings?section=models&target=image_generator" })],
    });
    await act(async () => { await result.current.continueAfterMediaCapabilityConfiguration(); });
    expect(onOpenSSE).toHaveBeenCalledWith(expect.any(Array), ChatConversationsRequestActionEnum.ChatActionNext,
      {}, expect.any(Object));
    expect(result.current.mediaCapabilityDependency).toBeNull();
  });

  it("sends a configuration-complete follow-up when the user continues", async () => {
    const { stream } = createMockStream();
    const onOpenSSE = vi.fn(() => stream);
    const firstRender = renderConversation({ onOpenSSE });
    act(() => {
      firstRender.result.current.replaceMessageList("conversation-capability", [
        {
          role: RoleTypes.USER,
          delta: "生成一张小狗的照片",
          inputs: [{ input_type: "text", text: "生成一张小狗的照片" }],
        },
        {
          role: RoleTypes.ASSISTANT,
          delta: "尚未配置文生图模型。",
          finish_reason:
            ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
        },
      ]);
      window.dispatchEvent(new CustomEvent(
        "lazymind:chat-media-capability-missing",
        {
          detail: {
            status: "blocked",
            workflow: "CREATE_NEW",
            required: ["image_generator"],
            missing: [{
              id: "image_generator",
              label: "文生图模型",
              available: false,
              settings_url: "/settings?section=models",
              reason: "尚未配置文生图模型。",
            }],
            message: "缺少文生图模型。",
            conversation_id: "conversation-capability",
            failure_id: "task-capability",
          },
        },
      ));
    });

    await waitFor(() => {
      expect(firstRender.result.current.mediaCapabilityDependency).not.toBeNull();
    });
    expect(onOpenSSE).not.toHaveBeenCalled();
    firstRender.unmount();

    const { result } = renderConversation({ onOpenSSE });
    act(() => {
      result.current.replaceMessageList("conversation-capability", [
        {
          role: RoleTypes.USER,
          delta: "生成一张小狗的照片",
          inputs: [{ input_type: "text", text: "生成一张小狗的照片" }],
        },
      ]);
    });
    expect(result.current.mediaCapabilityDependency).toMatchObject({
      failure_id: "task-capability",
      conversation_id: "conversation-capability",
    });

    await act(async () => {
      await result.current.continueAfterMediaCapabilityConfiguration();
    });

    expect(listToolAssetsMock).toHaveBeenCalledWith({ silentError: true });
    expect(onOpenSSE).toHaveBeenCalledWith(
      [{ input_type: "text", text: "已完成配置，继续工作流" }],
      ChatConversationsRequestActionEnum.ChatActionNext,
      {},
      expect.objectContaining({
        __prepareClientConversationId: expect.any(Function),
      }),
    );
    expect(result.current.messageList.filter((item) => item.role === RoleTypes.USER)
      .map((item) => item.delta)).toEqual(["生成一张小狗的照片", "已完成配置，继续工作流"]);
    expect(result.current.mediaCapabilityDependency).toBeNull();
    expect(sessionStorage.getItem("chat-capability-pending:conversation-capability"))
      .toBeNull();
  });

  it("keeps the setup card blocked when Continue finds configuration still missing", async () => {
    const onOpenSSE = vi.fn();
    const { result } = renderConversation({ onOpenSSE });
    listToolAssetsMock.mockResolvedValueOnce([{
      id: "image_generator",
      isAvailable: false,
    }]);

    act(() => {
      result.current.replaceMessageList("conversation-capability", [{
        role: RoleTypes.USER,
        delta: "生成一张小狗的照片",
        inputs: [{ input_type: "text", text: "生成一张小狗的照片" }],
      }]);
      window.dispatchEvent(new CustomEvent(
        "lazymind:chat-media-capability-missing",
        {
          detail: {
            status: "blocked",
            workflow: "DIRECT_CHAT",
            required: ["image_generator"],
            missing: [{
              id: "image_generator",
              label: "文生图模型",
              available: false,
              settings_url: "/settings?section=models&target=image_generator",
              reason: "尚未配置文生图模型。",
            }],
            message: "缺少文生图模型。",
            conversation_id: "conversation-capability",
          },
        },
      ));
    });

    await act(async () => {
      expect(await result.current.continueAfterMediaCapabilityConfiguration())
        .toBe(false);
    });

    expect(onOpenSSE).not.toHaveBeenCalled();
    expect(result.current.mediaCapabilityDependency).not.toBeNull();
    expect(message.warning).toHaveBeenCalledWith(
      "chat.mediaCapabilityStillMissing",
    );
    expect(sessionStorage.getItem(
      "chat-capability-pending:conversation-capability",
    )).not.toBeNull();
  });

  it("does not open parallel regeneration requests", async () => {
    let resolveOpen: ((stream: unknown) => void) | undefined;
    const onOpenSSE = vi.fn(
      () =>
        new Promise((resolve) => {
          resolveOpen = resolve;
        }),
    );
    const messages = [
      {
        role: RoleTypes.USER,
        delta: "hello",
        inputs: [{ input_type: "text", text: "hello" }],
      },
      {
        role: RoleTypes.ASSISTANT,
        delta: "previous answer",
        finish_reason:
          ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
      },
    ];
    const { result } = renderConversation({
      onOpenSSE,
    });

    act(() => {
      result.current.replaceMessageList("conversation-1", messages);
    });

    let firstRequest: Promise<boolean> | undefined;
    act(() => {
      firstRequest = result.current.regenerate();
      void result.current.regenerate();
    });

    await waitFor(() => expect(onOpenSSE).toHaveBeenCalledTimes(1));

    const { stream } = createMockStream();
    await act(async () => {
      resolveOpen?.(stream);
      await firstRequest;
    });

    expect(onOpenSSE).toHaveBeenCalledTimes(1);
  });

  it("does not reopen regeneration before synchronous stream state renders", async () => {
    const { stream } = createMockStream();
    const onOpenSSE = vi.fn(() => stream);
    const { result } = renderConversation({
      onOpenSSE,
    });

    act(() => {
      result.current.replaceMessageList("conversation-1", [
        {
          role: RoleTypes.USER,
          delta: "hello",
          inputs: [{ input_type: "text", text: "hello" }],
        },
        {
          role: RoleTypes.ASSISTANT,
          delta: "failed",
          run_status: "failed",
        },
      ]);
    });

    await act(async () => {
      await result.current.regenerate();
      await result.current.regenerate();
    });

    expect(onOpenSSE).toHaveBeenCalledTimes(1);
  });

  it("does not retry while a model PATCH is pending and retries after release", async () => {
    let modelSelectionSaving = true;
    const { stream } = createMockStream();
    const onOpenSSE = vi.fn(() => stream);
    const { result } = renderConversation({
      onOpenSSE,
      isModelSelectionSaving: () => modelSelectionSaving,
    });

    act(() => {
      result.current.replaceMessageList("conversation-1", [
        {
          role: RoleTypes.USER,
          delta: "retry me",
          inputs: [{ input_type: "text", text: "retry me" }],
          history_id: "history-1",
        },
        {
          role: RoleTypes.ASSISTANT,
          delta: "",
          run_status: "failed",
          run_terminal: {
            status: "failed",
            reason: "model_failure",
            code: "rate_limited",
            partial_output: false,
          },
        },
      ]);
    });

    await act(async () => {
      await result.current.regenerate();
    });
    expect(onOpenSSE).not.toHaveBeenCalled();

    modelSelectionSaving = false;
    await act(async () => {
      await result.current.regenerate();
    });
    expect(onOpenSSE).toHaveBeenCalledTimes(1);
    expect(onOpenSSE).toHaveBeenCalledWith(
      [{ input_type: "text", text: "retry me" }],
      ChatConversationsRequestActionEnum.ChatActionRegeneration,
      {},
      expect.objectContaining({
        __prepareClientConversationId: expect.any(Function),
      }),
    );
  });

  it("keeps a first submitted attachment retryable when runtime readiness fails", async () => {
    waitForRuntimeCapabilityMock
      .mockRejectedValueOnce(new Error("runtime unavailable"))
      .mockResolvedValue(undefined);
    const { stream } = createMockStream();
    const onOpenSSE = vi.fn(() => stream);
    const clearFiles = vi.fn();
    const { result } = renderConversation({
      onOpenSSE,
    });

    await act(async () => {
      await result.current.sendMessage({
        text: "review the attachment",
        fileList: [
          {
            uid: "file-1",
            name: "brief.pdf",
            base64: "",
            suffix: ".pdf",
            size: "1 KB",
          },
        ],
        fileListRef: {
          current: { clear: clearFiles },
        } as any,
        files: [
          { uid: "file-1", name: "brief.pdf", uri: "/uploads/brief.pdf" },
        ] as any,
      });
    });

    expect(onOpenSSE).not.toHaveBeenCalled();
    expect(clearFiles).toHaveBeenCalledTimes(1);
    expect(result.current.messageList[0]).toMatchObject({
      role: RoleTypes.USER,
      delta: "review the attachment",
      fileList: [
        expect.objectContaining({
          uid: "file-1",
          name: "brief.pdf",
        }),
      ],
      files: [
        expect.objectContaining({
          uid: "file-1",
          name: "brief.pdf",
        }),
      ],
      inputs: expect.arrayContaining([
        expect.objectContaining({
          input_type: "file",
          uri: "/uploads/brief.pdf",
        }),
      ]),
    });
    expect(result.current.messageList[1]).toMatchObject({
      role: RoleTypes.ASSISTANT,
      run_status: "failed",
      run_terminal: {
        status: "failed",
        reason: "model_failure",
        code: "service_unavailable",
        partial_output: false,
      },
    });

    await act(async () => {
      await result.current.regenerate();
    });

    expect(onOpenSSE).toHaveBeenCalledWith(
      expect.arrayContaining([
        expect.objectContaining({
          input_type: "file",
          uri: "/uploads/brief.pdf",
        }),
      ]),
      ChatConversationsRequestActionEnum.ChatActionNext,
      {},
      expect.objectContaining({
        __prepareClientConversationId: expect.any(Function),
      }),
    );
    expect(
      result.current.messageList.filter((item) => item.role === RoleTypes.USER),
    ).toHaveLength(1);
  });

  it("keeps a first submitted turn retryable when opening SSE rejects", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const clientConversationId = "11111111-1111-4111-8111-111111111111";
    const { stream } = createMockStream();
    let attempt = 0;
    const onOpenSSE = vi.fn(
      (
        _input: unknown,
        _action: unknown,
        _callbacks: unknown,
        extras?: Record<string, unknown>,
      ) => {
        const prepareClientConversationId =
          extras?.__prepareClientConversationId;
        if (typeof prepareClientConversationId === "function") {
          prepareClientConversationId(clientConversationId);
        }
        attempt += 1;
        return attempt === 1
          ? Promise.reject(new Error("open failed"))
          : stream;
      },
    );
    const { result } = renderConversation({
      onOpenSSE,
    });

    await act(async () => {
      await result.current.sendMessage({ text: "keep this turn" });
    });

    expect(result.current.currentConversationIdRef.current).toBe(
      clientConversationId,
    );
    expect(result.current.messageList[0]).toMatchObject({
      role: RoleTypes.USER,
      delta: "keep this turn",
      inputs: [{ input_type: "text", text: "keep this turn" }],
    });
    expect(result.current.messageList[1]).toMatchObject({
      role: RoleTypes.ASSISTANT,
      run_terminal: expect.objectContaining({
        status: "failed",
        code: "transport_error",
      }),
    });

    await act(async () => {
      await result.current.regenerate();
    });

    expect(onOpenSSE).toHaveBeenCalledTimes(2);
    expect(
      result.current.messageList.filter((item) => item.role === RoleTypes.USER),
    ).toHaveLength(1);
  });

  it("reports a submitted request while runtime startup is pending", async () => {
    let resolveRuntime: (() => void) | undefined;
    waitForRuntimeCapabilityMock.mockImplementationOnce(
      () => new Promise<void>((resolve) => { resolveRuntime = resolve; }),
    );
    const { stream } = createMockStream();
    const onRequestPendingChange = vi.fn();
    const { result } = renderConversation({
      onOpenSSE: vi.fn(() => stream),
      onRequestPendingChange,
    });

    let send: Promise<void> | undefined;
    act(() => {
      send = result.current.sendMessage({ text: "pending question" });
    });
    await waitFor(() =>
      expect(onRequestPendingChange).toHaveBeenCalledWith(true),
    );
    expect(result.current.messageList[0]).toMatchObject({
      role: RoleTypes.USER,
      delta: "pending question",
    });

    await act(async () => {
      resolveRuntime?.();
      await send;
    });
  });

  it("keeps the failed attempt visible while retrying the same user turn", async () => {
    const { stream } = createMockStream();
    const onOpenSSE = vi.fn(() => stream);
    const switches = [modelSwitch()];
    const failedMessages = [
      {
        role: RoleTypes.USER,
        delta: "hello",
        inputs: [{ input_type: "text", text: "hello" }],
        history_id: "history-1",
      },
      {
        role: RoleTypes.ASSISTANT,
        delta: "partial",
        history_id: "history-1",
        run_id: "run-failed-1",
        run_status: "failed",
        model_switches: switches,
        run_terminal: {
          status: "failed",
          reason: "model_failure",
          code: "rate_limited",
          partial_output: true,
        },
      },
    ];
    const { result } = renderConversation({
      onOpenSSE,
    });

    await act(async () => {
      result.current.replaceMessageList("conversation-1", failedMessages);
      await result.current.regenerate();
    });

    expect(onOpenSSE).toHaveBeenCalledTimes(1);
    expect(result.current.messageList).toHaveLength(3);
    expect(result.current.messageList[1]).toMatchObject({
      delta: "partial",
      archived_failure: true,
      original_history_id: "history-1",
      run_status: "failed",
      model_switches: switches,
    });
    expect(result.current.messageList[2]).toMatchObject({
      role: RoleTypes.ASSISTANT,
      history_id: "history-1",
      delta: "",
    });
    expect(result.current.messageList[2].run_status).toBeUndefined();
    expect(result.current.messageList[2].model_switches).toBeUndefined();

    act(() => result.current.replaceMessageList("conversation-1", buildChatMessageListFromHistory([{
      id: "history-1", query: "hello", result: "retried answer",
      failed_attempts: [{ result: "partial", run_id: "run-failed-1", run_status: "failed", model_switches: switches }],
    }])));
    expect(result.current.messageList[1]).toMatchObject({ archived_failure: true, model_switches: switches });
    expect(result.current.messageList[2].model_switches).toBeUndefined();
  });

  it.each([
    [{ code: 2001336, message: "conflict", data: { detail: { reason: "path_unavailable" } } }, "chat.workspace.reason.path_unavailable"],
    [{ code: 2002813, message: "conversation project directory_conflict" }, "errors.2002813"],
  ])("keeps rejected workspace creation local and allows a new attempt", async (body, key) => {
    const { listeners, onOpenSSE } = createPreparedStream("new-workspace-conversation");
    const onOpenResumeSSE = vi.fn();
    const onConversationIdChange = vi.fn();
    const { result } = renderConversation({ onOpenSSE, onOpenResumeSSE, onConversationIdChange });
    await act(async () => {
      await result.current.sendMessage({ text: "keep question", workspace_id: "grant", workspace_permission_mode: "always_ask", project_name: "my project" });
    });
    act(() => listeners.get("error")?.({ type: "error", status: 409, data: JSON.stringify(body) }));
    expect(result.current.creationError).toBe(key);
    expect(result.current.loading).toBe(false);
    expect(result.current.isStreaming).toBe(false);
    expect(result.current.streamRecovery.status).toBe("idle");
    expect(onOpenResumeSSE).not.toHaveBeenCalled();
    expect(onConversationIdChange).not.toHaveBeenCalled();
    expect(result.current.currentConversationIdRef.current).toBe("");
    expect(result.current.messageList).toHaveLength(1);
    expect(result.current.messageList[0].delta).toBe("keep question");
    expect(result.current.draftWorkspace).toMatchObject({ workspace_id: "grant", workspace_permission_mode: "always_ask", project_name: "my project" });
    await act(async () => { await result.current.sendMessage({ text: "retry", workspace_id: "new-grant" }); });
    expect(onOpenSSE).toHaveBeenCalledTimes(2);
    expect(result.current.creationError).toBeUndefined();
    expect(onOpenSSE.mock.lastCall?.[3]).toMatchObject({ workspace_id: "new-grant" });
    act(() => listeners.get("error")?.({ type: "error", status: 409, data: JSON.stringify(body) }));
    expect(result.current.draftWorkspace?.workspace_id).toBe("new-grant");
  });

  it("confirms the prepared conversation after a mapped 503 so model switching can target it", async () => {
    const clientConversationId = "44444444-4444-4444-8444-444444444444";
    const { listeners, onOpenSSE } = createPreparedStream(clientConversationId);
    const onOpenResumeSSE = vi.fn();
    const onConversationIdChange = vi.fn();
    const { result } = renderConversation({
      onOpenSSE,
      onOpenResumeSSE,
      onConversationIdChange,
    });

    await act(async () => {
      await result.current.sendMessage({ text: "keep this question", clearInput: false });
    });
    act(() => {
      listeners.get("error")?.({
        type: "error",
        status: 503,
        data: JSON.stringify({
          code: 2001597,
          message: "provider-secret: model config unavailable",
          data: { detail: { history_id: "persisted-preflight-history" } },
        }),
      });
    });

    expect(onOpenResumeSSE).not.toHaveBeenCalled();
    expect(onConversationIdChange).toHaveBeenCalledOnce();
    expect(onConversationIdChange).toHaveBeenCalledWith(clientConversationId);
    expect(result.current.loading).toBe(false);
    expect(result.current.isStreaming).toBe(false);
    expect(result.current.streamRecovery.status).toBe("idle");
    expect(result.current.messageList[0]).toMatchObject({
      role: RoleTypes.USER,
      delta: "keep this question",
    });
    expect(result.current.messageList[1]).toMatchObject({
      role: RoleTypes.ASSISTANT,
      run_status: "failed",
      run_terminal: {
        status: "failed",
        reason: "model_failure",
        code: "not_found",
        partial_output: false,
      },
    });
    expect(JSON.stringify(result.current.messageList)).not.toContain(
      "provider-secret",
    );
    expect(result.current.messageList.map(item => item.history_id)).toEqual([
      "persisted-preflight-history", "persisted-preflight-history",
    ]);
    await act(async () => { await result.current.regenerate(); });
    expect(onOpenSSE).toHaveBeenLastCalledWith(
      expect.any(Array), ChatConversationsRequestActionEnum.ChatActionRegeneration, {}, expect.any(Object),
    );
  });

  it("does not treat a structured 403 as a lost SSE connection", async () => {
    const clientConversationId = "66666666-6666-4666-8666-666666666666";
    const { listeners, onOpenSSE } = createPreparedStream(clientConversationId);
    const onOpenResumeSSE = vi.fn();
    const { result } = renderConversation({
      onOpenSSE,
      onOpenResumeSSE,
    });

    await act(async () => {
      await result.current.sendMessage({ text: "run paused workflow", clearInput: false });
    });
    act(() => {
      listeners.get("error")?.({
        type: "error",
        status: 403,
        data: JSON.stringify({ code: 2000102, message: "forbidden" }),
      });
    });

    expect(onOpenResumeSSE).not.toHaveBeenCalled();
    expect(result.current.streamRecovery.status).toBe("idle");
    expect(result.current.messageList[1]).toMatchObject({
      run_status: "failed",
      run_terminal: {
        status: "failed",
        reason: "runtime_failure",
        code: "request_rejected",
        partial_output: false,
      },
    });
  });

  it("confirms only the prepared client conversation id from the first SSE event", async () => {
    vi.useFakeTimers();
    listConversationsMock.mockResolvedValue({
      data: { conversations: [{ conversation_id: "old-conversation" }] },
    });
    const clientConversationId = "22222222-2222-4222-8222-222222222222";
    const { listeners, onOpenSSE } = createPreparedStream(clientConversationId);
    const onConversationIdChange = vi.fn();
    const { result, unmount } = renderConversation({
      onOpenSSE,
      onConversationIdChange,
    });

    await act(async () => {
      await result.current.sendMessage({ text: "keep this question" });
    });
    expect(result.current.currentConversationIdRef.current).toBe(
      clientConversationId,
    );
    act(() => {
      vi.advanceTimersByTime(400);
    });

    expect(listConversationsMock).not.toHaveBeenCalled();
    expect(onConversationIdChange).not.toHaveBeenCalled();

    act(() => {
      listeners.get("message")?.({
        type: "message",
        data: JSON.stringify({
          result: {
            conversation_id: clientConversationId,
            delta: "answer",
          },
        }),
      });
    });

    expect(onConversationIdChange).toHaveBeenCalledWith(clientConversationId);
    expect(result.current.currentConversationIdRef.current).toBe(
      clientConversationId,
    );
    unmount();
  });

  it("ignores a first SSE event with a different conversation id", async () => {
    const clientConversationId = "33333333-3333-4333-8333-333333333333";
    const { listeners, onOpenSSE } = createPreparedStream(clientConversationId);
    const onConversationIdChange = vi.fn();
    const { result } = renderConversation({
      onOpenSSE,
      onConversationIdChange,
    });

    await act(async () => {
      await result.current.sendMessage({ text: "keep this question" });
    });
    expect(result.current.currentConversationIdRef.current).toBe(
      clientConversationId,
    );
    act(() => {
      listeners.get("message")?.({
        type: "message",
        data: JSON.stringify({
          result: {
            conversation_id: "different-conversation",
            delta: "must not attach",
          },
        }),
      });
    });

    expect(onConversationIdChange).not.toHaveBeenCalled();
    expect(result.current.messageList[1]).toMatchObject({
      role: RoleTypes.ASSISTANT,
      delta: "",
    });
    expect(result.current.messageList[1].run_terminal).toBeUndefined();
    expect(JSON.stringify(result.current.messageList)).not.toContain(
      "must not attach",
    );
  });

  it("does not replay the previous answer when a new turn is rejected before SSE", async () => {
    const { stream, listeners } = createMockStream();
    const onOpenResumeSSE = vi.fn();
    const { result } = renderConversation({ onOpenSSE: vi.fn(() => stream), onOpenResumeSSE });
    act(() => result.current.replaceMessageList("conversation-1", buildChatMessageListFromHistory([
      { id: "old-history", seq: 1, query: "old question", result: "old answer", run_status: "completed" },
    ])));
    await act(async () => { await result.current.sendMessage({ text: "new question" }); });
    act(() => listeners.get("error")?.({ type: "error", status: 500, data: JSON.stringify({
      code: 2000000,
      message: "Unable to access user environment variables; check the credential key configuration",
    }) }));
    expect(onOpenResumeSSE).not.toHaveBeenCalled();
    expect(result.current.streamRecovery.status).toBe("idle");
    expect(result.current.isStreaming).toBe(false);
    expect(result.current.messageList.map(item => item.delta)).toEqual(["old question", "old answer", "new question", ""]);
    expect(result.current.messageList.at(-1)?.run_terminal).toMatchObject({
      status: "failed", reason: "runtime_failure", code: "user_env_unavailable", partial_output: false,
    });
  });

  it.each([false, true])("retries an unpersisted rejected turn without replacing old history (repeat=%s)", async (repeat) => {
    const { stream, listeners } = createMockStream();
    const onOpenSSE = vi.fn(() => stream);
    const { result } = renderConversation({ onOpenSSE });
    const oldMessages = buildChatMessageListFromHistory([
      { id: "old-history", seq: 1, query: "old question", result: "old answer", run_status: "completed" },
    ]);
    act(() => result.current.replaceMessageList("conversation-1", oldMessages));
    const mentions = [{ type: "tool" as const, mention_id: "mention-1", resource_id: "test-tool", display_name: "test tool" }];
    await act(async () => { await result.current.sendMessage({
      text: "new question", mentions, citeHistoryIds: ["cited-history"],
    }); });
    const reject = () => listeners.get("error")?.({ type: "error", status: 500, data: JSON.stringify({
      code: 2000000, message: "Internal error", data: { detail: { reason: "user_env_unavailable" } },
    }) });
    for (let attempt = 0; attempt < (repeat ? 2 : 1); attempt++) {
      act(reject);
      await act(async () => { await result.current.regenerate(); });
      expect(onOpenSSE.mock.lastCall).toEqual([
        [{ input_type: "text", text: "new question" }],
        ChatConversationsRequestActionEnum.ChatActionNext,
        {},
        expect.objectContaining({ mentions, cite_history_ids: ["cited-history"] }),
      ]);
      expect(result.current.messageList.slice(0, 2)).toEqual(oldMessages);
    }
    const emit = (frame: object) => listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "conversation-1", history_id: "new-history", seq: 2, ...frame,
    } }) });
    act(() => emit({ delta: "new answer" }));
    act(() => emit({ finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonStop }));
    expect(result.current.messageList.slice(0, 2)).toEqual(oldMessages);
    expect(result.current.messageList[result.current.messageList.length - 1]).toMatchObject({
      history_id: "new-history", delta: "new answer",
    });
    expect(result.current.messageList.filter(item => item.role === RoleTypes.USER)).toHaveLength(2);
    await act(async () => { await result.current.regenerate(); });
    expect(onOpenSSE).toHaveBeenLastCalledWith(
      expect.any(Array), ChatConversationsRequestActionEnum.ChatActionRegeneration, {}, expect.any(Object),
    );
  });

  it("merges repeated terminal frames into the same answer", async () => {
    const { stream, listeners } = createMockStream();
    const { result } = renderConversation({ onOpenSSE: vi.fn(() => stream) });
    act(() => result.current.replaceMessageList("conversation-1", []));
    await act(async () => { await result.current.sendMessage({ text: "question" }); });
    const emit = (frame: object) => listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "conversation-1", history_id: "history-1", ...frame,
    } }) });
    act(() => emit({ delta: "answer" }));
    act(() => emit({ finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonStop }));
    act(() => emit({ finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonStop }));
    expect(result.current.messageList.map(item => item.delta)).toEqual(["question", "answer"]);
  });

  it("ends generation when a model failure terminal also carries the final partial answer", async () => {
    const { stream, listeners } = createMockStream();
    const { result } = renderConversation({ onOpenSSE: vi.fn(() => stream) });
    act(() => result.current.replaceMessageList("conversation-1", []));
    await act(async () => { await result.current.sendMessage({ text: "question" }); });
    expect(result.current.isStreaming).toBe(true);

    act(() => listeners.get("message")?.({ data: JSON.stringify({ result: {
      conversation_id: "conversation-1", history_id: "history-1", delta: "partial answer",
      finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonUnknown,
      runtime_event: { type: "run_finished", run_id: "failed-run", data: {
        status: "failed", reason: "model_failure", code: "provider_internal_error", partial_output: true,
      } },
    } }) }));

    expect(result.current.isStreaming).toBe(false);
    expect(result.current.messageList[result.current.messageList.length - 1]).toMatchObject({ delta: "partial answer", run_status: "failed" });
    expect(stream.close).toHaveBeenCalled();
    expect(result.current.streamRecovery.status).toBe("idle");
  });

  it("keeps status-zero failures on the existing stream recovery path", async () => {
    const clientConversationId = "55555555-5555-4555-8555-555555555555";
    const { listeners, onOpenSSE } = createPreparedStream(clientConversationId);
    const onConversationIdChange = vi.fn();
    const { result, unmount } = renderConversation({
      onOpenSSE,
      onOpenResumeSSE: vi.fn(),
      onConversationIdChange,
    });

    await act(async () => {
      await result.current.sendMessage({ text: "network test", clearInput: false });
    });
    act(() => {
      listeners.get("error")?.({
        type: "error",
        status: 0,
        data: JSON.stringify({ code: 2001597, message: "model config unavailable" }),
      });
    });

    expect(result.current.streamRecovery.status).toBe("resuming");
    expect(onConversationIdChange).not.toHaveBeenCalled();
    expect(result.current.messageList[1].run_terminal).toBeUndefined();
    unmount();
  });

  it("keeps temporary resume endpoint failures recoverable", async () => {
    const { stream, listeners } = createMockStream();
    const { result, unmount } = renderConversation({ onOpenResumeSSE: vi.fn(() => stream) });
    act(() => result.current.replaceMessageList("resume-conversation", [
      { role: RoleTypes.USER, delta: "question", history_id: "h1" },
      { role: RoleTypes.ASSISTANT, delta: "partial", history_id: "h1" },
    ]));
    await act(async () => { await result.current.openResumeSSE("resume-conversation"); });
    act(() => listeners.get("error")?.({ type: "error", status: 503,
      data: JSON.stringify({ code: 2000000, message: "Internal server error" }),
    }));
    expect(result.current.streamRecovery.status).toBe("resuming");
    expect(result.current.messageList.at(-1)?.run_terminal).toBeUndefined();
    expect(result.current.messageList.at(-1)?.delta).toBe("partial");
    unmount();
  });
});
