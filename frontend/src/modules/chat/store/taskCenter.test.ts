import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const sseHarness = vi.hoisted(() => ({
  callbacks: new Map<string, Record<string, (event: CustomEvent) => void>>(),
}));

const workflowState = vi.hoisted(() => ({
  loadActiveSession: vi.fn().mockResolvedValue(undefined),
  setAutoRunning: vi.fn(),
  sessionByConversation: {} as Record<string, any>,
  setSession: vi.fn(),
}));

const requestHarness = vi.hoisted(() => ({
  listConversationTasks: vi.fn(),
  listConversationArtifacts: vi.fn(),
  getTaskDetail: vi.fn(),
  getProjection: vi.fn(),
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getAuthHeaders: () => ({}) },
}));

vi.mock("@/components/request", () => ({
  axiosInstance: { get: vi.fn() },
  localizeErrorCode: (code: string) => code,
}));

vi.mock("@/modules/chat/utils/request", () => ({
  convEventsUrl: (conversationId: string) => `/events/${conversationId}`,
  taskStreamUrl: (taskId: string) => `/tasks/${taskId}/stream`,
  WorkflowSessionApi: () => ({ getProjection: requestHarness.getProjection }),
  TaskServiceApi: () => ({
    listConversationTasks: requestHarness.listConversationTasks,
    listConversationArtifacts: requestHarness.listConversationArtifacts,
    getTaskDetail: requestHarness.getTaskDetail,
  }),
}));

vi.mock("@/modules/chat/utils/sse", () => ({
  Method: { GET: "GET" },
  SSE: class MockSSE {
    constructor(url: string, options: { callbacks?: Record<string, (event: CustomEvent) => void> }) {
      sseHarness.callbacks.set(url, options.callbacks ?? {});
    }

    close() {}
  },
}));

vi.mock("@/modules/chat/utils/ui", () => ({
  default: { jsonParser: JSON.parse },
}));

vi.mock("@/modules/chat/store/workflowPanel", () => ({
  useWorkflowStore: { getState: () => workflowState },
}));

vi.mock("@/modules/knowledge/utils/imageUrl", () => ({
  resolveCoreAssetUrl: (url: string) => url,
}));

vi.mock("@/components/StateGraphModal", () => ({
  WORKFLOW_GRAPH_REFRESH_EVENT: "workflow-graph-refresh",
}));

import { useTaskCenterStore } from "./taskCenter";
import { ordinary } from "../components/TaskCenter/ordinaryTestFixtures";
import type { ChatModelSwitchRecord } from "@/api/generated/core-client";
import {
  CHAT_AUTO_ADVANCE_EVENT,
  CHAT_WORKFLOW_STEP_FEEDBACK_EVENT,
} from "@/modules/chat/constants/chat";

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

function emitConversationEvent(
  event: Record<string, unknown>,
  conversationId = "conversation-1",
) {
  sseHarness.callbacks.get(`/events/${conversationId}`)?.message?.({
    data: JSON.stringify(event),
  } as unknown as CustomEvent);
}

