import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const sseHarness = vi.hoisted(() => ({
  messages: {} as Record<string, ((event: CustomEvent) => void) | undefined>,
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
  taskStreamUrl: (taskId: string) => `/tasks/${taskId}:stream`,
  TaskServiceApi: () => ({
    listConversationTasks: vi.fn().mockResolvedValue({ data: { tasks: [] } }),
    listConversationArtifacts: vi.fn().mockResolvedValue({ data: { artifacts: [] } }),
  }),
}));

vi.mock("@/modules/chat/utils/sse", () => ({
  Method: { GET: "GET" },
  SSE: class MockSSE {
    constructor(url: string, options: { callbacks?: Record<string, (event: CustomEvent) => void> }) {
      sseHarness.messages[url] = options.callbacks?.message;
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
  it("keeps workflow execution tasks visible for detailed progress", () => {
    expect(isTaskCenterVisibleTask({ agent_type: "workflow_step" })).toBe(true);
  });

  it("keeps ordinary subagent tasks visible", () => {
    expect(isTaskCenterVisibleTask({ agent_type: "subagent" })).toBe(true);
  });
});

describe("task center workflow events", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    sseHarness.messages = {};
    useTaskCenterStore.setState({
      activeConversationId: "",
      tasksByConversation: {},
      artifactsByConversation: {},
      _loadingTasks: {},
      _loadingArtifacts: {},
      _streams: {},
      _convStream: null,
    });
  });

  afterEach(() => {
    useTaskCenterStore.getState().unsubscribeConvEvents("conversation-1");
    vi.useRealTimers();
  });

  it("shows a newly created workflow step immediately", () => {
    useTaskCenterStore.getState().subscribeConvEvents("conversation-1");

    sseHarness.messages["/events/conversation-1"]?.({
      data: JSON.stringify({
        type: "task_created",
        payload: {
          task_id: "workflow-task-1",
          agent_type: "workflow_step",
          title: "image-workflow:analyze_subject",
          status: "running",
        },
      }),
    } as CustomEvent);

    expect(useTaskCenterStore.getState().getTasks("conversation-1")).toEqual([
      expect.objectContaining({
        task_id: "workflow-task-1",
        agent_type: "workflow_step",
        status: "running",
      }),
    ]);
  });

  it("uses the workflow task stream without replaying its fallback conversation event", () => {
    useTaskCenterStore.getState().subscribeConvEvents("conversation-1");
    const conversationMessage = sseHarness.messages["/events/conversation-1"];
    conversationMessage?.({
      data: JSON.stringify({
        type: "task_created",
        payload: {
          task_id: "workflow-task-1",
          agent_type: "workflow_step",
          title: "writer:generate_draft",
          status: "running",
        },
      }),
    } as CustomEvent);

    sseHarness.messages["/tasks/workflow-task-1:stream"]?.({
      data: JSON.stringify({ type: "text", text: "drafting" }),
    } as CustomEvent);
    conversationMessage?.({
      data: JSON.stringify({
        type: "task_updated",
        payload: {
          task_id: "workflow-task-1",
          event: { type: "text", text: "drafting" },
        },
      }),
    } as CustomEvent);

    expect(useTaskCenterStore.getState().getTasks("conversation-1")[0].execution_log).toEqual([
      { type: "text", content: "drafting" },
    ]);
  });
});
