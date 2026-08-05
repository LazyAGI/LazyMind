import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import TaskList from "./TaskList";
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
    conversation_title: "My Conversation",
    task_type: "plugin_run",
    title: "Do something",
    status: "succeeded",
    steps: [{ step_id: "step-1", status: "succeeded" }],
    created_at: "2024-01-01T00:00:00Z",
    updated_at: "2024-01-02T00:00:00Z",
    ...overrides,
  };
}

function response(overrides: Partial<TaskListResponse> = {}): TaskListResponse {
  return {
    items: [task()],
    total: 1,
    page: 1,
    page_size: 20,
    status_counts: {
      all: 1,
      pending: 0,
      waiting: 0,
      waiting_inputs: 0,
      running: 0,
      succeeded: 1,
      failed: 0,
      canceled: 0,
    },
    ...overrides,
  };
}

const statusChangeMock = vi.fn();
const pageChangeMock = vi.fn();

// Status and page live in the parent page, so every render needs the controlled
// pair plus their callbacks.
function renderTaskList(active = true) {
  return renderWithProviders(
    <TaskList
      active={active}
      status=""
      onStatusChange={statusChangeMock}
      page={1}
      onPageChange={pageChangeMock}
    />,
  );
}

describe("TaskList", () => {
  beforeEach(() => {
    navigateMock.mockReset();
    statusChangeMock.mockReset();
    pageChangeMock.mockReset();
    listTasksMock.mockReset().mockResolvedValue(response());
    removeTaskMock.mockReset().mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("loads tasks when active becomes true", async () => {
    renderTaskList();
    await waitFor(() => expect(listTasksMock).toHaveBeenCalledTimes(1));
    expect(await screen.findByText("My Conversation")).toBeInTheDocument();
  });

  it("does not load tasks when inactive", () => {
    renderTaskList(false);
    expect(listTasksMock).not.toHaveBeenCalled();
  });

  it("searches by keyword and resets the page", async () => {
    renderTaskList();
    await waitFor(() => expect(listTasksMock).toHaveBeenCalledTimes(1));

    fireEvent.change(screen.getByPlaceholderText("taskCenter.searchPlaceholder"), {
      target: { value: "rag" },
    });

    await waitFor(() =>
      expect(listTasksMock).toHaveBeenLastCalledWith(
        expect.objectContaining({ keyword: "rag", page: 1 }),
      ),
    );
    expect(pageChangeMock).toHaveBeenCalledWith(1);
  });

  it("opens the task detail drawer when a row is clicked", async () => {
    renderTaskList();
    const title = await screen.findByText("My Conversation");
    fireEvent.click(title);
    expect(await screen.findByText("taskCenter.taskDetail")).toBeInTheDocument();
  });

  it("reloads the list after successfully deleting a task from the detail drawer", async () => {
    renderTaskList();
    const title = await screen.findByText("My Conversation");
    fireEvent.click(title);

    fireEvent.click(await screen.findByText("taskCenter.taskRemoveBtn"));
    fireEvent.click(await screen.findByText("taskCenter.taskRemoveConfirmOk"));

    await waitFor(() => expect(removeTaskMock).toHaveBeenCalledWith("t1"));
    await waitFor(() => expect(listTasksMock).toHaveBeenCalledTimes(2));
  });
});