describe("task center workflow events", () => {
  it.each(["task", "conversation"])("keeps same-execution live task_start idempotent on the %s stream", (source) => {
    const store = useTaskCenterStore.getState();
    store.subscribeConvEvents("conversation-1");
    store.upsertTask("conversation-1", { task_id: "model-task", status: "running" });
    store.subscribeTask("conversation-1", "model-task");
    const emit = (event: object) => sseHarness.callbacks.get("/tasks/model-task/stream")!.message({
      data: JSON.stringify({ execution_id: "execution", ...event }),
    } as any);
    const output = (call_index: number, phase?: string) => ({ run_id: "execution", call_index, ...(phase ? { phase } : {}) });
    emit({ type: "task_start", snapshot: true });
    emit({ type: "text", text: "COMMITTED", model_output: output(1) });
    emit({ type: "model_output", model_output: output(1, "commit") });
    emit({ type: "tool_results", tool_results: [{ id: "receipt", name: "save", result: "SAVED-ONCE" }] });
    emit({ type: "artifact", slot: "saved", content_type: "text", seq: 1, value: { text: "SAVED-ONCE" } });
    if (source === "task") emit({ type: "task_start" });
    else emitConversationEvent({ type: "task_updated", payload: { task_id: "model-task", event: { type: "task_start", execution_id: "execution" } } });
    emit({ type: "model_output", model_output: output(2, "begin") });
    emit({ type: "text", text: "FAILED PREVIEW", model_output: output(2) });
    emit({ type: "model_output", model_output: output(2, "rollback") });
    emit({ type: "model_output", model_output: output(3, "begin") });
    emit({ type: "text", text: "GOOD ANSWER", model_output: output(3) });
    emit({ type: "model_output", model_output: output(3, "commit") });
    const task = store.getTasks("conversation-1")[0];
    expect(task.execution_log.filter(item => item.type === "text").map(item => item.content)).toEqual(["COMMITTED", "GOOD ANSWER"]);
    expect(task.execution_log.filter(item => item.type === "tool_results")).toHaveLength(1);
    expect(task.artifacts).toHaveLength(1);
    expect(task.model_output).toEqual(output(3, "commit"));
    expect(task.model_output_hydrating).toBe(false);
    expect(requestHarness.listConversationTasks).not.toHaveBeenCalled();
  });

  it("rebuilds the same execution on reconnect and rejects late live starts from an older execution", () => {
    const store = useTaskCenterStore.getState();
    store.subscribeConvEvents("conversation-1");
    store.upsertTask("conversation-1", { task_id: "model-task", status: "running" });
    store.subscribeTask("conversation-1", "model-task");
    const emit = (event: object) => sseHarness.callbacks.get("/tasks/model-task/stream")!.message({ data: JSON.stringify(event) } as any);
    const snapshot = (run_id: string) => {
      emit({ type: "task_start", execution_id: run_id, snapshot: true });
      emit({ type: "text", execution_id: run_id, text: "COMMITTED", model_output: { run_id, call_index: 1 } });
      emit({ type: "model_output", execution_id: run_id, model_output: { run_id, call_index: 1, phase: "commit" } });
    };
    snapshot("current");
    store.unsubscribeTask("model-task");
    store.subscribeTask("conversation-1", "model-task");
    snapshot("current");
    expect(store.getTasks("conversation-1")[0].execution_log.map(item => item.content)).toEqual(["COMMITTED"]);
    snapshot("new");
    emit({ type: "task_start", execution_id: "current" });
    emitConversationEvent({ type: "task_updated", payload: { task_id: "model-task", event: { type: "task_start", execution_id: "current" } } });
    emit({ type: "text", execution_id: "current", text: "STALE" });
    emit({ type: "model_output", execution_id: "new", model_output: { run_id: "new", call_index: 2, phase: "begin" } });
    emit({ type: "text", execution_id: "new", text: "NEW PREVIEW", model_output: { run_id: "new", call_index: 2 } });
    emit({ type: "model_output", execution_id: "new", model_output: { run_id: "new", call_index: 2, phase: "rollback" } });
    const task = store.getTasks("conversation-1")[0];
    expect(task.execution_id).toBe("new");
    expect(task.execution_log.map(item => item.content)).toEqual(["COMMITTED"]);
    expect(task.model_output).toEqual({ run_id: "new", call_index: 2, phase: "rollback" });
    expect(task.model_output_hydrating).toBe(false);
  });

  it("rolls back only the current model call while retaining committed calls and real effects", () => {
    const store = useTaskCenterStore.getState();
    store.upsertTask("conversation-1", { task_id: "model-task", status: "running" });
    const output = (call_index: number, phase?: string) => ({ run_id: "execution", call_index, ...(phase ? { phase } : {}) });
    const emit = (event: object) => store.applyTaskEvent("conversation-1", "model-task", { execution_id: "execution", ...event });
    emit({ type: "task_start", snapshot: true });
    emit({ type: "text", text: "COMMITTED", model_output: output(1) });
    emit({ type: "text", text: "SECOND", model_output: output(2) });
    emit({ type: "model_output", model_output: output(2, "commit") });
    emit({ type: "model_output", model_output: output(3, "begin") });
    emit({ type: "text", text: "BAD TEXT", model_output: output(3) });
    emit({ type: "think", think: "BAD THINK", model_output: output(3) });
    emit({ type: "tool_results", tool_results: [{ id: "tool", name: "saved_tool", result: "real effect" }] });
    emit({ type: "artifact", slot: "real.txt", content_type: "text", seq: 1, value: { text: "saved" } });
    emit({ type: "model_output", model_output: output(3, "rollback") });
    const current = store.getTasks("conversation-1")[0];
    expect(current.execution_log.map(item => item.content).join("")).not.toContain("BAD");
    expect(current.execution_log.filter(item => item.type === "text").map(item => item.content)).toEqual(["COMMITTED", "SECOND"]);
    expect(current.execution_log.find(item => item.type === "tool_results")?.tool_results?.[0].result).toBe("real effect");
    expect(current.artifacts[0].value).toEqual({ text: "saved" });
    emit({ type: "model_output", model_output: output(3, "begin") });
    emit({ type: "text", text: "LATE", model_output: output(3) });
    emit({ type: "text", text: "FOREIGN", model_output: { run_id: "old-execution", call_index: 4 } });
    emit({ type: "model_output", model_output: output(4, "begin") });
    emit({ type: "text", text: "NEXT", model_output: output(4) });
    emit({ type: "text", text: " PART", model_output: output(4) });
    emit({ type: "model_output", model_output: output(4, "commit") });
    expect(store.getTasks("conversation-1")[0].execution_log.filter(item => item.type === "text").map(item => item.content))
      .toEqual(["COMMITTED", "SECOND", "NEXT PART"]);
  });

  it("takes short authoritative runtime snapshots and filters scoped history from older executions", async () => {
    const store = useTaskCenterStore.getState();
    const cursor = { run_id: "new-execution", call_index: 2, phase: "commit" };
    store.upsertTask("conversation-1", { task_id: "model-task", status: "running", execution_id: "new-execution",
      execution_log: [{ type: "text", content: "BAD" }, { type: "think", content: "BAD" }] } as any);
    store.upsertTask("conversation-1", { task_id: "model-task", execution_id: "new-execution", model_output: cursor,
      execution_log: [{ type: "text", content: "short" }] } as any);
    expect(store.getTasks("conversation-1")[0].execution_log).toEqual([{ type: "text", content: "short" }]);
    requestHarness.listConversationTasks.mockResolvedValue({ data: { tasks: [{ task_id: "model-task", status: "running", execution_id: "new-execution", steps: [
      { role: "text", execution_id: "old-execution", content: { content: "OLD" } },
      { role: "text", execution_id: "new-execution", content: { content: "one", model_output: { run_id: "new-execution", call_index: 1 } } },
      { role: "text", execution_id: "new-execution", content: { content: "two", model_output: { run_id: "new-execution", call_index: 2 } } },
      { role: "model_runtime", execution_id: "new-execution", content: { model_output: cursor } },
    ] }] } });
    await store.loadConversationTasks("conversation-1");
    expect(store.getTasks("conversation-1")[0]).toMatchObject({ execution_id: "new-execution", model_output: cursor });
    expect(store.getTasks("conversation-1")[0].execution_log.map(item => item.content)).toEqual(["one", "two"]);
    store.applyTaskEvent("conversation-1", "model-task", { type: "task_start", execution_id: "new-execution", snapshot: true });
    expect(store.getTasks("conversation-1")[0].execution_log).toEqual([]);
  });

  it("ignores queued callbacks from a replaced task SSE on the same conversation and view", () => {
    useTaskCenterStore.setState({ activeConversationId: "conversation-1" });
    const store = useTaskCenterStore.getState();
    store.upsertTask("conversation-1", { task_id: "model-task", status: "running" });
    store.subscribeTask("conversation-1", "model-task");
    const old = sseHarness.callbacks.get("/tasks/model-task/stream")!;
    store.unsubscribeTask("model-task");
    store.subscribeTask("conversation-1", "model-task");
    const currentStream = useTaskCenterStore.getState()._taskStreams["model-task"];
    old.message({ data: JSON.stringify({ type: "text", text: "LATE" }) } as any);
    old.error({ type: "error" } as any);
    expect(useTaskCenterStore.getState()._taskStreams["model-task"]).toBe(currentStream);
    expect(store.getTasks("conversation-1")[0].execution_log).toEqual([]);
    const next = sseHarness.callbacks.get("/tasks/model-task/stream")!;
    next.message({ data: JSON.stringify({ type: "task_start", execution_id: "current" }) } as any);
    next.message({ data: JSON.stringify({ type: "error", execution_id: "old", message: "late failure" }) } as any);
    expect(useTaskCenterStore.getState()._taskStreams["model-task"]).toBe(currentStream);
    expect(store.getTasks("conversation-1")[0].status).toBe("running");
  });

  it("does not let a REST response interrupt committed DB replay or restore an older call cursor", async () => {
    const store = useTaskCenterStore.getState();
    store.upsertTask("conversation-1", { task_id: "model-task", status: "running" });
    const output = (call_index: number, phase?: string) => ({ run_id: "execution", call_index, ...(phase ? { phase } : {}) });
    const emit = (event: object) => store.applyTaskEvent("conversation-1", "model-task", { execution_id: "execution", ...event });
    emit({ type: "task_start", snapshot: true });
    emit({ type: "text", text: "one", model_output: output(1) });
    requestHarness.listConversationTasks.mockResolvedValue({ data: { tasks: [{ task_id: "model-task", status: "running", execution_id: "execution", steps: [
      { role: "text", execution_id: "execution", content: { content: "one", model_output: output(1) } },
      { role: "model_runtime", execution_id: "execution", content: { model_output: output(1, "commit") } },
    ] }] } });
    await store.loadConversationTasks("conversation-1");
    emit({ type: "text", text: "two", model_output: output(2) });
    emit({ type: "model_output", model_output: output(2, "commit") });
    expect(store.getTasks("conversation-1")[0].execution_log.map(item => item.content)).toEqual(["one", "two"]);
    emit({ type: "model_output", model_output: output(3, "begin") });
    emit({ type: "text", text: "current preview", model_output: output(3) });
    await store.loadConversationTasks("conversation-1");
    expect(store.getTasks("conversation-1")[0].execution_log.map(item => item.content)).toEqual(["one", "two", "current preview"]);
  });

  it("ignores queued callbacks from a replaced conversation SSE without changing its successor", () => {
    const store = useTaskCenterStore.getState();
    store.subscribeConvEvents("conversation-1");
    const old = sseHarness.callbacks.get("/events/conversation-1")!;
    store.unsubscribeConvEvents("conversation-1");
    store.subscribeConvEvents("conversation-1");
    const current = useTaskCenterStore.getState()._convStream;
    old.message({ data: JSON.stringify({ type: "task_created", payload: { task_id: "late" } }) } as any);
    old.error({ type: "error" } as any);
    expect(useTaskCenterStore.getState()._convStream).toBe(current);
    expect(store.getTasks("conversation-1")).toEqual([]);
  });

  it("keeps a new live execution when an older list request resolves and still accepts its next frame", async () => {
    const store = useTaskCenterStore.getState();
    store.upsertTask("conversation-1", { task_id: "model-task", status: "running", execution_id: "old" });
    const pending = deferred<any>();
    requestHarness.listConversationTasks.mockReturnValue(pending.promise);
    const loading = store.loadConversationTasks("conversation-1");
    const output = (call_index: number, phase?: string) => ({ run_id: "new", call_index, ...(phase ? { phase } : {}) });
    const emit = (event: object) => store.applyTaskEvent("conversation-1", "model-task", { execution_id: "new", ...event });
    emit({ type: "task_start", snapshot: true });
    emit({ type: "model_output", model_output: output(0, "commit") });
    emit({ type: "model_output", model_output: output(1, "begin") });
    emit({ type: "text", text: "NEW-PREVIEW", model_output: output(1) });
    pending.resolve({ data: { tasks: [{ task_id: "model-task", status: "running", execution_id: "old", steps: [
      { role: "text", execution_id: "old", content: { content: "OLD-TEXT", model_output: { run_id: "old", call_index: 4 } } },
      { role: "model_runtime", execution_id: "old", content: { model_output: { run_id: "old", call_index: 4, phase: "commit" } } },
    ] }] } });
    await loading;
    expect(store.getTasks("conversation-1")[0]).toMatchObject({ execution_id: "new", model_output: output(1, "begin") });
    expect(store.getTasks("conversation-1")[0].execution_log.map(item => item.content)).toEqual(["NEW-PREVIEW"]);
    emit({ type: "text", text: " NEXT", model_output: output(1) });
    expect(store.getTasks("conversation-1")[0].execution_log.map(item => item.content)).toEqual(["NEW-PREVIEW NEXT"]);
    expect(requestHarness.listConversationTasks).toHaveBeenCalledTimes(1);
    store.unsubscribeTask("model-task");
  });

  it("keeps same-execution live text, thinking and real tools without continuously rereading during streaming", async () => {
    const store = useTaskCenterStore.getState();
    const cursor = { run_id: "execution", call_index: 1, phase: "commit" };
    store.upsertTask("conversation-1", { task_id: "model-task", status: "running", execution_id: "execution",
      model_output: cursor, execution_log: [{ type: "text", content: "COMMITTED" }] } as any);
    const pending = deferred<any>();
    const oldSnapshot = { data: { tasks: [{ task_id: "model-task", status: "running", execution_id: "execution", steps: [
      { role: "text", execution_id: "execution", content: { content: "COMMITTED" } },
      { role: "model_runtime", execution_id: "execution", content: { model_output: cursor } },
    ] }] } };
    requestHarness.listConversationTasks.mockReturnValue(pending.promise);
    const loading = store.loadConversationTasks("conversation-1");
    const emit = (event: object) => store.applyTaskEvent("conversation-1", "model-task", { execution_id: "execution", ...event });
    emit({ type: "text", text: " LIVE-TEXT" });
    emit({ type: "think", think: "LIVE-THINK" });
    emit({ type: "tool_results", tool_results: [{ id: "real-tool", name: "save", result: "saved once" }] });
    pending.resolve(oldSnapshot);
    await loading;
    expect(store.getTasks("conversation-1")[0].execution_log.map(item => item.content)).toEqual(["COMMITTED LIVE-TEXT", "LIVE-THINK", ""]);
    emit({ type: "text", text: "MORE-LIVE" });
    const current = store.getTasks("conversation-1")[0];
    expect(current.execution_log.map(item => item.content)).toEqual(["COMMITTED LIVE-TEXT", "LIVE-THINK", "", "MORE-LIVE"]);
    expect(current.execution_log[2].tool_results).toEqual([{ tool_call_id: "real-tool", name: "save", result: "saved once" }]);
    expect(current.model_output).toEqual(cursor);
    expect(requestHarness.listConversationTasks).toHaveBeenCalledTimes(1);
    expect(useTaskCenterStore.getState()._loadingTasks["conversation-1"]).toBe(false);
    store.unsubscribeTask("model-task");
  });

  it("accepts a newly discovered execution and a shorter rollback snapshot when live state has not advanced", async () => {
    const store = useTaskCenterStore.getState();
    store.upsertTask("conversation-1", { task_id: "model-task", status: "running", execution_id: "old",
      execution_log: [{ type: "text", content: "OLD LONG PREVIEW" }] } as any);
    const cursor = { run_id: "new", call_index: 1, phase: "rollback" };
    requestHarness.listConversationTasks.mockResolvedValueOnce({ data: { tasks: [{
      task_id: "model-task", status: "running", execution_id: "new", steps: [
        { role: "text", execution_id: "new", content: { content: "OK" } },
        { role: "model_runtime", execution_id: "new", content: { model_output: cursor } },
      ],
    }] } });
    await store.loadConversationTasks("conversation-1");
    expect(store.getTasks("conversation-1")[0]).toMatchObject({ execution_id: "new", model_output: cursor });
    expect(store.getTasks("conversation-1")[0].execution_log.map(item => item.content)).toEqual(["OK"]);
    store.applyTaskEvent("conversation-1", "model-task", { type: "model_output", execution_id: "new",
      model_output: { run_id: "new", call_index: 2, phase: "begin" } });
    store.applyTaskEvent("conversation-1", "model-task", { type: "text", execution_id: "new",
      model_output: { run_id: "new", call_index: 2 }, text: "BAD LONG PREVIEW" });
    requestHarness.listConversationTasks.mockResolvedValueOnce({ data: { tasks: [{
      task_id: "model-task", status: "running", execution_id: "new", steps: [
        { role: "model_runtime", execution_id: "new", content: { model_output: { run_id: "new", call_index: 2, phase: "rollback" } } },
      ],
    }] } });
    await store.loadConversationTasks("conversation-1");
    expect(store.getTasks("conversation-1")[0].execution_log).toEqual([]);
    expect(store.getTasks("conversation-1")[0].model_output).toEqual({ run_id: "new", call_index: 2, phase: "rollback" });
    expect(requestHarness.listConversationTasks).toHaveBeenCalledTimes(2);
    store.unsubscribeTask("model-task");
  });

  it("seeds the first public model switch snapshot silently and publishes only later live switches", () => {
    const record: ChatModelSwitchRecord = { event_id: "switch-1", switch_index: 1, reason_code: "rate_limited", context_compacted: false,
      from_max_input_tokens: 131072, to_max_input_tokens: 65536,
      from: { candidate_id: "own:a", source: "own", model_id: "a", provider_group_id: "a" },
      to: { candidate_id: "own:b", source: "own", model_id: "b", provider_group_id: "b" } };
    useTaskCenterStore.setState({ activeConversationId: "conversation-1", viewMode: "ordinary" });
    const store = useTaskCenterStore.getState();
    store.applyOrdinarySnapshot("conversation-1", ordinary("model-task", { status: "running" }));
    store.subscribeTask("conversation-1", "model-task");
    const notice = vi.fn();
    window.addEventListener("lazymind:chat-model-switch", notice);
    const emit = (revision: number, model_switch: ChatModelSwitchRecord) => sseHarness.callbacks.get("/tasks/model-task/stream")!.message({
      data: JSON.stringify({ type: "task_snapshot", data: ordinary("model-task", { revision, status: "running", model_switch }) }),
    } as any);
    emit(1, record);
    expect(notice.mock.calls.filter(call => call[0].detail.live)).toHaveLength(0);
    emit(2, { ...record, event_id: "switch-2", switch_index: 2 });
    emit(2, { ...record, event_id: "switch-2", switch_index: 2 });
    expect(notice.mock.calls.filter(call => call[0].detail.live)).toHaveLength(1);
    store.unsubscribeTask("model-task");
    store.subscribeTask("conversation-1", "model-task");
    emit(2, { ...record, event_id: "switch-2", switch_index: 2 });
    expect(notice.mock.calls.filter(call => call[0].detail.live)).toHaveLength(1);
    window.removeEventListener("lazymind:chat-model-switch", notice);
  });

  it("announces a live committed switch once even if its durable snapshot wins the race", () => {
    useTaskCenterStore.setState({ activeConversationId: "conversation-1" });
    const store = useTaskCenterStore.getState();
    store.upsertTask("conversation-1", { task_id: "model-task", status: "running" });
    store.subscribeTask("conversation-1", "model-task");
    const emit = (event: object) => sseHarness.callbacks.get("/tasks/model-task/stream")!.message({ data: JSON.stringify(event) } as any);
    const metadata = { run_id: "execution", call_index: 1 };
    emit({ type: "task_start", execution_id: "execution", snapshot: true });
    emit({ type: "model_output", execution_id: "execution", model_output: { ...metadata, call_index: 0, phase: "commit" } });
    emit({ type: "model_output", execution_id: "execution", model_output: { ...metadata, phase: "begin" } });
    store.upsertTask("conversation-1", { task_id: "model-task", execution_id: "execution", model_output: { ...metadata, phase: "commit" },
      execution_log: [{ type: "text", content: "saved" }] } as any);
    const notice = vi.fn();
    window.addEventListener("lazymind:chat-model-switch", notice);
    const frame = { type: "model_output", execution_id: "execution", model_output: { ...metadata, phase: "commit" }, runtime_event: {
      schema_version: 1, type: "model_switched", run_id: "execution", event_id: "switch", data: {
        switch_index: 1, reason_code: "rate_limited", from_max_input_tokens: null, to_max_input_tokens: null, context_compacted: false,
        from: { candidate_id: "own:a", source: "own", model_id: "a", provider_group_id: "a" },
        to: { candidate_id: "own:b", source: "own", model_id: "b", provider_group_id: "b" },
      },
    } };
    emit(frame); emit(frame);
    expect(notice).toHaveBeenCalledTimes(1);
    expect(store.getTasks("conversation-1")[0].execution_log).toEqual([{ type: "text", content: "saved" }]);
    window.removeEventListener("lazymind:chat-model-switch", notice);
  });

  it.each([1, 2])("announces the latest ordinary switch when REST finishes before a delayed stream snapshot (%s)", async (switchIndex) => {
    const record: ChatModelSwitchRecord = { event_id: "switch-1", switch_index: 1, reason_code: "rate_limited", context_compacted: false,
      from_max_input_tokens: 131072, to_max_input_tokens: 65536,
      from: { candidate_id: "own:a", source: "own", model_id: "a", provider_group_id: "a" },
      to: { candidate_id: "own:b", source: "own", model_id: "b", provider_group_id: "b" } };
    const latest: ChatModelSwitchRecord = switchIndex === 1 ? record : { ...record, event_id: "switch-2", switch_index: 2,
      from: record.to, from_max_input_tokens: 65536, to_max_input_tokens: 32768,
      to: { candidate_id: "own:c", source: "own", model_id: "c", provider_group_id: "c" } };
    useTaskCenterStore.setState({ activeConversationId: "conversation-1", viewMode: "ordinary" });
    const store = useTaskCenterStore.getState();
    store.applyOrdinarySnapshot("conversation-1", ordinary("model-task", { status: "running" }));
    store.subscribeTask("conversation-1", "model-task");
    const emit = (data: unknown) => sseHarness.callbacks.get("/tasks/model-task/stream")!.message({
      data: JSON.stringify({ type: "task_snapshot", data }),
    } as any);
    emit(ordinary("model-task", { status: "running" }));
    const notice = vi.fn();
    window.addEventListener("lazymind:chat-model-switch", notice);
    try {
      requestHarness.listConversationTasks.mockResolvedValue({ data: { data: { tasks: [
        ordinary("model-task", { revision: 3, status: "succeeded", model_switch: latest }),
      ] } } });
      await store.loadConversationTasks("conversation-1");
      await vi.waitFor(() => {
        expect(store.getTasks("conversation-1")[0]?.ordinary).toMatchObject({ revision: 3, status: "succeeded", model_switch: latest });
      });
      expect(notice).not.toHaveBeenCalled();
      const delayed = ordinary("model-task", { revision: 2, status: "running", model_switch: record });
      emit(delayed);
      emit(delayed);
      expect(notice).toHaveBeenCalledTimes(1);
      expect(notice.mock.calls[0][0].detail).toEqual({ conversationId: "conversation-1", runId: "1", record: latest, live: true });
      expect(store.getTasks("conversation-1")[0].ordinary).toMatchObject({ revision: 3, status: "succeeded", model_switch: latest });
    } finally {
      window.removeEventListener("lazymind:chat-model-switch", notice);
      store.unsubscribeTask("model-task");
    }
  });
  it.each(["ordinary", "developer"] as const)("keeps logical panel identity separate from each turn's immutable delivery in %s mode", async (viewMode) => {
    useTaskCenterStore.setState({ viewMode });
    const base = { artifact_id: "receipt", v2_artifact_id: "logical", conversation_id: "conv", history_id: "h1", producer_type: "main_agent", slot: "a", content_type: "text", seq: 1, value: { text: "one" } };
    useTaskCenterStore.getState().upsertConversationArtifact("conv", base);
    useTaskCenterStore.getState().upsertConversationArtifact("conv", { ...base, history_id: "h2", value: { text: "two" } });
    expect(useTaskCenterStore.getState().artifactsByConversation.conv).toHaveLength(1);
    expect(useTaskCenterStore.getState().artifactsByConversation.conv[0].artifact_id).toBe("logical");
    expect(useTaskCenterStore.getState().deliveriesByConversation.conv.map(item => item.value.text)).toEqual(["one", "two"]);
    requestHarness.listConversationArtifacts.mockResolvedValue({ data: { artifacts: [{ ...base, artifact_id: "logical", value: { text: "one" } }], deliveries: useTaskCenterStore.getState().deliveriesByConversation.conv, history_order: { h1: 0, h2: 1 } } });
    await useTaskCenterStore.getState().loadConversationArtifacts("conv");
    expect(useTaskCenterStore.getState().artifactsByConversation.conv[0].value.text).toBe("one");
    expect(useTaskCenterStore.getState().deliveriesByConversation.conv[1].value.text).toBe("two");
    expect(useTaskCenterStore.getState().artifactHistoryOrderByConversation.conv).toEqual({ h1: 0, h2: 1 });
  });
  beforeEach(() => {
    vi.useFakeTimers();
    sseHarness.callbacks.clear();
    requestHarness.getTaskDetail.mockReset();
    requestHarness.getProjection.mockReset();
    workflowState.sessionByConversation = {};
    workflowState.setSession.mockImplementation((id, session) => { workflowState.sessionByConversation[id] = session; });
    requestHarness.listConversationTasks.mockReset();
    requestHarness.listConversationTasks.mockResolvedValue({ data: { tasks: [] } });
    requestHarness.listConversationArtifacts.mockReset();
    requestHarness.listConversationArtifacts.mockResolvedValue({ data: { artifacts: [] } });
    workflowState.loadActiveSession.mockClear();
    workflowState.setAutoRunning.mockClear();
    useTaskCenterStore.setState({
      viewMode: "developer", _viewEpoch: 0, runsByConversation: {},
      activeConversationId: "",
      tasksByConversation: {},
      artifactsByConversation: {},
      deliveriesByConversation: {},
      artifactHistoryOrderByConversation: {},
      _loadingTasks: {},
      _queuedTaskLoads: {},
      _taskLoadErrors: {},
      _loadingArtifacts: {},
      _queuedArtifactLoads: {},
      _convStream: null,
      _taskStreams: {},
    });
  });

  afterEach(() => {
    const activeConversationId = useTaskCenterStore.getState().activeConversationId;
    if (activeConversationId) {
      useTaskCenterStore.getState().unsubscribeConvEvents(activeConversationId);
    }
    vi.useRealTimers();
  });

  it("uses public snapshots in ordinary mode and rejects raw execution events", async () => {
    useTaskCenterStore.setState({ viewMode: "ordinary" });
    requestHarness.listConversationTasks.mockResolvedValue({ data: { data: {
      tasks: [ordinary("one", { status: "running" })], runs: [{ run_id: "run-1", revision: 1, final_output_refs: [] }],
    } } });
    await useTaskCenterStore.getState().loadConversationTasks("conversation-1");
    expect(requestHarness.listConversationTasks).toHaveBeenCalledWith("conversation-1", { params: { view: "ordinary" } });
    const task = useTaskCenterStore.getState().getTasks("conversation-1")[0];
    expect(task.ordinary?.display_key).toBe("task:one:1");
    useTaskCenterStore.getState().applyTaskEvent("conversation-1", "one", { type: "think", content: "private" });
    expect(useTaskCenterStore.getState().getTasks("conversation-1")[0].execution_log).toEqual([]);
    expect(useTaskCenterStore.getState().runsByConversation["conversation-1"]).toHaveLength(1);
  });

  it("keeps a newer SSE snapshot when an older REST list resolves", async () => {
    useTaskCenterStore.setState({ viewMode: "ordinary" });
    const pending = deferred<any>();
    requestHarness.listConversationTasks.mockReturnValue(pending.promise);
    const loading = useTaskCenterStore.getState().loadConversationTasks("conversation-1");
    useTaskCenterStore.getState().applyTaskEvent("conversation-1", "one", {
      type: "task_snapshot", data: ordinary("one", { revision: 3, status: "succeeded" }),
    });
    pending.resolve({ data: { tasks: [ordinary("one", { revision: 2, status: "running" })] } });
    await loading;
    expect(useTaskCenterStore.getState().getTasks("conversation-1")[0].status).toBe("succeeded");
  });

  it("paginates workflow display keys through the workflow projection, including nodes with a task id", async () => {
    useTaskCenterStore.setState({ viewMode: "ordinary" });
    const source = (id: string) => ({ source_id: id, platform: "web", domain: "example.com", title: id, url: `https://example.com/${id}`, kind: "web" as const });
    const first = ordinary("one", { display_key: "workflow:session:step:attempt", session_id: "session", workflow_step_id: "step", sources: [source("first")] });
    first.pages.sources = { total: 2, next_cursor: "second", revision: 1 };
    const second = { ...first, sources: [source("second")], pages: { ...first.pages, sources: { total: 2, next_cursor: null, revision: 1 } } };
    workflowState.sessionByConversation["conversation-1"] = { session_id: "session", ordinary_revision: 1,
      ordinary_tasks: [first], steps: [{ ordinary: first }] };
    requestHarness.getProjection.mockResolvedValue({ data: { data: { revision: 1, tasks: [second] } } });
    await useTaskCenterStore.getState().loadOrdinaryTask("conversation-1", first.display_key, "sources", "second");
    expect(requestHarness.getTaskDetail).not.toHaveBeenCalled();
    expect(requestHarness.getProjection).toHaveBeenCalledWith("session", { params: { view: "ordinary", collection: "sources", cursor: "second", limit: 100, display_key: first.display_key } });
    const merged = workflowState.sessionByConversation["conversation-1"].steps[0].ordinary;
    expect(merged.sources.map((item: { source_id: string }) => item.source_id)).toEqual(["first", "second"]);
    expect(merged.pages.sources.next_cursor).toBeNull();
  });

  it("retains the last public list if the server returns an incompatible legacy response", async () => {
    useTaskCenterStore.setState({ viewMode: "ordinary" });
    useTaskCenterStore.getState().applyOrdinarySnapshot("conversation-1", ordinary("one"));
    requestHarness.listConversationTasks.mockResolvedValue({ data: { tasks: [{ task_id: "one", objective: "private" }] } });
    await useTaskCenterStore.getState().loadConversationTasks("conversation-1");
    expect(useTaskCenterStore.getState().getTasks("conversation-1")[0].ordinary?.display_key).toBe("task:one:1");
    expect(useTaskCenterStore.getState()._taskLoadErrors["conversation-1"]).toBe(true);
  });

  it("discards developer responses that finish after switching to ordinary view", async () => {
    const pending = deferred<any>();
    requestHarness.listConversationTasks.mockReturnValueOnce(pending.promise);
    const loading = useTaskCenterStore.getState().loadConversationTasks("conversation-1");
    useTaskCenterStore.getState().setViewMode("ordinary");
    pending.resolve({ data: { tasks: [{ task_id: "private", objective: "private", steps: [] }] } });
    await loading;
    expect(useTaskCenterStore.getState().getTasks("conversation-1")).toEqual([]);
  });

  it("closes a task stream created before its list row exists when the view changes", async () => {
    useTaskCenterStore.setState({ viewMode: "ordinary" });
    const pending = deferred<any>();
    requestHarness.listConversationTasks.mockReturnValueOnce(pending.promise);
    useTaskCenterStore.getState().subscribeConvEvents("conversation-1");
    emitConversationEvent({ type: "task_created", payload: { task_id: "new-task" } });
    const oldStream = useTaskCenterStore.getState()._taskStreams["new-task"];
    expect(oldStream).toBeDefined();
    expect(useTaskCenterStore.getState().getTasks("conversation-1")).toEqual([]);
    useTaskCenterStore.getState().setViewMode("developer");
    expect(useTaskCenterStore.getState()._taskStreams).toEqual({});
    useTaskCenterStore.getState().subscribeConvEvents("conversation-1");
    useTaskCenterStore.getState().subscribeTask("conversation-1", "new-task");
    expect(useTaskCenterStore.getState()._taskStreams["new-task"]).not.toBe(oldStream);
    pending.resolve({ data: { tasks: [] } });
    await Promise.resolve();
  });

  it("does not restore legacy artifacts after changing to ordinary view", async () => {
    const pending = deferred<any>();
    requestHarness.listConversationArtifacts.mockReturnValueOnce(pending.promise);
    const loading = useTaskCenterStore.getState().loadConversationArtifacts("conversation-1");
    useTaskCenterStore.getState().setViewMode("ordinary");
    pending.resolve({ data: { artifacts: [{ artifact_id: "old", value: { internal: true } }] } });
    await loading;
    expect(useTaskCenterStore.getState().artifactsByConversation).toEqual({});
  });

  it("hydrates ordinary artifact notices without inserting incomplete delivery rows", async () => {
    useTaskCenterStore.setState({ viewMode: "ordinary" });
    const pending = deferred<any>();
    requestHarness.listConversationArtifacts.mockReturnValueOnce(pending.promise);
    useTaskCenterStore.getState().subscribeConvEvents("conversation-1");
    emitConversationEvent({ type: "artifact_created", payload: { artifact_id: "receipt", history_id: "h1" } });
    expect(requestHarness.listConversationArtifacts).toHaveBeenCalledWith("conversation-1");
    expect(useTaskCenterStore.getState().artifactsByConversation).toEqual({});
    expect(useTaskCenterStore.getState().deliveriesByConversation).toEqual({});
    const artifact = { artifact_id: "logical", conversation_id: "conversation-1", history_id: "h1", producer_type: "main_agent", slot: "result", content_type: "text", seq: 1, value: { text: "complete" } };
    pending.resolve({ data: { artifacts: [artifact], deliveries: [{ ...artifact, artifact_id: "receipt", v2_artifact_id: "logical" }] } });
    await vi.waitFor(() => {
      expect(useTaskCenterStore.getState().artifactsByConversation["conversation-1"]).toEqual([artifact]);
      expect(useTaskCenterStore.getState().deliveriesByConversation["conversation-1"]).toEqual([expect.objectContaining({ artifact_id: "receipt", value: { text: "complete" } })]);
    });
  });

  it.each(["ordinary", "developer"] as const)("runs a queued artifact refresh after switching to %s", async (viewMode) => {
    useTaskCenterStore.setState({ viewMode: viewMode === "ordinary" ? "developer" : "ordinary" });
    const pending = deferred<any>();
    const artifact = { artifact_id: "current", value: { text: "saved result" } };
    requestHarness.listConversationArtifacts.mockReturnValueOnce(pending.promise)
      .mockResolvedValueOnce({ data: { artifacts: [artifact], deliveries: [artifact], history_order: { h1: 1 } } });
    const loading = useTaskCenterStore.getState().loadConversationArtifacts("conversation-1");
    useTaskCenterStore.getState().setViewMode(viewMode);
    await useTaskCenterStore.getState().loadConversationArtifacts("conversation-1");
    pending.resolve({ data: { artifacts: [{ artifact_id: "stale" }] } });
    await loading;
    expect(requestHarness.listConversationArtifacts).toHaveBeenCalledTimes(2);
    const state = useTaskCenterStore.getState();
    expect(state.artifactsByConversation["conversation-1"]).toEqual([artifact]);
    expect(state.deliveriesByConversation["conversation-1"]).toEqual([artifact]);
    expect(state.artifactHistoryOrderByConversation["conversation-1"]).toEqual({ h1: 1 });
    expect(state._loadingArtifacts["conversation-1"]).toBe(false);
    expect(state._queuedArtifactLoads["conversation-1"]).toBe(false);
  });

  it("retains public detail on a failed refresh and renews an expired page cursor", async () => {
    useTaskCenterStore.setState({ viewMode: "ordinary" });
    const task = ordinary("one");
    useTaskCenterStore.getState().applyOrdinarySnapshot("conversation-1", task);
    requestHarness.getTaskDetail.mockRejectedValueOnce(new Error("offline"));
    await expect(useTaskCenterStore.getState().loadOrdinaryTask("conversation-1", "one")).rejects.toThrow("offline");
    expect(useTaskCenterStore.getState().getTasks("conversation-1")[0].ordinary).toBe(task);
    requestHarness.getTaskDetail.mockRejectedValueOnce({ response: { status: 409 } });
    requestHarness.getTaskDetail.mockResolvedValueOnce({ data: { data: { task: ordinary("one", { revision: 2 }) } } });
    await useTaskCenterStore.getState().loadOrdinaryTask("conversation-1", "one", "sources", "expired");
    expect(requestHarness.getTaskDetail).toHaveBeenLastCalledWith("one", {
      params: { view: "ordinary", collection: undefined, cursor: undefined, limit: 100, display_key: "task:one:1" },
    });
    expect(useTaskCenterStore.getState().getTasks("conversation-1")[0].ordinary?.revision).toBe(2);
  });

  it("receives a plan and restores it from persisted steps", async () => {
    const steps = ["Read sales data", "Compare quarters", "Write report"];
    useTaskCenterStore.getState().upsertTask("conversation-1", { task_id: "plan-task" });
    useTaskCenterStore.getState().applyTaskEvent("conversation-1", "plan-task", { type: "plan", steps });
    expect(useTaskCenterStore.getState().getTasks("conversation-1")[0].plan_steps).toEqual(steps);
    requestHarness.listConversationTasks.mockResolvedValue({ data: { tasks: [{
      task_id: "plan-task", status: "succeeded", steps: [{ role: "plan", content: { steps } }],
    }] } });
    await useTaskCenterStore.getState().loadConversationTasks("conversation-1");
    expect(useTaskCenterStore.getState().getTasks("conversation-1")[0].plan_steps).toEqual(steps);
    expect(useTaskCenterStore.getState().getTasks("conversation-1")[0].execution_log).toEqual([]);
  });

  it("shows a newly created workflow step immediately", () => {
    useTaskCenterStore.getState().subscribeConvEvents("conversation-1");

    emitConversationEvent({
      type: "task_created",
      payload: {
        task_id: "workflow-task-1",
        agent_type: "workflow_step",
        title: "image-workflow:analyze_subject",
        status: "running",
      },
    });

    expect(useTaskCenterStore.getState().getTasks("conversation-1")).toEqual([
      expect.objectContaining({
        task_id: "workflow-task-1",
        agent_type: "workflow_step",
        status: "running",
      }),
    ]);
    expect(sseHarness.callbacks.has("/tasks/workflow-task-1/stream")).toBe(true);
  });

  it("applies live progress and execution updates to a workflow step", () => {
    useTaskCenterStore.getState().subscribeConvEvents("conversation-1");

    emitConversationEvent({
      type: "task_created",
      payload: {
        task_id: "workflow-task-1",
        agent_type: "workflow_step",
        title: "image-workflow:collect_materials",
        status: "pending",
      },
    });
    const taskMessage = sseHarness.callbacks.get("/tasks/workflow-task-1/stream")?.message;
    taskMessage?.({
      data: JSON.stringify({
        type: "progress",
        progress: 50,
        current_phase: "collecting references",
        writing_subtasks: [{
          subtask_id: "research-1",
          node_id: "section-1",
          question: "Find current market data",
          subtask_type: "retrieve",
          status: "running",
          retry_count: 0,
          tools_used: ["kb_search", "llm"],
        }],
      }),
    } as unknown as CustomEvent);
    taskMessage?.({
      data: JSON.stringify({
        type: "think",
        think: "Searching for seasonal material.",
      }),
    } as unknown as CustomEvent);

    expect(useTaskCenterStore.getState().getTasks("conversation-1")).toEqual([
      expect.objectContaining({
        task_id: "workflow-task-1",
        status: "running",
        progress_pct: 50,
        current_phase: "collecting references",
        writing_subtasks: [expect.objectContaining({
          subtask_id: "research-1",
          status: "running",
          tools_used: ["kb_search", "llm"],
        })],
        execution_log: [
          { type: "think", content: "Searching for seasonal material." },
        ],
      }),
    ]);
  });

  it("restores persisted writing subtasks after a terminal task reload", async () => {
    requestHarness.listConversationTasks.mockResolvedValue({
      data: {
        tasks: [{
          task_id: "workflow-task-1",
          agent_type: "workflow_step",
          title: "writer-workflow:write_document",
          status: "succeeded",
          progress_pct: 100,
          writing_subtasks: [{
            subtask_id: "extract-1",
            node_id: "section-1",
            question: "Extract narrative structure",
            subtask_type: "extract",
            status: "completed",
            retry_count: 0,
            tools_used: ["llm"],
            result_summary: "Resolved structure.",
          }],
        }],
      },
    });

    await useTaskCenterStore.getState().loadConversationTasks("conversation-1");

    expect(useTaskCenterStore.getState().getTasks("conversation-1")[0]).toEqual(
      expect.objectContaining({
        status: "succeeded",
        writing_subtasks: [expect.objectContaining({
          subtask_id: "extract-1",
          status: "completed",
          tools_used: ["llm"],
        })],
      }),
    );
  });

  it("refreshes workflow slots when a task publishes an artifact before completion", async () => {
    useTaskCenterStore.getState().subscribeConvEvents("conversation-1");
    emitConversationEvent({
      type: "task_created",
      payload: {
        task_id: "workflow-task-ppt",
        agent_type: "workflow_step",
        title: "ppt-workflow:generate_ppt",
        status: "running",
      },
    });
    await vi.advanceTimersByTimeAsync(100);
    workflowState.loadActiveSession.mockClear();

    const taskMessage = sseHarness.callbacks.get("/tasks/workflow-task-ppt/stream")?.message;
    taskMessage?.({
      data: JSON.stringify({
        type: "artifact",
        slot: "preview_html",
        content_type: "text",
        seq: 1,
        value: { text: "<html>page one</html>", list_index: 0 },
      }),
    } as unknown as CustomEvent);
    await vi.advanceTimersByTimeAsync(100);

    expect(workflowState.loadActiveSession).toHaveBeenCalledWith("conversation-1", {
      silentError: true,
    });
  });

  it("merges consecutive token deltas into one execution-log entry", () => {
    useTaskCenterStore.getState().subscribeConvEvents("conversation-1");
    emitConversationEvent({
      type: "task_created",
      payload: {
        task_id: "workflow-task-stream",
        agent_type: "workflow_step",
        title: "ppt-workflow:generate_ppt",
        status: "running",
      },
    });
    const taskMessage = sseHarness.callbacks.get("/tasks/workflow-task-stream/stream")?.message;
    for (const token of ["<", "html", ">"]) {
      taskMessage?.({
        data: JSON.stringify({ type: "text", text: token }),
      } as unknown as CustomEvent);
    }

    expect(useTaskCenterStore.getState().getTasks("conversation-1")[0].execution_log)
      .toEqual([{ type: "text", content: "<html>" }]);
  });

  it("keeps a live task when an older REST snapshot resolves and queues a reload", async () => {
    const firstSnapshot = deferred<{ data: { tasks: any[] } }>();
    const reconciledSnapshot = deferred<{ data: { tasks: any[] } }>();
    requestHarness.listConversationTasks
      .mockImplementationOnce(() => firstSnapshot.promise)
      .mockImplementationOnce(() => reconciledSnapshot.promise);
    useTaskCenterStore.getState().subscribeConvEvents("conversation-1");

    const loadPromise = useTaskCenterStore.getState().loadConversationTasks("conversation-1");
    expect(requestHarness.listConversationTasks).toHaveBeenCalledTimes(1);

    emitConversationEvent({
      type: "task_created",
      payload: {
        task_id: "workflow-task-2",
        agent_type: "workflow_step",
        title: "image-workflow:optimize_prompt",
        status: "running",
      },
    });

    expect(useTaskCenterStore.getState()._queuedTaskLoads["conversation-1"]).toBe(true);
    expect(useTaskCenterStore.getState().getTasks("conversation-1")).toEqual([
      expect.objectContaining({ task_id: "workflow-task-2", status: "running" }),
    ]);

    firstSnapshot.resolve({ data: { tasks: [] } });
    await Promise.resolve();
    await Promise.resolve();

    expect(requestHarness.listConversationTasks).toHaveBeenCalledTimes(2);
    expect(useTaskCenterStore.getState().getTasks("conversation-1")).toEqual([
      expect.objectContaining({ task_id: "workflow-task-2", status: "running" }),
    ]);

    reconciledSnapshot.resolve({
      data: {
        tasks: [{
          task_id: "workflow-task-2",
          agent_type: "workflow_step",
          title: "image-workflow:optimize_prompt",
          status: "running",
          progress_pct: 25,
        }],
      },
    });
    await loadPromise;

    expect(useTaskCenterStore.getState().getTasks("conversation-1")).toEqual([
      expect.objectContaining({
        task_id: "workflow-task-2",
        status: "running",
        progress_pct: 25,
      }),
    ]);
    expect(useTaskCenterStore.getState()._loadingTasks["conversation-1"]).toBe(false);
  });

  it("hydrates replayed task state without replaying automatic chat commands", async () => {
    const dispatchSpy = vi.spyOn(window, "dispatchEvent");
    requestHarness.listConversationTasks.mockResolvedValue({
      data: {
        tasks: [{
          task_id: "workflow-task-replayed",
          agent_type: "workflow_step",
          title: "image-workflow:collect_materials",
          status: "succeeded",
          progress_pct: 100,
        }],
      },
    });
    useTaskCenterStore.getState().subscribeConvEvents("conversation-1");

    emitConversationEvent({
      type: "task_created",
      replayed: true,
      payload: {
        task_id: "workflow-task-replayed",
        agent_type: "workflow_step",
        title: "image-workflow:collect_materials",
        status: "running",
      },
    });
    expect(useTaskCenterStore.getState().getTasks("conversation-1")).toEqual([]);
    await Promise.resolve();
    await Promise.resolve();

    emitConversationEvent({
      type: "driver_input",
      replayed: true,
      payload: { message: "continue" },
    });
    emitConversationEvent({
      type: "auto_chat_started",
      replayed: true,
      payload: { driver_message: "continue" },
    });

    expect(useTaskCenterStore.getState().getTasks("conversation-1")).toEqual([
      expect.objectContaining({
        task_id: "workflow-task-replayed",
        status: "succeeded",
      }),
    ]);
    expect(requestHarness.listConversationTasks).toHaveBeenCalledTimes(1);
    expect(workflowState.setAutoRunning).not.toHaveBeenCalledWith("conversation-1", true);
    expect(dispatchSpy.mock.calls.map(([event]) => event.type)).not.toContain(
      CHAT_AUTO_ADVANCE_EVENT,
    );
    dispatchSpy.mockRestore();
  });

  it("forwards each live workflow step feedback to chat and ignores replay", () => {
    const received: CustomEvent[] = [];
    const listener = (event: Event) => received.push(event as CustomEvent);
    window.addEventListener(CHAT_WORKFLOW_STEP_FEEDBACK_EVENT, listener);
    useTaskCenterStore.getState().subscribeConvEvents("conversation-1");

    const workflowFeedback = {
      type: "workflow_step_feedback",
      payload: {
        task_id: "workflow-task-feedback",
        history_id: "history-1",
        status: "succeeded",
        message: "步骤「生成大纲」已完成：已生成 10 页大纲。",
      },
    };
    emitConversationEvent(workflowFeedback);
    emitConversationEvent({ ...workflowFeedback, replayed: true });

    expect(received).toHaveLength(1);
    expect(received[0].detail).toEqual({
      conversationId: "conversation-1",
      feedbackId: "workflow-task-feedback",
      historyId: "history-1",
      message: "步骤「生成大纲」已完成：已生成 10 页大纲。",
      status: "succeeded",
    });
    window.removeEventListener(CHAT_WORKFLOW_STEP_FEEDBACK_EVENT, listener);
  });

  it("forwards ordinary feedback identity for history hydration without requiring raw message text", () => {
    useTaskCenterStore.setState({ viewMode: "ordinary" });
    const dispatchSpy = vi.spyOn(window, "dispatchEvent");
    useTaskCenterStore.getState().subscribeConvEvents("conversation-1");
    const feedback = { type: "workflow_step_feedback", payload: {
      task_id: "task-1", history_id: "history-1", status: "failed",
    } };
    emitConversationEvent(feedback);
    emitConversationEvent({ ...feedback, replayed: true });
    const events = dispatchSpy.mock.calls.map(([event]) => event as CustomEvent)
      .filter((event) => event.type === CHAT_WORKFLOW_STEP_FEEDBACK_EVENT);
    expect(events).toHaveLength(1);
    expect(events[0].detail).toEqual({ conversationId: "conversation-1", feedbackId: "task-1",
      historyId: "history-1", message: undefined, status: "failed" });
    dispatchSpy.mockRestore();
  });

  it("refreshes the active workflow session for live and replayed creation events", async () => {
    const dispatchSpy = vi.spyOn(window, "dispatchEvent");
    useTaskCenterStore.getState().subscribeConvEvents("conversation-1");

    emitConversationEvent({
      type: "workflow_session_created",
      replayed: true,
      payload: {
        conversation_id: "conversation-1",
        session_id: "session-replayed",
        workflow_id: "image-workflow",
      },
    });
    await vi.advanceTimersByTimeAsync(100);

    expect(workflowState.loadActiveSession).toHaveBeenCalledWith("conversation-1", {
      silentError: true,
    });
    expect(dispatchSpy).not.toHaveBeenCalled();

    workflowState.loadActiveSession.mockClear();
    emitConversationEvent({
      type: "workflow_session_created",
      payload: {
        conversation_id: "conversation-1",
        session_id: "session-live",
        workflow_id: "image-workflow",
      },
    });
    await vi.advanceTimersByTimeAsync(100);

    expect(workflowState.loadActiveSession).toHaveBeenCalledWith("conversation-1", {
      silentError: true,
    });
    expect(dispatchSpy.mock.calls.map(([event]) => event.type)).toContain("workflow-graph-refresh");
    dispatchSpy.mockRestore();
  });

  it("does not run a delayed workflow refresh after switching conversations", async () => {
    useTaskCenterStore.getState().subscribeConvEvents("conversation-1");
    emitConversationEvent({
      type: "workflow_session_created",
      replayed: true,
      payload: {
        conversation_id: "conversation-1",
        session_id: "session-old-conversation",
        workflow_id: "image-workflow",
      },
    });

    useTaskCenterStore.getState().subscribeConvEvents("conversation-2");
    await vi.advanceTimersByTimeAsync(100);

    expect(workflowState.loadActiveSession).not.toHaveBeenCalled();
  });

  it("keeps a live artifact when an older REST snapshot resolves and queues a reload", async () => {
    const firstSnapshot = deferred<{ data: { artifacts: any[] } }>();
    const reconciledSnapshot = deferred<{ data: { artifacts: any[] } }>();
    requestHarness.listConversationArtifacts
      .mockImplementationOnce(() => firstSnapshot.promise)
      .mockImplementationOnce(() => reconciledSnapshot.promise);

    const loadPromise = useTaskCenterStore.getState().loadConversationArtifacts("conversation-1");
    expect(requestHarness.listConversationArtifacts).toHaveBeenCalledTimes(1);

    useTaskCenterStore.getState().upsertConversationArtifact("conversation-1", {
      artifact_id: "live-1",
      conversation_id: "conversation-1",
      history_id: "h1",
      producer_type: "main_agent",
      filename: "report.md",
      content_type: "text",
      seq: 1,
      value: { text: "live" },
    });
    await useTaskCenterStore.getState().loadConversationArtifacts("conversation-1");
    expect(useTaskCenterStore.getState()._queuedArtifactLoads["conversation-1"]).toBe(true);

    firstSnapshot.resolve({ data: { artifacts: [] } });
    await Promise.resolve();
    await Promise.resolve();

    expect(requestHarness.listConversationArtifacts).toHaveBeenCalledTimes(2);
    expect(useTaskCenterStore.getState().artifactsByConversation["conversation-1"]).toEqual([
      expect.objectContaining({ artifact_id: "live-1" }),
    ]);

    reconciledSnapshot.resolve({
      data: {
        artifacts: [{
          artifact_id: "live-1",
          conversation_id: "conversation-1",
          history_id: "h1",
          producer_type: "main_agent",
          filename: "report.md",
          content_type: "text",
          seq: 1,
          value: { text: "persisted" },
        }],
      },
    });
    await loadPromise;

    expect(useTaskCenterStore.getState().artifactsByConversation["conversation-1"]).toEqual([
      expect.objectContaining({ artifact_id: "live-1", value: { text: "persisted" } }),
    ]);
    expect(useTaskCenterStore.getState()._loadingArtifacts["conversation-1"]).toBe(false);
  });

  it("keeps a live same-id replacement over an older REST snapshot", async () => {
    const firstSnapshot = deferred<{ data: { artifacts: any[] } }>();
    requestHarness.listConversationArtifacts.mockImplementationOnce(() => firstSnapshot.promise);

    const loadPromise = useTaskCenterStore.getState().loadConversationArtifacts("conversation-1");
    useTaskCenterStore.getState().upsertConversationArtifact("conversation-1", {
      artifact_id: "live-1",
      conversation_id: "conversation-1",
      history_id: "h1",
      producer_type: "main_agent",
      filename: "report.md",
      content_type: "text",
      seq: 1,
      value: { text: "v2" },
    });

    firstSnapshot.resolve({
      data: {
        artifacts: [{
          artifact_id: "live-1",
          conversation_id: "conversation-1",
          history_id: "h1",
          producer_type: "main_agent",
          filename: "report.md",
          content_type: "text",
          seq: 1,
          value: { text: "v1" },
        }],
      },
    });
    await loadPromise;

    expect(useTaskCenterStore.getState().artifactsByConversation["conversation-1"]).toEqual([
      expect.objectContaining({ artifact_id: "live-1", value: { text: "v2" } }),
    ]);
    expect(requestHarness.listConversationArtifacts).toHaveBeenCalledTimes(1);
  });
});
