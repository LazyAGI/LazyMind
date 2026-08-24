import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const sseHarness = vi.hoisted(() => ({
  callbacks: new Map<string, Record<string, (event: CustomEvent) => void>>(),
}));

const workflowState = vi.hoisted(() => ({
  loadActiveSession: vi.fn().mockResolvedValue(undefined),
  setAutoRunning: vi.fn(),
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
    listConversationTasks: vi.fn().mockResolvedValue({ data: { tasks: [] } }),
    listConversationArtifacts: vi.fn().mockResolvedValue({ data: { artifacts: [] } }),
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

import { isTaskCenterVisibleTask, useTaskCenterStore } from "./taskCenter";

describe("isTaskCenterVisibleTask", () => {
  it("hides workflow execution tasks from the standalone developer task list", () => {
    expect(isTaskCenterVisibleTask({ agent_type: "workflow_step" })).toBe(false);
  });

  it("keeps ordinary subagent tasks visible", () => {
    expect(isTaskCenterVisibleTask({ agent_type: "subagent" })).toBe(true);
  });
});

describe("task center workflow events", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    sseHarness.callbacks.clear();
    useTaskCenterStore.setState({
      activeConversationId: "",
      tasksByConversation: {},
      artifactsByConversation: {},
      _loadingTasks: {},
      _loadingArtifacts: {},
      _convStream: null,
      _taskStreams: {},
    });
  });

  afterEach(() => {
    useTaskCenterStore.getState().unsubscribeConvEvents("conversation-1");
    vi.useRealTimers();
  });

  it("shows a newly created workflow step immediately", () => {
    useTaskCenterStore.getState().subscribeConvEvents("conversation-1");

    sseHarness.callbacks.get("/events/conversation-1")?.message?.({
      data: JSON.stringify({
        type: "task_created",
        payload: {
          task_id: "workflow-task-1",
          agent_type: "workflow_step",
          title: "image-workflow:analyze_subject",
          status: "running",
        },
      }),
    } as unknown as CustomEvent);

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

    sseHarness.callbacks.get("/events/conversation-1")?.message?.({
      data: JSON.stringify({
        type: "task_created",
        payload: {
          task_id: "workflow-task-1",
          agent_type: "workflow_step",
          title: "image-workflow:collect_materials",
          status: "pending",
        },
      }),
    } as unknown as CustomEvent);
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
});
