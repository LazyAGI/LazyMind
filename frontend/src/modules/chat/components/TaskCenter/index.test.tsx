import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderWithProviders, screen, fireEvent } from "@/test/testUtils";
import TaskCenter from "./index";
import type { SubAgentTask } from "@/modules/chat/store/taskCenter";

const mockLoadActiveSession = vi.fn();
let mockTasksByConversation: Record<string, SubAgentTask[]> = {};

vi.mock("@/modules/chat/store/taskCenter", async () => {
  const actual = await vi.importActual<typeof import("@/modules/chat/store/taskCenter")>(
    "@/modules/chat/store/taskCenter",
  );
  return {
    ...actual,
    useTaskCenterStore: (selector: (state: unknown) => unknown) =>
      selector({ tasksByConversation: mockTasksByConversation }),
  };
});

vi.mock("@/modules/chat/store/pluginPanel", () => ({
  usePluginStore: (selector: (state: unknown) => unknown) =>
    selector({ loadActiveSession: mockLoadActiveSession }),
}));

vi.mock("@/modules/knowledge/utils/imageUrl", () => ({
  basenameFromPath: (path: string) => path.split("/").pop() || "",
  resolveCoreAssetUrl: (path: string) => (path ? `https://cdn.example.com${path}` : ""),
}));

vi.mock("@/modules/chat/utils/download", () => ({
  downloadStream: vi.fn(),
}));

function makeTask(overrides: Partial<SubAgentTask> = {}): SubAgentTask {
  return {
    task_id: "task-1",
    title: "Writer subagent",
    agent_type: "writer",
    mode: "auto",
    status: "running",
    progress_pct: 40,
    artifacts: [],
    execution_log: [],
    ...overrides,
  };
}

describe("TaskCenter", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockTasksByConversation = {};
  });

  it("loads the active plugin session when a sessionId is provided", () => {
    renderWithProviders(<TaskCenter sessionId="conv-1" />);
    expect(mockLoadActiveSession).toHaveBeenCalledWith("conv-1");
  });

  it("shows the empty state when there are no tasks", () => {
    renderWithProviders(<TaskCenter sessionId="conv-1" />);
    expect(screen.getByText("taskCenter.empty")).toBeInTheDocument();
  });

  it("renders task cards for the current session", () => {
    mockTasksByConversation = { "conv-1": [makeTask()] };
    renderWithProviders(<TaskCenter sessionId="conv-1" />);
    expect(screen.getByText("Writer subagent")).toBeInTheDocument();
    expect(screen.getByText("taskCenter.statusRunning")).toBeInTheDocument();
  });

  it("filters tasks by status when a filter button is clicked", () => {
    mockTasksByConversation = {
      "conv-1": [
        makeTask({ task_id: "t1", title: "Running task", status: "running" }),
        makeTask({ task_id: "t2", title: "Done task", status: "succeeded" }),
      ],
    };
    renderWithProviders(<TaskCenter sessionId="conv-1" />);
    expect(screen.getByText("Running task")).toBeInTheDocument();
    expect(screen.getByText("Done task")).toBeInTheDocument();

    fireEvent.click(screen.getByText("taskCenter.filterSucceeded"));
    expect(screen.queryByText("Running task")).not.toBeInTheDocument();
    expect(screen.getByText("Done task")).toBeInTheDocument();
  });

  it("collapses and expands a task card", () => {
    mockTasksByConversation = { "conv-1": [makeTask()] };
    renderWithProviders(<TaskCenter sessionId="conv-1" />);
    const collapseBtn = screen.getByLabelText("common.collapse");
    fireEvent.click(collapseBtn);
    expect(screen.getByLabelText("common.expand")).toBeInTheDocument();
  });

  it("renders the close button and invokes onClose when clicked", () => {
    const onClose = vi.fn();
    mockTasksByConversation = { "conv-1": [makeTask()] };
    renderWithProviders(<TaskCenter sessionId="conv-1" onClose={onClose} />);
    fireEvent.click(screen.getByTitle("taskCenter.panelTitle"));
    expect(onClose).toHaveBeenCalled();
  });

  it("hides the header when showHeader is false", () => {
    renderWithProviders(<TaskCenter sessionId="conv-1" showHeader={false} />);
    expect(document.querySelector(".task-center-header")).not.toBeInTheDocument();
  });

  it("renders execution log entries and artifacts for a task", () => {
    mockTasksByConversation = {
      "conv-1": [
        makeTask({
          execution_log: [{ type: "text", content: "hello from the agent" }],
          artifacts: [
            {
              slot: "notes",
              content_type: "text",
              seq: 0,
              value: { text: "artifact body" },
            },
          ],
        }),
      ],
    };
    renderWithProviders(<TaskCenter sessionId="conv-1" />);
    expect(screen.getByText("hello from the agent")).toBeInTheDocument();
    expect(screen.getByText("artifact body")).toBeInTheDocument();
  });
});
