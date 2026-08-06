import { beforeEach, describe, expect, it, vi } from "vitest";

const { MockSSE } = vi.hoisted(() => {
  class MockSSE {
    static instances: MockSSE[] = [];
    url: string;
    options: any;
    closed = false;
    constructor(url: string, options: any) {
      this.url = url;
      this.options = options;
      MockSSE.instances.push(this);
    }
    close() {
      this.closed = true;
    }
    emitMessage(data: string) {
      this.options.callbacks?.message?.({ data } as CustomEvent);
    }
    emitError() {
      this.options.callbacks?.error?.(new CustomEvent("error"));
    }
  }
  return { MockSSE };
});

vi.mock("@/modules/chat/utils/sse", () => ({
  Method: { GET: "GET" },
  SSE: MockSSE,
}));

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getAuthHeaders: () => ({ authorization: "Bearer token" }) },
}));

const mockTaskServiceApi = {
  listConversationTasks: vi.fn(),
  listConversationArtifacts: vi.fn(),
};

vi.mock("@/modules/chat/utils/request", () => ({
  TaskServiceApi: () => mockTaskServiceApi,
  taskStreamUrl: (id: string) => `/tasks/${id}:stream`,
  convEventsUrl: (id: string) => `/conversations/${id}/events`,
}));

vi.mock("@/components/StateGraphModal", () => ({
  PLUGIN_GRAPH_REFRESH_EVENT: "plugin-graph-refresh",
}));

vi.mock("@/components/request", () => ({
  localizeErrorCode: (code?: string, fallback = "") => (code ? `localized:${code}` : fallback),
}));

const mockLoadActiveSession = vi.fn();
const mockSetAutoRunning = vi.fn();
vi.mock("@/modules/chat/store/pluginPanel", () => ({
  usePluginStore: {
    getState: () => ({
      loadActiveSession: mockLoadActiveSession,
      setAutoRunning: mockSetAutoRunning,
    }),
  },
}));

import { useTaskCenterStore } from "./taskCenter";

function resetStore() {
  useTaskCenterStore.setState({
    tasksByConversation: {},
    artifactsByConversation: {},
    activeConversationId: "",
    _loadingTasks: {},
    _loadingArtifacts: {},
    _streams: {},
    _convStreams: {},
  });
}

