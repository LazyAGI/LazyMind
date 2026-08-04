import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import TaskDetail, { StatusTag, formatDate } from "./TaskDetail";
import type { Task } from "./api";

const axiosGetMock = vi.hoisted(() => vi.fn());
vi.mock("@/components/request", () => ({
  axiosInstance: { get: axiosGetMock },
  BASE_URL: "http://localhost",
}));

function task(overrides: Partial<Task> = {}): Task {
  return {
    id: "t1",
    user_id: "u1",
    conversation_id: "c1",
    conversation_title: "My Conversation",
    task_type: "plugin_run",
    title: "Do something",
    status: "running",
    steps: [
      { step_id: "step-1", status: "succeeded" },
      { step_id: "step-2", status: "running" },
    ],
    created_at: "2024-01-01T00:00:00Z",
    updated_at: "2024-01-02T00:00:00Z",
    ...overrides,
  };
}

describe("TaskDetail", () => {
  beforeEach(() => {
    axiosGetMock.mockReset().mockRejectedValue(new Error("no projection"));
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders nothing meaningful when there is no task", () => {
    renderWithProviders(
      <TaskDetail task={null} onClose={vi.fn()} onOpenConversation={vi.fn()} />,
    );
    expect(screen.queryByText("taskCenter.taskDetail")).not.toBeInTheDocument();
  });

  it("renders task title, steps, and meta when a task is provided", () => {
    renderWithProviders(
      <TaskDetail task={task()} onClose={vi.fn()} onOpenConversation={vi.fn()} />,
    );
    expect(screen.getByText("My Conversation")).toBeInTheDocument();
    expect(screen.getByText("step-1")).toBeInTheDocument();
    expect(screen.getByText("step-2")).toBeInTheDocument();
  });

  it("fetches the plugin session projection when a plugin_session_id is present", async () => {
    renderWithProviders(
      <TaskDetail
        task={task({ plugin_session_id: "session-1" })}
        onClose={vi.fn()}
        onOpenConversation={vi.fn()}
      />,
    );
    await waitFor(() => expect(axiosGetMock).toHaveBeenCalled());
    expect(axiosGetMock.mock.calls[0][0]).toContain("session-1");
  });

  it("calls onOpenConversation when the open-conversation button is clicked", () => {
    const onOpenConversation = vi.fn();
    renderWithProviders(
      <TaskDetail task={task()} onClose={vi.fn()} onOpenConversation={onOpenConversation} />,
    );
    fireEvent.click(screen.getByText("taskCenter.openConversation"));
    expect(onOpenConversation).toHaveBeenCalledWith("c1");
  });

  it("disables the open-conversation button when there is no conversation id", () => {
    renderWithProviders(
      <TaskDetail
        task={task({ conversation_id: "" })}
        onClose={vi.fn()}
        onOpenConversation={vi.fn()}
      />,
    );
    expect(screen.getByText("taskCenter.openConversation").closest("button")).toBeDisabled();
  });

  it("shows an empty state with the waiting reason when there are no steps", () => {
    renderWithProviders(
      <TaskDetail
        task={task({ steps: [], waiting_reason: "Waiting on approval" })}
        onClose={vi.fn()}
        onOpenConversation={vi.fn()}
      />,
    );
    expect(screen.getByText("Waiting on approval")).toBeInTheDocument();
  });

  it("invokes onDelete through the remove confirmation flow", async () => {
    const onDelete = vi.fn().mockResolvedValue(undefined);
    renderWithProviders(
      <TaskDetail
        task={task()}
        onClose={vi.fn()}
        onOpenConversation={vi.fn()}
        onDelete={onDelete}
      />,
    );

    fireEvent.click(screen.getByText("taskCenter.taskRemoveBtn"));
    const okButton = await screen.findByText("taskCenter.taskRemoveConfirmOk");
    fireEvent.click(okButton);

    await waitFor(() => expect(onDelete).toHaveBeenCalledWith(task()));
  });

  it("calls onClose when the drawer close icon is clicked", () => {
    const onClose = vi.fn();
    renderWithProviders(
      <TaskDetail task={task()} onClose={onClose} onOpenConversation={vi.fn()} />,
    );
    const closeButton = document.querySelector(".ant-drawer-close") as HTMLElement;
    fireEvent.click(closeButton);
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

describe("StatusTag", () => {
  it("renders the translated status key and calls onClick when clickable", () => {
    const onClick = vi.fn();
    renderWithProviders(<StatusTag status="running" onClick={onClick} />);
    fireEvent.click(screen.getByText("taskCenter.statusRunning"));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("maps succeeded to the Completed i18n key", () => {
    renderWithProviders(<StatusTag status="succeeded" />);
    expect(screen.getByText("taskCenter.statusCompleted")).toBeInTheDocument();
  });
});

describe("formatDate", () => {
  it("formats a valid ISO date string", () => {
    expect(formatDate("2024-01-01T00:00:00Z")).not.toBe("—");
  });

  it("returns an em dash when the value is undefined", () => {
    expect(formatDate(undefined)).toBe("—");
  });
});
