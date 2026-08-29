import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const sseHarness = vi.hoisted(() => ({
  callbacks: new Map<string, Record<string, (event: CustomEvent) => void>>(),
}));

const workflowState = vi.hoisted(() => ({
  loadActiveSession: vi.fn().mockResolvedValue(undefined),
  setAutoRunning: vi.fn(),
}));

const requestHarness = vi.hoisted(() => ({
  listConversationTasks: vi.fn(),
  listConversationArtifacts: vi.fn(),
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
  TaskServiceApi: () => ({
    listConversationTasks: requestHarness.listConversationTasks,
    listConversationArtifacts: requestHarness.listConversationArtifacts,
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
import { CHAT_AUTO_ADVANCE_EVENT } from "@/modules/chat/constants/chat";

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
  beforeEach(() => {
    vi.useFakeTimers();
    sseHarness.callbacks.clear();
    requestHarness.listConversationTasks.mockReset();
    requestHarness.listConversationTasks.mockResolvedValue({ data: { tasks: [] } });
    requestHarness.listConversationArtifacts.mockReset();
    requestHarness.listConversationArtifacts.mockResolvedValue({ data: { artifacts: [] } });
    workflowState.loadActiveSession.mockClear();
    workflowState.setAutoRunning.mockClear();
    useTaskCenterStore.setState({
      activeConversationId: "",
      tasksByConversation: {},
      artifactsByConversation: {},
      _loadingTasks: {},
      _queuedTaskLoads: {},
      _taskLoadErrors: {},
      _loadingArtifacts: {},
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
        execution_log: [
          { type: "think", content: "Searching for seasonal material." },
        ],
      }),
    ]);
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
});