describe("useTaskCenterStore", () => {
  beforeEach(() => {
    resetStore();
    MockSSE.instances = [];
    mockTaskServiceApi.listConversationTasks.mockReset();
    mockTaskServiceApi.listConversationArtifacts.mockReset();
    mockLoadActiveSession.mockReset();
    mockSetAutoRunning.mockReset();
  });

  it("upsertTask inserts a new task with sensible defaults", () => {
    useTaskCenterStore.getState().upsertTask("conv-1", { task_id: "t1", title: "Task 1" });

    const tasks = useTaskCenterStore.getState().getTasks("conv-1");
    expect(tasks).toHaveLength(1);
    expect(tasks[0]).toMatchObject({ task_id: "t1", title: "Task 1", status: "pending", progress_pct: 0 });
  });

  it("upsertTask merges into an existing task and preserves the longer execution_log", () => {
    useTaskCenterStore.getState().upsertTask("conv-1", {
      task_id: "t1",
      execution_log: [{ type: "text", content: "a" }, { type: "text", content: "b" }],
    });

    useTaskCenterStore.getState().upsertTask("conv-1", {
      task_id: "t1",
      status: "succeeded",
      execution_log: [{ type: "text", content: "a" }],
    });

    const task = useTaskCenterStore.getState().getTasks("conv-1")[0];
    expect(task.status).toBe("succeeded");
    expect(task.execution_log).toHaveLength(2);
  });

  it("upsertTask keeps the existing trigger_history_id/seq when the incoming update omits them", () => {
    useTaskCenterStore.getState().upsertTask("conv-1", {
      task_id: "t1",
      trigger_history_id: "h1",
      seq_in_conversation: 3,
    });

    useTaskCenterStore.getState().upsertTask("conv-1", { task_id: "t1", status: "running" });

    const task = useTaskCenterStore.getState().getTasks("conv-1")[0];
    expect(task.trigger_history_id).toBe("h1");
    expect(task.seq_in_conversation).toBe(3);
  });

  it("applyTaskEvent is a no-op when the task doesn't exist in the store", () => {
    useTaskCenterStore.getState().applyTaskEvent("conv-1", "missing", { type: "progress", progress: 50 });
    expect(useTaskCenterStore.getState().getTasks("conv-1")).toEqual([]);
  });

  it("applyTaskEvent handles progress, artifact (deduped), done and error events", () => {
    useTaskCenterStore.getState().upsertTask("conv-1", { task_id: "t1" });

    useTaskCenterStore.getState().applyTaskEvent("conv-1", "t1", { type: "progress", progress: 40 });
    expect(useTaskCenterStore.getState().getTasks("conv-1")[0].status).toBe("running");
    expect(useTaskCenterStore.getState().getTasks("conv-1")[0].progress_pct).toBe(40);

    const artifactEvent = { type: "artifact", slot: "s1", content_type: "text", seq: 1, value: "v1" };
    useTaskCenterStore.getState().applyTaskEvent("conv-1", "t1", artifactEvent);
    useTaskCenterStore.getState().applyTaskEvent("conv-1", "t1", artifactEvent);
    expect(useTaskCenterStore.getState().getTasks("conv-1")[0].artifacts).toHaveLength(1);

    useTaskCenterStore.getState().applyTaskEvent("conv-1", "t1", { type: "done", status: "succeeded", summary: "ok" });
    const doneTask = useTaskCenterStore.getState().getTasks("conv-1")[0];
    expect(doneTask.status).toBe("succeeded");
    expect(doneTask.progress_pct).toBe(100);
    expect(doneTask.summary).toBe("ok");
  });

  it("applyTaskEvent sets a localized error summary on error events", () => {
    useTaskCenterStore.getState().upsertTask("conv-1", { task_id: "t1" });

    useTaskCenterStore.getState().applyTaskEvent("conv-1", "t1", { type: "error", error_code: "1001" });

    const task = useTaskCenterStore.getState().getTasks("conv-1")[0];
    expect(task.status).toBe("failed");
    expect(task.summary).toBe("localized:1001");
  });

  it("applyTaskEvent appends text/think/tool_calls/tool_results log entries", () => {
    useTaskCenterStore.getState().upsertTask("conv-1", { task_id: "t1" });

    useTaskCenterStore.getState().applyTaskEvent("conv-1", "t1", { type: "text", text: "hello" });
    useTaskCenterStore.getState().applyTaskEvent("conv-1", "t1", { type: "think", think: "thinking..." });
    useTaskCenterStore.getState().applyTaskEvent("conv-1", "t1", {
      type: "tool_calls",
      tool_calls: [{ id: "c1", name: "search", args: { q: "x" } }],
    });
    useTaskCenterStore.getState().applyTaskEvent("conv-1", "t1", {
      type: "tool_results",
      tool_results: [{ tool_call_id: "c1", name: "search", result: "found" }],
    });

    const log = useTaskCenterStore.getState().getTasks("conv-1")[0].execution_log;
    expect(log.map((entry) => entry.type)).toEqual(["text", "think", "tool_calls", "tool_results"]);
  });

  it("subscribeTask opens an SSE stream and skips tasks already in a terminal state", () => {
    useTaskCenterStore.getState().upsertTask("conv-1", { task_id: "t1", status: "succeeded" });
    useTaskCenterStore.getState().subscribeTask("conv-1", "t1");
    expect(MockSSE.instances).toHaveLength(0);

    useTaskCenterStore.getState().upsertTask("conv-1", { task_id: "t2", status: "running" });
    useTaskCenterStore.getState().subscribeTask("conv-1", "t2");
    expect(MockSSE.instances).toHaveLength(1);
    expect(MockSSE.instances[0].url).toBe("/tasks/t2:stream");
  });

  it("subscribeTask does not open a second stream for the same task", () => {
    useTaskCenterStore.getState().upsertTask("conv-1", { task_id: "t1", status: "running" });
    useTaskCenterStore.getState().subscribeTask("conv-1", "t1");
    useTaskCenterStore.getState().subscribeTask("conv-1", "t1");

    expect(MockSSE.instances).toHaveLength(1);
  });

  it("streamed message events apply task events and unsubscribe on done", async () => {
    mockTaskServiceApi.listConversationTasks.mockResolvedValue({ data: { data: { tasks: [] } } });
    mockTaskServiceApi.listConversationArtifacts.mockResolvedValue({ data: { data: { artifacts: [] } } });
    useTaskCenterStore.getState().upsertTask("conv-1", { task_id: "t1", status: "running" });
    useTaskCenterStore.getState().subscribeTask("conv-1", "t1");

    const sse = MockSSE.instances[0];
    sse.emitMessage(JSON.stringify({ type: "progress", progress: 60 }));
    expect(useTaskCenterStore.getState().getTasks("conv-1")[0].progress_pct).toBe(60);

    sse.emitMessage(JSON.stringify({ type: "done", status: "succeeded" }));
    await Promise.resolve();

    expect(sse.closed).toBe(true);
    expect(useTaskCenterStore.getState()._streams["t1"]).toBeUndefined();
  });

  it("unsubscribeTask closes and removes the stream entry", () => {
    useTaskCenterStore.getState().upsertTask("conv-1", { task_id: "t1", status: "running" });
    useTaskCenterStore.getState().subscribeTask("conv-1", "t1");
    const sse = MockSSE.instances[0];

    useTaskCenterStore.getState().unsubscribeTask("t1");

    expect(sse.closed).toBe(true);
    expect(useTaskCenterStore.getState()._streams["t1"]).toBeUndefined();
  });

  it("loadConversationTasks populates tasks and subscribes non-terminal ones, deduping concurrent calls", async () => {
    mockTaskServiceApi.listConversationTasks.mockResolvedValue({
      data: {
        data: {
          tasks: [
            { task_id: "t1", status: "running", title: "Task 1" },
            { task_id: "t2", status: "succeeded", title: "Task 2" },
          ],
        },
      },
    });

    await Promise.all([
      useTaskCenterStore.getState().loadConversationTasks("conv-1"),
      useTaskCenterStore.getState().loadConversationTasks("conv-1"),
    ]);

    expect(mockTaskServiceApi.listConversationTasks).toHaveBeenCalledTimes(1);
    expect(useTaskCenterStore.getState().getTasks("conv-1")).toHaveLength(2);
    // Only the running task should have an active SSE subscription.
    expect(MockSSE.instances).toHaveLength(1);
    expect(MockSSE.instances[0].url).toBe("/tasks/t1:stream");
  });

  it("loadConversationTasks is a no-op for an empty conversationId and swallows API errors", async () => {
    await useTaskCenterStore.getState().loadConversationTasks("");
    expect(mockTaskServiceApi.listConversationTasks).not.toHaveBeenCalled();

    mockTaskServiceApi.listConversationTasks.mockRejectedValueOnce(new Error("fail"));
    await useTaskCenterStore.getState().loadConversationTasks("conv-err");
    expect(useTaskCenterStore.getState()._loadingTasks["conv-err"]).toBe(false);
  });

  it("loadConversationArtifacts stores the fetched artifacts and clears the loading flag", async () => {
    mockTaskServiceApi.listConversationArtifacts.mockResolvedValue({
      data: { data: { artifacts: [{ artifact_id: "a1" }] } },
    });

    await useTaskCenterStore.getState().loadConversationArtifacts("conv-1");

    expect(useTaskCenterStore.getState().artifactsByConversation["conv-1"]).toEqual([{ artifact_id: "a1" }]);
    expect(useTaskCenterStore.getState()._loadingArtifacts["conv-1"]).toBe(false);
  });

  it("upsertConversationArtifact inserts new artifacts and updates existing ones by artifact_id", () => {
    useTaskCenterStore.getState().upsertConversationArtifact("conv-1", { artifact_id: "a1", value: "v1" } as never);
    useTaskCenterStore.getState().upsertConversationArtifact("conv-1", { artifact_id: "a1", value: "v2" } as never);
    useTaskCenterStore.getState().upsertConversationArtifact("conv-1", { artifact_id: "a2", value: "v3" } as never);

    const artifacts = useTaskCenterStore.getState().artifactsByConversation["conv-1"];
    expect(artifacts).toHaveLength(2);
    expect(artifacts?.find((a) => a.artifact_id === "a1")?.value).toBe("v2");
  });

  it("upsertConversationArtifact ignores calls with no conversationId or artifact_id", () => {
    useTaskCenterStore.getState().upsertConversationArtifact("", { artifact_id: "a1" } as never);
    useTaskCenterStore.getState().upsertConversationArtifact("conv-1", {} as never);

    expect(useTaskCenterStore.getState().artifactsByConversation["conv-1"]).toBeUndefined();
  });

  it("reset clears tasks/artifacts and unsubscribes all streams for the conversation", () => {
    useTaskCenterStore.getState().upsertTask("conv-1", { task_id: "t1", status: "running" });
    useTaskCenterStore.getState().subscribeTask("conv-1", "t1");
    useTaskCenterStore.getState().subscribeConvEvents("conv-1");

    useTaskCenterStore.getState().reset("conv-1");

    expect(useTaskCenterStore.getState().tasksByConversation["conv-1"]).toEqual([]);
    expect(useTaskCenterStore.getState().artifactsByConversation["conv-1"]).toEqual([]);
    expect(useTaskCenterStore.getState()._streams["t1"]).toBeUndefined();
    expect(useTaskCenterStore.getState()._convStreams["conv-1"]).toBeUndefined();
  });

  it("subscribeConvEvents does not open a second stream for the same conversation", () => {
    useTaskCenterStore.getState().subscribeConvEvents("conv-1");
    useTaskCenterStore.getState().subscribeConvEvents("conv-1");

    expect(MockSSE.instances).toHaveLength(1);
  });

  it("subscribeConvEvents handles a task_created event by creating/subscribing the task", () => {
    useTaskCenterStore.getState().subscribeConvEvents("conv-1");
    const sse = MockSSE.instances[0];

    sse.emitMessage(
      JSON.stringify({
        type: "task_created",
        payload: { task_id: "t1", title: "New Task", status: "pending" },
      }),
    );

    expect(useTaskCenterStore.getState().getTasks("conv-1")).toHaveLength(1);
    expect(MockSSE.instances).toHaveLength(2); // conv stream + task stream
  });

  it("subscribeConvEvents handles artifact_created events", () => {
    useTaskCenterStore.getState().subscribeConvEvents("conv-1");
    const sse = MockSSE.instances[0];

    sse.emitMessage(
      JSON.stringify({ type: "artifact_created", payload: { artifact_id: "a1" } }),
    );

    expect(useTaskCenterStore.getState().artifactsByConversation["conv-1"]).toHaveLength(1);
  });

  it("unsubscribeConvEvents closes and removes the conversation stream", () => {
    useTaskCenterStore.getState().subscribeConvEvents("conv-1");
    const sse = MockSSE.instances[0];

    useTaskCenterStore.getState().unsubscribeConvEvents("conv-1");

    expect(sse.closed).toBe(true);
    expect(useTaskCenterStore.getState()._convStreams["conv-1"]).toBeUndefined();
  });

  it("setActiveConversation stores the active conversation id", () => {
    useTaskCenterStore.getState().setActiveConversation("conv-9");
    expect(useTaskCenterStore.getState().activeConversationId).toBe("conv-9");
  });
});
