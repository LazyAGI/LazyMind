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
