import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import Workbench from "./Workbench";
import type { Task, TaskListResponse } from "./api";

const listTasksMock = vi.hoisted(() => vi.fn());
const removeTaskMock = vi.hoisted(() => vi.fn());
vi.mock("./api", () => ({
  listTasks: listTasksMock,
  removeTask: removeTaskMock,
}));

vi.mock("@/components/StateGraphModal", () => ({
  default: () => <div data-testid="state-graph-modal" />,
}));

const navigateMock = vi.hoisted(() => vi.fn());
vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return { ...actual, useNavigate: () => navigateMock };
});

function task(overrides: Partial<Task> = {}): Task {
  return {
    id: "t1",
    user_id: "u1",
    conversation_id: "c1",
    conversation_title: "Waiting task",
    task_type: "plugin_run",
    title: "Do something",
    status: "waiting",
    steps: [],
    created_at: "2024-01-01T00:00:00Z",
    updated_at: "2024-01-02T00:00:00Z",
    ...overrides,
  };
}

function response(items: Task[] = [task()]): TaskListResponse {
  return {
    items,
    total: items.length,
    page: 1,
    page_size: 60,
    status_counts: {
      all: items.length,
      pending: 0,
      waiting: items.filter((t) => t.status === "waiting").length,
      waiting_inputs: 0,
      running: items.filter((t) => t.status === "running").length,
      succeeded: items.filter((t) => ["completed", "succeeded"].includes(t.status)).length,
      failed: items.filter((t) => t.status === "failed").length,
      canceled: items.filter((t) => t.status === "canceled").length,
    },
  };
}

describe("Workbench", () => {
  beforeEach(() => {
    navigateMock.mockReset();
    listTasksMock.mockReset().mockResolvedValue(response());
    removeTaskMock.mockReset().mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("loads tasks and shows the needs-attention metric when active", async () => {
    renderWithProviders(<Workbench active />);
    await waitFor(() => expect(listTasksMock).toHaveBeenCalledTimes(1));
    expect(await screen.findByText("Waiting task")).toBeInTheDocument();
  });

  it("does not load tasks when inactive", () => {
    renderWithProviders(<Workbench active={false} />);
    expect(listTasksMock).not.toHaveBeenCalled();
  });

  it("renders an empty state for a section with no tasks", async () => {
    listTasksMock.mockResolvedValue(response([]));
    renderWithProviders(<Workbench active />);
    await waitFor(() => expect(listTasksMock).toHaveBeenCalledTimes(1));
    expect((await screen.findAllByText("taskCenter.empty")).length).toBeGreaterThan(0);
  });

  it("opens the task detail drawer when a waiting task card action is clicked", async () => {
    renderWithProviders(<Workbench active />);
    await screen.findByText("Waiting task");

    fireEvent.click(screen.getByText("taskCenter.confirmAction"));

    expect(await screen.findByText("taskCenter.taskDetail")).toBeInTheDocument();
  });

  it("reloads tasks when the refresh button is clicked", async () => {
    renderWithProviders(<Workbench active />);
    await waitFor(() => expect(listTasksMock).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByText("taskCenter.refresh"));

    await waitFor(() => expect(listTasksMock).toHaveBeenCalledTimes(2));
  });
});
